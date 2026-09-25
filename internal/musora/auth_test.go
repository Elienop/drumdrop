package musora

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// stubLogin points AuthBase at a /sessions endpoint that answers status with
// body and, on a 2xx, a session cookie.
func stubLogin(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status/100 == 2 {
			http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "abc123"})
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := AuthBase
	t.Cleanup(func() { AuthBase = prev })
	AuthBase = srv.URL
}

// TestLoginSaysWhetherMusoraRejectedTheCredentials (UI X1) proves Login's
// error tells a refusal of the email and password (a 4xx) from Musora being
// unreachable or unreadable (a 5xx, a 408 or 429, an undecodable 2xx), so the
// API can answer each differently; and that the CLI's text is unchanged.
func TestLoginSaysWhetherMusoraRejectedTheCredentials(t *testing.T) {
	t.Setenv("DRUMDROP_CONFIG_DIR", t.TempDir())
	for _, c := range []struct {
		status   int
		body     string
		rejected bool
	}{
		{http.StatusUnauthorized, `{"message":"Invalid credentials"}`, true},
		{http.StatusUnprocessableEntity, `{"message":"The email field is required."}`, true},
		{http.StatusForbidden, ``, true},
		{http.StatusInternalServerError, `{"message":"Server Error"}`, false},
		{http.StatusBadGateway, `<html>bad gateway</html>`, false},
		{http.StatusTooManyRequests, `{"message":"Too Many Attempts."}`, false},
		{http.StatusRequestTimeout, ``, false},
		{http.StatusOK, `not json`, false},
		{http.StatusOK, `{"message":"no user"}`, false},
	} {
		stubLogin(t, c.status, c.body)
		_, err := Login("a@b.com", "pw")
		if err == nil {
			t.Fatalf("%d %s: Login succeeded, want an error", c.status, c.body)
		}
		if got := errors.Is(err, ErrLoginRejected); got != c.rejected {
			t.Errorf("%d %s: rejected = %v (%v), want %v", c.status, c.body, got, err, c.rejected)
		}
		if errors.Is(err, ErrSessionNotSaved) {
			t.Errorf("%d %s: %v wraps ErrSessionNotSaved", c.status, c.body, err)
		}
	}
	stubLogin(t, http.StatusUnauthorized, `{"message":"Invalid credentials"}`)
	if _, err := Login("a@b.com", "pw"); err == nil || err.Error() != "login failed: Invalid credentials" {
		t.Errorf("CLI text = %v, want \"login failed: Invalid credentials\"", err)
	}
}

// TestLoginSaysWhenTheSessionCouldNotBeSaved proves a login Musora accepted
// but whose cookie could not be written is told apart from Musora failing.
func TestLoginSaysWhenTheSessionCouldNotBeSaved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "file-not-folder")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRUMDROP_CONFIG_DIR", dir)
	stubLogin(t, http.StatusOK, `{"user":{"email":"a@b.com"}}`)
	_, err := Login("a@b.com", "pw")
	if !errors.Is(err, ErrSessionNotSaved) || errors.Is(err, ErrLoginRejected) {
		t.Fatalf("Login = %v, want ErrSessionNotSaved alone", err)
	}
	if !strings.Contains(err.Error(), "file-not-folder") {
		t.Errorf("Login = %v, want the cause kept for the log", err)
	}
}
