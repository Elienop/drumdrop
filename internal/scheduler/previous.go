package scheduler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// A FOLDER of the lesson's previous download at a DIFFERENT place from the new
// one (the title changed, a library was added after it was downloaded, the
// layout changed) is removed only when the new download brings back every file
// in it, at the corresponding path in the new lesson folder. Otherwise it stays
// where it is, untouched and no longer recorded, and the log names it and says
// why (owner ruling 2026-09-24 (e)). So a file the owner added in such a
// folder, or a resource the re-download failed to fetch again, is never lost
// to a rename. The rule is for folders: a plex-tv lesson's recorded FILES at an
// old title's names (its video, nfo, captions, poster) are its own by record
// and still go (ruling #66), whether or not the re-download brought each back.
// Recorded entries at the current episode base that the re-download did not
// bring back stay, and stay recorded (owner ruling 2026-09-24 (j)).

// keptFolder is a previous folder of the lesson a placement left where it
// was: why says what the new download did not bring back.
type keptFolder struct {
	path, why string
}

// downloadTree is what a placement brings to the lesson: every folder and
// regular file of the downloaded lesson folder, by its slash path in it (true
// for a folder). The placement places each of them, at every depth.
type downloadTree map[string]bool

// readDownloadTree lists the downloaded lesson folder src. The walk reads only
// inside src and follows no link.
func readDownloadTree(src *os.Root) (downloadTree, error) {
	t := downloadTree{}
	err := fs.WalkDir(src.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == ".":
		case d.IsDir():
			t[p] = true
		case d.Type().IsRegular():
			t[p] = false
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the downloaded lesson %q: %w", src.Name(), err)
	}
	return t, nil
}

// missing names the first entry of the folder old that the download does not
// bring back, or is "" when it brings back every one. An entry is brought back
// when the download has one of the same kind (a folder, or a regular file) at
// the corresponding path: the same path under the download's folder into (""
// for the lesson folder itself), except that a top-level file named after
// oldBase corresponds to the one named after newBase, as the download names
// its files after the lesson folder. Anything else in old (a symlink, say) is
// never brought back. The walk reads only inside old and follows no link.
func (t downloadTree) missing(old *os.Root, into, oldBase, newBase string) (string, error) {
	var miss string
	err := fs.WalkDir(old.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return err
		}
		q := p
		if rest, ok := strings.CutPrefix(p, oldBase); ok && oldBase != "" && !d.IsDir() && !strings.Contains(p, "/") {
			q = newBase + rest
		}
		if into != "" {
			q = into + "/" + q
		}
		dir, ok := t[q]
		if !ok || dir != d.IsDir() || (!dir && !d.Type().IsRegular()) {
			miss = p
			return fs.SkipAll
		}
		return nil
	})
	return miss, err
}

// previousStays decides for the entry at h, part of the lesson's previous
// download at a place the new placement puts nothing: it returns why it stays
// where it is, or "" when it may be set aside (to be removed once the new
// download is recorded), which is also the answer when nothing is there.
//
// A real folder may go only when the download brings back every entry in it
// (missing): into says which of the download's folders it corresponds to,
// for its name (false: none does, so it stays). A file may go only when
// fileIsOwn (a plex-tv record names it as the lesson's, owner ruling #66);
// anything else there stays. So does an entry that can not be read: when
// unsure, keep.
func (t downloadTree) previousStays(h heldPath, into func(name string) (string, bool), oldBase, newBase string, fileIsOwn bool) string {
	rel, ok := relInside(h.root, h.path)
	if !ok {
		return fmt.Sprintf("it is not inside %q", h.root)
	}
	r, err := os.OpenRoot(h.root)
	if err != nil {
		return fmt.Sprintf("it could not be read: %v", err)
	}
	defer r.Close()
	parent, err := r.OpenRoot(filepath.Dir(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("it could not be read: %v", err)
	}
	defer parent.Close()
	name := filepath.Base(rel)
	info, err := parent.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ""
	case err != nil:
		return fmt.Sprintf("it could not be read: %v", err)
	case !info.IsDir() && fileIsOwn:
		return ""
	case !info.IsDir():
		return "it is not a real folder"
	}
	sub, ok := into(name)
	if !ok {
		return "the new download has no folder in its place"
	}
	old, err := openRealDir(parent, name)
	if err != nil {
		return fmt.Sprintf("it could not be read: %v", err)
	}
	defer old.Close()
	miss, err := t.missing(old, sub, oldBase, newBase)
	switch {
	case err != nil:
		return fmt.Sprintf("it could not be read: %v", err)
	case miss != "":
		return fmt.Sprintf("the new download did not bring back %q", filepath.FromSlash(miss))
	}
	return ""
}

// lessonFolderInto is previousStays' into for a previous lesson folder: it
// corresponds to the downloaded lesson folder itself.
func lessonFolderInto(string) (string, bool) { return "", true }

// plexFolderInto is previousStays' into for a plex-tv entry of the lesson's
// previous download under an old episode name: the folder "<old episode>
// <folder>" corresponds to the download's folder <folder> (the longest that
// fits, so " extra resources" wins over " resources").
func plexFolderInto(steps []plexMoveStep) func(name string) (string, bool) {
	return func(name string) (string, bool) {
		best, found := "", false
		for _, st := range steps {
			if st.dir && strings.HasSuffix(name, " "+st.name) && len(st.name) >= len(best) {
				best, found = st.name, true
			}
		}
		return best, found
	}
}

// placedAt is the destination among steps that IS the entry p, however each
// is spelled: p itself when a step places at p, else a step's destination that
// is the same file (a title that changed only in case, on a case-insensitive
// disk). "" when none is.
func placedAt(p string, steps []plexMoveStep) string {
	for _, st := range steps {
		if st.dst == p {
			return p
		}
	}
	pi, err := os.Lstat(p)
	if err != nil {
		return ""
	}
	for _, st := range steps {
		if si, err := os.Lstat(st.dst); err == nil && os.SameFile(pi, si) {
			return st.dst
		}
	}
	return ""
}
