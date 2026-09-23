package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// realWorker is a worker over a real, migrated store holding lesson 100
// ("Lesson A", position 5) of a node follow, with a library in the given
// layout; it returns the worker, the store, the follow id and the scratch
// lesson folder the download writes.
func realWorker(t *testing.T, layout string) (*Worker, *database.Store, *fakeDownloader, int64, string) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	s := database.NewStore(db)
	t.Cleanup(func() { s.Close() })
	f, err := s.AddNodeFollow(ctx, 4242, "Beginner Course", "drumeo", "1080")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertLesson(ctx, 100, "Lesson A", sql.NullInt64{}, "drumeo", sql.NullInt64{Int64: 5, Valid: true}, sql.NullInt64{Int64: f.ID, Valid: true}); err != nil {
		t.Fatal(err)
	}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("new mp4")
	tmp := t.TempDir()
	w := newTestWorker(s, fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = filepath.Join(tmp, "dl")
	w.Cfg.LibraryDir = filepath.Join(tmp, "lib")
	w.Cfg.Layout = layout
	return w, s, dl, f.ID, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
}

// TestWorkerSkipSticks (code M1) proves a Skip, queued or mid-download, is
// never undone by the download: nothing is recorded, the lesson stays
// skipped with its reason, and what the download wrote is gone.
func TestWorkerSkipSticks(t *testing.T) {
	for _, layout := range []string{LayoutPlexTV, ""} {
		for _, when := range []string{"queued", "mid-download"} {
			t.Run(when+"/layout="+layout, func(t *testing.T) {
				ctx := context.Background()
				w, s, dl, f, scratch := realWorker(t, layout)
				if _, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 100); err != nil {
					t.Fatal(err)
				}
				skip := func() {
					if _, err := s.SkipLesson(ctx, 100, "not wanted"); err != nil {
						t.Fatalf("SkipLesson: %v", err)
					}
				}
				if when == "queued" {
					skip()
				} else {
					dl.afterWrite = func(string) { skip() }
				}
				if _, err := w.RunOnce(ctx, 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				l, err := s.GetLesson(ctx, 100)
				if err != nil {
					t.Fatal(err)
				}
				if l.Status != database.StatusSkipped || l.Error.String != "not wanted" || l.OutputDir.Valid || l.LibraryEntries.Valid {
					t.Errorf("lesson = %+v, want skipped with its reason and no files", l)
				}
				assertExist(t, false, scratch, filepath.Join(w.Cfg.LibraryDir, "Beginner Course"))
			})
		}
	}
}

// TestWorkerFailsBeforeDownloadingWhenTheLessonCannotBeRead (code L2) proves
// a lesson row that can not be read is never taken for "owns nothing": the
// job fails without downloading, and nothing is moved or recorded.
func TestWorkerFailsBeforeDownloadingWhenTheLessonCannotBeRead(t *testing.T) {
	w, store, dl, lib, _ := plexWorker(t)
	store.getLessonErr = errors.New("database is locked")
	var log bytes.Buffer
	w.Log = &log
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(dl.calls) != 0 || len(store.markDownloaded) != 0 || !reflect.DeepEqual(store.markFailed, []int{100}) {
		t.Errorf("downloads %d, recorded %+v, failed %v; want no download, lesson 100 failed", len(dl.calls), store.markDownloaded, store.markFailed)
	}
	assertExist(t, false, lib)
	if !strings.Contains(log.String(), "the lesson's record could not be read") {
		t.Errorf("log %q does not say why", log.String())
	}
}

// failingWriter is a downloader that writes a lesson's video and a partial
// file into its folder, then fails, as a download that dies at the end does.
type failingWriter struct {
	calls int
	// onFail, when set, runs before each failure.
	onFail func()
}

