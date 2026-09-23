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
	want := recordOf(season, base+".mp4", base+".nfo")
	if rec.outputDir != season || !reflect.DeepEqual(sorted(rec.entries), sorted(want)) {
		t.Errorf("record = %+v, want the season folder and exactly %v", rec, want)
	}
	if got, _ := os.ReadFile(filepath.Join(season, base+".nfo")); !strings.Contains(string(got), "<episodedetails>") {
		t.Errorf("episode nfo = %q, want the rewritten <episodedetails>", got)
	}
}

// TestWorkerPlexTvWritesNoEpisodeNFOTheMoveDidNotPlace proves the episode nfo
// is only ever written over the download's own nfo, before the move places it.
// A download that produced none moves without one, and the name it would have
// used may be another lesson's file: it is left alone, and the lesson records
// only what was placed.
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
	if rec := onlyRecord(t, store); !reflect.DeepEqual(rec.entries, recordOf(season, base+".mp4")) {
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
	if !reflect.DeepEqual(rec.entries, recordOf(season, mine...)) {
		t.Errorf("entries = %v, want the previous entry still owned", rec.entries)
	}
	if got, _ := os.ReadFile(filepath.Join(season, base+".mp4")); string(got) != base+".mp4" {
		t.Errorf("the other lesson's video was overwritten: %q", got)
	}
}

// TestWorkerPlexTvRetriesWhenTheOtherLessonsCannotBeRead (D58) proves that
// when the other lessons' files can not be read (the rows, or a damaged record
// among them), the worker neither moves (it could not tell whose entries are
// whose) nor records the lesson in scratch (a lesson moved before the record
// existed would lose track of its library copy): the attempt fails and is
// retried, and after the last one the lesson is failed with its previous
// record untouched.
func TestWorkerPlexTvRetriesWhenTheOtherLessonsCannotBeRead(t *testing.T) {
	for name, broken := range map[string]func(*fakeWorkerStore, string){
		"rows unreadable": func(s *fakeWorkerStore, _ string) { s.withFilesErr = errors.New("database is locked") },
		"damaged record": func(s *fakeWorkerStore, season string) {
			bad := recordedRow(7, season)
			bad.LibraryEntries = sql.NullString{String: "not json", Valid: true}
			s.withFiles = []database.Lesson{bad}
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, store, dl, lib, season := plexWorker(t)
			w.Cfg.MaxAttempts = 2
			store.lessons[100] = recordedRow(100, season, "Beginner Course - s01e05 - Lesson A.mp4")
			broken(store, season)
			var log bytes.Buffer
			w.Log = &log

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if len(store.markDownloaded) != 0 || !reflect.DeepEqual(store.markFailed, []int{100}) {
				t.Errorf("recorded %+v, failed %v; want nothing recorded and lesson 100 failed", store.markDownloaded, store.markFailed)
			}
			if n := len(dl.calls); n != 2 {
				t.Errorf("download attempts = %d, want 2 (a failed attempt is retried)", n)
			}
			if _, err := os.Stat(lib); !os.IsNotExist(err) {
				t.Errorf("library written (err=%v), want untouched", err)
			}
			if !strings.Contains(log.String(), "not moved to the library: the other lessons' files") {
				t.Errorf("log %q does not say why it did not move", log.String())
			}
		})
	}
}

