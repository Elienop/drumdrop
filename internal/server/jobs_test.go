package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// seedJob upserts a lesson then enqueues a job for it, returning the job id so
// the jobs-read tests can build state without reaching into the scheduler.
func seedJob(t *testing.T, store *database.Store, railcontentID int) int64 {
	t.Helper()
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, railcontentID, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson %d: %v", railcontentID, err)
	}
	id, _, err := store.EnqueueJob(ctx, sql.NullInt64{}, railcontentID)
	if err != nil {
		t.Fatalf("EnqueueJob %d: %v", railcontentID, err)
	}
	return id
}

func TestListJobs(t *testing.T) {
	store := newTestStore(t)
	seedJob(t, store, 11)
	seedJob(t, store, 12)
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []JobDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestListJobsEmptyArray(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "[]\n" {
		t.Errorf("body = %q, want %q", body, "[]\n")
	}
}

func TestListJobsStateFilter(t *testing.T) {
	store := newTestStore(t)
	seedJob(t, store, 21)
	canceled := seedJob(t, store, 22)
	if err := store.CancelJob(t.Context(), canceled); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs?state=canceled", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []JobDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != canceled || got[0].Status != "canceled" {
		t.Errorf("got[0] = %+v, want id %d canceled", got[0], canceled)
	}
}

func TestListJobsBadLimit(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs?limit=abc", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetJob(t *testing.T) {
	store := newTestStore(t)
	id := seedJob(t, store, 404201)
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got JobDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id {
		t.Errorf("got.ID = %d, want %d", got.ID, id)
	}
	if got.RailcontentID != 404201 {
		t.Errorf("got.RailcontentID = %d, want 404201", got.RailcontentID)
	}
}

func TestGetJobNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/999999", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetJobBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/not-a-number", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSummary(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 31, "Pending", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson 31: %v", err)
	}
	if err := store.UpsertLesson(ctx, 32, "Skipped", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson 32: %v", err)
	}
	if err := store.MarkSkipped(ctx, 32, "no"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if _, err := store.AddNodeFollow(ctx, 101, "Course A", "drumeo", "best"); err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	// seedJob upserts lesson 33 (status pending), so together with lesson 31
	// there are two pending lessons; lesson 32 is skipped.
	seedJob(t, store, 33)
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got SummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Follows != 1 {
		t.Errorf("Follows = %d, want 1", got.Follows)
	}
	if got.Lessons["pending"] != 2 {
		t.Errorf("Lessons[pending] = %d, want 2", got.Lessons["pending"])
	}
	if got.Lessons["skipped"] != 1 {
		t.Errorf("Lessons[skipped] = %d, want 1", got.Lessons["skipped"])
	}
	// Zero-count enums must be present, not absent.
	if _, ok := got.Lessons["downloaded"]; !ok {
		t.Errorf("Lessons missing zero-count key downloaded: %+v", got.Lessons)
	}
	if got.Jobs["queued"] != 1 {
		t.Errorf("Jobs[queued] = %d, want 1", got.Jobs["queued"])
	}
	if _, ok := got.Jobs["done"]; !ok {
		t.Errorf("Jobs missing zero-count key done: %+v", got.Jobs)
	}
}
