package server

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]string{"hello": "world"})

	if rec.Code != http.StatusCreated {
		t.Errorf("code = %d, want %d", rec.Code, http.StatusCreated)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["hello"] != "world" {
		t.Errorf("body = %v, want hello=world", got)
	}
}

func TestWriteErr(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErr(rec, http.StatusNotFound, "missing thing")

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error != "missing thing" {
		t.Errorf("error field = %q, want %q", got.Error, "missing thing")
	}
}

// TestWriteStoreErr pins each store error's answer, for a write
// (writeStoreErr) and a read (writeLoadErr): only the 500 differs, since a
// read changes nothing and sits next to a Retry button (UI N3).
func TestWriteStoreErr(t *testing.T) {
	captureLog(t)
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
		loadMsg  string
	}{
		{
			name:     "no rows maps to 404 with notFoundMsg",
			err:      fmt.Errorf("get follow 1: %w", sql.ErrNoRows),
			wantCode: http.StatusNotFound,
			wantMsg:  msgNoSuchFollow,
		},
		{
			name:     "job not active maps to 409 with a sentence",
			err:      fmt.Errorf("cancel job 1: %w", database.ErrJobNotActive),
			wantCode: http.StatusConflict,
			wantMsg:  msgJobNotActive,
		},
		{
			name:     "job not terminal maps to 409 with a sentence",
			err:      fmt.Errorf("retry job 1: %w", database.ErrJobNotTerminal),
			wantCode: http.StatusConflict,
			wantMsg:  msgJobNotRetry,
		},
		{
			name:     "lesson deleting maps to 409 with a sentence",
			err:      fmt.Errorf("lesson 1: %w", database.ErrLessonDeleting),
			wantCode: http.StatusConflict,
			wantMsg:  msgBeingDeleted,
		},
		{
			name:     "default maps to 500 without leaking raw error",
			err:      fmt.Errorf("sql: no rows in result set: secret table internals"),
			wantCode: http.StatusInternalServerError,
			wantMsg:  msgServerError,
			loadMsg:  msgLoadFailed,
		},
	}
	for _, tc := range tests {
		for _, w := range []struct {
			name  string
			write func(http.ResponseWriter, error, string)
			want  string
		}{
			{"write", writeStoreErr, tc.wantMsg},
			{"read", writeLoadErr, cmp.Or(tc.loadMsg, tc.wantMsg)},
		} {
			t.Run(w.name+" "+tc.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				w.write(rec, tc.err, msgNoSuchFollow)

				if rec.Code != tc.wantCode {
					t.Errorf("code = %d, want %d", rec.Code, tc.wantCode)
				}
				var got struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if got.Error != w.want {
					t.Errorf("error field = %q, want %q", got.Error, w.want)
				}
			})
		}
	}
}