// TestWorkerAbandonedMidMoveRemovesWhatItPlaced proves a delete that removes
// the lesson's files, landing while the download is being moved (after it was
// confirmed), gets what the download placed removed too, in both layouts; an
// entry a lesson row records by then is kept, and another lesson's files are
// never touched.
func TestWorkerAbandonedMidMoveRemovesWhatItPlaced(t *testing.T) {
	for _, layout := range []string{LayoutPlexTV, ""} {
		t.Run("layout="+layout, func(t *testing.T) {
			w, store, _, lib, season := plexWorker(t)
			w.Cfg.Layout = layout
			store.gone = map[int64]bool{}
			store.onConfirm = func() { store.gone[1] = true }
			neighbour := "Beginner Course - s01e06 - Lesson B.mp4"
			seedSeason(t, season, neighbour)
			seedSeason(t, filepath.Join(lib, "Beginner Course"), "06 - Lesson B/")
			store.withFiles = []database.Lesson{recordedRow(6, season, neighbour)}

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if len(store.markDownloaded) != 0 {
				t.Errorf("recorded %+v after the delete, want nothing", store.markDownloaded)
			}
			assertNoEpisodeIn(t, season, neighbour)
			assertExist(t, false, filepath.Join(lib, "Beginner Course", "05 - Lesson A"), filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A"))
			assertExist(t, true, filepath.Join(season, neighbour), filepath.Join(lib, "Beginner Course", "06 - Lesson B"))
		})
	}
}

// TestWorkerAbandonedKeepsWhatARowRecords proves the discard never removes a
// path a lesson row records by then (read fresh): a placed entry another row
// names, and a lesson folder a row records (the delete that abandoned the
// download could not remove it, and the row says so).
func TestWorkerAbandonedKeepsWhatARowRecords(t *testing.T) {
	t.Run("placed entry", func(t *testing.T) {
		w, store, _, _, season := plexWorker(t)
		store.gone = map[int64]bool{}
		base := "Beginner Course - s01e05 - Lesson A"
		store.onConfirm = func() {
			store.gone[1] = true
			store.withFiles = []database.Lesson{recordedRow(100, season, base+".mp4")}
		}
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		assertExist(t, true, filepath.Join(season, base+".mp4"))
		assertExist(t, false, filepath.Join(season, base+".nfo"))
	})
	t.Run("lesson folder", func(t *testing.T) {
		w, store, dl, _, _ := plexWorker(t)
		w.Cfg.LibraryDir = ""
		store.gone = map[int64]bool{}
		folder := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
		dl.afterWrite = func(string) {
			store.gone[1] = true
			store.withFiles = []database.Lesson{{RailcontentID: 100, OutputDir: sql.NullString{String: folder, Valid: true}}}
		}
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		assertExist(t, true, filepath.Join(folder, "05 - Lesson A.mp4"))
	})
	t.Run("rows unreadable", func(t *testing.T) {
		w, store, dl, _, _ := plexWorker(t)
		w.Cfg.LibraryDir = ""
		store.gone = map[int64]bool{}
		store.withFilesErr = errors.New("database is locked")
		dl.afterWrite = func(string) { store.gone[1] = true }
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		assertExist(t, true, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A", "05 - Lesson A.mp4"))
	})
}

// TestWorkerDefaultLayoutCarriesTheLibraryRecord proves a default-layout
// download leaves the lesson's library record as it is (after a layout switch
// its episode entries are still on disk and still its own) instead of
// dropping them untracked: it records no entries at all (nil), which
// FinishDownload reads as "unchanged". A damaged record is carried the same
// way, never parsed and dropped.
func TestWorkerDefaultLayoutCarriesTheLibraryRecord(t *testing.T) {
	for _, value := range []string{`["Beginner Course/Season 01/Beginner Course - s01e05 - Lesson A.mp4"]`, `not json`} {
		w, store, _, _, season := plexWorker(t)
		w.Cfg.Layout = ""
		prev := recordedRow(100, season)
		prev.LibraryEntries = sql.NullString{String: value, Valid: true}
		store.lessons[100] = prev
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if rec := onlyRecord(t, store); rec.entries != nil {
			t.Errorf("%s: entries = %v, want nil (the record left as it is)", value, rec.entries)
		}
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

// TestWorkerAbandonedWithoutALibrary proves that without a library (where the
// lesson folder is also the lesson's permanent home) a delete that removes the
// lesson's files gets the folder the download wrote removed too (the delete
// already removed what the row recorded, so what is there now is only this
// download's), while one that keeps the files (a follow removed without its
// files) leaves it whole.
func TestWorkerAbandonedWithoutALibrary(t *testing.T) {
	for _, discard := range []bool{true, false} {
		w, store, dl, _, _ := plexWorker(t)
		w.Cfg.LibraryDir = ""
		store.gone, store.kept = map[int64]bool{}, map[int64]bool{}
		dl.afterWrite = func(string) {
			if discard {
				store.gone[1] = true
			} else {
				store.kept[1] = true
			}
		}
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if len(store.markDownloaded) != 0 {
			t.Errorf("discard=%v: recorded %+v, want nothing", discard, store.markDownloaded)
		}
		assertExist(t, !discard, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A"))
	}
}

// TestWorkerKeepsFilesWhenTheDeleteKeepsThem (D60) proves
// a download stopped by a delete that keeps the files (a follow removed
// without its files) removes nothing, wherever it is stopped: mid-download
// (its scratch folder, which may hold a copy a row still records, stays whole,
// partial files included) or mid-move (what it placed, and the previous copy's
// replacement, stay).
func TestWorkerKeepsFilesWhenTheDeleteKeepsThem(t *testing.T) {
	for _, layout := range []string{LayoutPlexTV, ""} {
		t.Run("mid-download/layout="+layout, func(t *testing.T) {
			w, store, dl, _, _ := plexWorker(t)
			w.Cfg.Layout = layout
			store.kept = map[int64]bool{}
			scratch := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
			dl.afterWrite = func(dir string) {
				seedSeason(t, dir, "05 - Lesson A.mp4.part", "old-sheet.pdf")
				store.kept[1] = true
			}
			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			assertExist(t, true, filepath.Join(scratch, "05 - Lesson A.mp4"), filepath.Join(scratch, "old-sheet.pdf"), filepath.Join(scratch, "05 - Lesson A.mp4.part"))
		})
		t.Run("mid-move/layout="+layout, func(t *testing.T) {
			w, store, _, lib, season := plexWorker(t)
			w.Cfg.Layout = layout
			store.kept = map[int64]bool{}
			store.onConfirm = func() { store.kept[1] = true }
			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if layout == LayoutPlexTV {
				assertExist(t, true, filepath.Join(season, "Beginner Course - s01e05 - Lesson A.mp4"))
			} else {
				assertExist(t, true, filepath.Join(lib, "Beginner Course", "05 - Lesson A", "05 - Lesson A.mp4"))
			}
		})
	}
}

// TestWorkerHonoursADatabaseCancel (D60) proves a job canceled in the
// database while the worker holds it (before its process was registered, so
// no kill reached it) stops: before any download when canceled up front, and
// before the move when canceled during the download. The cancel is recorded,
// and nothing is moved.
func TestWorkerHonoursADatabaseCancel(t *testing.T) {
	t.Run("before the download", func(t *testing.T) {
		w, store, dl, _, _ := plexWorker(t)
		store.canceled = map[int64]bool{1: true}
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if len(dl.calls) != 0 || len(store.markDownloadng) != 0 {
			t.Errorf("downloads = %d, marked downloading %v; want none", len(dl.calls), store.markDownloadng)
		}
		if !reflect.DeepEqual(store.markJobCanceled, []int64{1}) || len(store.markDownloaded) != 0 {
			t.Errorf("canceled %v, recorded %+v; want the cancel recorded and nothing else", store.markJobCanceled, store.markDownloaded)
		}
	})
	t.Run("during the download", func(t *testing.T) {
		w, store, dl, lib, _ := plexWorker(t)
		store.canceled = map[int64]bool{}
		dl.afterWrite = func(string) { store.canceled[1] = true }
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !reflect.DeepEqual(store.markJobCanceled, []int64{1}) || len(store.markDownloaded) != 0 {
			t.Errorf("canceled %v, recorded %+v; want the cancel recorded and nothing else", store.markJobCanceled, store.markDownloaded)
		}
		if _, err := os.Stat(lib); !os.IsNotExist(err) {
			t.Errorf("library written (err=%v), want nothing moved", err)
		}
	})
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
