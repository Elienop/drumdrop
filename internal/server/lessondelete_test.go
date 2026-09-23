package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// seedPlexLesson files lesson rcID of followID in season through the real
// download path (a job, then FinishDownload) with a record of exactly names,
// which it creates on disk. The recorded video is the first ".mp4" in names.
func seedPlexLesson(t *testing.T, store *database.Store, followID int64, rcID int, title, season string, names ...string) {
	t.Helper()
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, rcID, title, sql.NullInt64{}, "drumeo", sql.NullInt64{Int64: 5, Valid: true}, sql.NullInt64{Int64: followID, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	seedEntries(t, season, names...)
	rec := database.DownloadRecord{Quality: "1080", OutputDir: season, Bytes: 5}
	rec.LibraryEntries = recordOf(season, names...)
	for _, n := range names {
		if rec.VideoPath == "" && strings.HasSuffix(n, ".mp4") {
			rec.VideoPath = filepath.Join(season, n)
		}
	}
	finishWithNewJob(t, store, followID, rcID, rec)
}

// finishWithNewJob records rec for lesson rcID the way the worker does: a new
// job, claimed, then FinishDownload under it.
func finishWithNewJob(t *testing.T, store *database.Store, followID int64, rcID int, rec database.DownloadRecord) {
	t.Helper()
	ctx := t.Context()
	jobID, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: followID, Valid: true}, rcID)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, jobID)
	if err := store.FinishDownload(ctx, jobID, rcID, rec); err != nil {
		t.Fatalf("FinishDownload: %v", err)
	}
}

// claimJob claims queued jobs, oldest first, until job id is running (the
// worker's claim), failing if it never comes up.
func claimJob(t *testing.T, store *database.Store, id int64) {
	t.Helper()
	for {
		j, ok, err := store.ClaimNextJob(t.Context())
		if err != nil || !ok {
			t.Fatalf("job %d was never claimed (ok=%v, err=%v)", id, ok, err)
		}
		if j.ID == id {
			return
		}
	}
}

func addFollow(t *testing.T, store *database.Store, rcID int) int64 {
	t.Helper()
	f, err := store.AddNodeFollow(t.Context(), rcID, "F", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	return f.ID
}

func serveDelete(t *testing.T, srv http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, target, nil))
	return rec
}

// wantError asserts a JSON error response with exactly code and msg.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	var body errorBody
	if rec.Code != code || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error != msg {
		t.Fatalf("response = %d %s, want %d {\"error\":%q}", rec.Code, rec.Body.String(), code, msg)
	}
}

func mustLesson(t *testing.T, store *database.Store, id int) database.Lesson {
	t.Helper()
	l, err := store.GetLesson(t.Context(), id)
	if err != nil {
		t.Fatalf("GetLesson(%d): %v", id, err)
	}
	return l
}

