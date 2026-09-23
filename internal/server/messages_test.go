package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInlineAnswersAreFixedSentences (D14) proves the answers the web UI
// shows inline in its dialogs are the fixed sentences, never the input sent
// back nor a bare "not found".
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
