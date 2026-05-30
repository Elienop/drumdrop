package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// stubSanity points the musora GROQ endpoint at a test server returning body
// (200) for any request, restoring the real base when the test ends. It keeps
// the follow-create handler hermetic — ResolveLesson/ResolveInstructorID never
// touch the live network.
func stubSanity(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(musora.SetSanityBase(srv.URL))
}

func TestCreateNodeFollow(t *testing.T) {
	// ResolveLesson returns a single lesson whose title becomes the follow title.
	stubSanity(t, `{"result":[{"id":409875,"title":"Course A"}]}`)
	store := newTestStore(t)
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	body := `{"kind":"node","id":"409875","brand":"drumeo","quality":"best"}`
	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got FollowDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != "node" {
		t.Errorf("Kind = %q, want node", got.Kind)
	}
	if got.RailcontentID == nil || *got.RailcontentID != 409875 {
		t.Errorf("RailcontentID = %v, want 409875", got.RailcontentID)
	}
	if got.Title != "Course A" {
		t.Errorf("Title = %q, want Course A", got.Title)
	}
	if got.Brand != "drumeo" {
		t.Errorf("Brand = %q, want drumeo", got.Brand)
	}
	if got.Quality != "best" {
		t.Errorf("Quality = %q, want best", got.Quality)
	}
}

func TestCreateNodeFollowFromURL(t *testing.T) {
	// A Musora URL must be parsed by engine.ExtractID into the trailing id.
	stubSanity(t, `{"result":[{"id":12345,"title":"From URL"}]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	body := `{"kind":"node","url":"https://www.drumeo.com/members/lesson/12345"}`
	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got FollowDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RailcontentID == nil || *got.RailcontentID != 12345 {
		t.Errorf("RailcontentID = %v, want 12345", got.RailcontentID)
	}
	// Defaults applied when brand/quality omitted.
	if got.Brand != "drumeo" {
		t.Errorf("Brand = %q, want default drumeo", got.Brand)
	}
	if got.Quality != "best" {
		t.Errorf("Quality = %q, want default best", got.Quality)
	}
}

func TestCreateNodeFollowResolveFailureStillCreates(t *testing.T) {
	// A 500 from Sanity makes ResolveLesson fail; title resolution is best-effort
	// so the follow is still created (with an empty title).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(musora.SetSanityBase(srv.URL))

	api := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")
	body := `{"kind":"node","id":"777"}`
	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got FollowDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RailcontentID == nil || *got.RailcontentID != 777 {
		t.Errorf("RailcontentID = %v, want 777", got.RailcontentID)
	}
	if got.Title != "" {
		t.Errorf("Title = %q, want empty (best-effort resolve failed)", got.Title)
	}
}

func TestCreateNodeFollowIdempotent(t *testing.T) {
	stubSanity(t, `{"result":[{"id":555,"title":"Dup"}]}`)
	store := newTestStore(t)
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	body := `{"kind":"node","id":"555"}`
	first := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	firstRec := httptest.NewRecorder()
	srv.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want %d", firstRec.Code, http.StatusCreated)
	}
	var firstDTO FollowDTO
	if err := json.Unmarshal(firstRec.Body.Bytes(), &firstDTO); err != nil {
		t.Fatalf("decode first: %v", err)
	}

	second := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	secondRec := httptest.NewRecorder()
	srv.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d (idempotent)", secondRec.Code, http.StatusOK)
	}
	var secondDTO FollowDTO
	if err := json.Unmarshal(secondRec.Body.Bytes(), &secondDTO); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if secondDTO.ID != firstDTO.ID {
		t.Errorf("second ID = %d, want existing %d", secondDTO.ID, firstDTO.ID)
	}
}

func TestCreateInstructorFollow(t *testing.T) {
	// ResolveInstructorID returns the Sanity _id and display name.
	stubSanity(t, `{"result":[{"_id":"abc123","name":"Jane Doe"}]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	body := `{"kind":"instructor","slug":"jane-doe","brand":"pianote"}`
	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got FollowDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != "instructor" {
		t.Errorf("Kind = %q, want instructor", got.Kind)
	}
	if got.Slug == nil || *got.Slug != "jane-doe" {
		t.Errorf("Slug = %v, want jane-doe", got.Slug)
	}
	if got.Title != "Jane Doe" {
		t.Errorf("Title = %q, want Jane Doe", got.Title)
	}
	if got.Brand != "pianote" {
		t.Errorf("Brand = %q, want pianote", got.Brand)
	}
}

func TestCreateInstructorFollowUnknown(t *testing.T) {
	// No instructor matches the slug → 400.
	stubSanity(t, `{"result":[]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	body := `{"kind":"instructor","slug":"nobody"}`
	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreateFollowBadKind(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	body := `{"kind":"bogus","id":"1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateNodeFollowUnparseableID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	body := `{"kind":"node","id":"no-digits-here"}`
	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreateInstructorFollowMissingSlug(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	body := `{"kind":"instructor"}`
	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateFollowMalformedJSON(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(`{not json`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestDeleteFollow(t *testing.T) {
	store := newTestStore(t)
	f, err := store.AddNodeFollow(t.Context(), 909, "Gone", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodDelete, "/api/follows/"+strconv.FormatInt(f.ID, 10), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if _, err := store.GetFollow(t.Context(), f.ID); err == nil {
		t.Error("GetFollow after delete: want error, got nil")
	}
}

func TestDeleteFollowNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodDelete, "/api/follows/424242", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteFollowBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodDelete, "/api/follows/not-a-number", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
