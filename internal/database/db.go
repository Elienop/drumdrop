// Package database provides the pure-Go SQLite persistence layer for drumdrop.
//
// The driver is modernc.org/sqlite, registered under the name "sqlite" via the
// blank import below. It requires no cgo, which keeps drumdrop a single static
// binary that cross-compiles trivially.
package database

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Open opens (creating if necessary) the SQLite database at path and applies the
// pragmas drumdrop relies on: WAL journaling, enforced foreign keys, and a
// busy timeout. It verifies foreign_keys actually reads back as enabled and
// returns a wrapped error otherwise.
//
// SetMaxOpenConns(1) is used because drumdrop is a single-process CLI; one
// connection sidesteps SQLITE_BUSY contention under WAL. A later server phase
// can revisit this.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply %q: %w", p, err)
		}
	}

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
