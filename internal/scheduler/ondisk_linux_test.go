package scheduler

import (
	"database/sql"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// TestRecordedFilesPresentRefusesAFIFO proves a recorded path counts only as
// a regular file (or, for a library entry, a folder): a FIFO at the recorded
// video, or at a recorded entry, is not the lesson's file on disk (security
// round 5d I2).
func TestRecordedFilesPresentRefusesAFIFO(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "lib")
	season := filepath.Join(lib, "Show", "Season 01")
	seedSeason(t, season, "Show - s01e01 - A.nfo")
	fifo := filepath.Join(season, "Show - s01e01 - A.en.vtt")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	w := &Worker{Cfg: Config{LibraryDir: lib}}
	for name, row := range map[string]database.Lesson{
		"the video": {OutputDir: str(season), VideoPath: str(fifo)},
		"an entry":  {OutputDir: str(season), LibraryEntries: database.EncodeLibraryEntries(recordOf(season, "Show - s01e01 - A.nfo", "Show - s01e01 - A.en.vtt"))},
	} {
		if w.recordedFilesPresent(row) {
			t.Errorf("%s a FIFO: recordedFilesPresent = true, want false", name)
		}
	}
}
