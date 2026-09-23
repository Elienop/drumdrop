//go:build darwin

package scheduler

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// renameat renames src in from to dst in to with renameatx_np on the two open
// folders, and RENAME_EXCL, so an entry already at dst fails the rename
// (EEXIST) instead of being replaced. A filesystem that does not support the
// flag (ENOTSUP, EINVAL) gets a plain renameat on the same open folders: still
// anchored, but it would replace an entry that appeared at dst after the move
// checked it was free.
func renameat(from *os.Root, src string, to *os.Root, dst string) error {
	ff, err := dirFile(from)
	if err != nil {
		return err
	}
	defer ff.Close()
	tf, err := dirFile(to)
	if err != nil {
		return err
	}
	defer tf.Close()
	err = unix.RenameatxNp(int(ff.Fd()), src, int(tf.Fd()), dst, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) {
		err = unix.Renameat(int(ff.Fd()), src, int(tf.Fd()), dst)
	}
	runtime.KeepAlive(ff)
	runtime.KeepAlive(tf)
	if err != nil {
		return &os.LinkError{Op: "renameatx_np", Old: filepath.Join(from.Name(), src), New: filepath.Join(to.Name(), dst), Err: err}
	}
	return nil
}