func (d *failingWriter) Download(_ context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls++
	base := fmt.Sprintf("%02d - %s", o.Index, musora.Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, n := range []string{base + ".mp4", base + ".f137.mp4.part"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err != nil {
			return err
		}
	}
	if d.onFail != nil {
		d.onFail()
	}
	return errors.New("ffmpeg died")
}

// TestWorkerFinalFailureLeavesNothingUntracked (code L1) proves a download
// that fails every attempt leaves no copy in downloads that no lesson records:
// its scratch folder is removed, unless a lesson row records something in it
// (as the lesson's own earlier download, recorded there after a failed move),
// and then only yt-dlp's partial files go.
func TestWorkerFinalFailureLeavesNothingUntracked(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			w, store, _, _, _ := plexWorker(t)
			dl := &failingWriter{}
			w.Downloader = dl
			w.Cfg.MaxAttempts = 2
			scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
			if held {
				store.withFiles = []database.Lesson{{RailcontentID: 100, OutputDir: sql.NullString{String: scratch, Valid: true}}}
			}
			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if dl.calls != 2 || !reflect.DeepEqual(store.markFailed, []int{100}) {
				t.Errorf("attempts %d, failed %v; want 2 and lesson 100 failed", dl.calls, store.markFailed)
			}
			assertExist(t, held, filepath.Join(scratch, "05 - Lesson A.mp4"))
			assertExist(t, false, filepath.Join(scratch, "05 - Lesson A.f137.mp4.part"))
		})
	}
}

// TestWorkerFailureStoppedByADeleteAppliesItsIntent (code L1) proves a
// download that fails after a delete or a skip removed its job applies what
// the stopper wanted, like one stopped mid-download: a discard removes the
// scratch folder, a keep-files removal keeps what finished and drops only the
// partial files.
func TestWorkerFailureStoppedByADeleteAppliesItsIntent(t *testing.T) {
	for _, discard := range []bool{true, false} {
		t.Run(fmt.Sprintf("discard=%v", discard), func(t *testing.T) {
			w, store, _, _, _ := plexWorker(t)
			store.skipped, store.kept = map[int64]bool{}, map[int64]bool{}
			w.Cfg.MaxAttempts = 1
			w.Downloader = &failingWriter{onFail: func() {
				if discard {
					store.skipped[1] = true
				} else {
					store.kept[1] = true
				}
			}}
			scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if len(store.markFailed) != 0 {
				t.Errorf("failure recorded for a stopped job: %v", store.markFailed)
			}
			assertExist(t, !discard, filepath.Join(scratch, "05 - Lesson A.mp4"))
			assertExist(t, false, filepath.Join(scratch, "05 - Lesson A.f137.mp4.part"))
		})
	}
}

// TestWorkerUnrecordedDownloadIsNotReportedDone (code Info) proves a download
// FinishDownload could not record is a failed attempt, never a success: no
// download_ok event and no ✓, and the lesson ends failed.
func TestWorkerUnrecordedDownloadIsNotReportedDone(t *testing.T) {
	w, store, _, _, _ := plexWorker(t)
	w.Cfg.MaxAttempts = 1
	store.finishErr = errors.New("disk I/O error")
	sink := &recordingSink{}
	w.Progress = sink
	var log bytes.Buffer
	w.Log = &log
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := sink.kinds()["download_ok"]; n != 0 || strings.Contains(log.String(), "✓") {
		t.Errorf("an unrecorded download was reported done (download_ok %d, log %q)", n, log.String())
	}
	if !reflect.DeepEqual(store.markFailed, []int{100}) {
		t.Errorf("failed %v, want lesson 100", store.markFailed)
	}
}

