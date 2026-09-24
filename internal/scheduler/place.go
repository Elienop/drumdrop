package scheduler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	dir   string
	dest  *os.Root
	src   *os.Root
	steps []placedStep
	// merged are the subfolders placed entry by entry into a folder already
	// at their name (mergeFolder), in the order they were placed.
	merged []placedLevel
	aside  *asideArea
	// created is the lesson folder the placement made, if it made one.
	created *createdDir
	// kept are the lesson's previous folders the placement left where they
	// were (previousStays), for the log once it is committed.
	kept []keptFolder
}

// commit keeps the placement: the entries it replaced are removed. It returns
// them (where they were, and whether each was the lesson's own), for the log.
func (p *placement) commit() (replaced []asideEntry, err error) {
	replaced = append(replaced, p.aside.entries...)
	err = p.aside.finish(false)
	p.dest.Close()
	closeLevels(p.merged)
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
	mstuck, merr := undoLevels(p.merged)
	p.dest.Close()
	p.created.remove()
	kept, rerr := p.aside.restore(dropOwn)
	ferr := p.aside.finish(len(kept) > 0)
	return append(append(stuck, mstuck...), kept...), errors.Join(uerr, merr, rerr, ferr)
}

// placedLevel is a subfolder of the download placed entry by entry into a
// real folder already at its name (mergeFolder): its entries of src placed in
// dest. It holds both folders open.
type placedLevel struct {
	dest, src *os.Root
	steps     []placedStep
}

// undoLevels takes every merged level's placed entries back, newest level
// first (undoSteps), and releases the levels. It returns what could not be
// taken back.
func undoLevels(levels []placedLevel) (stuck []string, err error) {
	var errs []error
	for i := len(levels) - 1; i >= 0; i-- {
		s, uerr := undoSteps(levels[i].dest, levels[i].src, levels[i].steps)
		stuck = append(stuck, s...)
		errs = append(errs, uerr)
	}
	closeLevels(levels)
	return stuck, errors.Join(errs...)
}

// closeLevels releases every merged level's folders.
func closeLevels(levels []placedLevel) {
	for _, l := range levels {
		l.dest.Close()
		l.src.Close()
	}
}

// clearNames readies dest for steps, entries of src: an entry already at a
// step's name is set aside under root (the lesson's own when own says so for
// that destination), as it is replaced (owner ruling #66). A real folder
// where a folder is placed is not: the two are merged (mergeFolder), the same
// rule one level down, so what that folder holds at other names stays (owner
// ruling 2026-09-24). It returns the steps still to place in dest; each merged
// one is placed already, and appended to levels.
func clearNames(root string, dest, src *os.Root, steps []plexMoveStep, own func(dst string) bool, aside *asideArea, levels *[]placedLevel) ([]plexMoveStep, error) {
	var rest []plexMoveStep
	for _, st := range steps {
		name := filepath.Base(st.dst)
		info, lerr := dest.Lstat(name)
		switch {
		case lerr != nil:
		case st.dir && info.IsDir():
			if err := mergeFolder(root, dest, src, st, own(st.dst), aside, levels); err != nil {
				return nil, err
			}
			continue
		default:
			if err := aside.setAside(root, dest, name, own(st.dst)); err != nil {
				return nil, err
			}
		}
		rest = append(rest, st)
	}
	return rest, nil
}

