//go:build unix

package scheduler

import (
	"context"
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
