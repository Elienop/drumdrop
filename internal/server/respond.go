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

// writeStoreErr writes the appropriate HTTP error response for a database
// error:
//   - a wrapped sql.ErrNoRows → 404 with notFoundMsg
//   - ErrJobNotActive → 409 "job is not active"
//   - ErrJobNotTerminal → 409 "job is not in a terminal state"
//   - anything else → 500 "internal error" (the raw err is never leaked, so
//     internal SQL phrasing such as "sql: no rows in result set" stays out of
//     responses; callers should log the raw err server-side if useful)
//
// ErrAlreadyFollowing is intentionally not handled here: the follow handler maps
// it to a 200 with the existing row, so it never reaches writeStoreErr.
func writeStoreErr(w http.ResponseWriter, err error, notFoundMsg string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, notFoundMsg)
	case errors.Is(err, database.ErrJobNotActive):
		writeErr(w, http.StatusConflict, "job is not active")
	case errors.Is(err, database.ErrJobNotTerminal):
		writeErr(w, http.StatusConflict, "job is not in a terminal state")
	default:
		writeErr(w, http.StatusInternalServerError, "internal error")
	}
}
