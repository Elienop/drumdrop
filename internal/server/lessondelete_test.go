package server

import (
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
	rec := database.DownloadRecord{Quality: "1080", OutputDir: season, Bytes: 5, LibraryEntries: []string{}}
	for _, n := range names {
		p := filepath.Join(season, strings.TrimSuffix(n, "/"))
		rec.LibraryEntries = append(rec.LibraryEntries, p)
		if rec.VideoPath == "" && strings.HasSuffix(n, ".mp4") {
			rec.VideoPath = p
		}
	}
	finishWithNewJob(t, store, followID, rcID, rec)
}

// finishWithNewJob records rec for lesson rcID the way the worker does: a new
// job, then FinishDownload under it.
func finishWithNewJob(t *testing.T, store *database.Store, followID int64, rcID int, rec database.DownloadRecord) {
	t.Helper()
	ctx := t.Context()
	jobID, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: followID, Valid: true}, rcID)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := store.FinishDownload(ctx, jobID, rcID, rec); err != nil {
		t.Fatalf("FinishDownload: %v", err)
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
	if err := store.MarkDownloaded(ctx, 3, "1080", season, filepath.Join(season, song[0]), 5); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
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
	wantError(t, rec, http.StatusInternalServerError, "could not delete the lesson's files; the lesson was kept (see the server log)")
	assertGone(t, season, "Show - s01e05 - Five.mp4")
	l := mustLesson(t, store, 1)
	entries, recorded, err := l.PlacedEntries()
	if l.Status != database.StatusDownloaded || !l.OutputDir.Valid || err != nil || !recorded || !reflect.DeepEqual(entries, []string{filepath.Join(season, stuck)}) {
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
	wantError(t, rec, http.StatusInternalServerError, "could not delete every lesson's files; the follow was kept (see the server log)")
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
	if err := store.MarkDownloaded(ctx, 1, "1080", dir, filepath.Join(dir, "v.mp4"), 5); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
	jobID, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := store.MarkJobRunning(ctx, jobID); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
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

// TestDeleteLessonDownloadedAgainMeanwhileIs409 proves a download that starts
// after the delete began and records new files before it finishes is never
// tombstoned over: the delete answers 409 and the new record stands.
func TestDeleteLessonDownloadedAgainMeanwhileIs409(t *testing.T) {
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
	if err := store.MarkJobRunning(ctx, running); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	fresh := database.DownloadRecord{Quality: "720", OutputDir: season, VideoPath: filepath.Join(season, "Show - s01e05 - Five (new).mp4"), LibraryEntries: []string{filepath.Join(season, "Show - s01e05 - Five (new).mp4")}}
	// The kill is the one moment the handler hands control out between reading
	// the row and finishing it: a new download lands there.
	deps := Deps{CancelRunning: func(int64) bool { finishWithNewJob(t, store, f, 1, fresh); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	wantError(t, serveDelete(t, srv, "/api/lessons/1"), http.StatusConflict, "the lesson was downloaded again while it was being deleted; try again")
	if l := mustLesson(t, store, 1); l.Status != database.StatusDownloaded || l.VideoPath.String != fresh.VideoPath {
		t.Errorf("lesson = %+v, want the new download's record", l)
	}
}

// TestDeleteFollowFilesDownloadedMeanwhileIs409 proves the follow cascade
// refuses to drop a lesson that recorded files after the delete read it (it
// had none then, so the file step skipped it): 409, and the follow and the
// lesson's new record stay.
func TestDeleteFollowFilesDownloadedMeanwhileIs409(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads := t.TempDir()
	f := addFollow(t, store, 100)
	if err := store.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	running, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := store.MarkJobRunning(ctx, running); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	dir := mkLessonDir(t, downloads, "01 - L")
	fresh := database.DownloadRecord{Quality: "720", OutputDir: dir, VideoPath: filepath.Join(dir, "v.mp4")}
	deps := Deps{CancelRunning: func(int64) bool { finishWithNewJob(t, store, f, 1, fresh); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads}, "test")

	wantError(t, serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true"), http.StatusConflict, "a lesson of this follow was downloaded while it was being deleted; try again")
	if _, err := store.GetFollow(ctx, f); err != nil {
		t.Errorf("follow removed (err=%v), want kept", err)
	}
	if l := mustLesson(t, store, 1); l.OutputDir.String != dir {
		t.Errorf("lesson = %+v, want the new download's record", l)
	}
	assertPresent(t, dir, "v.mp4")
}
