package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// plexWorker builds a plex-tv worker over one queued job for lesson 100 at
// position 5 ("Lesson A" in the "Beginner Course" show), with a downloader that
// writes a real video and nfo, and returns it with its store, library root and
// season folder.
func plexWorker(t *testing.T) (w *Worker, store *fakeWorkerStore, dl *fakeDownloader, lib, season string) {
	t.Helper()
	store = newFakeWorkerStore(queuedJob(1, nodeFollow().ID, 100))
	store.follows[nodeFollow().ID] = nodeFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100, Position: sql.NullInt64{Int64: 5, Valid: true}}
	dl = newFakeDownloader()
	dl.writeMP4 = []byte("new mp4")
	tmp := t.TempDir()
	lib = filepath.Join(tmp, "lib")
	w = newTestWorker(store, fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = filepath.Join(tmp, "dl")
	w.Cfg.LibraryDir = lib
	w.Cfg.Layout = LayoutPlexTV
	return w, store, dl, lib, filepath.Join(lib, "Beginner Course", "Season 01")
}

func onlyRecord(t *testing.T, store *fakeWorkerStore) markDownloadedCall {
	t.Helper()
	if len(store.markDownloaded) != 1 {
		t.Fatalf("recorded %d downloads, want 1: %+v", len(store.markDownloaded), store.markDownloaded)
	}
	return store.markDownloaded[0]
}

// TestWorkerPlexTvRecordsWhatTheMovePlaced proves the worker records, with the
// download, exactly the season-folder entries the move placed (the episode nfo
// it rewrites included).
func TestWorkerPlexTvRecordsWhatTheMovePlaced(t *testing.T) {
	w, store, _, _, season := plexWorker(t)
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	rec := onlyRecord(t, store)
	base := "Beginner Course - s01e05 - Lesson A"
	want := paths(season, base+".mp4", base+".nfo")
	if rec.outputDir != season || !reflect.DeepEqual(sorted(rec.entries), sorted(want)) {
		t.Errorf("record = %+v, want the season folder and exactly %v", rec, want)
	}
	if got, _ := os.ReadFile(want[1]); !strings.Contains(string(got), "<episodedetails>") {
		t.Errorf("episode nfo = %q, want the rewritten <episodedetails>", got)
	}
}

// TestWorkerPlexTvWritesNoEpisodeNFOTheMoveDidNotPlace proves the episode nfo
// is only ever written over the nfo the move itself placed. A download that
// produced none moves without one, and the name it would have used, which the
// move never checked, may be another lesson's file: it is left alone, and the
// lesson records only what was placed.
func TestWorkerPlexTvWritesNoEpisodeNFOTheMoveDidNotPlace(t *testing.T) {
	w, store, dl, _, season := plexWorker(t)
	dl.afterWrite = func(dir string) {
		if err := os.Remove(filepath.Join(dir, "05 - Lesson A.nfo")); err != nil {
			t.Errorf("drop the download's nfo: %v", err)
		}
	}
	base := "Beginner Course - s01e05 - Lesson A"
	seedSeason(t, season, base+".nfo")
	store.withFiles = []database.Lesson{recordedRow(200, season, base+".nfo")}

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(season, base+".nfo")); string(got) != base+".nfo" {
		t.Errorf("the other lesson's nfo = %q, want it untouched", got)
	}
	if rec := onlyRecord(t, store); !reflect.DeepEqual(rec.entries, paths(season, base+".mp4")) {
		t.Errorf("entries = %v, want only the placed video", rec.entries)
	}
}

// TestWorkerPlexTvReDownloadReplacesByRecord proves a re-download removes the
// lesson's previously recorded entries (here under an old title) and never
// another lesson's at the same episode number.
func TestWorkerPlexTvReDownloadReplacesByRecord(t *testing.T) {
	w, store, _, _, season := plexWorker(t)
	mine := []string{"Beginner Course - s01e05 - Old Name.mp4", "Beginner Course - s01e05 - Old Name resources/"}
	theirs := []string{"Beginner Course - s01e05 - Lesson A [Live].mp4", "Beginner Course - s01e05 - Lesson A-Part 2.mp4"}
	seedSeason(t, season, mine...)
	seedSeason(t, season, theirs...)
	prev := recordedRow(100, season, mine...)
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev, recordedRow(200, season, theirs[0]), legacyRow(300, "Lesson A-Part 2", 5, season, theirs[1])}

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertExist(t, false, paths(season, mine...)...)
	assertExist(t, true, paths(season, theirs...)...)
	if rec := onlyRecord(t, store); len(rec.entries) != 2 {
		t.Errorf("recorded entries %v, want the 2 new ones", rec.entries)
	}
}

// TestWorkerPlexTvRefusedMoveKeepsThePreviousRecord proves a move refused
// because another lesson owns one of its names records the lesson in scratch
// and still records the previous library entries as its own.
func TestWorkerPlexTvRefusedMoveKeepsThePreviousRecord(t *testing.T) {
	w, store, _, _, season := plexWorker(t)
	base := "Beginner Course - s01e05 - Lesson A"
	mine := []string{"Beginner Course - s01e05 - Lesson A (old).mp4"}
	seedSeason(t, season, mine...)
	seedSeason(t, season, base+".mp4")
	prev := recordedRow(100, season, mine...)
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{recordedRow(200, season, base+".mp4")}

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	rec := onlyRecord(t, store)
	scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
	if rec.outputDir != scratch || rec.videoPath != filepath.Join(scratch, "05 - Lesson A.mp4") {
		t.Errorf("record = %+v, want the scratch folder", rec)
	}
	if !reflect.DeepEqual(rec.entries, paths(season, mine...)) {
		t.Errorf("entries = %v, want the previous entry still owned", rec.entries)
	}
	if got, _ := os.ReadFile(filepath.Join(season, base+".mp4")); string(got) != base+".mp4" {
		t.Errorf("the other lesson's video was overwritten: %q", got)
	}
}

