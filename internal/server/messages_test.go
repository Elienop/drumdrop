package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// TestInlineAnswersAreFixedSentences (D14, UI N3, security Info 6) proves the
// answers the web UI shows, inline in its dialogs or in a toast, are the fixed
// sentences, never the input sent back nor a bare "not found".
func TestInlineAnswersAreFixedSentences(t *testing.T) {
	store := newTestStore(t)
	srv := NewServer(store, Deps{}, nil, Config{}, "test")
	for _, c := range []struct {
		method, target, body string
		code                 int
		msg                  string
	}{
		{http.MethodGet, "/api/preview?id=%3Cscript%3Eno-digits", "", http.StatusBadRequest, msgNoContentID},
		{http.MethodGet, "/api/preview", "", http.StatusBadRequest, msgPreviewNothing},
		{http.MethodPost, "/api/follows", `{"kind":"node","id":"no digits"}`, http.StatusBadRequest, msgNoContentID},
		{http.MethodPost, "/api/follows", `{"kind":"bogus"}`, http.StatusBadRequest, msgBadKind},
		{http.MethodPost, "/api/follows", `{"kind":"instructor"}`, http.StatusBadRequest, msgSlugRequired},
		{http.MethodPost, "/api/follows", `{"kind":"node","id":"5","quality":"1080p"}`, http.StatusBadRequest, msgBadQuality},
		{http.MethodPost, "/api/follows", `not json`, http.StatusBadRequest, msgBadBody},
		{http.MethodPatch, "/api/follows/404", `{"quality":"720"}`, http.StatusNotFound, msgEditGone},
		{http.MethodDelete, "/api/follows/404", "", http.StatusNotFound, msgFollowGone},
		{http.MethodDelete, "/api/follows/404?files=true", "", http.StatusNotFound, msgFollowGone},
		{http.MethodDelete, "/api/lessons/404", "", http.StatusNotFound, msgLessonGone},
		{http.MethodPost, "/api/lessons/404/skip", "", http.StatusNotFound, msgSkipGone},
		{http.MethodPost, "/api/lessons/404/download", "", http.StatusNotFound, msgDownloadGone},
		{http.MethodPost, "/api/lessons/404/unskip", "", http.StatusNotFound, msgUnskipGone},
		{http.MethodPost, "/api/jobs/404/cancel", "", http.StatusNotFound, msgCancelGone},
		{http.MethodPost, "/api/jobs/404/retry", "", http.StatusNotFound, msgRetryGone},
		{http.MethodGet, "/api/lessons/404", "", http.StatusNotFound, msgNoSuchLesson},
		{http.MethodGet, "/api/follows/404", "", http.StatusNotFound, msgNoSuchFollow},
		{http.MethodGet, "/api/follows/404/lessons", "", http.StatusNotFound, msgNoSuchFollow},
		{http.MethodGet, "/api/jobs/404", "", http.StatusNotFound, msgNoSuchJob},
		{http.MethodGet, "/api/lessons/%3Cscript%3E", "", http.StatusBadRequest, msgBadPathNumber("id")},
		{http.MethodDelete, "/api/follows/%3Cscript%3E", "", http.StatusBadRequest, msgBadPathNumber("id")},
		{http.MethodGet, "/api/lessons?limit=%3Cscript%3E", "", http.StatusBadRequest, msgBadQueryNumber("limit")},
		{http.MethodPost, "/api/sync", `not json`, http.StatusBadRequest, msgBadBody},
		{http.MethodPost, "/api/pause", "", http.StatusServiceUnavailable, msgNoDaemon},
	} {
		t.Run(c.method+" "+c.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, strings.NewReader(c.body)))
			wantError(t, rec, c.code, c.msg)
			if strings.Contains(rec.Body.String(), "script") {
				t.Errorf("the answer echoes the input: %s", rec.Body.String())
			}
		})
	}
}

// allMessages returns every msg* constant in messages.go, by name, plus the
// two number messages, read from the source so a new constant can't skip the
// copy rules.
func allMessages(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "messages.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]string{
		"msgBadPathNumber":  msgBadPathNumber("id"),
		"msgBadQueryNumber": msgBadQueryNumber("limit"),
	}
	for _, d := range file.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || !strings.HasPrefix(name.Name, "msg") {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %v", name.Name, err)
				}
				msgs[name.Name] = v
			}
		}
	}
	return msgs
}

