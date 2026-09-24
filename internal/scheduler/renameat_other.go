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
//
// That is worse for a set-aside (asideArea) than for a placement: a folder the
// placement sets an entry aside from, swapped for a junction there, moves an
// entry OUTSIDE the library into the set-aside area, and once the download is
// recorded the commit deletes that area, so the outside entry is deleted
// (security L2, BACKLOG D72). That folder is the lesson folder (the season
// folder in plex-tv), and also every subfolder merged entry by entry
// (mergeFolder), at any depth: each is a swap point of its own.
func renameat(from *os.Root, src string, to *os.Root, dst string) error {
	return os.Rename(filepath.Join(from.Name(), src), filepath.Join(to.Name(), dst))
}
