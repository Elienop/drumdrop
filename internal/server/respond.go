package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/elienop/drumdrop/internal/database"
)

// writeJSON serializes v as JSON and writes it with the given status code and
// an application/json Content-Type. A marshal failure is rare for the DTO types
// the API serves; if it happens after the header is committed there is nothing
// useful left to do, so the error is dropped.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody is the uniform JSON envelope for API errors: {"error": "..."}.
type errorBody struct {
	Error string `json:"error"`
}

// writeErr writes a JSON error envelope with the given status code and message.
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errorBody{Error: msg})
}

// mapStoreErr translates a database error into an HTTP status code:
//   - a wrapped sql.ErrNoRows → 404 Not Found
//   - ErrJobNotActive / ErrJobNotTerminal → 409 Conflict
//   - anything else → 500 Internal Server Error
//
// ErrAlreadyFollowing is intentionally not handled here: the follow handler maps
// it to a 200 with the existing row, so it never reaches mapStoreErr.
func mapStoreErr(err error) int {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case errors.Is(err, database.ErrJobNotActive),
		errors.Is(err, database.ErrJobNotTerminal):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
