//go:build unix

package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
)

// TestRenameEpisodeFilesReadsOnlyARegularFile proves the rename reads an old
// name only when it is a regular file: a FIFO there (opening one to read
// waits for a writer that never comes) is not renamed, and the pass does not
// hang on it.
func TestRenameEpisodeFilesReadsOnlyARegularFile(t *testing.T) {
	w, store, _, _, season := renameWorker(t)
	seedSeason(t, season, renameBase+".mp4")
	if err := syscall.Mkfifo(filepath.Join(season, renameBase+"-poster.jpg"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	row := recordedRow(1, season, renameBase+".mp4", renameBase+"-poster.jpg")
	store.withFiles = []database.Lesson{row}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.RenameEpisodeFiles(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the rename hung on a FIFO at an old name")
	}
	assertExist(t, false, filepath.Join(season, renameBase+".jpg"))
	if got := store.row(t, 1).LibraryEntries; got != row.LibraryEntries {
		t.Errorf("record = %v, want it as it was", got)
	}
}

// TestRenameEpisodeFilesNeverWaitsOnAFIFOSwappedIn pins that a FIFO swapped
// in for an old name after its Lstat (which saw a regular file) does not
// hang the pass, and with it the daemon's cycles and its shutdown: the file
// is opened without waiting, found not to be the file the Lstat saw, and
// left alone. The FIFO gets the inode number the filesystem gives it: the
// removed file's on ext4 (CI's), which gives a freed number to the next file
// made at once, a new one on tmpfs and btrfs.
func TestRenameEpisodeFilesNeverWaitsOnAFIFOSwappedIn(t *testing.T) {
	checkFIFOSwappedIn(t, false)
}

// TestRenameEpisodeFilesNeverReadsAFIFOWithTheOldNumber is that swap with the
// FIFO given the removed file's inode number on every filesystem, as ext4
// gives it: the number matches, so only what the FIFO is (not a regular
// file) tells it apart. Taken for the old file, it read as empty: an empty
// image was made, recorded, and the FIFO removed in the old file's place.
func TestRenameEpisodeFilesNeverReadsAFIFOWithTheOldNumber(t *testing.T) {
	checkFIFOSwappedIn(t, true)
}

// checkFIFOSwappedIn runs the rename over a lesson whose old image is
// replaced by a FIFO between readRegular's Lstat and its open, the FIFO given
// the removed file's inode number when oldNumber, and fails unless the pass
// ends in time and leaves it alone: no new name, the record as it was, and
// the FIFO where it was.
func checkFIFOSwappedIn(t *testing.T, oldNumber bool) {
	t.Helper()
	if err := syscall.Mkfifo(filepath.Join(t.TempDir(), "probe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	w, store, _, _, season := renameWorker(t)
	old := renameBase + "-poster.jpg"
	seedSeason(t, season, renameBase+".mp4", old)
	row := recordedRow(1, season, renameBase+".mp4", old)
	store.withFiles = []database.Lesson{row}
	fifo := filepath.Join(season, old)
	var removed os.FileInfo // the old file, as it was before the swap
	origOpen, origStat := openRegular, statOpened
	t.Cleanup(func() { openRegular, statOpened = origOpen, origStat })
	openRegular = func(dir *os.Root, name string) (*os.File, error) {
		if name == old {
			removed = swapInFIFO(t, fifo)
		}
		return origOpen(dir, name)
	}
	statOpened = func(f *os.File) (os.FileInfo, error) {
		st, err := origStat(f)
		if oldNumber && err == nil && st.Mode()&os.ModeNamedPipe != 0 && !giveNumber(st, removed) {
			t.Error("the FIFO could not be given the removed file's inode number")
		}
		return st, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.RenameEpisodeFiles(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Free the reader stuck on the FIFO before failing.
		if f, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			f.Close()
		}
		<-done
		t.Fatal("the rename waited on a FIFO swapped in after the Lstat")
	}
	assertExist(t, false, filepath.Join(season, renameBase+".jpg"))
	if got := store.row(t, 1).LibraryEntries; got != row.LibraryEntries {
		t.Errorf("record = %v, want it as it was", got)
	}
	if fi, err := os.Lstat(fifo); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("%s = %v, %v; want the FIFO left where it was", fifo, fi, err)
	}
}

// swapInFIFO replaces the regular file at p with a FIFO and returns the file
// as it was. It runs in the pass's goroutine, so it reports with t.Error.
func swapInFIFO(t *testing.T, p string) os.FileInfo {
	was, err := os.Lstat(p)
	if err != nil {
		t.Error(err)
	}
	if err := os.Remove(p); err != nil {
		t.Error(err)
	}
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Error(err)
	}
	return was
}

// giveNumber gives info the device and inode number of other, as a
// filesystem that hands a freed number to the next file made shows them
// once info's file is removed and other made in its place (ext4 does, at
// once), and reports whether os.SameFile now takes the two for one file. It
// writes through Sys(), which on unix points into info itself.
func giveNumber(info, other os.FileInfo) bool {
	if info == nil || other == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	ost, ook := other.Sys().(*syscall.Stat_t)
	if !ok || !ook {
		return false
	}
	st.Dev, st.Ino = ost.Dev, ost.Ino
	return os.SameFile(info, other)
}

// removeCase is a case of TestRemoveIfSameRemovesOnlyTheFileItSaw: seen
// makes the entry "f" in dir and returns the file removeIfSame is told it
// saw there; remove is whether "f" is to go.
type removeCase struct {
	name   string
	seen   func(t *testing.T, dir string) os.FileInfo
	remove bool
}

// TestRemoveIfSameRemovesOnlyTheFileItSaw pins removeIfSame's check on every
// filesystem: it removes the entry at the name only when that is a regular
// file with the seen file's type, inode number, size and modification time.
// Each refused case differs from the entry in one of these alone. Where a
// case needs the numbers to match, the seen file is given the entry's
// (giveNumber), as ext4 gives a freed number to the next file made at once,
// so the filesystem's own numbering decides no case.
func TestRemoveIfSameRemovesOnlyTheFileItSaw(t *testing.T) {
	for _, c := range []removeCase{
		{"the file it saw, unchanged", seenUnchanged, true},
		{"nothing seen", seenNothing, false},
		{"another file of the same size and time", seenAnotherFile, false},
		{"the same number, another size", seenOtherSize, false},
		{"the same number, another modification time", seenOtherTime, false},
		{"a FIFO, seen as that very FIFO", seenTheFIFO, false},
		{"a regular file, with the number of the FIFO seen", seenAFIFOBefore, false},
	} {
		t.Run(c.name, func(t *testing.T) { checkRemoveIfSame(t, c) })
	}
}

// checkRemoveIfSame runs removeIfSame on the entry c.seen makes, and fails
// unless it is removed (and no error said) exactly when c.remove.
func checkRemoveIfSame(t *testing.T, c removeCase) {
	dir := t.TempDir()
	seen := c.seen(t, dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = removeIfSame(root, "f", seen)
	_, lerr := os.Lstat(filepath.Join(dir, "f"))
	if removed := errors.Is(lerr, os.ErrNotExist); removed != c.remove || (err == nil) != c.remove {
		t.Errorf("removeIfSame = %v, and the entry removed %v; want it removed %v", err, removed, c.remove)
	}
}

// seenAt is when the cases' files were last modified: whole seconds, which
// every filesystem's timestamps keep exactly.
var seenAt = time.Unix(1_700_000_000, 0)

func seenUnchanged(t *testing.T, dir string) os.FileInfo {
	return makeFile(t, dir, "f", "abc", seenAt)
}

func seenNothing(t *testing.T, dir string) os.FileInfo {
	makeFile(t, dir, "f", "abc", seenAt)
	return nil
}

// seenAnotherFile: another file, of f's size and time, but with its own number.
func seenAnotherFile(t *testing.T, dir string) os.FileInfo {
	makeFile(t, dir, "f", "abc", seenAt)
	return makeFile(t, dir, "g", "xyz", seenAt)
}

func seenOtherSize(t *testing.T, dir string) os.FileInfo {
	f := makeFile(t, dir, "f", "abc", seenAt)
	return withNumberOf(t, makeFile(t, dir, "g", "abcd", seenAt), f)
}

func seenOtherTime(t *testing.T, dir string) os.FileInfo {
	f := makeFile(t, dir, "f", "abc", seenAt)
	return withNumberOf(t, makeFile(t, dir, "g", "abc", seenAt.Add(time.Second)), f)
}

// seenTheFIFO: f is a FIFO, seen as itself; it differs from what it was in
// nothing, but it is not a regular file.
func seenTheFIFO(t *testing.T, dir string) os.FileInfo {
	return makeFIFO(t, dir, "f", seenAt)
}

// seenAFIFOBefore: f is an empty regular file, and what was seen is a FIFO of
// its size and time given f's number: they differ in type alone.
func seenAFIFOBefore(t *testing.T, dir string) os.FileInfo {
	f := makeFile(t, dir, "f", "", seenAt)
	return withNumberOf(t, makeFIFO(t, dir, "g", seenAt), f)
}

// makeFile writes name in dir holding data, modified at mod, and returns its
// Lstat.
func makeFile(t *testing.T, dir, name, data string, mod time.Time) os.FileInfo {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return modifiedAt(t, p, mod)
}

// makeFIFO makes the FIFO name in dir, modified at mod, and returns its Lstat.
func makeFIFO(t *testing.T, dir, name string, mod time.Time) os.FileInfo {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	return modifiedAt(t, p, mod)
}

// modifiedAt sets p's modification time to mod and returns its Lstat.
func modifiedAt(t *testing.T, p string, mod time.Time) os.FileInfo {
	t.Helper()
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// withNumberOf is info given other's device and inode number (giveNumber).
func withNumberOf(t *testing.T, info, other os.FileInfo) os.FileInfo {
	t.Helper()
	if !giveNumber(info, other) {
		t.Fatal("the seen file could not be given the entry's inode number")
	}
	return info
}
