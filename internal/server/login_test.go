package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// stubSessions points musora.AuthBase at a /sessions endpoint answering status
// with body (and, on a 2xx, a session cookie), restoring it when the test ends.
func stubSessions(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status/100 == 2 {
			http.SetCookie(w, &http.Cookie{Name: "musora_platform_backend_session", Value: "tok"})
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := musora.AuthBase
	musora.AuthBase = srv.URL
	t.Cleanup(func() { musora.AuthBase = prev })
}

// stubUnreachable points musora.AuthBase at an address nothing listens on.
func stubUnreachable(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	prev := musora.AuthBase
	musora.AuthBase = url
	t.Cleanup(func() { musora.AuthBase = prev })
}

func postLogin(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(body)))
	return rec
}

// TestLoginNeverAnswers401 (UI X1) pins POST /api/session's contract with the
// web client, which takes any 401 for its own API token being refused and
// clears it: credentials Musora refused are a 422 with the fixed sentence;
// Musora unreachable, failing or unreadable is a 502; a session that couldn't
// be saved is a 500; a bad body or a missing field is a 400. None is a 401.
func TestLoginNeverAnswers401(t *testing.T) {
	creds := `{"email":"a@b.com","password":"pw"}`
	for _, c := range []struct {
		name    string
		musora  int    // Musora's status; 0: Musora can't be reached
		answer  string // Musora's body
		body    string // the request's body
		unsaved bool   // the config folder can't be written
		code    int
		msg     string
	}{
		{"rejected 401", http.StatusUnauthorized, `{"message":"Invalid credentials"}`, creds, false, http.StatusUnprocessableEntity, msgLoginRejected},
		{"rejected 422", http.StatusUnprocessableEntity, `{"message":"The given data was invalid."}`, creds, false, http.StatusUnprocessableEntity, msgLoginRejected},
		{"unreachable", 0, ``, creds, false, http.StatusBadGateway, msgLoginUnreachable},
		{"musora 5xx", http.StatusServiceUnavailable, `<html>down</html>`, creds, false, http.StatusBadGateway, msgLoginUnreachable},
		{"undecodable 2xx", http.StatusOK, `not json`, creds, false, http.StatusBadGateway, msgLoginUnreachable},
		{"session not saved", http.StatusOK, `{"user":{"id":1}}`, creds, true, http.StatusInternalServerError, msgLoginNotSaved},
		{"bad body", 0, ``, `not json`, false, http.StatusBadRequest, msgBadBody},
		{"missing field", 0, ``, `{"email":"a@b.com","password":""}`, false, http.StatusBadRequest, msgLoginMissing},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.unsaved {
				dir = filepath.Join(dir, "a-file")
				if err := os.WriteFile(dir, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("DRUMDROP_CONFIG_DIR", dir)
			captureLog(t)
			if c.musora == 0 {
				stubUnreachable(t)
			} else {
				stubSessions(t, c.musora, c.answer)
			}
			wantError(t, postLogin(t, c.body), c.code, c.msg)
		})
	}
}

// TestOnlyTheAuthMiddlewareAnswers401 (UI X1) guards the other half of the
// contract: the web client clears its API token on ANY 401, so the auth
// middleware (withMiddleware) must be the only code in the package that names
// the status. It reads the package's own source: a 401 spelled as
// http.StatusUnauthorized or as the number, anywhere else, fails it. (A status
// passed through from elsewhere, like an upstream answer's, isn't caught.)
func TestOnlyTheAuthMiddlewareAnswers401(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var found []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if n.Sel.Name == "StatusUnauthorized" {
						found = append(found, f+":"+fn.Name.Name)
					}
				case *ast.BasicLit:
					if n.Kind == token.INT && n.Value == "401" {
						found = append(found, f+":"+fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	if len(found) != 1 || found[0] != "auth.go:withMiddleware" {
		t.Errorf("401 is named in %v, want only auth.go:withMiddleware", found)
	}
}
