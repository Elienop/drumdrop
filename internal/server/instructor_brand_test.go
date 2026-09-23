package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/musora/musoratest"
)

// TestPreviewAndAddUseTheBrandsInstructor proves an instructor Musora files
// under one slug in several documents, the other brand's first in _id order,
// previews the lessons of the brand being followed, with that brand's name
// for them, and that the add stores the same name and brand. Before, both
// took the first document: jared-falk on drumeo previewed zero lessons.
func TestPreviewAndAddUseTheBrandsInstructor(t *testing.T) {
	for _, c := range []struct {
		input, brand         string
		wantBrand, wantTitle string
		wantCount            int
	}{
		{"Jared Falk", "", "drumeo", "Jared Falk", 2},
		{"jared-falk", "drumeo", "drumeo", "Jared Falk", 2},
		{"https://app.musora.com/singeo/coaches/jared-falk/314120", "", "singeo", "Jared Falk (Singeo)", 2},
		{"https://app.musora.com/drumeo/coaches/jared-falk/31880", "", "drumeo", "Jared Falk", 2},
	} {
		t.Run(c.input+" "+c.brand, func(t *testing.T) {
			musoratest.Serve(t, musoratest.JaredFalk())
			preview, add, follows := previewThenAdd(t, c.input, c.brand)

			var p previewResponse
			if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &p) != nil {
				t.Fatalf("preview = %d %s, want 200", preview.Code, preview.Body.String())
			}
			want := previewResponse{Title: c.wantTitle, LessonCount: c.wantCount, Kind: "instructor", Slug: "jared-falk", Brand: c.wantBrand}
			if p.RootID != nil || p.Title != want.Title || p.LessonCount != want.LessonCount || p.Kind != want.Kind || p.Slug != want.Slug || p.Brand != want.Brand {
				t.Errorf("preview = %s, want %+v", preview.Body.String(), want)
			}
			if add.Code != http.StatusCreated {
				t.Fatalf("add = %d %s, want 201", add.Code, add.Body.String())
			}
			if len(follows) != 1 || follows[0].Title != c.wantTitle || follows[0].Brand != c.wantBrand {
				t.Fatalf("stored follows = %+v, want one titled %q in %s", follows, c.wantTitle, c.wantBrand)
			}
		})
	}
}

// TestACoachLinkIsNotANode proves a coach page pasted where a lesson or course
// goes is refused with a sentence saying how to follow the instructor, on
// preview and on add (by id or url), before Musora is asked: its number is
// the instructor's, and would otherwise be followed as a node.
func TestACoachLinkIsNotANode(t *testing.T) {
	for _, link := range []string{
		"https://app.musora.com/drumeo/coaches/jared-falk/31880",
		"www.musora.com/singeo/coaches/jared-falk/314120/",
	} {
		t.Run(link, func(t *testing.T) {
			queries := musoratest.Serve(t, musoratest.JaredFalk())
			store := newTestStore(t)
			srv := NewServer(store, Deps{}, nil, Config{}, "test")

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/preview?"+url.Values{"id": {link}}.Encode(), nil))
			wantError(t, rec, http.StatusBadRequest, msgCoachLinkAsNode)

			for _, field := range []string{"id", "url"} {
				rec := httptest.NewRecorder()
				body := `{"kind":"node","` + field + `":` + quoteJSON(link) + `}`
				srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body)))
				wantError(t, rec, http.StatusBadRequest, msgCoachLinkAsNode)
			}
			follows, err := store.ListFollows(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if n := len(queries()); n != 0 || len(follows) != 0 {
				t.Errorf("Musora asked %d times, %d follows stored; want neither", n, len(follows))
			}
		})
	}
}

// TestANodePreviewHasNoBrand proves the preview's brand, like its slug, stays
// off a node preview (the contract: it's the brand an instructor follow would
// use).
func TestANodePreviewHasNoBrand(t *testing.T) {
	stubSanity(t, `{"result":[{"id":409875,"title":"Course A"}]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/preview?id=409875", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"brand"`) {
		t.Errorf("node preview = %d %s, want 200 without a brand", rec.Code, rec.Body.String())
	}
}
