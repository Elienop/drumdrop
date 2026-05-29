package musora

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogin(t *testing.T) {
	t.Setenv("DRUMDROP_CONFIG_DIR", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "abc123"})
		w.Write([]byte(`{"user":{"email":"a@b.com"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// Save and restore the package global so a torn-down server URL can never
	// leak into later tests (httptest URLs are dead once srv.Close runs).
	prevBase := AuthBase
	t.Cleanup(func() { AuthBase = prevBase })
	AuthBase = srv.URL

	cookie, err := Login("a@b.com", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if cookie != sessionCookieName+"=abc123" {
		t.Fatalf("cookie = %q", cookie)
	}
	if LoadCookie() != cookie {
		t.Fatal("cookie not persisted")
	}
}