// TestWorkerPlexTvDoesNotMoveWithoutTheOtherLessons proves that when the other
// lessons' files can not be read, the worker does not move at all (it could
// not tell whose entries are whose): the lesson is recorded in scratch and
// keeps its previous record.
func TestWorkerPlexTvDoesNotMoveWithoutTheOtherLessons(t *testing.T) {
	w, store, _, lib, season := plexWorker(t)
	prev := recordedRow(100, season, "Beginner Course - s01e05 - Lesson A.mp4")
	store.lessons[100] = prev
	store.withFilesErr = errors.New("database is locked")
	var log bytes.Buffer
	w.Log = &log

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	rec := onlyRecord(t, store)
	if !strings.HasPrefix(rec.outputDir, w.Cfg.DownloadsDir) || !reflect.DeepEqual(rec.entries, paths(season, "Beginner Course - s01e05 - Lesson A.mp4")) {
		t.Errorf("record = %+v, want scratch plus the previous record", rec)
	}
	if _, err := os.Stat(lib); !os.IsNotExist(err) {
		t.Errorf("library written (err=%v), want untouched", err)
	}
	if !strings.Contains(log.String(), "not moved, the other lessons' files could not be read") {
		t.Errorf("log %q does not say why it did not move", log.String())
	}
}

// TestWorkerDefaultLayoutCarriesTheLibraryRecord proves a default-layout
// download keeps the lesson's previous library record (after a layout switch
// its episode entries are still on disk and still its own) instead of
// dropping them untracked.
func TestWorkerDefaultLayoutCarriesTheLibraryRecord(t *testing.T) {
	w, store, _, _, season := plexWorker(t)
	w.Cfg.Layout = ""
	prev := recordedRow(100, season, "Beginner Course - s01e05 - Lesson A.mp4")
	store.lessons[100] = prev
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rec := onlyRecord(t, store); !reflect.DeepEqual(rec.entries, paths(season, "Beginner Course - s01e05 - Lesson A.mp4")) {
		t.Errorf("entries = %v, want the previous record carried", rec.entries)
	}
}

// TestWorkerAbandonedDownloadLeavesNothingUntracked covers the delete race: a
// delete removes the job while the download is being moved. The worker records
// nothing and removes what it wrote, in both layouts, so once the delete has
// answered no file of that download is left without a row.
func TestWorkerAbandonedDownloadLeavesNothingUntracked(t *testing.T) {
	for _, layout := range []string{LayoutPlexTV, ""} {
		t.Run("layout="+layout, func(t *testing.T) {
			w, store, dl, lib, season := plexWorker(t)
			w.Cfg.Layout = layout
			store.gone = map[int64]bool{}
			dl.afterWrite = func(string) { store.gone[1] = true } // the delete lands mid-job
			// Another lesson's files next to where this one lands: never touched.
			neighbour := "Beginner Course - s01e06 - Lesson B.mp4"
			seedSeason(t, season, neighbour)
			seedSeason(t, filepath.Join(lib, "Beginner Course"), "06 - Lesson B/")

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if len(store.markDownloaded) != 0 || len(store.markDone) != 0 {
				t.Errorf("recorded %+v / done %v after the delete, want nothing", store.markDownloaded, store.markDone)
			}
			if layout == LayoutPlexTV {
				assertNoEpisodeIn(t, season, neighbour)
			} else {
				assertExist(t, false, filepath.Join(lib, "Beginner Course", "05 - Lesson A"))
			}
			assertExist(t, true, filepath.Join(season, neighbour), filepath.Join(lib, "Beginner Course", "06 - Lesson B"))
			assertExist(t, false, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A"))
		})
	}
}

// TestWorkerAbandonedWithoutALibraryLeavesTheFolder proves that without a
// library the lesson folder is left alone when the job is abandoned: it is the
// lesson's permanent home, which the delete itself removes (and a follow
// deleted with its files kept must keep).
func TestWorkerAbandonedWithoutALibraryLeavesTheFolder(t *testing.T) {
	w, store, dl, _, _ := plexWorker(t)
	w.Cfg.LibraryDir = ""
	store.gone = map[int64]bool{}
	dl.afterWrite = func(string) { store.gone[1] = true }
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(store.markDownloaded) != 0 {
		t.Errorf("recorded %+v, want nothing", store.markDownloaded)
	}
	assertExist(t, true, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A", "05 - Lesson A.mp4"))
}

// TestWorkerStopsWhenDeletedBetweenAttempts proves an abandoned job stops at the
// next attempt instead of downloading again, and records no failure.
func TestWorkerStopsWhenDeletedBetweenAttempts(t *testing.T) {
	w, store, dl, _, _ := plexWorker(t)
	w.Cfg.MaxAttempts = 3
	dl.alwaysFail = true
	store.gone = map[int64]bool{}
	w.sleep = func(time.Duration) { store.gone[1] = true } // deleted during the backoff

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := len(dl.calls); n != 1 {
		t.Errorf("download attempts = %d, want 1 (stop once deleted)", n)
	}
	if len(store.markFailed) != 0 || len(store.markJobFailed) != 0 {
		t.Errorf("failure recorded for a deleted job: %v / %v", store.markFailed, store.markJobFailed)
	}
}
