//go:build !linux && !darwin

package scheduler

import (
	"os"
	"path/filepath"
)

// renameat renames src in from to dst in to BY PATH (os.Rename of the two
// folders' paths): Go offers no rename relative to open folders here
// (Windows). The move checked both folders through os.Root first, but a folder
// swapped for a symlink or junction between that check and this rename is
// followed, and an entry already at dst is replaced. The README says so.
func renameat(from *os.Root, src string, to *os.Root, dst string) error {
	return os.Rename(filepath.Join(from.Name(), src), filepath.Join(to.Name(), dst))
}
