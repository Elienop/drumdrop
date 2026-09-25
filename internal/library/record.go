package library

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elienop/drumdrop/internal/database"
)

// A lesson's record (lessons.library_entries) names every entry the plex-tv
// move placed for it in a season folder, each RELATIVE to the library folder
// and written with forward slashes: "<show>/Season NN/<entry>". Relative, so a
// record stays true when the library moves: DRUMDROP_LIBRARY_DIR spelled
// another way (relative, through a symlink) or mounted at a new path. Every
// use resolves it under the library folder configured now.

// Record returns lesson l's recorded entries (relative, slash-separated) and
// whether it has a record at all. A value that is not a JSON list of entries,
// or holds an entry that is not exactly "<show>/Season NN/<name>", is an
// error, never an empty record, so a damaged row can not pass for "owns
// nothing" nor aim a removal anywhere else.
func Record(l database.Lesson) (entries []string, recorded bool, err error) {
	entries, recorded, err = l.PlacedEntries()
	if err != nil || !recorded {
		return nil, recorded, err
	}
	for _, e := range entries {
		if err := checkEntry(e); err != nil {
			return nil, true, fmt.Errorf("lesson %d: %w", l.RailcontentID, err)
		}
	}
	return entries, true, nil
}

// checkEntry accepts only a record entry the move writes: three plain,
// local path parts, the middle one a season folder.
func checkEntry(e string) error {
	native := filepath.FromSlash(e)
	parts := strings.Split(e, "/")
	if !filepath.IsLocal(native) || filepath.ToSlash(filepath.Clean(native)) != e ||
		len(parts) != 3 || parts[0] == "" || parts[2] == "" || !IsSeasonDir(parts[1]) {
		return fmt.Errorf("library record entry %q is not <show>/Season NN/<name> inside the library; refusing to touch it", e)
	}
	return nil
}

// EntryFor is the record entry for path, an entry of a season folder under
// the library folder root.
func EntryFor(root, path string) (string, error) {
	rel, ok := relUnder(root, path)
	if !ok {
		return "", fmt.Errorf("%q is not inside the library folder %q", path, root)
	}
	e := filepath.ToSlash(rel)
	if err := checkEntry(e); err != nil {
		return "", err
	}
	return e, nil
}

// EntriesFor is EntryFor over paths. It returns a non-nil slice, so an empty
// result is an empty record ("[]"), not "no record".
func EntriesFor(root string, paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		e, err := EntryFor(root, p)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// Resolve is the path of record entry e under the library folder root.
func Resolve(root, e string) string {
	return filepath.Join(root, filepath.FromSlash(e))
}
