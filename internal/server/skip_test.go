package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

func postSkip(t *testing.T, srv http.Handler, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/lessons/"+id+"/skip", strings.NewReader(body)))
	return rec
}

// TestSkipStopsEveryDownloadOfTheLesson (code M1) proves Skip sticks through
// the API: its queued job is removed, its running one is removed and killed,
// and a later step of that download records nothing.
func TestSkipStopsEveryDownloadOfTheLesson(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	f := addFollow(t, store, 100)
	for _, id := range []int{1, 2} {
		if err := store.UpsertLesson(ctx, id, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
			t.Fatalf("UpsertLesson: %v", err)
		}
	}
	running, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, running)
	queued, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 2)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	var killed []int64
	srv := NewServer(store, Deps{CancelRunning: func(id int64) bool { killed = append(killed, id); return true }}, nil, Config{}, "test")

	for _, id := range []string{"1", "2"} {
		if rec := postSkip(t, srv, id, `{"reason":"not for me"}`); rec.Code != http.StatusOK {
			t.Fatalf("skip %s = %d %s, want 200", id, rec.Code, rec.Body.String())
		}
	}
	if !reflect.DeepEqual(killed, []int64{running}) {
		t.Errorf("killed %v, want [%d]", killed, running)
	}
	for _, j := range []int64{running, queued} {
		if _, err := store.GetJob(ctx, j); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("job %d survived the skip (err=%v)", j, err)
		}
	}
	if err := store.FinishDownload(ctx, running, 1, database.DownloadRecord{OutputDir: "/dl/C/01 - L"}); !errors.Is(err, database.ErrDownloadAbandoned) {
		t.Errorf("the killed download's FinishDownload = %v, want ErrDownloadAbandoned", err)
	}
	if l := mustLesson(t, store, 1); l.Status != database.StatusSkipped || l.Error.String != "not for me" || l.OutputDir.Valid {
		t.Errorf("lesson 1 = %+v, want skipped with its reason", l)
	}
}

// TestSkipWhileDeletingIs409 proves Skip refuses while a delete holds the
// lesson, with its fixed sentence, and changes nothing.
func TestSkipWhileDeletingIs409(t *testing.T) {
	store := newTestStore(t)
	f := addFollow(t, store, 100)
	dir := mkLessonDir(t, t.TempDir(), "C/01 - L")
	if err := store.UpsertLesson(t.Context(), 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	finishWithNewJob(t, store, f, 1, database.DownloadRecord{OutputDir: dir})
	if _, _, err := store.BeginLessonDelete(t.Context(), 1); err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")
	wantError(t, postSkip(t, srv, "1", ""), http.StatusConflict, msgSkipDeleting)
	if l := mustLesson(t, store, 1); l.Status != database.StatusDownloaded {
		t.Errorf("a refused skip changed the lesson to %s", l.Status)
	}
	wantError(t, postSkip(t, srv, "404", ""), http.StatusNotFound, msgSkipGone)
}

// TestLessonDTODeleting proves the lesson's "deleting" field is always
// present and true exactly while a delete holds the lesson: the predicate the
// server refuses with.
func TestLessonDTODeleting(t *testing.T) {
	store, path := newTestStoreAt(t)
	f := addFollow(t, store, 100)
	if err := store.UpsertLesson(t.Context(), 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")
	deleting := func() any {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lessons/1", nil))
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
		v, ok := body["deleting"]
		if !ok {
			t.Fatalf("lesson %s has no deleting field", rec.Body.String())
		}
		return v
	}
	if got := deleting(); got != false {
		t.Errorf("deleting = %v before any delete, want false", got)
	}
	if _, _, err := store.BeginLessonDelete(t.Context(), 1); err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	if got := deleting(); got != true {
		t.Errorf("deleting = %v during the delete, want true", got)
	}
	rawExec(t, path, `UPDATE lessons SET deleting_until = datetime('now', '-1 second') WHERE railcontent_id = 1`)
	if got := deleting(); got != false {
		t.Errorf("deleting = %v once the lease lapsed, want false", got)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/lessons/1/download", nil))
	if rec.Code != http.StatusAccepted {
		t.Errorf("download once the lease lapsed = %d %s, want 202", rec.Code, rec.Body.String())
	}
}
