package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// The tests below pin owner rulings 2026-09-24 (f) and (i) by their reason
// (round-5c security M1, code L1): a refused library placement falls back to
// the downloads folder only when that fallback neither deletes nor stops
// recording a library file the lesson owns. What decides it is what the row
// records, written under either layout, not the layout configured today.

// refuseIntoSeason refuses (a permission error) every rename from outside
// lib into the season folder, as a full library disk fails a copy: the
// plex-tv move's placement fails and is undone.
func refuseIntoSeason(t *testing.T, lib, season string) {
	t.Helper()
	stubRename(t, func(oldpath, newpath string) error {
		if filepath.Dir(newpath) == season && !library.Inside(lib, oldpath) {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrPermission}
		}
		return renameNoReplace(oldpath, newpath)
	})
}

// assertKeptInLibrary fails unless the job recorded nothing, ended with
// failKeptInLibrary (the lesson's note and its kept note alike), and placed
// nothing in the downloads folder.
func assertKeptInLibrary(t *testing.T, w *Worker, store *fakeWorkerStore) {
	t.Helper()
	if len(store.markDownloaded) != 0 {
		t.Errorf("recorded %+v, want nothing (the library copy stays recorded)", store.markDownloaded)
	}
	if !reflect.DeepEqual(store.markFailed, []int{100}) || store.keptErr[100] != failKeptInLibrary.kept || store.jobs[1].Error.String != failKeptInLibrary.job {
		t.Errorf("failed %v, kept note %q, job %q; want lesson 100 ended with failKeptInLibrary", store.markFailed, store.keptErr[100], store.jobs[1].Error.String)
	}
	if p := findContent(t, w.Cfg.DownloadsDir, "new mp4"); p != "" {
		t.Errorf("the download was placed in downloads at %q", p)
	}
	assertNoReplacedArea(t, w)
}

// TestWorkerPlexTvRefusedMoveKeepsAFolderTheDefaultLayoutPlaced proves a
// plex-tv install whose row still records a lesson folder the default layout
// placed in the library (before a layout switch) keeps it when the plex-tv
// move is refused: the downloads fallback would replace that folder, or leave
// it untracked when it holds a file the download does not bring back. The
// attempt fails instead, the folder stays whole and recorded.
func TestWorkerPlexTvRefusedMoveKeepsAFolderTheDefaultLayoutPlaced(t *testing.T) {
	for _, how := range []string{"another lesson claims its name", "the placement fails"} {
		for _, extra := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/owner's file=%v", how, extra), func(t *testing.T) {
				w, store, _, lib, season := plexWorker(t)
				dir := filepath.Join(lib, "Beginner Course", "05 - Lesson A")
				files := map[string]string{"05 - Lesson A.mp4": "old mp4", "05 - Lesson A.nfo": "old nfo"}
				if extra {
					files["notes.txt"] = "owner notes"
				}
				writeTree(t, dir, files)
				prev := database.Lesson{
					RailcontentID: 100, Status: database.StatusDownloaded,
					Position:  sql.NullInt64{Int64: 5, Valid: true},
					OutputDir: sql.NullString{String: dir, Valid: true},
					VideoPath: sql.NullString{String: filepath.Join(dir, "05 - Lesson A.mp4"), Valid: true},
				}
				store.lessons[100] = prev
				store.withFiles = []database.Lesson{prev}
				if how == "the placement fails" {
					refuseIntoSeason(t, lib, season)
				} else {
					base := "Beginner Course - s01e05 - Lesson A"
					seedSeason(t, season, base+".mp4")
					store.withFiles = append(store.withFiles, recordedRow(200, season, base+".mp4"))
				}

				if _, err := w.RunOnce(context.Background(), 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				assertTree(t, dir, files)
				assertKeptInLibrary(t, w, store)
			})
		}
	}
}

// TestWorkerPlexTvRefusedMoveKeepsALegacyEpisodeItCanNotName proves a legacy
// row (no record: it owns its season-folder files only through its
// output_dir) whose episode the move can not tell apart from a look-alike is
// not moved to the downloads folder when the move fails: nothing would claim
// its files then. The attempt fails instead, and the season folder is as it
// was.
func TestWorkerPlexTvRefusedMoveKeepsALegacyEpisodeItCanNotName(t *testing.T) {
	w, store, _, lib, season := plexWorker(t)
	video := "Beginner Course - s01e05 - Old Title [Live] [Drumless].mp4"
	caps := "Beginner Course - s01e05 - Old Title [Live] [Drumless].en.vtt"
	seedSeason(t, season, video, caps)
	prev := legacyRow(100, "Lesson A", 5, season, video)
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}
	refuseIntoSeason(t, lib, season)

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertContent(t, season, video, caps)
	assertKeptInLibrary(t, w, store)
}