// TestDeleteLessonPlexTvRemovesOnlyItsEntries drives a plex-tv lesson delete
// through DELETE /api/lessons/{id}: of two lessons at one episode number
// ("Five" and "Five [Live]", recorded), only the deleted one's entries go, its
// row is tombstoned with no record, and the other lesson's row and files stay.
func TestDeleteLessonPlexTvRemovesOnlyItsEntries(t *testing.T) {
	store := newTestStore(t)
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	f := addFollow(t, store, 100)
	seedPlexLesson(t, store, f, 1, "Five", season, fiveLookAlikes["Five"]...)
	seedPlexLesson(t, store, f, 2, "Five [Live]", season, fiveLookAlikes["Five [Live]"]...)
	before2 := mustLesson(t, store, 2)
	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	if rec := serveDelete(t, srv, "/api/lessons/1"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	assertGone(t, season, fiveLookAlikes["Five"]...)
	assertPresent(t, season, fiveLookAlikes["Five [Live]"]...)
	if l := mustLesson(t, store, 1); l.Status != database.StatusSkipped || l.OutputDir.Valid || l.LibraryEntries.Valid {
		t.Errorf("lesson 1 = %+v, want tombstoned with no paths and no record", l)
	}
	if after2 := mustLesson(t, store, 2); !reflect.DeepEqual(after2, before2) {
		t.Errorf("lesson 2 changed: %+v, want %+v", after2, before2)
	}
}

// TestDeleteFollowFilesPlexTvRemovesOnlyItsLessonsEntries drives
// DELETE /api/follows/{id}?files=true over a season folder shared with another
// follow: the follow's recorded lesson and its legacy lesson (moved before the
// record existed, D51) lose their entries; the other follow's lesson at the same
// episode number keeps its row and files, and the season folder stays.
func TestDeleteFollowFilesPlexTvRemovesOnlyItsLessonsEntries(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	mine, theirs := addFollow(t, store, 100), addFollow(t, store, 200)
	seedPlexLesson(t, store, mine, 1, "Five", season, fiveLookAlikes["Five"]...)
	seedPlexLesson(t, store, theirs, 2, "Five-Part Fill", season, fiveLookAlikes["Five-Part Fill"]...)

	// A legacy lesson of mine at episode 3: a song, recorded the old way.
	song := []string{"Show - s01e03 - Three [Drumless].mp4", "Show - s01e03 - Three [Original].mp4", "Show - s01e03 - Three.nfo", "Show - s01e03 - Three play-along/"}
	seedEntries(t, season, song...)
	if err := store.UpsertLesson(ctx, 3, "Three", sql.NullInt64{}, "drumeo", sql.NullInt64{Int64: 3, Valid: true}, sql.NullInt64{Int64: mine, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	finishWithNewJob(t, store, mine, 3, database.DownloadRecord{Quality: "1080", OutputDir: season, VideoPath: filepath.Join(season, song[0]), Bytes: 5})
	before2 := mustLesson(t, store, 2)
	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	if rec := serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(mine, 10)+"?files=true"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	assertGone(t, season, fiveLookAlikes["Five"]...)
	assertGone(t, season, song...)
	assertPresent(t, season, fiveLookAlikes["Five-Part Fill"]...)
	assertPresent(t, library, "Show/Season 01/")
	if _, err := store.GetFollow(ctx, mine); err == nil {
		t.Error("follow still present after delete")
	}
	if after2 := mustLesson(t, store, 2); !reflect.DeepEqual(after2, before2) {
		t.Errorf("the other follow's lesson changed: %+v, want %+v", after2, before2)
	}
}

// TestDeleteFollowFilesRemovesAnEntryOnlyItsOwnLessonsShared proves an entry
// two lessons of the same follow both record (identical titles at one episode)
// goes with the follow: each lesson alone must keep it, since the other still
// claims it, but once the first is deleted the second is its only claimant.
func TestDeleteFollowFilesRemovesAnEntryOnlyItsOwnLessonsShared(t *testing.T) {
	store := newTestStore(t)
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	f := addFollow(t, store, 100)
	shared := "Show - s01e05 - Same.mp4"
	seedPlexLesson(t, store, f, 1, "Same", season, shared, "Show - s01e05 - Same.nfo")
	seedPlexLesson(t, store, f, 2, "Same", season, shared, "Show - s01e05 - Same-poster.jpg")
	captureLog(t)
	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	if rec := serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	assertGone(t, season, shared, "Show - s01e05 - Same.nfo", "Show - s01e05 - Same-poster.jpg")
}

// TestDeleteLessonKeepsTheLessonWhenAFileCannotBeRemoved (D56) proves a lesson
// whose files could not all be removed keeps its paths and a record of exactly
// what is left, and the caller is told: a fixed 500 (no path, no OS detail),
// with the detail in the server log. The entries that could go are gone.
func TestDeleteLessonKeepsTheLessonWhenAFileCannotBeRemoved(t *testing.T) {
	store := newTestStore(t)
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	stuck := "Show - s01e05 - Five resources"
	lockedEntry(t, season, stuck)
	f := addFollow(t, store, 100)
	seedPlexLesson(t, store, f, 1, "Five", season, stuck+"/", "Show - s01e05 - Five.mp4")
	log := captureLog(t)
	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	rec := serveDelete(t, srv, "/api/lessons/1")
	wantError(t, rec, http.StatusInternalServerError, msgLessonFilesKept)
	assertGone(t, season, "Show - s01e05 - Five.mp4")
	l := mustLesson(t, store, 1)
	entries, recorded, err := l.PlacedEntries()
	if l.Status != database.StatusDownloaded || !l.OutputDir.Valid || err != nil || !recorded || !reflect.DeepEqual(entries, recordOf(season, stuck)) {
		t.Errorf("lesson = %+v (entries %v, err %v), want still downloaded, recording only %q", l, entries, err, stuck)
	}
	if !strings.Contains(log.String(), filepath.Join(season, stuck)) || !strings.Contains(log.String(), "permission denied") {
		t.Errorf("server log %q lacks the failing path and cause", log.String())
	}
}

// TestDeleteFollowFilesKeepsTheFollowWhenAFileCannotBeRemoved (D56) proves the
// follow cascade never drops a row whose files remain: when one lesson's files
// can not all be removed, the follow and every lesson row stay (the failing one
// recording what is left), the other lessons' files are still removed, and the
// caller gets a fixed 500.
func TestDeleteFollowFilesKeepsTheFollowWhenAFileCannotBeRemoved(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	stuck := "Show - s01e05 - Five resources"
	lockedEntry(t, season, stuck)
	f := addFollow(t, store, 100)
	seedPlexLesson(t, store, f, 1, "Five", season, stuck+"/", "Show - s01e05 - Five.mp4")
	seedPlexLesson(t, store, f, 2, "Six", season, "Show - s01e06 - Six.mp4")
	captureLog(t)
	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	rec := serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true")
	wantError(t, rec, http.StatusInternalServerError, msgFollowFilesKept)
	if _, err := store.GetFollow(ctx, f); err != nil {
		t.Errorf("follow removed (err=%v), want kept", err)
	}
	if l := mustLesson(t, store, 1); !l.LibraryEntries.Valid || !strings.Contains(l.LibraryEntries.String, stuck) {
		t.Errorf("lesson 1 = %+v, want kept with its record of %q", l, stuck)
	}
	if l := mustLesson(t, store, 2); l.Status != database.StatusSkipped || l.OutputDir.Valid {
		t.Errorf("lesson 2 = %+v, want its row kept, tombstoned", l)
	}
	assertGone(t, season, "Show - s01e05 - Five.mp4", "Show - s01e06 - Six.mp4")
}

// TestDeleteLessonStopsItsDownloadForGood proves the race invariant through
// the handler: the delete removes the lesson's running job and kills it, so a
// later step of that download (its FinishDownload) records nothing.
func TestDeleteLessonStopsItsDownloadForGood(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads := t.TempDir()
	f := addFollow(t, store, 100)
	dir := mkLessonDir(t, downloads, "01 - Lesson")
	if err := store.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	finishWithNewJob(t, store, f, 1, database.DownloadRecord{Quality: "1080", OutputDir: dir, VideoPath: filepath.Join(dir, "v.mp4"), Bytes: 5})
	jobID, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, jobID)
	var canceled []int64
	deps := Deps{CancelRunning: func(id int64) bool { canceled = append(canceled, id); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads}, "test")

	if rec := serveDelete(t, srv, "/api/lessons/1"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(canceled, []int64{jobID}) {
		t.Errorf("CancelRunning calls = %v, want [%d]", canceled, jobID)
	}
	late := database.DownloadRecord{Quality: "1080", OutputDir: dir, VideoPath: filepath.Join(dir, "v.mp4")}
	if err := store.FinishDownload(ctx, jobID, 1, late); !errors.Is(err, database.ErrDownloadAbandoned) {
		t.Errorf("the killed download's FinishDownload = %v, want ErrDownloadAbandoned", err)
	}
	if l := mustLesson(t, store, 1); l.Status != database.StatusSkipped || l.OutputDir.Valid {
		t.Errorf("lesson = %+v, want it to stay deleted", l)
	}
}

// rawExec runs query on a second handle to the store's database file: a
// change no Store method would make (the tests below use it to change a row
// while a delete runs, which nothing in drumdrop can do any more).
func rawExec(t *testing.T, path, query string, args ...any) {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// TestDeleteLessonBlocksNewDownloadsWhileItRuns (D56) proves that from the moment a delete begins until it answers, no
// download of the lesson can be enqueued, from the API or the planner, so no
// new download can land files the delete is removing; and that the lesson can
// be downloaded again once the delete has answered.
func TestDeleteLessonBlocksNewDownloadsWhileItRuns(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	f := addFollow(t, store, 100)
	seedPlexLesson(t, store, f, 1, "Five", season, "Show - s01e05 - Five.mp4")
	running, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, running)
	var srv http.Handler
	var during []*httptest.ResponseRecorder
	var enqueueErr error
	var skip bool
	deps := Deps{CancelRunning: func(int64) bool {
		_, _, enqueueErr = store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1)
		skip, _ = store.ShouldSkipEnqueue(ctx, 1)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/lessons/1/download", nil))
		during = append(during, rec, serveDelete(t, srv, "/api/lessons/1"))
		return true
	}}
	srv = NewServer(store, deps, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	if rec := serveDelete(t, srv, "/api/lessons/1"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !errors.Is(enqueueErr, database.ErrLessonDeleting) || !skip {
		t.Errorf("during the delete: EnqueueJob = %v, planner skip = %v; want ErrLessonDeleting and a skip", enqueueErr, skip)
	}
	wantError(t, during[0], http.StatusConflict, msgBeingDeleted)
	wantError(t, during[1], http.StatusConflict, msgLessonDeleting)
	if l := mustLesson(t, store, 1); l.Status != database.StatusSkipped || l.OutputDir.Valid || l.Deleting {
		t.Errorf("lesson = %+v, want tombstoned and the delete ended", l)
	}
	if _, created, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1); err != nil || !created {
		t.Errorf("EnqueueJob after the delete = %v, %v, want a new job", created, err)
	}
}

// TestDeleteLessonChangedMeanwhileIs409 proves the delete's final write is
// still a compare-and-swap: a row whose files changed while the delete ran
// (only possible outside drumdrop now) is left exactly as it is, the delete
// answers 409 saying so, and the delete still ends.
func TestDeleteLessonChangedMeanwhileIs409(t *testing.T) {
	store, path := newTestStoreAt(t)
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	f := addFollow(t, store, 100)
	seedPlexLesson(t, store, f, 1, "Five", season, "Show - s01e05 - Five.mp4")
	running, _, err := store.EnqueueJob(t.Context(), sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, running)
	captureLog(t)
	deps := Deps{CancelRunning: func(int64) bool {
		rawExec(t, path, `UPDATE lessons SET video_path = 'elsewhere.mp4' WHERE railcontent_id = 1`)
		return true
	}}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	wantError(t, serveDelete(t, srv, "/api/lessons/1"), http.StatusConflict, msgLessonChanged)
	if l := mustLesson(t, store, 1); l.Status != database.StatusDownloaded || l.VideoPath.String != "elsewhere.mp4" || l.Deleting {
		t.Errorf("lesson = %+v, want left as it was changed, the delete ended", l)
	}
}

// TestDeleteLessonRunsToTheEndWhenTheClientLeaves proves that once a delete
// has begun, a client that goes away (its request context canceled) does not
// stop it half way: the files go, the row is tombstoned, and the delete ends.
func TestDeleteLessonRunsToTheEndWhenTheClientLeaves(t *testing.T) {
	store := newTestStore(t)
	downloads := t.TempDir()
	f := addFollow(t, store, 100)
	dir := mkLessonDir(t, downloads, "Course/01 - Lesson")
	if err := store.UpsertLesson(t.Context(), 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	finishWithNewJob(t, store, f, 1, database.DownloadRecord{OutputDir: dir, VideoPath: filepath.Join(dir, "v.mp4")})
	running, _, err := store.EnqueueJob(t.Context(), sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, running)
	reqCtx, leave := context.WithCancel(context.Background())
	deps := Deps{CancelRunning: func(int64) bool { leave(); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads}, "test")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/lessons/1", nil).WithContext(reqCtx))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	assertGone(t, downloads, "Course/01 - Lesson/")
	if l := mustLesson(t, store, 1); l.Status != database.StatusSkipped || l.OutputDir.Valid || l.Deleting {
		t.Errorf("lesson = %+v, want tombstoned and the delete ended", l)
	}
}

// TestDeleteFollowFilesNewLessonDownloadedMeanwhileIs409 proves the follow
// cascade refuses to drop a lesson that recorded files after the delete read
// the follow's lessons (one the planner found meanwhile, which the delete
// could not know): 409, and the follow and that lesson's record stay, while
// the lessons the delete read are deleted.
func TestDeleteFollowFilesNewLessonDownloadedMeanwhileIs409(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads := t.TempDir()
	f := addFollow(t, store, 100)
	old := mkLessonDir(t, downloads, "Course/01 - Old")
	if err := store.UpsertLesson(ctx, 1, "Old", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	finishWithNewJob(t, store, f, 1, database.DownloadRecord{OutputDir: old})
	running, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, running)
	dir := mkLessonDir(t, downloads, "Course/02 - New")
	deps := Deps{CancelRunning: func(int64) bool {
		if err := store.UpsertLesson(ctx, 2, "New", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
			t.Fatalf("UpsertLesson: %v", err)
		}
		finishWithNewJob(t, store, f, 2, database.DownloadRecord{OutputDir: dir, VideoPath: filepath.Join(dir, "v.mp4")})
		return true
	}}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads}, "test")

	wantError(t, serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true"), http.StatusConflict, msgFollowNewFiles)
	if _, err := store.GetFollow(ctx, f); err != nil {
		t.Errorf("follow removed (err=%v), want kept", err)
	}
	if l := mustLesson(t, store, 2); l.OutputDir.String != dir {
		t.Errorf("lesson 2 = %+v, want the new download's record", l)
	}
	assertPresent(t, dir, "v.mp4")
	assertGone(t, downloads, "Course/01 - Old/")
	if l := mustLesson(t, store, 1); l.Status != database.StatusSkipped || l.Deleting {
		t.Errorf("lesson 1 = %+v, want tombstoned and the delete ended", l)
	}
}

// TestRemoveFollowKeepingFilesStopsOnlyItsOwnDownloads (D60) proves a
// follow removed without its files stops the downloads of its own lessons and
// no other follow's, even one it queued, and removes no file.
func TestRemoveFollowKeepingFilesStopsOnlyItsOwnDownloads(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads := t.TempDir()
	f, g := addFollow(t, store, 100), addFollow(t, store, 200)
	dir := mkLessonDir(t, downloads, "Course/01 - Mine")
	if err := store.UpsertLesson(ctx, 1, "Mine", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	finishWithNewJob(t, store, f, 1, database.DownloadRecord{OutputDir: dir})
	own, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, own)
	if err := store.UpsertLesson(ctx, 2, "Theirs", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: g, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	collateral, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 2)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, collateral)
	var killed []int64
	deps := Deps{CancelRunning: func(id int64) bool { killed = append(killed, id); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads}, "test")

	if rec := serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(killed, []int64{own}) {
		t.Errorf("killed %v, want only the follow's own download [%d]", killed, own)
	}
	if j, err := store.GetJob(ctx, collateral); err != nil || j.Status != database.JobRunning {
		t.Errorf("the other follow's lesson's job = %+v (err %v), want still running", j, err)
	}
	assertPresent(t, dir, "v.mp4")
}

// TestDeleteRefusesWhileARecordIsDamaged proves one damaged library record
// anywhere refuses every delete (no ownership can be decided without it) with
// the fixed 500, removes nothing, and ends the delete, for a lesson and for a
// follow; the log names the damaged lesson.
func TestDeleteRefusesWhileARecordIsDamaged(t *testing.T) {
	store, path := newTestStoreAt(t)
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	f := addFollow(t, store, 100)
	seedPlexLesson(t, store, f, 1, "Five", season, "Show - s01e05 - Five.mp4")
	seedPlexLesson(t, store, f, 2, "Six", season, "Show - s01e06 - Six.mp4")
	rawExec(t, path, `UPDATE lessons SET library_entries = '["/etc/passwd"]' WHERE railcontent_id = 2`)
	log := captureLog(t)
	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	wantError(t, serveDelete(t, srv, "/api/lessons/1"), http.StatusInternalServerError, msgLessonFilesKept)
	wantError(t, serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true"), http.StatusInternalServerError, msgFollowFilesKept)
	assertPresent(t, season, "Show - s01e05 - Five.mp4", "Show - s01e06 - Six.mp4")
	if l := mustLesson(t, store, 1); l.Status != database.StatusDownloaded || !l.OutputDir.Valid || l.Deleting {
		t.Errorf("lesson 1 = %+v, want untouched and the delete ended", l)
	}
	if !strings.Contains(log.String(), "lesson 2") {
		t.Errorf("log %q does not name the damaged lesson", log.String())
	}
}
