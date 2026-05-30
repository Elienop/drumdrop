package server

import (
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

func TestWriteStoreErr(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{
			name:     "no rows maps to 404 with notFoundMsg",
			err:      fmt.Errorf("get follow 1: %w", sql.ErrNoRows),
			wantCode: http.StatusNotFound,
			wantMsg:  "follow not found",
		},
		{
			name:     "job not active maps to 409 with clean message",
			err:      fmt.Errorf("cancel job 1: %w", database.ErrJobNotActive),
			wantCode: http.StatusConflict,
			wantMsg:  "job is not active",
		},
		{
			name:     "job not terminal maps to 409 with clean message",
			err:      fmt.Errorf("retry job 1: %w", database.ErrJobNotTerminal),
			wantCode: http.StatusConflict,
			wantMsg:  "job is not in a terminal state",
		},
		{
			name:     "default maps to 500 without leaking raw error",
			err:      fmt.Errorf("sql: no rows in result set: secret table internals"),
			wantCode: http.StatusInternalServerError,
			wantMsg:  "internal error",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeStoreErr(rec, tc.err, "follow not found")

			if rec.Code != tc.wantCode {
				t.Errorf("code = %d, want %d", rec.Code, tc.wantCode)
			}
			var got struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Error != tc.wantMsg {
				t.Errorf("error field = %q, want %q", got.Error, tc.wantMsg)
			}
		})
	}
}
