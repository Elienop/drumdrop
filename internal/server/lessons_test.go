package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestListLessons(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 11, "Lesson One", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson 11: %v", err)
	}
	if err := store.UpsertLesson(ctx, 12, "Lesson Two", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson 12: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/lessons", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []LessonDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestListLessonsEmptyArray(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/lessons", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "[]\n" {
		t.Errorf("body = %q, want %q", body, "[]\n")
	}
}

func TestListLessonsStatusFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 21, "Pending", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson 21: %v", err)
	}
	if err := store.UpsertLesson(ctx, 22, "Skipped", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson 22: %v", err)
	}
	if err := store.MarkSkipped(ctx, 22, "no thanks"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/lessons?status=skipped", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []LessonDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].RailcontentID != 22 || got[0].Status != "skipped" {
		t.Errorf("got[0] = %+v, want railcontent 22 skipped", got[0])
	}
}

func TestListLessonsLimitOffset(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	for _, id := range []int{31, 32, 33} {
		if err := store.UpsertLesson(ctx, id, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
			t.Fatalf("UpsertLesson %d: %v", id, err)
		}
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/lessons?limit=1&offset=1", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []LessonDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestListLessonsBadLimit(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/lessons?limit=abc", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetLesson(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 404101, "Lesson Title", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/lessons/"+strconv.Itoa(404101), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got LessonDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RailcontentID != 404101 {
		t.Errorf("got.RailcontentID = %d, want 404101", got.RailcontentID)
	}
	if got.Title != "Lesson Title" {
		t.Errorf("got.Title = %q, want Lesson Title", got.Title)
	}
}

func TestGetLessonNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/lessons/999999", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetLessonBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/lessons/not-a-number", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
