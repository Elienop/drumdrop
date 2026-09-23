package scheduler

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// renameAt moves the entry src of the folder from to the new name dst in the
// folder to. Where the platform allows (see renameat), both folders are the
// ones already open, not their paths, so a folder on the way swapped for a
// symlink after it was opened can not redirect the rename, and an entry
// already at dst fails the rename (fs.ErrExist) instead of being replaced.
// It IS replaced in three cases: on Windows (the rename goes by path, with
// MOVEFILE_REPLACE_EXISTING), on a Linux filesystem that does not support
// RENAME_NOREPLACE (some network filesystems; the retry has no flag), and on
// a macOS filesystem that does not support RENAME_EXCL (the same retry). A
// package variable so a test can force the copy fallback or act between the
// checks and the rename.
var renameAt = renameIn

// crossDevice reports whether a rename failed only because its two folders
// are on different filesystems (volumes), the one failure a move falls back to
// a copy for. Any other failure, an entry already at the destination above
// all, is a refusal: nothing is copied over it.
func crossDevice(err error) bool {
	return errors.Is(err, errCrossDevice)
}

// renameIn is renameAt's default: it refuses a name that is not a single path
// part (the rename acts in exactly the two folders given), then renames.
func renameIn(from *os.Root, src string, to *os.Root, dst string) error {
	for _, n := range []string{src, dst} {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
			return fmt.Errorf("refusing to rename %q -> %q: not a single name", src, dst)
		}
	}
	return renameat(from, src, to, dst)
}

// dirFile opens the folder r itself, for a call that needs its descriptor. It
// is the folder r was opened on (its identity), whatever its path now names.
func dirFile(r *os.Root) (*os.File, error) {
	f, err := r.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open folder %q: %w", r.Name(), err)
	}
	return f, nil
}
