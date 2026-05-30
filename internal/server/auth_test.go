package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// newAuthTestServer builds a server with the given Config so the auth and CORS
// middleware can be exercised end-to-end against the real /api routes.
func newAuthTestServer(t *testing.T, cfg Config) http.Handler {
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
	return NewServer(store, Deps{}, nil, cfg, "test-version")
}

func TestAuthAllowsBearerToken(t *testing.T) {
	srv := newAuthTestServer(t, Config{ListenAddr: "0.0.0.0:8080", APIToken: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/follows", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAuthAllowsAccessTokenQuery(t *testing.T) {
	srv := newAuthTestServer(t, Config{ListenAddr: "0.0.0.0:8080", APIToken: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/follows?access_token=secret", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAuthRejectsMissingToken(t *testing.T) {
	srv := newAuthTestServer(t, Config{ListenAddr: "0.0.0.0:8080", APIToken: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/follows", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthRejectsWrongToken(t *testing.T) {
	srv := newAuthTestServer(t, Config{ListenAddr: "0.0.0.0:8080", APIToken: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/follows", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthLoopbackNoTokenAllowed(t *testing.T) {
	srv := newAuthTestServer(t, Config{ListenAddr: "127.0.0.1:8080", APIToken: ""})

	req := httptest.NewRequest(http.MethodGet, "/api/follows", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAuthNonLoopbackNoTokenRejected(t *testing.T) {
	srv := newAuthTestServer(t, Config{ListenAddr: "0.0.0.0:8080", APIToken: ""})

	req := httptest.NewRequest(http.MethodGet, "/api/follows", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHealthzExemptFromAuth(t *testing.T) {
	srv := newAuthTestServer(t, Config{ListenAddr: "0.0.0.0:8080", APIToken: "secret"})

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
}

func TestCORSPreflight(t *testing.T) {
	srv := newAuthTestServer(t, Config{
		ListenAddr: "0.0.0.0:8080",
		APIToken:   "secret",
		CORSOrigin: "https://app.example.com",
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/follows", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "https://app.example.com")
	}
	allowHeaders := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(allowHeaders, "Authorization") {
		t.Errorf("Allow-Headers = %q, want it to include Authorization", allowHeaders)
	}
}

func TestCORSHeaderOnAPIResponse(t *testing.T) {
	srv := newAuthTestServer(t, Config{
		ListenAddr: "127.0.0.1:8080",
		CORSOrigin: "https://app.example.com",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/follows", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "https://app.example.com")
	}
}

func TestGuardListen(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		token   string
		wantErr bool
	}{
		{"loopback ip no token", "127.0.0.1:8080", "", false},
		{"loopback range no token", "127.0.0.5:8080", "", false},
		{"ipv6 loopback no token", "[::1]:8080", "", false},
		{"localhost no token", "localhost:8080", "", false},
		{"empty host no token", ":8080", "", true},
		{"empty whole-addr no token", "", "", false},
		{"non-loopback no token", "0.0.0.0:8080", "", true},
		{"non-loopback public no token", "192.168.1.10:8080", "", true},
		{"non-loopback with token", "0.0.0.0:8080", "secret", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := GuardListen(tt.addr, tt.token)
			if tt.wantErr && err == nil {
				t.Fatalf("GuardListen(%q, %q) = nil, want error", tt.addr, tt.token)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("GuardListen(%q, %q) = %v, want nil", tt.addr, tt.token, err)
			}
		})
	}
}
