package server

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// The tests below pin round 5j: a delete refuses for files left behind only
// while the lesson's OWN files are still in the old season folder (J1), and
// a refusal known up front comes before the delete begins (J3). The setting
// moved from <root>/old to <root>/new; the lesson was filed in
// <root>/old/Beginner Course/Season 01.

// ownMove is where a lesson's files are after the setting moved: the names
// left in the old season folder, and those in the same place in the new one.
type ownMove struct {
	name       string
	old, moved []string
	// sibling: another lesson's episode stays in the old folder.
	sibling    bool
	leftBehind bool
}

var siblingName = "Beginner Course - s01e06 - Lesson B.mp4"

var ownMoves = []ownMove{
	{name: "E1: every file moved to the same place, the old folder left empty", moved: leftBehindNames},
	{name: "only this lesson moved, a sibling's episode left in the old folder", moved: leftBehindNames, sibling: true},
	{name: "a partial move: the .nfo still in the old folder", old: leftBehindNames[1:2], moved: []string{leftBehindNames[0], leftBehindNames[2]}, leftBehind: true},
	{name: "E2: a copy kept in both folders", old: leftBehindNames, moved: leftBehindNames, leftBehind: true},
}

// TestDeleteLessonAsksForItsOwnFilesInTheOldFolder proves DELETE
// /api/lessons/{id} after the library setting moved: once the lesson's own
// files are out of the old season folder (emptied, or holding only a
// sibling's episode) it deletes them at their new place, as the refusal's
// "move them to the same place in the new one" promises; while any is still
// there (a partial move, a copy in both) it refuses, removing nothing.
func TestDeleteLessonAsksForItsOwnFilesInTheOldFolder(t *testing.T) {
	for _, m := range ownMoves {
		for _, recorded := range []bool{true, false} {
			t.Run(m.name+"/recorded="+strconv.FormatBool(recorded), func(t *testing.T) { checkOwnMoveDelete(t, m, recorded) })
		}
	}
}

// checkOwnMoveDelete runs one TestDeleteLessonAsksForItsOwnFilesInTheOldFolder
// case: lesson 1's files placed as m says, then DELETE /api/lessons/1.
func checkOwnMoveDelete(t *testing.T, m ownMove, recorded bool) {
	t.Helper()
	captureLog(t)
	store := newTestStore(t)
	root := t.TempDir()
	oldSeason := filepath.Join(root, "old", "Beginner Course", "Season 01")
	lib := filepath.Join(root, "new")
	newSeason := filepath.Join(lib, "Beginner Course", "Season 01")
	f := addFollow(t, store, 4242)
	seedSeasonLesson(t, store, f, 1, oldSeason, newSeason, recorded)
	m.place(t, oldSeason, newSeason)
	before := mustLesson(t, store, 1)
	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: filepath.Join(root, "dl"), LibraryDir: lib}, "test")

	rec := serveDelete(t, srv, "/api/lessons/1")
	if m.leftBehind {
		wantError(t, rec, http.StatusConflict, msgLessonLeftBehind)
		assertOnDisk(t, true, oldSeason, m.old...)
		assertOnDisk(t, true, newSeason, m.moved...)
		assertUnchanged(t, store, before)
		return
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s, want 200", rec.Code, rec.Body.String())
	}
	assertOnDisk(t, false, newSeason, leftBehindNames...)
	if m.sibling {
		assertOnDisk(t, true, oldSeason, siblingName)
	}
	if l := mustLesson(t, store, 1); l.Status != database.StatusSkipped || l.HasFiles() {
		t.Errorf("lesson = %q, has files %v; want tombstoned", l.Status, l.HasFiles())
	}
}

// place turns a lesson whose files are all in newSeason into m: it removes
// from newSeason every file m did not move, and makes m's old names (and the
// sibling's episode, when m has one) in oldSeason.
func (m ownMove) place(t *testing.T, oldSeason, newSeason string) {
	t.Helper()
	for _, n := range leftBehindNames {
		if !slices.Contains(m.moved, n) {
			if err := os.Remove(filepath.Join(newSeason, n)); err != nil {
				t.Fatal(err)
			}
		}
	}
	seedEntries(t, oldSeason, m.old...)
	if m.sibling {
		seedEntries(t, oldSeason, siblingName)
	}
}

