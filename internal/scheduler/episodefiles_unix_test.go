//go:build unix

package scheduler

import (
	"context"
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
// left alone.
func TestRenameEpisodeFilesNeverWaitsOnAFIFOSwappedIn(t *testing.T) {
	w, store, _, _, season := renameWorker(t)
	old := renameBase + "-poster.jpg"
	seedSeason(t, season, renameBase+".mp4", old)
	row := recordedRow(1, season, renameBase+".mp4", old)
	store.withFiles = []database.Lesson{row}
	fifo := filepath.Join(season, old)
	orig := openRegular
	t.Cleanup(func() { openRegular = orig })
	openRegular = func(dir *os.Root, name string) (*os.File, error) {
		if name == old {
			if err := os.Remove(fifo); err != nil {
				t.Error(err)
			}
			if err := syscall.Mkfifo(fifo, 0o644); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
		}
		return orig(dir, name)
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
}