// TestWorkerPlexTvRefusedMoveOfALegacyRowRecordsWhatItOwns proves a legacy
// row whose episode files the move could name still falls back to the
// downloads folder when the move fails (ruling (i)): those files are
// recorded as the lesson's now, untouched, so none is left unclaimed.
func TestWorkerPlexTvRefusedMoveOfALegacyRowRecordsWhatItOwns(t *testing.T) {
	w, store, _, lib, season := plexWorker(t)
	old := []string{"Beginner Course - s01e05 - Old Title.mp4", "Beginner Course - s01e05 - Old Title.nfo"}
	seedSeason(t, season, old...)
	prev := legacyRow(100, "Old Title", 5, season, old[0])
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}
	refuseIntoSeason(t, lib, season)

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	rec := onlyRecord(t, store)
	if want := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A"); rec.outputDir != want {
		t.Errorf("recorded %q, want the downloads folder %q", rec.outputDir, want)
	}
	if !reflect.DeepEqual(sorted(rec.entries), sorted(recordOf(season, old...))) {
		t.Errorf("entries = %v, want the legacy files recorded %v", rec.entries, recordOf(season, old...))
	}
	assertContent(t, season, old...)
}

// TestWorkerDefaultLayoutRefusedPlacementOfASeasonFolderRow proves the
// default layout, for a row a plex-tv install wrote (before a layout switch):
// with a record of its season-folder entries, a refused placement falls back
// to the downloads folder, touching nothing in the season folder and leaving
// the record as it is; a legacy row, with no record, is not moved to the
// downloads folder, which would leave its season-folder files unclaimed.
func TestWorkerDefaultLayoutRefusedPlacementOfASeasonFolderRow(t *testing.T) {
	base := "Beginner Course - s01e05 - Lesson A"
	names := []string{base + ".mp4", base + ".nfo"}
	for _, recorded := range []bool{true, false} {
		t.Run(fmt.Sprintf("recorded=%v", recorded), func(t *testing.T) {
			w, store, _, lib, season := plexWorker(t)
			w.Cfg.Layout = ""
			var log bytes.Buffer
			w.Log = &log
			seedSeason(t, season, names...)
			prev := legacyRow(100, "Lesson A", 5, season, names[0])
			if recorded {
				prev.LibraryEntries = database.EncodeLibraryEntries(recordOf(season, names...))
			}
			store.lessons[100] = prev
			store.withFiles = []database.Lesson{prev}
			refuseIntoLessonFolder(t, lib, filepath.Join(lib, "Beginner Course", "05 - Lesson A"))

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			assertContent(t, season, names...)
			if !recorded {
				assertKeptInLibrary(t, w, store)
				return
			}
			rec := onlyRecord(t, store)
			want := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
			if rec.outputDir != want || rec.entries != nil {
				t.Errorf("record = %+v, want the downloads folder %q and the library record left as it is (nil)", rec, want)
			}
			if !strings.Contains(log.String(), fmt.Sprintf("⚠ 100 is kept in downloads at %q", want)) {
				t.Errorf("log %q does not say where the lesson was kept", log.String())
			}
		})
	}
}

// refuseFromJobInto refuses (a permission error) every rename from job 1's
// private download folder into dir, as a full disk fails a copy: the
// placement there fails and is undone. Only the download's own renames are
// refused, so the set-aside renames and their put-back go through wherever
// the roots are (the library may be, or hold, the downloads folder).
func refuseFromJobInto(t *testing.T, w *Worker, dir string) {
	t.Helper()
	job := filepath.Join(w.Cfg.DownloadsDir, privateRootName, "job-1")
	stubRename(t, func(oldpath, newpath string) error {
		if filepath.Dir(newpath) == dir && library.Inside(job, oldpath) {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrPermission}
		}
		return renameNoReplace(oldpath, newpath)
	})
}

