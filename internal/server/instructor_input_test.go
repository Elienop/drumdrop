package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// recordSanity points the musora query endpoint at a server that answers one
// instructor with one lesson, and returns every GROQ query it was sent.
func recordSanity(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("query"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"result":[{"_id":"abc","name":"Jared Falk","id":7}]}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(musora.SetSanityBase(srv.URL))
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), queries...)
	}
}

// previewThenAdd previews input in brand, then adds it, on one server, and
// returns both answers and the follows the store holds afterwards.
func previewThenAdd(t *testing.T, input, brand string) (preview, add *httptest.ResponseRecorder, follows []database.Follow) {
	t.Helper()
	store := newTestStore(t)
	srv := NewServer(store, Deps{}, nil, Config{}, "test")
	q := url.Values{"slug": {input}}
	if brand != "" {
		q.Set("brand", brand)
	}
	preview = httptest.NewRecorder()
	srv.ServeHTTP(preview, httptest.NewRequest(http.MethodGet, "/api/preview?"+q.Encode(), nil))
	body := `{"kind":"instructor","slug":` + quoteJSON(input) + `,"brand":` + quoteJSON(brand) + `}`
	add = httptest.NewRecorder()
	srv.ServeHTTP(add, httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body)))
	follows, err := store.ListFollows(t.Context())
	if err != nil {
		t.Fatalf("ListFollows: %v", err)
	}
	return preview, add, follows
}

// TestInstructorInputIsNormalisedAlike (ruling #70) proves a name, a slug in
// any case and a coach-page link are normalised the same way on preview and
// on add: the preview's slug is the one the add stores, both ask Musora for
// that slug only, and a link's brand fills an empty Brand. What the UI itself
// shows is accepted back (round-5 code Low 4, UI M1): "@jared-falk", and a
// brand as the tables write it ("Pianote"), padded, or in capitals.
func TestInstructorInputIsNormalisedAlike(t *testing.T) {
	for _, c := range []struct {
		input, brand    string
		slug, wantBrand string
	}{
		{"Jared Falk", "", "jared-falk", "drumeo"},
		{"Jared-Falk", "", "jared-falk", "drumeo"},
		{"  jared-falk\n", "pianote", "jared-falk", "pianote"},
		{"https://app.musora.com/drumeo/coaches/jared-falk/31880", "", "jared-falk", "drumeo"},
		{"https://app.musora.com/singeo/coaches/jared-falk/314120", "", "jared-falk", "singeo"},
		{"https://app.musora.com/singeo/coaches/jared-falk/314120", "singeo", "jared-falk", "singeo"},
		{"@jared-falk", "", "jared-falk", "drumeo"},
		{"jared-falk", "Pianote", "jared-falk", "pianote"},
		{"jared-falk", " pianote", "jared-falk", "pianote"},
		{"Jared Falk", "DRUMEO", "jared-falk", "drumeo"},
	} {
		t.Run(c.input+" "+c.brand, func(t *testing.T) {
			queries := recordSanity(t)
			preview, add, follows := previewThenAdd(t, c.input, c.brand)

			var p previewResponse
			if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &p) != nil {
				t.Fatalf("preview = %d %s, want 200", preview.Code, preview.Body.String())
			}
			if p.Slug != c.slug || p.Kind != "instructor" || p.Title != "Jared Falk" {
				t.Errorf("preview = %+v, want slug %q, kind instructor, title Jared Falk", p, c.slug)
			}
			var f FollowDTO
			if add.Code != http.StatusCreated || json.Unmarshal(add.Body.Bytes(), &f) != nil {
				t.Fatalf("add = %d %s, want 201", add.Code, add.Body.String())
			}
			if f.Slug == nil || *f.Slug != p.Slug || f.Brand != c.wantBrand {
				t.Errorf("add answered slug %v, brand %q; want the preview's %q, brand %q", f.Slug, f.Brand, p.Slug, c.wantBrand)
			}
			if len(follows) != 1 || follows[0].Slug.String != c.slug || follows[0].Brand != c.wantBrand {
				t.Fatalf("stored follows = %+v, want one of %q in %q", follows, c.slug, c.wantBrand)
			}
			for _, q := range queries() {
				if !strings.Contains(q, "slug.current=='"+c.slug+"'") {
					t.Errorf("Musora was asked about another slug: %s", q)
				}
			}
		})
	}
}

