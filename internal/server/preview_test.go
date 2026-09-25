package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// stubAuth points the musora auth endpoint (AuthBase) at a test server that
// returns status for /me and /sessions, restoring the real base when the test
// ends. It keeps the session handlers hermetic — Me/Login never touch the live
// network. The session cookie name musora.Login looks for is set on the
// /sessions response so a 200 yields a usable cookie.
func stubAuth(t *testing.T, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sessions") {
			http.SetCookie(w, &http.Cookie{Name: "musora_platform_backend_session", Value: "tok"})
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"user":{"id":1}}`))
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	prev := musora.AuthBase
	musora.AuthBase = srv.URL
	t.Cleanup(func() { musora.AuthBase = prev })
}

func TestPreviewNode(t *testing.T) {
	// One object satisfies both Hierarchy ({railcontent_id,children}) and
	// ResolveLesson ({id,title}) decodes: a leaf node with a title.
	stubSanity(t, `{"result":[{"id":42,"title":"Lesson 42","railcontent_id":42,"children":[]}]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/preview?id=42", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got previewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != "node" {
		t.Errorf("Kind = %q, want node", got.Kind)
	}
	if got.RootID == nil || *got.RootID != 42 {
		t.Errorf("RootID = %v, want 42", got.RootID)
	}
	if got.LessonCount != 1 {
		t.Errorf("LessonCount = %d, want 1", got.LessonCount)
	}
	if got.Title != "Lesson 42" {
		t.Errorf("Title = %q, want Lesson 42", got.Title)
	}
}

func TestPreviewNodeMissingID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/preview?id=not-a-number", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPreviewNoParams(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/preview", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPreviewInstructor(t *testing.T) {
	// One object satisfies both ResolveInstructorID ({_id,name}) and the
	// LessonRef parse used by InstructorLessons ({id}).
	stubSanity(t, `{"result":[{"_id":"abc","name":"Jane","id":7}]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/preview?slug=jane", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got previewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != "instructor" {
		t.Errorf("Kind = %q, want instructor", got.Kind)
	}
	if got.RootID != nil {
		t.Errorf("RootID = %v, want nil for instructor preview", got.RootID)
	}
	if got.Title != "Jane" {
		t.Errorf("Title = %q, want Jane", got.Title)
	}
	if got.LessonCount != 1 {
		t.Errorf("LessonCount = %d, want 1", got.LessonCount)
	}
}

func TestPreviewInstructorUnknown(t *testing.T) {
	stubSanity(t, `{"result":[]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/preview?slug=nobody", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestSessionGetConnected(t *testing.T) {
	t.Setenv("DRUMDROP_CONFIG_DIR", t.TempDir()) // never read the real session.cookie
	stubAuth(t, http.StatusOK)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Connected {
		t.Errorf("Connected = false, want true")
	}
}

func TestSessionGetDisconnected(t *testing.T) {
	t.Setenv("DRUMDROP_CONFIG_DIR", t.TempDir()) // never read the real session.cookie
	stubAuth(t, http.StatusUnauthorized)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Connected {
		t.Errorf("Connected = true, want false")
	}
}

func TestSessionLoginSuccess(t *testing.T) {
	stubAuth(t, http.StatusOK)
	// A login writes session.cookie and credentials.enc: into a throwaway
	// folder, never the real one, whether or not DRUMDROP_CONFIG_DIR is set.
	dir := t.TempDir()
	t.Setenv("DRUMDROP_CONFIG_DIR", dir)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	body := `{"email":"a@b.com","password":"pw"}`
	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	for _, name := range []string{"session.cookie", "credentials.enc"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s not saved in the test's config folder: %v", name, err)
		}
	}
}
