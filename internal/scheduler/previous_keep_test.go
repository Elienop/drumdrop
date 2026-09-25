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
	"runtime"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// TestWorkerKeepsAPreviousFolderItCannotRead (round-5b code L4, security L3)
// proves "when unsure, keep" for a read error: a previous folder under an
// old title with a subfolder that can not be read is kept whole, untouched,
// and logged, even though the download brings back everything that could be
// read (previous.go: the walk's error is never skipped, and previousStays
// never answers "may go" for a folder it could not read).
func TestWorkerKeepsAPreviousFolderItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a folder whatever its mode")
	}
	c := previousCases[0] // title changed, no library
	w, store, log := previousWorker(t, c)
	dir, files := c.seed(t, w, store, map[string]string{"resources/deep/mine.pdf": "owner file"})
	deep := filepath.Join(dir, "resources", "deep")
	chmodAllOnCleanup(t, filepath.Dir(w.Cfg.DownloadsDir))
	if err := os.Chmod(deep, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if err := os.Chmod(deep, 0o755); err != nil {
		t.Fatalf("the unreadable folder is not where it was: %v", err)
	}
	assertTree(t, dir, files)
	if rec := onlyRecord(t, store); rec.outputDir != c.placedIn(w) {
		t.Errorf("recorded %q, want %q", rec.outputDir, c.placedIn(w))
	}
	if want := fmt.Sprintf("⚠ 100 left its previous folder %q where it was, no longer recorded: it could not be read", dir); !strings.Contains(log.String(), want) {
		t.Errorf("log %q does not say %s", log.String(), want)
	}
	assertNoReplacedArea(t, w)
}

// chmodAllOnCleanup makes every folder under root readable again before the
// test's temporary folders are removed, wherever a mutant may have moved an
// unreadable one.
func chmodAllOnCleanup(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if d != nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
}

// TestWorkerLogsAPreviousFolderItMayNotTouch (round-5b security I3) proves
// the two previous folders a placement may never touch, one another lesson
// records something in and one outside the downloads folder and the library
// (the library moved, say), are left as they were and logged with why, as
// every other previous folder that stays is; in the default layout and in
// plex-tv (round-5c code L4), whose move asks the same question.
func TestWorkerLogsAPreviousFolderItMayNotTouch(t *testing.T) {
	for _, pc := range []previousCase{previousCases[0], previousCases[3]} {
		t.Run(pc.name, func(t *testing.T) { logsAPreviousFolderItMayNotTouch(t, pc) })
	}
}

// logsAPreviousFolderItMayNotTouch is TestWorkerLogsAPreviousFolderItMayNotTouch
// in pc's setup, placed where pc places the re-download.
func logsAPreviousFolderItMayNotTouch(t *testing.T, pc previousCase) {
	for _, c := range []struct {
		name string
		// dir is the previous folder, under the downloads folder dl.
		dir func(dl string) string
		// other, when set, is another lesson recording a file in dir.
		other bool
		why   string
	}{
		{"another lesson records a file in it", func(dl string) string { return filepath.Join(dl, "Beginner Course", "05 - Old Title") }, true, "lessons [200] record files in it"},
		{"outside every root", func(dl string) string {
			return filepath.Join(filepath.Dir(dl), "moved", "Beginner Course", "05 - Old Title")
		}, false, "it is inside neither the downloads folder nor the library"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, store, log := previousWorker(t, pc)
			dir, files := seedLessonFolder(t, store, c.dir(w.Cfg.DownloadsDir), ownerExtras)
			if c.other {
				store.withFiles = append(store.withFiles, database.Lesson{
					RailcontentID: 200, Status: database.StatusDownloaded,
					VideoPath: sql.NullString{String: filepath.Join(dir, "05 - Old Title.mp4"), Valid: true},
				})
			}

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if rec := onlyRecord(t, store); rec.outputDir != pc.placedIn(w) {
				t.Errorf("recorded %q, want %q", rec.outputDir, pc.placedIn(w))
			}
			assertTree(t, dir, files)
			if want := fmt.Sprintf("⚠ 100 left its previous folder %q where it was, no longer recorded: %s", dir, c.why); !strings.Contains(log.String(), want) {
				t.Errorf("log %q does not say %s", log.String(), want)
			}
		})
	}
}

