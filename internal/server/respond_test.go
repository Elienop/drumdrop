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

func TestMapStoreErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", fmt.Errorf("get follow 1: %w", sql.ErrNoRows), http.StatusNotFound},
		{"job not active", fmt.Errorf("cancel job 1: %w", database.ErrJobNotActive), http.StatusConflict},
		{"job not terminal", fmt.Errorf("retry job 1: %w", database.ErrJobNotTerminal), http.StatusConflict},
		{"other", fmt.Errorf("boom"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapStoreErr(tc.err); got != tc.want {
				t.Errorf("mapStoreErr(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
