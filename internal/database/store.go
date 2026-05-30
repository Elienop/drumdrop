package database

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// Store wraps the application database handle. Every mutation in the database
// package flows through Store.withTx, the single transaction chokepoint, so
// rollback-on-error and commit-on-success are guaranteed in exactly one place.
//
// mu serializes writers. Under WAL the connection pool may hold more than one
// connection (see Open) so reads run concurrently, but SQLite still permits
// only one writer at a time. Taking mu in withTx makes that single-writer
// constraint explicit in Go rather than relying on SQLITE_BUSY/busy_timeout
// retries, which keeps writes deterministic and avoids "database is locked"
// surfacing to callers. Reads (plain QueryContext, not withTx) never take mu.
type Store struct {
	mu sync.Mutex
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

// Ping verifies a live connection to the database, establishing one if needed.
// It backs the server's /readyz readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// withTx runs fn inside a single transaction. The transaction is rolled back if
// fn returns an error (or panics, via the deferred Rollback) and committed only
// when fn returns nil. This is the one place mutations are allowed to commit, so
// no store method opens its own transaction.
//
// It holds s.mu for the duration so writers are serialized explicitly (see the
// Store doc comment); readers do not take s.mu and run concurrently under WAL.
//
// The deferred Rollback after a successful Commit is a harmless no-op:
// database/sql returns sql.ErrTxDone, which we deliberately ignore.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
