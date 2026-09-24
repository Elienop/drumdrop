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

func TestCancelJobQueued(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 6001, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	id, _, err := store.EnqueueJob(ctx, sql.NullInt64{}, 6001)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+strconv.FormatInt(id, 10)+"/cancel", nil)
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
	if got.Status != "canceled" {
		t.Errorf("got.Status = %q, want canceled", got.Status)
	}
}

func TestCancelJobTerminalConflict(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 6002, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	id, _, err := store.EnqueueJob(ctx, sql.NullInt64{}, 6002)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, id)
	if err := store.FinishDownload(ctx, id, 6002, database.DownloadRecord{}); err != nil {
		t.Fatalf("FinishDownload: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+strconv.FormatInt(id, 10)+"/cancel", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestCancelJobNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/999999/cancel", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCancelJobBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/not-a-number/cancel", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestRetryJobFailed(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 6003, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	id, _, err := store.EnqueueJob(ctx, sql.NullInt64{}, 6003)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, id)
	if err := store.FailDownload(ctx, id, 6003, "boom", "kept", "boom", true); err != nil {
		t.Fatalf("FailDownload: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+strconv.FormatInt(id, 10)+"/retry", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var got JobDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id {
		t.Errorf("got.ID = %d, want %d", got.ID, id)
	}
	if got.Status != "queued" {
		t.Errorf("got.Status = %q, want queued", got.Status)
	}
}

func TestRetryJobNotTerminalConflict(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 6004, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	id, _, err := store.EnqueueJob(ctx, sql.NullInt64{}, 6004)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+strconv.FormatInt(id, 10)+"/retry", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestRetryJobNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/999999/retry", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRetryJobBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/not-a-number/retry", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
