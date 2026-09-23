package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Remove removes path (a file, a symlink itself, or a folder with everything
// in it) through the root that holds it, with os.Root, so no symlinked folder
// on the way can make it act outside that root: such a path is refused ("path
// escapes from parent"). The root that holds path is the longest of roots it
// is written inside; failing that, the nearest folder above it that IS one of
// roots under another spelling (a symlink to it, or another mount of it),
// decided by identity (os.SameFile), not by spelling.
//
// A path inside a root that is missing is already gone: nil. So is a path in
// none of the roots that does not exist (a record written under a library path
// that has since moved). A path that exists outside every root is refused, and
// so is a root itself. Relative paths and roots are read from the working
// directory, as the OS reads them.
func Remove(roots []string, path string) error {
	if path == "" {
		return errors.New("refusing to remove an empty path")
	}
	path = absPath(path)
	root, rel, err := holdingRoot(roots, path)
	if err != nil {
		return err
	}
	if root == "" {
		return nil // outside every root, and missing
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open root %q to remove %q: %w", root, path, err)
	}
	defer r.Close()
	if err := r.RemoveAll(rel); err != nil {
		return fmt.Errorf("remove %q: %w", path, err)
	}
	return nil
}

// holdingRoot finds the root that holds path and path relative to it (see
// Remove). root == "" with a nil error means path is in none of them and does
// not exist.
func holdingRoot(roots []string, path string) (root, rel string, err error) {
	for _, r := range roots {
		if r == "" {
			continue
		}
		r = absPath(r)
		if samePath(r, path) {
			return "", "", fmt.Errorf("%q is the root %q itself; refusing to remove it", path, r)
		}
		if x, ok := relUnder(r, path); ok && len(r) > len(root) {
			root, rel = r, x
		}
	}
	if root != "" {
		return root, rel, nil
	}
	// Not written inside any root: find the nearest existing folder above path
	// that is a root under another spelling.
	infos := rootInfos(roots)
	for dir := path; ; dir = filepath.Dir(dir) {
		if info, serr := os.Stat(dir); serr == nil {
			for r, ri := range infos {
				if os.SameFile(info, ri) {
					if dir == path {
						return "", "", fmt.Errorf("%q is the root %q itself; refusing to remove it", path, r)
					}
					x, _ := filepath.Rel(dir, path)
					return r, x, nil
				}
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if _, lerr := os.Lstat(path); errors.Is(lerr, fs.ErrNotExist) {
		return "", "", nil
	}
	return "", "", fmt.Errorf("%q is not safely inside any of %q; refusing to remove it", path, roots)
}

// rootInfos stats every root that exists, keyed by its absolute path.
func rootInfos(roots []string) map[string]os.FileInfo {
	out := make(map[string]os.FileInfo, len(roots))
	for _, r := range roots {
		if r == "" {
			continue
		}
		r = absPath(r)
		if info, err := os.Stat(r); err == nil {
			out[r] = info
		}
	}
	return out
}

// samePath reports whether a and b are the same path once cleaned.
func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// absPath is p made absolute against the working directory (as the OS would
// read it), and cleaned. filepath.Abs only fails when the working directory
// can not be read; p is then returned cleaned, and relUnder refuses it against
// an absolute root.
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

// relUnder returns path relative to root when it is strictly inside it: a
// non-empty root, and a relative path that is not ".", "..", ".."-prefixed or
// absolute.
func relUnder(root, path string) (string, bool) {
	if root == "" || path == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// Inside reports whether path is dir itself or inside it, as written.
func Inside(dir, path string) bool {
	if samePath(absPath(dir), absPath(path)) {
		return true
	}
	_, ok := relUnder(absPath(dir), absPath(path))
	return ok
}
