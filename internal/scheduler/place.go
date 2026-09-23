package scheduler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// placement is a finished download placed where its lesson lives, not yet
// recorded. The worker records it (FinishDownload) and then commits it, which
// drops the entries it replaced; or, when the record is refused (a Skip, a
// delete or a follow removal landed meanwhile, or the job was requeued), undoes
// it, which takes the placed entries back into the private folder and puts
// the replaced ones back. Either way nothing is lost before the record says
// the new files are the lesson's.
type placement struct {
	// dir is the folder the entries were placed in: the lesson's own folder in
	// the default layout, the season folder in plex-tv.
	dir string
	// placed are the entries placed, as absolute paths.
	placed []string
	dest   *os.Root
	src    *os.Root
	steps  []placedStep
	aside  *asideArea
	// created is the lesson folder the placement made, if it made one.
	created *createdDir
}

// commit keeps the placement: the entries it replaced are removed. It returns
// them (where they were, and whether each was the lesson's own), for the log.
func (p *placement) commit() (replaced []asideEntry, err error) {
	replaced = append(replaced, p.aside.entries...)
	err = p.aside.finish(false)
	p.dest.Close()
	p.created.keep()
	return replaced, err
}

// undo takes the placement back: every placed entry returns to the private
// folder (a copied one is removed; its source never left), a lesson folder it
// made goes if it is empty again, and every entry it replaced goes back where
// it was, except the lesson's own when dropOwn (a delete of the lesson's
// files, which wants them gone). It returns what could not be taken back or
// put back.
func (p *placement) undo(dropOwn bool) (stuck []string, err error) {
	stuck, uerr := undoSteps(p.dest, p.src, p.steps)
	p.dest.Close()
	p.created.remove()
	kept, rerr := p.aside.restore(dropOwn)
	ferr := p.aside.finish(len(kept) > 0)
	return append(stuck, kept...), errors.Join(uerr, rerr, ferr)
}

// createdDir is a folder a placement made (the lesson's folder), in its
// parent held open, so an undo can remove it again.
type createdDir struct {
	parent *os.Root
	name   string
}

// remove removes the folder if it is empty (os.Root.Remove removes no folder
// with anything in it), and releases it. A nil createdDir is a no-op.
func (c *createdDir) remove() {
	if c != nil {
		_ = c.parent.Remove(c.name)
		c.parent.Close()
	}
}

// keep releases the folder. A nil createdDir is a no-op.
func (c *createdDir) keep() {
	if c != nil {
		c.parent.Close()
	}
}

// placeSteps places every step's entry of src into dest (placeStep), or none:
// on a failure every entry placed so far is taken back. Copies are flushed to
// disk before it returns. stuck are entries an undo could not take back.
func placeSteps(dest, src *os.Root, steps []plexMoveStep) (placed []placedStep, stuck []string, err error) {
	copied := false
	for _, st := range steps {
		renamed, err := placeStep(dest, src, st)
		if err != nil {
			stuck, uerr := undoSteps(dest, src, placed)
			return nil, stuck, errors.Join(err, uerr)
		}
		copied = copied || !renamed
		placed = append(placed, placedStep{plexMoveStep: st, renamed: renamed})
	}
	if copied {
		if err := syncIn(dest, "."); err != nil {
			stuck, uerr := undoSteps(dest, src, placed)
			return nil, stuck, errors.Join(fmt.Errorf("flush %q after the copy: %w", dest.Name(), err), uerr)
		}
	}
	return placed, nil, nil
}

