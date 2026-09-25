package server

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// The tests below pin the check a delete makes again once it holds its
// lessons (round 5j J3's backstop): the disk or the records can change
// between the up-front check and Begin…Delete. The change is made while the
// delete stops the running download (Deps.CancelRunning runs after Begin and
// before the check), standing in for anything done elsewhere in that window.

// heldLesson files lesson rcID of follow f in <root>/old/Beginner Course/
// Season 01 with its files already moved to the same place under lib, so the
// up-front check lets the delete begin, and gives it a running download for
// the delete to stop. It returns the old and new season folders.
func heldLesson(t *testing.T, store *database.Store, f int64, rcID int, root, lib string) (oldSeason, newSeason string) {
	t.Helper()
	oldSeason = filepath.Join(root, "old", "Beginner Course", "Season 01")
	newSeason = filepath.Join(lib, "Beginner Course", "Season 01")
	seedSeasonLesson(t, store, f, rcID, oldSeason, newSeason, true)
	if err := os.MkdirAll(oldSeason, 0o755); err != nil {
		t.Fatal(err)
	}
	job, _, err := store.EnqueueJob(t.Context(), sql.NullInt64{Int64: f, Valid: true}, rcID)
	if err != nil {
		t.Fatal(err)
	}
	claimJob(t, store, job)
	return oldSeason, newSeason
}

// TestDeleteLessonChecksAgainOnceItHoldsTheLesson proves the lesson delete
// asks again after it begins: a file of the lesson put back in the old folder
// meanwhile refuses it (409, nothing removed), and a record damaged meanwhile
// refuses it with the sentence that says its download was stopped.
func TestDeleteLessonChecksAgainOnceItHoldsTheLesson(t *testing.T) {
	t.Run("a file put back in the old folder", func(t *testing.T) {
		captureLog(t)
		store := newTestStore(t)
		root := t.TempDir()
		lib := filepath.Join(root, "new")
		f := addFollow(t, store, 4242)
		oldSeason, newSeason := heldLesson(t, store, f, 1, root, lib)
		before := mustLesson(t, store, 1)
		deps := Deps{CancelRunning: func(int64) bool { seedEntries(t, oldSeason, leftBehindNames[1]); return true }}
		srv := NewServer(store, deps, nil, Config{DownloadsDir: filepath.Join(root, "dl"), LibraryDir: lib}, "test")

		wantError(t, serveDelete(t, srv, "/api/lessons/1"), http.StatusConflict, msgLessonLeftBehind)
		assertOnDisk(t, true, newSeason, leftBehindNames...)
		assertUnchanged(t, store, before)
	})
	t.Run("a record damaged meanwhile", func(t *testing.T) {
		captureLog(t)
		store, path := newTestStoreAt(t)
		root := t.TempDir()
		lib := filepath.Join(root, "new")
		f := addFollow(t, store, 4242)
		_, newSeason := heldLesson(t, store, f, 1, root, lib)
		before := mustLesson(t, store, 1)
		deps := Deps{CancelRunning: func(int64) bool {
			rawExec(t, path, `UPDATE lessons SET library_entries = '["/etc/passwd"]' WHERE railcontent_id = 1`)
			return true
		}}
		srv := NewServer(store, deps, nil, Config{DownloadsDir: filepath.Join(root, "dl"), LibraryDir: lib}, "test")

		wantError(t, serveDelete(t, srv, "/api/lessons/1"), http.StatusInternalServerError, msgLessonNoClaims)
		assertOnDisk(t, true, newSeason, leftBehindNames...)
		if l := mustLesson(t, store, 1); l.Status != before.Status || l.Deleting {
			t.Errorf("lesson = %q, deleting %v; want %q and the delete ended", l.Status, l.Deleting, before.Status)
		}
	})
}

// TestDeleteFollowChecksAgainBeforeTheFirstRemoval proves the follow delete
// asks again, for every lesson, before it removes anything: lesson 3's file
// put back in its old folder meanwhile refuses it (409), and lesson 2, which
// sorts first and could have been deleted, keeps its files (code round 5i,
// Low 2: a check made lesson by lesson would have removed them).
func TestDeleteFollowChecksAgainBeforeTheFirstRemoval(t *testing.T) {
	captureLog(t)
	store := newTestStore(t)
	root := t.TempDir()
	lib := filepath.Join(root, "new")
	downloads := filepath.Join(root, "dl")
	f := addFollow(t, store, 4242)
	own := filepath.Join(downloads, "F", "06 - Lesson B")
	if err := store.UpsertLesson(t.Context(), 2, "Lesson B", sql.NullInt64{}, "drumeo", sql.NullInt64{Int64: 6, Valid: true}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatal(err)
	}
	seedEntries(t, own, "06 - Lesson B.mp4")
	finishWithNewJob(t, store, f, 2, database.DownloadRecord{Quality: "1080", OutputDir: own, VideoPath: filepath.Join(own, "06 - Lesson B.mp4"), Bytes: 5})
	oldSeason, newSeason := heldLesson(t, store, f, 3, root, lib)
	before2, before3 := mustLesson(t, store, 2), mustLesson(t, store, 3)
	deps := Deps{CancelRunning: func(int64) bool { seedEntries(t, oldSeason, leftBehindNames[1]); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads, LibraryDir: lib}, "test")

	wantError(t, serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true"), http.StatusConflict, msgFollowLeftBehind)
	assertOnDisk(t, true, own, "06 - Lesson B.mp4")
	assertOnDisk(t, true, newSeason, leftBehindNames...)
	assertUnchanged(t, store, before2)
	assertUnchanged(t, store, before3)
	if follows, err := store.ListFollows(t.Context()); err != nil || len(follows) != 1 {
		t.Errorf("%d follows (err %v), want the follow kept", len(follows), err)
	}
}
