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

// The tests below pin the owner's ruling 2026-09-24 (f) (round-5 fix code
// seat M1): when the placement into the library fails and the lesson's row
// records its folder in the library, it is not placed in downloads instead
// (that fallback set the library folder aside whole, and deleted it with the
// owner's files once the download was recorded). The attempt fails, the
// library copy stays recorded and untouched, and after its attempts the job
// fails with failKeptInLibrary. A lesson not in the library yet still falls
// back to downloads.

// refuseIntoLessonFolder refuses (a permission error) every rename from
// outside root into its folder dir, as the code seat's probe did: the
// placement's renames (from the private download folder) fail; the set-aside
// renames and their put-back (within root) go through.
func refuseIntoLessonFolder(t *testing.T, root, dir string) {
	t.Helper()
	stubRename(t, func(oldpath, newpath string) error {
		if filepath.Dir(newpath) == dir && !library.Inside(root, oldpath) {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrPermission}
		}
		return renameNoReplace(oldpath, newpath)
	})
}

// TestWorkerLibraryPlacementFailureKeepsTheLibraryCopy proves a lesson whose
// folder is recorded in the library keeps it whole and recorded when the
// placement there fails every attempt: nothing is placed in downloads,
// nothing is recorded, and the job fails with failKeptInLibrary, the
// placement's error in the log.
func TestWorkerLibraryPlacementFailureKeepsTheLibraryCopy(t *testing.T) {
	w, store, dl := setupWorker(t, "separate", "")
	w.Cfg.MaxAttempts = 2
	var log bytes.Buffer
	w.Log = &log
	dir := filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Lesson A")
	files := map[string]string{
		"05 - Lesson A.mp4":      "old mp4",
		"notes.txt":              "owner notes",
		"resources/b.pdf":        "old b",
		"resources/my-notes.txt": "mine",
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
	refuseIntoLessonFolder(t, w.Cfg.LibraryDir, dir)

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertTree(t, dir, files)
	if len(store.markDownloaded) != 0 {
		t.Errorf("recorded %+v, want nothing (the library copy stays recorded)", store.markDownloaded)
	}
	if dl.calls != 2 {
		t.Errorf("attempts = %d, want 2", dl.calls)
	}
	if !reflect.DeepEqual(store.markFailed, []int{100}) || store.lessonErr[100] != failKeptInLibrary.lesson || store.jobs[1].Error.String != failKeptInLibrary.job {
		t.Errorf("failed %v, lesson %q, job %q; want lesson 100 failed with failKeptInLibrary", store.markFailed, store.lessonErr[100], store.jobs[1].Error.String)
	}
	assertExist(t, false, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course"))
	if p := findContent(t, w.Cfg.DownloadsDir, "new mp4"); p != "" {
		t.Errorf("the download was placed in downloads at %q", p)
	}
	if !strings.Contains(log.String(), "⚠ move to library 100: ") {
		t.Errorf("log %q does not name the placement's error", log.String())
	}
	assertNoReplacedArea(t, w)
}

// TestWorkerLibraryPlacementFailureFallsBackForALessonNotInTheLibrary proves
// the fallback to downloads still applies to a lesson never downloaded, and
// to one whose row records its folder in downloads (the library was added
// later): the download is placed and recorded there.
func TestWorkerLibraryPlacementFailureFallsBackForALessonNotInTheLibrary(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(fmt.Sprintf("recorded in downloads=%v", recorded), func(t *testing.T) {
			w, store, _ := setupWorker(t, "separate", "")
			want := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
			if recorded {
				seedLessonFolder(t, store, want, nil)
			}
			lib := filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Lesson A")
			refuseIntoLessonFolder(t, w.Cfg.LibraryDir, lib)

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if rec := onlyRecord(t, store); rec.outputDir != want {
				t.Errorf("recorded %q, want the downloads folder %q", rec.outputDir, want)
			}
			if got, err := os.ReadFile(filepath.Join(want, "05 - Lesson A.mp4")); err != nil || string(got) != "new mp4" {
				t.Errorf("video = %q, %v; want the new download in downloads", got, err)
			}
		})
	}
}
