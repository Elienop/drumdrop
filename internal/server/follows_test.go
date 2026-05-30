package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// newTestStore opens a fresh temp-file store with migrations applied, so the
// follows-read tests can seed rows directly before building the server over it.
func newTestStore(t *testing.T) *database.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	store := database.NewStore(db)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func TestListFollows(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if _, err := store.AddNodeFollow(ctx, 101, "Course A", "drumeo", "best"); err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	if _, err := store.AddInstructorFollow(ctx, "jane-doe", "Jane Doe", "drumeo", "best"); err != nil {
		t.Fatalf("AddInstructorFollow: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/follows", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []FollowDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Kind != "node" {
		t.Errorf("got[0].Kind = %q, want node", got[0].Kind)
	}
	if got[0].RailcontentID == nil || *got[0].RailcontentID != 101 {
		t.Errorf("got[0].RailcontentID = %v, want 101", got[0].RailcontentID)
	}
	if got[1].Kind != "instructor" {
		t.Errorf("got[1].Kind = %q, want instructor", got[1].Kind)
	}
}

func TestListFollowsEmptyArray(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/follows", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// An empty result must serialize as [] not null.
	if body := rec.Body.String(); body != "[]\n" {
		t.Errorf("body = %q, want %q", body, "[]\n")
	}
}

func TestGetFollow(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	f, err := store.AddNodeFollow(ctx, 202, "Course B", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/follows/"+strconv.FormatInt(f.ID, 10), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got FollowDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != f.ID {
		t.Errorf("got.ID = %d, want %d", got.ID, f.ID)
	}
	if got.Title != "Course B" {
		t.Errorf("got.Title = %q, want Course B", got.Title)
	}
}

func TestGetFollowNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/follows/999", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetFollowBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/follows/not-a-number", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestFollowLessons(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	f, err := store.AddNodeFollow(ctx, 303, "Course C", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	fid := sql.NullInt64{Int64: f.ID, Valid: true}
	if err := store.UpsertLesson(ctx, 11, "Lesson One", sql.NullInt64{}, "drumeo", fid); err != nil {
		t.Fatalf("UpsertLesson 11: %v", err)
	}
	if err := store.UpsertLesson(ctx, 12, "Lesson Two", sql.NullInt64{}, "drumeo", fid); err != nil {
		t.Fatalf("UpsertLesson 12: %v", err)
	}
	if err := store.MarkSkipped(ctx, 12, "no thanks"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	t.Run("all", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/follows/"+strconv.FormatInt(f.ID, 10)+"/lessons", nil)
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
	})

	t.Run("status filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/follows/"+strconv.FormatInt(f.ID, 10)+"/lessons?status=skipped", nil)
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
		if got[0].RailcontentID != 12 || got[0].Status != "skipped" {
			t.Errorf("got[0] = %+v, want railcontent 12 skipped", got[0])
		}
	})

	t.Run("status filter no match", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/follows/"+strconv.FormatInt(f.ID, 10)+"/lessons?status=downloaded", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if body := rec.Body.String(); body != "[]\n" {
			t.Errorf("body = %q, want %q", body, "[]\n")
		}
	})
}

func TestFollowLessonsBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/follows/nope/lessons", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestFollowLessonsUnknownID asserts an integer id for a follow that does not
// exist yields 404 (mirroring DELETE /api/follows/{id} and POST .../skip)
// rather than a 200 empty list, so the UI can distinguish "no such follow" from
// "follow with no lessons".
func TestFollowLessonsUnknownID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/follows/9999/lessons", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
