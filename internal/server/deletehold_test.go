package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
)

// seedRunningLesson is lesson rcID of follow f with a recorded download in
// dir and a running job, returned.
func seedRunningLesson(t *testing.T, store *database.Store, f int64, rcID int, dir string) int64 {
	t.Helper()
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, rcID, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	finishWithNewJob(t, store, f, rcID, database.DownloadRecord{OutputDir: dir, VideoPath: filepath.Join(dir, "v.mp4")})
	running, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, rcID)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, running)
	return running
}

// TestDeleteRenewsItsHoldWhileItRuns (security L3) proves a delete that runs
// longer than its lease keeps holding the lesson: it renews the lease while
// it runs, so a lease that ran out mid-delete is live again before the delete
// ends, and ended once it has.
func TestDeleteRenewsItsHoldWhileItRuns(t *testing.T) {
	orig := deleteRenewEvery
	deleteRenewEvery = 5 * time.Millisecond
	t.Cleanup(func() { deleteRenewEvery = orig })
	store, path := newTestStoreAt(t)
	downloads := t.TempDir()
	f := addFollow(t, store, 100)
	seedRunningLesson(t, store, f, 1, mkLessonDir(t, downloads, "C/01 - L"))
	var renewed bool
	deps := Deps{CancelRunning: func(int64) bool {
		rawExec(t, path, `UPDATE lessons SET deleting_until = datetime('now', '-1 second') WHERE railcontent_id = 1`)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && !renewed {
			time.Sleep(10 * time.Millisecond)
			renewed = mustLesson(t, store, 1).Deleting
		}
		return true
	}}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads}, "test")
	if rec := serveDelete(t, srv, "/api/lessons/1"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if !renewed {
		t.Error("a lease that ran out while the delete ran was never renewed")
	}
	if l := mustLesson(t, store, 1); l.Deleting {
		t.Error("the lesson is still held after the delete ended")
	}
}

// TestDeleteFollowRunsToTheEndWhenTheClientLeaves (security L4, M22c) proves
// a follow delete whose client goes away once it began still ends its hold on
// every lesson, so none is left refused to downloads.
func TestDeleteFollowRunsToTheEndWhenTheClientLeaves(t *testing.T) {
	store := newTestStore(t)
	downloads := t.TempDir()
	f := addFollow(t, store, 100)
	seedRunningLesson(t, store, f, 1, mkLessonDir(t, downloads, "C/01 - L"))
	reqCtx, leave := context.WithCancel(context.Background())
	deps := Deps{CancelRunning: func(int64) bool { leave(); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads}, "test")

	rec := httptest.NewRecorder()
	target := "/api/follows/" + strconv.FormatInt(f, 10) + "?files=true"
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, target, nil).WithContext(reqCtx))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d %s, want 204", rec.Code, rec.Body.String())
	}
	assertGone(t, downloads, "C/01 - L/")
	if _, err := store.GetLesson(t.Context(), 1); err == nil {
		t.Error("the follow's lesson survived its cascade")
	}
}

// TestDeleteFollowKillsWhatTheCascadeStops (code L5, m34) proves the
// follow's final cascade kills a download that started after the delete
// began (a lesson the planner found meanwhile): it is stopped too.
func TestDeleteFollowKillsWhatTheCascadeStops(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads := t.TempDir()
	f := addFollow(t, store, 100)
	first := seedRunningLesson(t, store, f, 1, mkLessonDir(t, downloads, "C/01 - L"))
	var late int64
	var killed []int64
	deps := Deps{CancelRunning: func(id int64) bool {
		killed = append(killed, id)
		if late == 0 {
			if err := store.UpsertLesson(ctx, 2, "New", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
				t.Fatalf("UpsertLesson: %v", err)
			}
			j, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 2)
			if err != nil {
				t.Fatalf("EnqueueJob: %v", err)
			}
			claimJob(t, store, j)
			late = j
		}
		return true
	}}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads}, "test")
	if rec := serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d %s, want 204", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(killed, []int64{first, late}) {
		t.Errorf("killed %v, want [%d %d]: the cascade's own stopped download too", killed, first, late)
	}
}

// TestDeleteRefusesAFolderOutsideEveryRoot (security M1) proves a
// default-layout lesson whose recorded folder is outside both roots (the
// library moved to a new path since) is never reported deleted: a fixed 500,
// the row keeps naming its files, the files at the new path stay, and the log
// says why.
func TestDeleteRefusesAFolderOutsideEveryRoot(t *testing.T) {
	store := newTestStore(t)
	tmp := t.TempDir()
	downloads, oldLib, newLib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "old-lib"), filepath.Join(tmp, "new-lib")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	f := addFollow(t, store, 100)
	dir := mkLessonDir(t, oldLib, "Course/05 - Five")
	if err := store.UpsertLesson(t.Context(), 1, "Five", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	finishWithNewJob(t, store, f, 1, database.DownloadRecord{OutputDir: dir, VideoPath: filepath.Join(dir, "v.mp4")})
	if err := os.Rename(oldLib, newLib); err != nil {
		t.Fatal(err)
	}
	log := captureLog(t)
	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: newLib}, "test")

	wantError(t, serveDelete(t, srv, "/api/lessons/1"), http.StatusInternalServerError, msgLessonFilesKept)
	l := mustLesson(t, store, 1)
	if l.Status != database.StatusDownloaded || l.OutputDir.String != dir || l.VideoPath.String != filepath.Join(dir, "v.mp4") || l.Deleting {
		t.Errorf("lesson = %+v, want it still downloaded, naming its folder and video", l)
	}
	assertPresent(t, filepath.Join(newLib, "Course", "05 - Five"), "v.mp4")
	if !strings.Contains(log.String(), "not reported as removed") {
		t.Errorf("log %q does not say why", log.String())
	}
}

// TestLessonFolderGuards (code M2, security L4 M14/M15) proves the two guards
// on removing a lesson's own folder, each with an input only it catches: a
// damaged output_dir naming a show folder (not "NN - Title") is refused, and
// so is a lesson folder that holds another lesson's recorded video.
func TestLessonFolderGuards(t *testing.T) {
	t.Run("shape", func(t *testing.T) {
		downloads, lib := t.TempDir(), t.TempDir()
		show := mkLessonDir(t, lib, "Show")
		if _, err := removeFiles(downloads, lib, dirLesson(1, show), nil); err == nil {
			t.Error("removing a show folder whole succeeded, want a refusal")
		}
		assertPresent(t, show, "v.mp4")
	})
	t.Run("holds", func(t *testing.T) {
		downloads, lib := t.TempDir(), t.TempDir()
		mine := mkLessonDir(t, lib, "Course/05 - Five")
		other := dirLesson(2, filepath.Join(mine, "nested"))
		other.VideoPath = sql.NullString{String: filepath.Join(mine, "v.mp4"), Valid: true}
		if _, err := removeFiles(downloads, lib, dirLesson(1, mine), []database.Lesson{other}); err == nil {
			t.Error("removing a folder holding another lesson's video succeeded, want a refusal")
		}
		assertPresent(t, mine, "v.mp4")
	})
}