// mergeFolder places the folder st of src into the real folder already at its
// destination in dest, entry by entry: clearNames, then placeSteps, in the
// two folders held open. On success the level is appended to levels (a
// commit or an undo releases it); on a failure what it placed is taken back.
func mergeFolder(root string, dest, src *os.Root, st plexMoveStep, own bool, aside *asideArea, levels *[]placedLevel) error {
	d, err := openRealDir(dest, filepath.Base(st.dst))
	if err != nil {
		return err
	}
	s, err := openRealDir(src, st.name)
	if err != nil {
		d.Close()
		return err
	}
	steps, err := lessonSteps(s, d.Name())
	if err == nil {
		steps, err = clearNames(root, d, s, steps, func(string) bool { return own }, aside, levels)
	}
	var placed []placedStep
	if err == nil {
		var stuck []string
		if placed, stuck, err = placeSteps(d, s, steps); err != nil {
			err = errors.Join(err, stuckErr(stuck))
		}
	}
	if err != nil {
		d.Close()
		s.Close()
		return err
	}
	*levels = append(*levels, placedLevel{dest: d, src: s, steps: placed})
	return nil
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
// on a failure every entry placed so far is taken back. stuck are entries an
// undo could not take back.
//
// What it placed is flushed to disk before it returns, however it got there
// (security I2): dest, which now names the entries, and src, which a rename
// took them out of. Without it a power loss after the download is recorded
// could leave a recorded file still in its private folder, which the next
// start's sweep removes. A failed flush is a failed placement.
func placeSteps(dest, src *os.Root, steps []plexMoveStep) (placed []placedStep, stuck []string, err error) {
	anyRenamed := false
	for _, st := range steps {
		renamed, err := placeStep(dest, src, st)
		if err != nil {
			stuck, uerr := undoSteps(dest, src, placed)
			return nil, stuck, errors.Join(err, uerr)
		}
		anyRenamed = anyRenamed || renamed
		placed = append(placed, placedStep{plexMoveStep: st, renamed: renamed})
	}
	if len(placed) == 0 {
		return nil, nil, nil
	}
	ferr := syncIn(dest, ".")
	if ferr == nil && anyRenamed {
		ferr = syncIn(src, ".")
	}
	if ferr != nil {
		stuck, uerr := undoSteps(dest, src, placed)
		return nil, stuck, errors.Join(fmt.Errorf("flush what was placed in %q: %w", dest.Name(), ferr), uerr)
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
// ruling #66). A real folder at the name of a folder being placed (resources/,
// say) is not replaced whole: the two are merged by the same rule, at every
// depth, so a file in it that the download did not produce again stays (owner
// ruling 2026-09-24; see clearNames). A folder another lesson records
// anything in refuses the whole placement. The lesson's previous folder, when
// its row records another one (the title changed, the library was added, or
// the download was kept in downloads when a move was refused), goes only when
// the download brings back every file in it and no other lesson records
// anything in it; otherwise it stays where it is, no longer recorded, and the
// placement's kept says why (previousStays). A replaced entry is only set
// aside (asideArea) until the placement is committed.
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
		merged  []placedLevel
	)
	fail := func(err error) (*placement, error) {
		if _, uerr := undoLevels(merged); uerr != nil {
			err = errors.Join(err, uerr)
		}
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
	var kept []keptFolder
	if prev, ok := previousFolder(self, dstDir, claims, roots); ok {
		tree, err := readDownloadTree(src.dir)
		if err != nil {
			return fail(err)
		}
		if why := tree.previousStays(prev, lessonFolderInto, filepath.Base(prev.path), src.base, false); why != "" {
			kept = append(kept, keptFolder{path: prev.path, why: why})
		} else if err := aside.setAsidePath(prev.root, prev.path, true); err != nil {
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
	steps, err = clearNames(root, dest, src.dir, steps, func(string) bool { return own }, aside, &merged)
	if err != nil {
		return fail(err)
	}
	placed, stuck, err := placeSteps(dest, src.dir, steps)
	if err != nil {
		if len(stuck) > 0 {
			err = fmt.Errorf("%w (left in %q: %q)", err, dstDir, stuck)
		}
		return fail(err)
	}
	// A folder the placement made: its own name is flushed too, as what was
	// placed in it was (placeSteps).
	if created != nil {
		if err := syncIn(parent, "."); err != nil {
			stuck, uerr := undoSteps(dest, src.dir, placed)
			return fail(errors.Join(fmt.Errorf("flush %q after the placement: %w", filepath.Dir(dstDir), err), uerr, stuckErr(stuck)))
		}
	}
	return &placement{dir: dstDir, dest: dest, src: src.dir, steps: placed, merged: merged, aside: aside, created: created, kept: kept}, nil
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

// previousFolder is the lesson's previous folder in the default layout, which
// a placement at dstDir replaces if the download brings back everything in it
// (previousStays): the folder its row records, when that is
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
