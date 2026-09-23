package scheduler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// asideArea is where a placement puts the entries it replaces until the
// download is recorded, so that a stop landing during the placement (a Skip,
// a delete, a follow removed) can put them back: the placement is undone
// whole, and the lesson's earlier files are where they were (D79). Once the
// download is recorded they are dropped.
//
// Each root the entries are under (the library, or downloads) gets its own
// <root>/.drumdrop-in-progress/replaced-<job>/ folder, so setting an entry
// aside is a rename within one filesystem, never a copy. Entry n goes to
// <n>/<its name>, so a folder a crash left can still be read by a person. Every
// rename acts on folders held open (renameIn), and refuses to replace.
//
// A folder of that name a crash left (the job, requeued, placing again) is
// never reused: it may hold an earlier download's only copy, and this area is
// removed whole when it is done. The area is then replaced-<job>.<k>, the
// first k free.
type asideArea struct {
	jobID int64
	// areas are the folders opened so far, by root.
	areas   map[string]openArea
	entries []asideEntry
}

// openArea is one root's area folder: its name in the private root, and the
// folder held open.
type openArea struct {
	name string
	dir  *os.Root
}

// asideEntry is one entry a placement set aside.
type asideEntry struct {
	// path is where it was, for messages.
	path string
	// parent is the folder it was in, held open; name its name there.
	parent *os.Root
	name   string
	// slot is its own folder in the area, holding it under name.
	slot *os.Root
	// own: the lesson's own recorded file (its previous download). A delete of
	// the lesson's files wants it gone, so an undo for that delete does not
	// put it back.
	own bool
}

func newAsideArea(jobID int64) *asideArea {
	return &asideArea{jobID: jobID, areas: map[string]openArea{}}
}

// area opens the area folder of root, creating it on first use under a name
// nothing holds yet.
func (a *asideArea) area(root string) (*os.Root, error) {
	root = filepath.Clean(root)
	if ar, ok := a.areas[root]; ok {
		return ar.dir, nil
	}
	s, err := openStaging(root)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	name := replacedFolderName(a.jobID)
	for k := 1; ; k++ {
		err := s.Mkdir(name, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || k > maxAsideAreas {
			return nil, fmt.Errorf("create %q: %w", filepath.Join(s.Name(), name), err)
		}
		name = replacedFolderName(a.jobID) + "." + strconv.Itoa(k)
	}
	r, err := openRealDir(s, name)
	if err != nil {
		return nil, err
	}
	a.areas[root] = openArea{name: name, dir: r}
	return r, nil
}

// maxAsideAreas bounds the search for a free area name: past it, the private
// root holds that many leftovers of one job, and the placement is refused.
const maxAsideAreas = 1000

// setAside moves the entry name of parent (a folder under root, held open by
// the caller) into the area of root. The area keeps its own handle on parent,
// so the caller may close its own.
func (a *asideArea) setAside(root string, parent *os.Root, name string, own bool) error {
	path := filepath.Join(parent.Name(), name)
	area, err := a.area(root)
	if err != nil {
		return fmt.Errorf("could not set %q aside: %w", path, err)
	}
	// A slot is a new folder; one an earlier placement of the job left (its
	// entry could not be put back) is skipped, never reused.
	var n string
	for i := len(a.entries) + 1; ; i++ {
		n = strconv.Itoa(i)
		err := area.Mkdir(n, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("could not set %q aside: %w", path, err)
		}
	}
	slot, err := area.OpenRoot(n)
	if err != nil {
		return fmt.Errorf("could not set %q aside: %w", path, err)
	}
	held, err := parent.OpenRoot(".")
	if err != nil {
		slot.Close()
		return fmt.Errorf("could not set %q aside: %w", path, err)
	}
	if err := renameIn(parent, name, slot, name); err != nil {
		slot.Close()
		held.Close()
		return fmt.Errorf("could not set %q aside: %w", path, err)
	}
	a.entries = append(a.entries, asideEntry{path: path, parent: held, name: name, slot: slot, own: own})
	return nil
}

// setAsidePath is setAside for an entry given by its path, which must be
// written inside root: its folder is opened through root, so no symlinked
// folder on the way leads out of it. A missing entry is already gone: nil.
func (a *asideArea) setAsidePath(root, path string, own bool) error {
	rel, ok := relInside(root, path)
	if !ok {
		return fmt.Errorf("could not set %q aside: it is not inside %q", path, root)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("could not set %q aside: %w", path, err)
	}
	defer r.Close()
	parent, err := r.OpenRoot(filepath.Dir(rel))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not set %q aside: %w", path, err)
	}
	defer parent.Close()
	if _, err := parent.Lstat(filepath.Base(rel)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return a.setAside(root, parent, filepath.Base(rel), own)
}

// restore puts every entry back where it was, newest first, except the
// lesson's own when dropOwn (a delete of its files). It returns the entries
// that could not go back, each also in the error; those stay in the area,
// which is then kept.
func (a *asideArea) restore(dropOwn bool) (stuck []string, err error) {
	var errs []error
	for i := len(a.entries) - 1; i >= 0; i-- {
		e := a.entries[i]
		if e.own && dropOwn {
			continue
		}
		if rerr := renameIn(e.slot, e.name, e.parent, e.name); rerr != nil {
			stuck = append(stuck, e.path)
			errs = append(errs, fmt.Errorf("could not put %q back; it is kept at %q: %w", e.path, filepath.Join(e.slot.Name(), e.name), rerr))
		}
	}
	return stuck, errors.Join(errs...)
}

// finish closes the area and removes its folders, unless keep (an entry
// could not be put back, so the area holds its only copy).
func (a *asideArea) finish(keep bool) error {
	var errs []error
	for _, e := range a.entries {
		e.slot.Close()
		e.parent.Close()
	}
	for root, ar := range a.areas {
		ar.dir.Close()
		if keep {
			continue
		}
		s, err := openExistingStaging(root)
		if err == nil {
			err = s.RemoveAll(ar.name)
			s.Close()
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("could not remove %q: %w", filepath.Join(root, privateRootName, ar.name), err))
		}
	}
	a.entries, a.areas = nil, map[string]openArea{}
	return errors.Join(errs...)
}

// relInside returns path relative to root when it is written strictly inside
// root.
func relInside(root, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return "", false
	}
	return rel, true
}
