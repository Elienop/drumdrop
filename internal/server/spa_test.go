package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":        {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/app.123.js": {Data: []byte("console.log('hi')")},
	}
}

func TestSPAServesIndexForUnknownClientRoute(t *testing.T) {
	h := spaHandler(testDist())
	req := httptest.NewRequest(http.MethodGet, "/follows", nil)
	rw := httptest.NewRecorder()
	h(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rw.Code)
	}
	body, _ := io.ReadAll(rw.Result().Body)
	if !strings.Contains(string(body), "id=root") {
		t.Fatalf("client route should fall back to index.html, got %q", body)
	}
}

func TestSPAServesAsset(t *testing.T) {
	h := spaHandler(testDist())
	req := httptest.NewRequest(http.MethodGet, "/assets/app.123.js", nil)
	rw := httptest.NewRecorder()
	h(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rw.Code)
	}
	if ct := rw.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("asset should keep its JS content-type, got %q", ct)
	}
}

func TestSPADoesNotShadowAPIOrHealth(t *testing.T) {
	h := spaHandler(testDist())
	for _, p := range []string{"/api/does-not-exist", "/api/follows", "/healthz", "/readyz"} {
		rw := httptest.NewRecorder()
		h(rw, httptest.NewRequest(http.MethodGet, p, nil))
		if rw.Code != http.StatusNotFound {
			t.Fatalf("%s: SPA handler must 404 (not serve index.html), got %d", p, rw.Code)
		}
		body, _ := io.ReadAll(rw.Result().Body)
		if strings.Contains(string(body), "id=root") {
			t.Fatalf("%s: must NOT serve index.html", p)
		}
	}
}

func TestSPAMethodNotAllowed(t *testing.T) {
	h := spaHandler(testDist())
	rw := httptest.NewRecorder()
	h(rw, httptest.NewRequest(http.MethodPost, "/follows", nil))
	if rw.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-GET to a client route should be 405, got %d", rw.Code)
	}
}

func TestSPANotBuiltNotice(t *testing.T) {
	h := spaHandler(fstest.MapFS{}) // empty: no index.html
	rw := httptest.NewRecorder()
	h(rw, httptest.NewRequest(http.MethodGet, "/", nil))
	if rw.Code != http.StatusOK {
		t.Fatalf("want 200 notice, got %d", rw.Code)
	}
	body, _ := io.ReadAll(rw.Result().Body)
	if !strings.Contains(string(body), "not built") {
		t.Fatalf("empty FS should serve the not-built notice, got %q", body)
	}
}
