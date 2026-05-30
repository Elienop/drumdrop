// Package database provides the pure-Go SQLite persistence layer for drumdrop.
//
// The driver is modernc.org/sqlite, registered under the name "sqlite" via the
// blank import below. It requires no cgo, which keeps drumdrop a single static
// binary that cross-compiles trivially.
package database

import (
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// maxOpenConns caps the connection pool. WAL lets readers run while a writer is
// in flight, so allowing several connections gives the HTTP server concurrent
// reads; writes are serialized by Store.mu, not by the pool. The cap stays
// modest because drumdrop is a single-process app, not a high-fanout service.
const maxOpenConns = 8

// Open opens (creating if necessary) the SQLite database at path and applies the
// pragmas drumdrop relies on: WAL journaling, enforced foreign keys, and a
// busy timeout. It verifies foreign_keys actually reads back as enabled and
// returns a wrapped error otherwise.
//
// The pragmas are passed as _pragma DSN parameters rather than via db.Exec so
// that modernc's driver applies them to every connection it opens, not just the
// first. foreign_keys in particular is per-connection in SQLite, so this is
// what makes SetMaxOpenConns(>1) safe: each pooled connection enforces FKs and
// shares the WAL journal. Writers are still serialized by Store.mu.
func Open(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "busy_timeout(5000)")
	dsn := path + "?" + q.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	db.SetMaxOpenConns(maxOpenConns)

	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		db.Close()
		return nil, fmt.Errorf("verify foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		db.Close()
		return nil, fmt.Errorf("foreign_keys not enabled (got %d)", foreignKeys)
	}

	return db, nil
}
