package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateNodeFollowFoldsTheBrand proves a node follow accepts its brand in
// any case, as an instructor follow does, and stores it as Musora files it;
// the allowlist still refuses one Musora doesn't have.
func TestCreateNodeFollowFoldsTheBrand(t *testing.T) {
	for given, want := range map[string]string{"Pianote": "pianote", " DRUMEO ": "drumeo", "": "drumeo"} {
		t.Run(given, func(t *testing.T) {
			stubSanity(t, `{"result":[{"id":409875,"title":"Course A"}]}`)
			store := newTestStore(t)
			srv := NewServer(store, Deps{}, nil, Config{}, "test")
			body := `{"kind":"node","id":"409875","brand":` + quoteJSON(given) + `}`
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body)))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d %s, want 201", rec.Code, rec.Body.String())
			}
			var got FollowDTO
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Brand != want {
				t.Errorf("brand = %q, want %q", got.Brand, want)
			}
		})
	}
	t.Run("not Musora's", func(t *testing.T) {
		store := newTestStore(t)
		srv := NewServer(store, Deps{}, nil, Config{}, "test")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(`{"kind":"node","id":"5","brand":"Rockstar"}`)))
		wantError(t, rec, http.StatusBadRequest, msgBadBrand)
	})
}