// TestDeleteRefusesAnOldFolderItCantRead pins "when unsure, keep" through the
// API (round 5i security S2, its X3 probe; code Low 3): the old library
// folder at mode 000 with the lesson's files still in it. Both deletes refuse
// with the fixed 500 before they begin, remove nothing, and log the error
// (S3a). A delete that took the unreadable folder for a gone one would read
// the new folder, find nothing, and answer "deleted".
func TestDeleteRefusesAnOldFolderItCantRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a folder's mode doesn't refuse this user")
	}
	for _, recorded := range []bool{true, false} {
		t.Run("recorded="+strconv.FormatBool(recorded), func(t *testing.T) {
			log := captureLog(t)
			store := newTestStore(t)
			root := t.TempDir()
			oldRoot := filepath.Join(root, "old")
			oldSeason := filepath.Join(oldRoot, "Beginner Course", "Season 01")
			lib := filepath.Join(root, "new")
			if err := os.MkdirAll(lib, 0o755); err != nil {
				t.Fatal(err)
			}
			f := addFollow(t, store, 4242)
			seedSeasonLesson(t, store, f, 1, oldSeason, oldSeason, recorded)
			before := mustLesson(t, store, 1)
			if err := os.Chmod(oldRoot, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(oldRoot, 0o755) })
			srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: filepath.Join(root, "dl"), LibraryDir: lib}, "test")

			wantError(t, serveDelete(t, srv, "/api/lessons/1"), http.StatusInternalServerError, msgLessonNoClaimsUpFront)
			wantError(t, serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true"), http.StatusInternalServerError, msgFollowNoClaimsUpFront)
			if err := os.Chmod(oldRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			assertOnDisk(t, true, oldSeason, leftBehindNames...)
			assertUnchanged(t, store, before)
			if !strings.Contains(log.String(), oldSeason) || !strings.Contains(log.String(), "permission denied") {
				t.Errorf("log %q does not name the folder and the error", log.String())
			}
		})
	}
}

// abandonedRows counts the stop intents recorded (abandoned_jobs).
func abandonedRows(t *testing.T, path string) int {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM abandoned_jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRefusedDeletesStopNothing pins J3 (round 5i security S5): a delete
// refused for files left behind is refused before it begins. A follow delete
// then leaves another lesson's first download running (not killed, not
// turned into a skip) and a queued job queued, and records no "delete"
// intent, so the running download still records its files; a lesson delete
// leaves its own running re-download alone the same way.
func TestRefusedDeletesStopNothing(t *testing.T) {
	store, path := newTestStoreAt(t)
	ctx := t.Context()
	captureLog(t)
	root := t.TempDir()
	oldSeason := filepath.Join(root, "old", "Beginner Course", "Season 01")
	lib := filepath.Join(root, "new")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	f := addFollow(t, store, 4242)
	seedSeasonLesson(t, store, f, 1, oldSeason, oldSeason, true)
	firstDownload := enqueueInFollow(t, store, f, 2)
	claimJob(t, store, firstDownload)
	queued := enqueueInFollow(t, store, f, 3)
	var killed []int64
	deps := Deps{CancelRunning: func(id int64) bool { killed = append(killed, id); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: filepath.Join(root, "dl"), LibraryDir: lib}, "test")

	wantError(t, serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true"), http.StatusConflict, msgFollowLeftBehind)
	assertStoppedNothing(t, killed, path)
	if j, err := store.GetJob(ctx, queued); err != nil || j.Status != database.JobQueued {
		t.Errorf("queued job = %+v (err %v), want still queued", j, err)
	}
	if skip, err := store.ShouldSkipEnqueue(ctx, 2); err != nil || skip {
		t.Errorf("ShouldSkipEnqueue(2) = %v, %v; want the first download not turned into a skip", skip, err)
	}
	dir := filepath.Join(root, "dl", "F", "02 - Lesson 2")
	seedEntries(t, dir, "02 - Lesson 2.mp4")
	if err := store.FinishDownload(ctx, firstDownload, 2, database.DownloadRecord{Quality: "1080", OutputDir: dir, VideoPath: filepath.Join(dir, "02 - Lesson 2.mp4"), Bytes: 5}); err != nil {
		t.Fatalf("the running download's FinishDownload = %v, want it recorded", err)
	}
	if l := mustLesson(t, store, 2); l.Status != database.StatusDownloaded {
		t.Errorf("lesson 2 = %q, want downloaded", l.Status)
	}

	redownload := enqueueInFollow(t, store, f, 1)
	claimJob(t, store, redownload)
	wantError(t, serveDelete(t, srv, "/api/lessons/1"), http.StatusConflict, msgLessonLeftBehind)
	assertStoppedNothing(t, killed, path)
	if j, err := store.GetJob(ctx, redownload); err != nil || j.Status != database.JobRunning {
		t.Errorf("the lesson's re-download = %+v (err %v), want still running", j, err)
	}
}

// enqueueInFollow queues a job for lesson rcID of follow f, first adding the
// lesson (except lesson 1, which the caller seeded), and returns its id.
func enqueueInFollow(t *testing.T, store *database.Store, f int64, rcID int) int64 {
	t.Helper()
	ctx := t.Context()
	if rcID != 1 {
		if err := store.UpsertLesson(ctx, rcID, "Lesson "+strconv.Itoa(rcID), sql.NullInt64{}, "drumeo", sql.NullInt64{Int64: int64(rcID), Valid: true}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
			t.Fatal(err)
		}
	}
	id, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, rcID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// assertStoppedNothing fails if a download was killed (killed, what
// CancelRunning was asked to stop) or a stop intent recorded in the database
// at path.
func assertStoppedNothing(t *testing.T, killed []int64, path string) {
	t.Helper()
	if len(killed) != 0 {
		t.Errorf("killed %v, want no download stopped", killed)
	}
	if n := abandonedRows(t, path); n != 0 {
		t.Errorf("%d stop intents recorded, want none", n)
	}
}