// lessonSteps lists the entries of the private lesson folder src in name
// order, each going to the same name in dstDir. Non-regular files are left
// out (drumdrop only produces regular files and folders).
func lessonSteps(src *os.Root, dstDir string) ([]plexMoveStep, error) {
	entries, err := fs.ReadDir(src.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("read the downloaded lesson %q: %w", src.Name(), err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var steps []plexMoveStep
	for _, e := range entries {
		name := e.Name()
		st := plexMoveStep{name: name, src: filepath.Join(src.Name(), name), dst: filepath.Join(dstDir, name), dir: e.IsDir()}
		if !st.dir {
			info, err := e.Info()
			if err != nil {
				return nil, fmt.Errorf("read %q: %w", st.src, err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			st.mode = info.Mode()
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// placeLessonFolder places a finished download in the default layout: every
// entry of the private lesson folder src goes into the lesson's folder
// <root>/<rel> ("<Course>/NN - Title"), created if missing. root is the
// library, or the downloads folder when there is none (or the placement in
// the library was refused); it is the same code either way.
//
// What is already in the lesson's folder stays, except an entry at one of the
// names being placed: that is the lesson's own earlier file (the folder is
// the lesson's by record) or one no lesson records, and it is replaced (owner
// ruling #66). A folder another lesson records anything in refuses the whole
// placement. The lesson's previous folder, when its row records another one
// (the title changed, the library was added, or the download was kept in
// downloads when a move was refused), is replaced too, unless another lesson
// records something in it. A replaced entry is only set aside (asideArea)
// until the placement is committed.
//
// Everything goes through folders held open (os.Root) and renameAt, which
// never replaces; a copy happens only across filesystems (EXDEV). It writes
// nothing unless it can place every entry: an error means the destination
// is as it was (an entry an undo could not take back is named in it).
func placeLessonFolder(root, rel string, src *scratchDir, self database.Lesson, claims *library.Claims, roots []string, jobID int64) (*placement, error) {
	if rel == "" || rel == "." || !filepath.IsLocal(rel) || filepath.Dir(rel) == "." {
		return nil, fmt.Errorf("refusing to place the lesson at %q under %q: not a lesson folder inside it", rel, root)
	}
	if isPrivateRel(rel) {
		return nil, fmt.Errorf("refusing to place the lesson in %q: that folder holds downloads in progress", filepath.Join(root, rel))
	}
	if claims == nil {
		return nil, errors.New("refusing to place the lesson: the other lessons' files were not read")
	}
	dstDir := filepath.Join(root, rel)
	if ids := claims.Holds(dstDir, self.RailcontentID); len(ids) > 0 {
		return nil, fmt.Errorf("refusing to place the lesson: %q holds files lessons %v record", dstDir, ids)
	}
	// The downloaded folder itself is never the destination: replacing an
	// entry there would delete what is being placed.
	if library.Inside(src.dir.Name(), dstDir) || sameDir(src.dir.Name(), dstDir) {
		return nil, fmt.Errorf("refusing to place the lesson at %q: it is the downloaded folder itself", dstDir)
	}
	own := self.OutputDir.Valid && (filepath.Clean(self.OutputDir.String) == filepath.Clean(dstDir) || sameDir(self.OutputDir.String, dstDir))

	aside := newAsideArea(jobID)
	var (
		dest    *os.Root
		created *createdDir
	)
	fail := func(err error) (*placement, error) {
		if dest != nil {
			dest.Close()
		}
		created.remove()
		if _, rerr := aside.restore(false); rerr != nil {
			err = errors.Join(err, rerr)
			return nil, errors.Join(err, aside.finish(true))
		}
		return nil, errors.Join(err, aside.finish(false))
	}
	if prev, ok := previousFolder(self, dstDir, claims, roots); ok {
		if err := aside.setAsidePath(prev.root, prev.path, true); err != nil {
			return fail(fmt.Errorf("refusing to place the lesson: %w", err))
		}
	}
	parent, err := openLibraryParent(root, filepath.Dir(rel))
	if err != nil {
		return fail(err)
	}
	defer parent.Close()
	leaf := filepath.Base(rel)
	if info, lerr := parent.Lstat(leaf); lerr == nil && !info.IsDir() {
		// Not a real folder (a file, a symlink): an entry at the lesson's name.
		if err := aside.setAside(root, parent, leaf, own); err != nil {
			return fail(err)
		}
	}
	switch err := parent.Mkdir(leaf, 0o755); {
	case err == nil:
		held, herr := parent.OpenRoot(".")
		if herr != nil {
			_ = parent.Remove(leaf)
			return fail(fmt.Errorf("create %q: %w", dstDir, herr))
		}
		created = &createdDir{parent: held, name: leaf}
	case !errors.Is(err, fs.ErrExist):
		return fail(fmt.Errorf("create %q: %w", dstDir, err))
	}
	if dest, err = openRealDir(parent, leaf); err != nil {
		dest = nil
		return fail(err)
	}
	steps, err := lessonSteps(src.dir, dstDir)
	if err != nil {
		return fail(err)
	}
	for _, st := range steps {
		if _, lerr := dest.Lstat(st.name); lerr != nil {
			continue
		}
		if err := aside.setAside(root, dest, st.name, own); err != nil {
			return fail(err)
		}
	}
	placed, stuck, err := placeSteps(dest, src.dir, steps)
	if err != nil {
		if len(stuck) > 0 {
			err = fmt.Errorf("%w (left in %q: %q)", err, dstDir, stuck)
		}
		return fail(err)
	}
	// A copied lesson in a folder the placement made: the folder's own name
	// is flushed too, as the copies were (placeSteps).
	if created != nil && slices.ContainsFunc(placed, func(st placedStep) bool { return !st.renamed }) {
		if err := syncIn(parent, "."); err != nil {
			stuck, uerr := undoSteps(dest, src.dir, placed)
			return fail(errors.Join(fmt.Errorf("flush %q after the copy: %w", filepath.Dir(dstDir), err), uerr, stuckErr(stuck)))
		}
	}
	p := &placement{dir: dstDir, dest: dest, src: src.dir, steps: placed, aside: aside, created: created}
	for _, st := range placed {
		p.placed = append(p.placed, st.dst)
	}
	return p, nil
}

// stuckErr names entries an undo could not take back, or is nil.
func stuckErr(stuck []string) error {
	if len(stuck) == 0 {
		return nil
	}
	return fmt.Errorf("left where they were placed: %q", stuck)
}

// heldPath is a path and the root it is inside.
type heldPath struct{ root, path string }

// previousFolder is the lesson's previous folder in the default layout, to be
// replaced by a placement at dstDir: the folder its row records, when that is
// a lesson folder (not a season folder), not dstDir, not holding or held by
// dstDir, inside one of roots, and holding nothing another lesson records.
func previousFolder(self database.Lesson, dstDir string, claims *library.Claims, roots []string) (heldPath, bool) {
	if !self.OutputDir.Valid || self.OutputDir.String == "" {
		return heldPath{}, false
	}
	prev := filepath.Clean(self.OutputDir.String)
	if !library.IsLessonFolder(prev) || library.IsSeasonDir(prev) || library.Inside(prev, dstDir) || library.Inside(dstDir, prev) || sameDir(prev, dstDir) {
		return heldPath{}, false
	}
	if len(claims.Holds(prev, self.RailcontentID)) > 0 {
		return heldPath{}, false
	}
	best := ""
	for _, r := range roots {
		if _, ok := relInside(r, prev); ok && len(r) > len(best) {
			best = r
		}
	}
	if best == "" {
		return heldPath{}, false
	}
	return heldPath{root: best, path: prev}, true
}
