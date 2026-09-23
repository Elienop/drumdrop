package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// scratchWriter is a downloader that writes one yt-dlp partial file into the
// lesson folder, runs during (when set), then fails: with the context's error
// once it was canceled (a Skip kills the download), else as yt-dlp does when
// Musora answers 403.
type scratchWriter struct {
	calls  int
	during func()
}

func (d *scratchWriter) Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls++
	base := fmt.Sprintf("%02d - %s", o.Index, musora.Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, base+".f137.mp4.part"), []byte("partial"), 0o644); err != nil {
		return err
	}
	if d.during != nil {
		d.during()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("yt-dlp: HTTP Error 403: Forbidden")
}

// TestWorkerKeepsWhatTheLessonFolderHeldBefore (security MEDIUM-1) proves a
// download that fails every attempt, or that a Skip stops, never removes what
// was in its lesson folder before the job began (a follow removed with its
// files kept, then followed again): with no library and with the library the
// downloads folder, that folder is the lesson's permanent home, and with a
// separate library it is a copy an earlier failed move left there. Only
// yt-dlp's partial file goes.
func TestWorkerKeepsWhatTheLessonFolderHeldBefore(t *testing.T) {
	kept := []string{"05 - Lesson A.mp4", "old-sheet.pdf"}
	for _, lib := range []string{"none", "downloads", "separate"} {
		for _, stop := range []string{"failure", "skip"} {
			t.Run(lib+"/"+stop, func(t *testing.T) {
				w, store, _, _, _ := plexWorker(t)
				w.Cfg.Layout = ""
				switch lib {
				case "none":
					w.Cfg.LibraryDir = ""
				case "downloads":
					w.Cfg.LibraryDir = w.Cfg.DownloadsDir
				}
				w.Cfg.MaxAttempts = 2
				scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
				seedSeason(t, scratch, kept...)
				dl := &scratchWriter{}
				if stop == "skip" {
					store.skipped = map[int64]bool{}
					dl.during = func() {
						store.skipped[1] = true // the Skip removed the job, then killed the download
						w.CancelRunning(1)
					}
				}
				w.Downloader = dl
				if _, err := w.RunOnce(context.Background(), 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				if stop == "failure" && (dl.calls != 2 || len(store.markFailed) != 1) {
					t.Errorf("attempts %d, failed %v; want 2 attempts and the lesson failed", dl.calls, store.markFailed)
				}
				assertContent(t, scratch, kept...)
				assertExist(t, false, filepath.Join(scratch, "05 - Lesson A.f137.mp4.part"))
			})
		}
	}
}

// findContent reports the path of a regular file under root whose content is
// want, or "".
func findContent(t *testing.T, root, want string) string {
	t.Helper()
	found := ""
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if b, _ := os.ReadFile(p); string(b) == want {
				found = p
			}
		}
		return nil
	})
	return found
}

// TestWorkerSkipDuringTheMoveKeepsWhatTheScratchFolderHeld (security MEDIUM-1)
// proves a Skip landing while a finished download is moved into a separate
// library keeps what the scratch folder held before the job: the move took it
// along, and the discard leaves it where the move put it, in both layouts.
func TestWorkerSkipDuringTheMoveKeepsWhatTheScratchFolderHeld(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		t.Run("layout="+layout, func(t *testing.T) {
			w, store, _, lib, _ := plexWorker(t)
			w.Cfg.Layout = layout
			scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
			seedSeason(t, scratch, "old-sheet.pdf")
			store.skipped = map[int64]bool{}
			store.onConfirm = func() { store.skipped[1] = true }
			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if len(store.markDownloaded) != 0 {
				t.Errorf("recorded %+v for a skipped lesson", store.markDownloaded)
			}
			if findContent(t, lib, "old-sheet.pdf") == "" && findContent(t, scratch, "old-sheet.pdf") == "" {
				t.Error("the file the scratch folder held before the job is gone")
			}
		})
	}
}

// TestWorkerDiscardStillCleansPartialsWhenTheFolderIsRefused (code
// Suggestions) proves a discard whose lesson folder can not be removed whole,
// because another lesson records something in it, still removes yt-dlp's
// partial files, and keeps everything else.
func TestWorkerDiscardStillCleansPartialsWhenTheFolderIsRefused(t *testing.T) {
	w, store, _, _, _ := plexWorker(t)
	w.Cfg.MaxAttempts = 1
	store.skipped = map[int64]bool{}
	w.Downloader = &failingWriter{onFail: func() { store.skipped[1] = true }}
	scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
	store.withFiles = []database.Lesson{{RailcontentID: 200, OutputDir: sql.NullString{String: scratch, Valid: true}}}
	var log bytes.Buffer
	w.Log = &log
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertExist(t, true, filepath.Join(scratch, "05 - Lesson A.mp4"))
	assertExist(t, false, filepath.Join(scratch, "05 - Lesson A.f137.mp4.part"))
	if !strings.Contains(log.String(), "kept the lesson folder") {
		t.Errorf("log %q does not say the folder was kept", log.String())
	}
}

// TestWorkerMovesNoPartialFileIntoTheLibrary (security Info 2) proves a
// finished download's scratch folder loses the partial files an earlier run
// left there (yt-dlp's ffmpeg ".temp.<ext>" output, drumdrop's own temporary
// nfo files) before the move, in both layouts, so none lands in the library.
func TestWorkerMovesNoPartialFileIntoTheLibrary(t *testing.T) {
	leftovers := []string{"05 - Lesson A.temp.mp4", "05 - Lesson A.nfo.drumdrop-part", "05 - Lesson A.nfo.drumdrop-episode"}
	for _, layout := range []string{"", LayoutPlexTV} {
		t.Run("layout="+layout, func(t *testing.T) {
			w, store, dl, lib, _ := plexWorker(t)
			w.Cfg.Layout = layout
			dl.afterWrite = func(dir string) { seedSeason(t, dir, leftovers...) }
			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			onlyRecord(t, store)
			for _, n := range leftovers {
				if p := findContent(t, lib, n); p != "" {
					t.Errorf("the leftover %q was moved into the library at %q", n, p)
				}
			}
		})
	}
}
