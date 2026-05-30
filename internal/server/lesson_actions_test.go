package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadLessonEnqueues(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 5001, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/5001/download", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var got JobDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RailcontentID != 5001 {
		t.Errorf("got.RailcontentID = %d, want 5001", got.RailcontentID)
	}
	if got.Status != "queued" {
		t.Errorf("got.Status = %q, want queued", got.Status)
	}

	jobs, err := store.ListJobsByStatus(ctx, "queued")
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("queued jobs = %d, want 1", len(jobs))
	}
}

func TestDownloadLessonReturnsExistingActiveJob(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 5002, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	existing, err := store.EnqueueJob(ctx, sql.NullInt64{}, 5002)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/5002/download", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got JobDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != existing {
		t.Errorf("got.ID = %d, want existing %d", got.ID, existing)
	}

	jobs, err := store.ListJobsByStatus(ctx, "queued")
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("queued jobs = %d, want 1 (no duplicate enqueued)", len(jobs))
	}
}

func TestDownloadLessonNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/999999/download", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDownloadLessonBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/not-a-number/download", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSkipLesson(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 5003, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	body := strings.NewReader(`{"reason":"not interested"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/lessons/5003/skip", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got LessonDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "skipped" {
		t.Errorf("got.Status = %q, want skipped", got.Status)
	}
	if got.Error == nil || *got.Error != "not interested" {
		t.Errorf("got.Error = %v, want \"not interested\"", got.Error)
	}
}

func TestSkipLessonNoBody(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 5004, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/"+strconv.Itoa(5004)+"/skip", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got LessonDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "skipped" {
		t.Errorf("got.Status = %q, want skipped", got.Status)
	}
}

func TestSkipLessonNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/999999/skip", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSkipLessonBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/not-a-number/skip", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