// TestPreviewAndAddRefuseTheSameInput proves every input nothing can
// normalise (the security seat's list: quotes, newlines, a link of another
// kind, an empty string, and what has no known slug spelling) is a 400 on
// preview and on add alike, saying what's accepted, without asking Musora,
// storing anything, or echoing the input.
func TestPreviewAndAddRefuseTheSameInput(t *testing.T) {
	for _, input := range []string{
		" ",
		"\n",
		`say "hi"`,
		`x'||true||'`,
		"a\nb",
		"jared_falk",
		"José Pérez",
		"Kenny",
		"https://app.musora.com/drumeo/lessons/course/409875/409875",
		"https://app.musora.com/drumeo/coaches/<script>/1",
		"https://www.drumeo.com/laravel/public/drumeo/coaches/jared-falk",
		"javascript:alert(1)//drumeo/coaches/x",
	} {
		t.Run(input, func(t *testing.T) {
			calls := countSanity(t, `{"result":[{"_id":"abc","name":"Jane","id":7}]}`)
			preview, add, follows := previewThenAdd(t, input, "")
			wantError(t, preview, http.StatusBadRequest, msgBadSlug)
			wantError(t, add, http.StatusBadRequest, msgBadSlug)
			if n := calls.Load(); n != 0 {
				t.Errorf("Musora was asked %d times, want none", n)
			}
			if len(follows) != 0 {
				t.Errorf("stored %d follows, want none", len(follows))
			}
			for _, rec := range []*httptest.ResponseRecorder{preview, add} {
				if strings.Contains(rec.Body.String(), "script") || strings.Contains(rec.Body.String(), "José") {
					t.Errorf("the answer echoes the input: %s", rec.Body.String())
				}
			}
		})
	}
}

// TestALinkForAnotherBrandIs400 proves a coach-page link never silently
// overrides the Brand the user gave: a different one is a 400 on preview and
// on add, before Musora is asked.
func TestALinkForAnotherBrandIs400(t *testing.T) {
	calls := countSanity(t, `{"result":[{"_id":"abc","name":"Jane","id":7}]}`)
	preview, add, follows := previewThenAdd(t, "https://app.musora.com/singeo/coaches/jared-falk/314120", "drumeo")
	wantError(t, preview, http.StatusBadRequest, msgBrandMismatch)
	wantError(t, add, http.StatusBadRequest, msgBrandMismatch)
	if n := calls.Load(); n != 0 || len(follows) != 0 {
		t.Errorf("Musora asked %d times, %d follows stored; want neither", n, len(follows))
	}
}

// TestANodeFollowStillChecksItsBrand proves moving the brand check into the
// node branch (an instructor settles its brand with its input) kept it: a
// brand Musora doesn't have is a 400 before Musora is asked.
func TestANodeFollowStillChecksItsBrand(t *testing.T) {
	calls := countSanity(t, `{"result":[{"id":5,"title":"A"}]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(`{"kind":"node","id":"5","brand":"rockstar"}`)))
	wantError(t, rec, http.StatusBadRequest, msgBadBrand)
	if n := calls.Load(); n != 0 {
		t.Errorf("Musora was asked %d times, want none", n)
	}
}

// TestANodePreviewHasNoSlug proves the preview's slug stays off a node
// preview, where it means nothing.
func TestANodePreviewHasNoSlug(t *testing.T) {
	stubSanity(t, `{"result":[{"id":409875,"title":"Course A"}]}`)
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/preview?id=409875", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"slug"`) {
		t.Errorf("node preview = %d %s, want 200 without a slug", rec.Code, rec.Body.String())
	}
}
