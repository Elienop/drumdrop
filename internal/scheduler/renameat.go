package scheduler

import (
	"fmt"
	"os"
	"strings"
)

// renameAt moves the entry src of the folder from to the new name dst in the
// folder to. Where the platform allows (see renameat), both folders are the
// ones already open, not their paths, so a folder on the way swapped for a
// symlink after it was opened can not redirect the rename, and an entry
// already at dst is never replaced. A package variable so a test can force the
// copy fallback or act between the checks and the rename.
var renameAt = renameIn

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