// TestWorkerCancelOfARequeuedJobCleansPartials (code L5, m43) proves a cancel
// whose job another process requeued meanwhile still removes the killed
// download's partial files, and records nothing.
func TestWorkerCancelOfARequeuedJobCleansPartials(t *testing.T) {
	w, store, dl, _, _ := plexWorker(t)
	store.canceled, store.requeued = map[int64]bool{}, map[int64]bool{}
	scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
	dl.afterWrite = func(dir string) {
		seedSeason(t, dir, "05 - Lesson A.mp4.part")
		store.canceled[1], store.requeued[1] = true, true
	}
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(store.markDownloaded) != 0 || len(store.markJobCanceled) != 0 {
		t.Errorf("recorded %+v, canceled %v; want nothing", store.markDownloaded, store.markJobCanceled)
	}
	assertExist(t, false, filepath.Join(scratch, "05 - Lesson A.mp4.part"))
	assertExist(t, true, filepath.Join(scratch, "05 - Lesson A.mp4"))
}

// TestCleanupPartialsRemovesOnlyPartials (security I4) proves the partial-file
// cleanup takes only yt-dlp's partial shapes of the lesson's own base: a
// finished subtitle, a title with dots in it, and another base's partials
// stay.
func TestCleanupPartialsRemovesOnlyPartials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "05 - Five")
	partials := []string{
		"05 - Five.mp4.part",
		"05 - Five.f137.mp4",
		"05 - Five.f251.webm.part",
		"05 - Five.mp4.ytdl",
		"05 - Five.f137.mp4.part-Frag12",
		"05 - Five [Original].f399.mp4",
	}
	kept := []string{
		"05 - Five.mp4",
		"05 - Five.fr.vtt",
		"05 - Five.fil.vtt",
		"05 - Five.nfo",
		"05 - Five [Original].mp4",
		"05 - Five.f137",
		"05 - Five.fx1.mp4",
		"Fill.for.fun.mp4",
		"06 - Six.mp4.part",
	}
	seedSeason(t, dir, append(append([]string{}, partials...), kept...)...)
	cleanupPartials(dir)
	assertExist(t, false, paths(dir, partials...)...)
	assertExist(t, true, paths(dir, kept...)...)
}

// TestWorkerPlexDiscardLeavesTheSeasonFolderQuietly (code M2, m19) proves a
// plex-tv download stopped after its move removes only what it placed: the
// season folder, and an episode in it no row records, stay, and the discard
// reports success rather than a refused folder.
func TestWorkerPlexDiscardLeavesTheSeasonFolderQuietly(t *testing.T) {
	w, store, _, _, season := plexWorker(t)
	untracked := "Beginner Course - s01e02 - Kept Untracked.mp4"
	seedSeason(t, season, untracked)
	store.gone = map[int64]bool{}
	store.onConfirm = func() { store.gone[1] = true }
	var log bytes.Buffer
	w.Log = &log
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertExist(t, true, filepath.Join(season, untracked))
	assertNoEpisodeIn(t, season, untracked)
	if !strings.Contains(log.String(), "removed what it had written") || strings.Contains(log.String(), "not everything") {
		t.Errorf("log %q, want a clean discard", log.String())
	}
}

// TestWorkerDefaultMoveRefusesAFolderAnotherLessonHolds (code Info) proves
// the default-layout move never replaces a library folder another lesson
// records (the same position and title in one course folder): the lesson
// stays whole in downloads and is recorded there.
func TestWorkerDefaultMoveRefusesAFolderAnotherLessonHolds(t *testing.T) {
	w, store, _, lib, _ := plexWorker(t)
	w.Cfg.Layout = ""
	theirs := filepath.Join(lib, "Beginner Course", "05 - Lesson A")
	seedSeason(t, theirs, "05 - Lesson A.mp4")
	store.withFiles = []database.Lesson{{RailcontentID: 200, OutputDir: sql.NullString{String: theirs, Valid: true}}}
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(theirs, "05 - Lesson A.mp4")); string(got) != "05 - Lesson A.mp4" {
		t.Errorf("the other lesson's video was replaced: %q", got)
	}
	scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
	if rec := onlyRecord(t, store); rec.outputDir != scratch {
		t.Errorf("recorded %q, want the scratch folder %q", rec.outputDir, scratch)
	}
}
