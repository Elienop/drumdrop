package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Store wraps the application database handle. Every mutation in the database
// package flows through Store.withTx, the single transaction chokepoint, so
// rollback-on-error and commit-on-success are guaranteed in exactly one place.
type Store struct {
	db *sql.DB
}

// NewStore returns a Store backed by the given handle. The handle must already
// have its pragmas applied (see Open) and its migrations run (see RunMigrations).
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying handle for read-only helpers and tests. Callers must
// not use it to bypass withTx for mutations.
func (s *Store) DB() *sql.DB {
	return s.db
}

// withTx runs fn inside a single transaction. The transaction is rolled back if
// fn returns an error (or panics, via the deferred Rollback) and committed only
// when fn returns nil. This is the one place mutations are allowed to commit, so
// no store method opens its own transaction.
//
// The deferred Rollback after a successful Commit is a harmless no-op:
// database/sql returns sql.ErrTxDone, which we deliberately ignore.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit; rolls back on error/panic

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