// TestMessagesFollowTheCopyRules (UI "server copy" 1-4) checks the mechanical
// copy rules on every message a client can read: short enough for a dialog,
// one voice with contractions, one wording for pointing at the log,
// "elsewhere" rather than "another window", and a finished sentence.
func TestMessagesFollowTheCopyRules(t *testing.T) {
	msgs := allMessages(t)
	if len(msgs) < 40 {
		t.Fatalf("found %d messages, want every msg constant", len(msgs))
	}
	uncontracted := []string{"could not", "can not", "cannot", "did not", "does not", "do not", "is not", "was not", "were not", "has not", "will not"}
	for name, m := range msgs {
		if n := utf8.RuneCountInString(m); n > maxMessageLen {
			t.Errorf("%s is %d characters, want at most %d: %q", name, n, maxMessageLen, m)
		}
		for _, u := range uncontracted {
			if strings.Contains(strings.ToLower(m), u) {
				t.Errorf("%s says %q, want the contraction: %q", name, u, m)
			}
		}
		if strings.Contains(m, "another window") {
			t.Errorf("%s says \"another window\", want \"elsewhere\": %q", name, m)
		}
		if strings.Contains(m, "server log") && !strings.Contains(m, "Check the server log, fix the problem, then ") {
			t.Errorf("%s points at the log in another wording: %q", name, m)
		}
		if !strings.HasSuffix(m, ".") || strings.Contains(m, "%") {
			t.Errorf("%s is not a finished sentence: %q", name, m)
		}
	}
}

// TestStoreFailuresSayWhatHappened (UI N3) proves a store failure on a read
// says the page couldn't be loaded (it sits next to a Retry button), and one
// on a change promises nothing either way (part of it may be done).
func TestStoreFailuresSayWhatHappened(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	store := database.NewStore(db)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	captureLog(t)
	srv := NewServer(store, Deps{}, nil, Config{}, "test")
	for _, c := range []struct {
		method, target string
		msg            string
	}{
		{http.MethodGet, "/api/lessons", msgLoadFailed},
		{http.MethodGet, "/api/lessons?status=failed", msgLoadFailed},
		{http.MethodGet, "/api/follows", msgLoadFailed},
		{http.MethodGet, "/api/jobs", msgLoadFailed},
		{http.MethodGet, "/api/summary", msgLoadFailed},
		{http.MethodGet, "/api/lessons/1", msgLoadFailed},
		{http.MethodPost, "/api/lessons/1/download", msgServerError},
		{http.MethodPost, "/api/jobs/1/retry", msgServerError},
	} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, nil))
		wantError(t, rec, http.StatusInternalServerError, c.msg)
	}
}

// TestBadBrandNamesTheBrandsAsTheUIDoes (round-5c UI) proves msgBadBrand
// names each brand Musora has as the web UI shows it (brandName,
// web/src/lib/format.ts: "Pianote"; playbass, whose casing is unconfirmed,
// as sent), and that each name it gives is one the Brand field accepts.
func TestBadBrandNamesTheBrandsAsTheUIDoes(t *testing.T) {
	for _, name := range []string{"Drumeo", "Pianote", "Guitareo", "Singeo", "playbass"} {
		if !strings.Contains(msgBadBrand, " "+name+",") && !strings.Contains(msgBadBrand, " "+name+" ") {
			t.Errorf("msgBadBrand %q does not name %s", msgBadBrand, name)
		}
		if _, err := musora.NodeBrand(name); err != nil {
			t.Errorf("the Brand field refuses %q, which msgBadBrand offers: %v", name, err)
		}
	}
	for _, lower := range []string{"drumeo", "pianote", "guitareo", "singeo"} {
		if strings.Contains(msgBadBrand, lower) {
			t.Errorf("msgBadBrand %q names %s in lower case, where the UI capitalises it", msgBadBrand, lower)
		}
	}
}
