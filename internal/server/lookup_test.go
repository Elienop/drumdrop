package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// countSanity points the musora query endpoint at a server that answers body
// and counts the requests it gets.
func countSanity(t *testing.T, body string) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(musora.SetSanityBase(srv.URL))
	return &n
}

// sanityUnreachable points the musora query endpoint at an address nothing
// listens on.
func sanityUnreachable(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	t.Cleanup(musora.SetSanityBase(base))
}

// previewSlug serves GET /api/preview?slug=slug&brand=brand.
func previewSlug(t *testing.T, slug, brand string) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")
	q := url.Values{"slug": {slug}}
	if brand != "" {
		q.Set("brand", brand)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/preview?"+q.Encode(), nil))
	return rec
}

// addInstructor serves POST /api/follows for an instructor follow of slug in
// brand.
func addInstructor(t *testing.T, slug, brand string) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")
	body := `{"kind":"instructor","slug":` + quoteJSON(slug) + `,"brand":` + quoteJSON(brand) + `}`
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/follows", strings.NewReader(body)))
	return rec
}

func quoteJSON(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

// TestAMalformedInstructorNameIs400 (security LOW-4) proves instructor input
// nothing can normalise into a slug is the user's to fix: preview and add both
// answer 400 saying what's accepted, without asking Musora, and without
// echoing the input. Before, it came back as 502 "Musora couldn't be reached".
// A name in any case, like "Jared Falk", is normalised since ruling #70
// (TestInstructorInputIsNormalisedAlike).
func TestAMalformedInstructorNameIs400(t *testing.T) {
	for _, slug := range []string{"https://www.drumeo.com/laravel/public/drumeo/coaches/jared-falk", `x'||true||'`, "a\nb", `say "hi"`} {
		t.Run(slug, func(t *testing.T) {
			calls := countSanity(t, `{"result":[{"_id":"abc","name":"Jane","id":7}]}`)
			rec := previewSlug(t, slug, "")
			wantError(t, rec, http.StatusBadRequest, msgBadSlug)
			rec = addInstructor(t, slug, "drumeo")
			wantError(t, rec, http.StatusBadRequest, msgBadSlug)
			if n := calls.Load(); n != 0 {
				t.Errorf("Musora was asked %d times, want none", n)
			}
		})
	}
}

// TestABrandMusoraDoesNotHaveIs400 (BACKLOG D9(c)) proves a brand outside
// Musora's is refused with a 400 on preview and on add, before any lookup;
// before, the preview answered 502 and the add stored it, so every sync of
// that follow failed.
func TestABrandMusoraDoesNotHaveIs400(t *testing.T) {
	calls := countSanity(t, `{"result":[{"_id":"abc","name":"Jane","id":7}]}`)
	wantError(t, previewSlug(t, "jane", "rockstar"), http.StatusBadRequest, msgBadBrand)
	wantError(t, addInstructor(t, "jane", "Drumeo"), http.StatusBadRequest, msgBadBrand)
	if n := calls.Load(); n != 0 {
		t.Errorf("Musora was asked %d times, want none", n)
	}
}

// TestAnUnreachableInstructorLookupIs502 (code review, unpinned) proves a
// well-formed slug Musora can't be asked about is still a 502, on preview and
// on add, with the detail logged.
func TestAnUnreachableInstructorLookupIs502(t *testing.T) {
	sanityUnreachable(t)
	log := captureLog(t)
	wantError(t, previewSlug(t, "jared-falk", ""), http.StatusBadGateway, msgPreviewUnreachable)
	wantError(t, addInstructor(t, "jared-falk", "drumeo"), http.StatusBadGateway, msgAddUnreachable)
	for _, want := range []string{"drumdrop: preview instructor:", "drumdrop: add follow: look up instructor:"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log %q lacks %q", log.String(), want)
		}
	}
}