// failsSecond is a downloader whose second call fails as yt-dlp does on a
// 403; every other call is inner's.
type failsSecond struct {
	inner Downloader
	calls int
}

func (d *failsSecond) Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls++
	if d.calls == 2 {
		return errors.New("ERROR: unable to download video data: HTTP Error 403: Forbidden")
	}
	return d.inner.Download(ctx, l, o)
}

// TestWorkerJobEndsWithTheLastAttemptsFailure (round-5b code L7, its probe
// D) proves the failure a job ends with is its last attempt's, reset on each
// attempt: attempt 1 is refused in the library (errKeptInLibrary), attempt 2
// fails in yt-dlp, so the job ends with failDownload, not failKeptInLibrary.
func TestWorkerJobEndsWithTheLastAttemptsFailure(t *testing.T) {
	w, store, _ := setupWorker(t, "separate", "")
	w.Cfg.MaxAttempts = 2
	dir, _ := seedLessonFolder(t, store, filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Lesson A"), nil)
	refuseIntoLessonFolder(t, w.Cfg.LibraryDir, dir)
	var log bytes.Buffer
	w.Log = &log
	w.Downloader = &failsSecond{inner: w.Downloader}

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !strings.Contains(log.String(), "attempt 1/2 failed: "+errKeptInLibrary.Error()) || !strings.Contains(log.String(), "attempt 2/2 failed: ERROR: unable to download") {
		t.Fatalf("log %q: want attempt 1 refused in the library, attempt 2 failed in yt-dlp", log.String())
	}
	if got := store.jobs[1].Error.String; got != failDownload.job {
		t.Errorf("job error = %q, want %q", got, failDownload.job)
	}
	if store.lessonErr[100] != failDownload.lesson || store.keptErr[100] != msgEarlierKept {
		t.Errorf("lesson error %q, kept note %q; want failDownload's", store.lessonErr[100], store.keptErr[100])
	}
}

// TestWorkerPlexTvMergeOfARecordSpelledAnotherWay (round-5b code I2) pins,
// on any disk, that a recorded entry the download places at under another
// spelling (here its show folder reached through a symlink; a case-only
// title change on a case-insensitive disk is the real case, which only the
// casefold test can make) is the lesson's own: the replaced video is logged
// as the lesson's earlier download (ours[dst]), and the recorded resources
// folder is merged, never decided on as a previous folder and logged as left
// behind (placedAt).
func TestWorkerPlexTvMergeOfARecordSpelledAnotherWay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	w, store, _, lib, season := plexWorker(t)
	w.Downloader = partialRedownload{}
	w.Cfg.MaxAttempts = 1
	var log bytes.Buffer
	w.Log = &log
	base := "Beginner Course - s01e05 - Lesson A"
	seedSeason(t, season, base+".mp4", base+" resources/")
	if err := os.Symlink("Beginner Course", filepath.Join(lib, "Alias")); err != nil {
		t.Fatal(err)
	}
	prev := recordedRow(100, filepath.Join(lib, "Alias", "Season 01"), base+".mp4", base+" resources/")
	prev.OutputDir = sql.NullString{String: season, Valid: true}
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	onlyRecord(t, store)
	if want := fmt.Sprintf("↻ 100 replaced %q (the lesson's earlier download)", filepath.Join(season, base+".mp4")); !strings.Contains(log.String(), want) {
		t.Errorf("log %q does not say %s", log.String(), want)
	}
	if strings.Contains(log.String(), "left its previous folder") {
		t.Errorf("log %q says a folder was left behind", log.String())
	}
	assertTree(t, filepath.Join(season, base+" resources"), map[string]string{
		"f.pdf":      base + " resources",
		"a.pdf":      "new a",
		"deep/x.pdf": "new x",
	})
}