// TestWorkerRefusedLibraryPlacementFallsBackIntoTheLessonsOwnFolder proves a
// refused library placement still falls back to the downloads folder when
// that fallback is the folder the lesson's row records (security round 5d
// F3): the library is the downloads folder, or holds it, and an earlier
// refused move kept the lesson in downloads. Placing it there replaces only
// the lesson's own files at the names the download brings back, keeps the
// rest, and the row goes on recording the same folder, so rulings (f) and (i)
// have nothing to keep: the download is recorded, not failed.
func TestWorkerRefusedLibraryPlacementFallsBackIntoTheLessonsOwnFolder(t *testing.T) {
	for _, c := range []struct {
		name, layout string
		nested       bool // the downloads folder is inside the library, not the library itself
	}{
		{"plex-tv, the library is the downloads folder", LayoutPlexTV, false},
		{"plex-tv, the downloads folder inside the library", LayoutPlexTV, true},
		{"default, the downloads folder inside the library", "", true},
	} {
		for _, how := range []string{"another lesson claims its place", "the placement fails"} {
			t.Run(c.name+"/"+how, func(t *testing.T) {
				w, store, dl, lib, season := plexWorker(t)
				w.Cfg.Layout = c.layout
				var log bytes.Buffer
				w.Log = &log
				w.Cfg.DownloadsDir = lib
				if c.nested {
					w.Cfg.DownloadsDir = filepath.Join(lib, "downloads")
				}
				dir := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
				writeTree(t, dir, map[string]string{"05 - Lesson A.mp4": "old mp4", "notes.txt": "owner notes"})
				prev := database.Lesson{
					RailcontentID: 100, Status: database.StatusDownloaded,
					Position:  sql.NullInt64{Int64: 5, Valid: true},
					OutputDir: sql.NullString{String: dir, Valid: true},
					VideoPath: sql.NullString{String: filepath.Join(dir, "05 - Lesson A.mp4"), Valid: true},
				}
				store.lessons[100] = prev
				store.withFiles = []database.Lesson{prev}
				// Where the library placement goes: the season folder, or the
				// lesson's folder in the library proper.
				target := season
				if c.layout == "" {
					target = filepath.Join(lib, "Beginner Course", "05 - Lesson A")
				}
				switch {
				case how == "the placement fails":
					refuseFromJobInto(t, w, target)
				case c.layout == LayoutPlexTV:
					base := "Beginner Course - s01e05 - Lesson A"
					seedSeason(t, season, base+".mp4")
					store.withFiles = append(store.withFiles, recordedRow(200, season, base+".mp4"))
				default:
					writeTree(t, target, map[string]string{"05 - Lesson A.mp4": "lesson 200"})
					store.withFiles = append(store.withFiles, database.Lesson{
						RailcontentID: 200, Status: database.StatusDownloaded,
						OutputDir: sql.NullString{String: target, Valid: true},
						VideoPath: sql.NullString{String: filepath.Join(target, "05 - Lesson A.mp4"), Valid: true},
					})
				}

				if _, err := w.RunOnce(context.Background(), 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				if len(dl.calls) != 1 || len(store.markFailed) != 0 {
					t.Fatalf("downloads %d, failed %v; want one download, recorded (log %q)", len(dl.calls), store.markFailed, log.String())
				}
				rec := onlyRecord(t, store)
				if rec.outputDir != dir || rec.videoPath != filepath.Join(dir, "05 - Lesson A.mp4") {
					t.Errorf("recorded %q, video %q; want the lesson's own folder %q", rec.outputDir, rec.videoPath, dir)
				}
				if got, err := os.ReadFile(filepath.Join(dir, "05 - Lesson A.mp4")); err != nil || string(got) != "new mp4" {
					t.Errorf("video = %q, %v; want the new download", got, err)
				}
				if got, err := os.ReadFile(filepath.Join(dir, "notes.txt")); err != nil || string(got) != "owner notes" {
					t.Errorf("notes.txt = %q, %v; want the owner's file kept", got, err)
				}
				if !strings.Contains(log.String(), fmt.Sprintf("⚠ 100 is kept in downloads at %q", dir)) {
					t.Errorf("log %q does not say where the lesson was kept", log.String())
				}
				assertNoReplacedArea(t, w)
			})
		}
	}
}
