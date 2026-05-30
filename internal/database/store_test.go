package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// newTestStore opens a fresh temp-file DB, runs migrations, and returns a Store
// backed by it. Tasks 5-7 reuse this helper, so its behavior is intentionally
// minimal and consistent: temp DB + migrations + cleanup wired by openMigrated.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(openMigrated(t))
}

// countFollows returns the number of rows in follows. Used to assert that a
// rolled-back insert left no trace.
func countFollows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.rawDB().QueryRow("SELECT count(*) FROM follows").Scan(&n); err != nil {
		t.Fatalf("count follows: %v", err)
	}
	return n
}

func TestWithTxCommitsOnNil(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"INSERT INTO follows(kind, railcontent_id) VALUES('node', 100)",
		)
		return err
	})
	if err != nil {
		t.Fatalf("withTx returned error on success path: %v", err)
	}

	if got := countFollows(t, s); got != 1 {
		t.Errorf("after committed insert, follows has %d rows, want 1", got)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sentinel := errors.New("boom")
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		// The insert itself succeeds inside the tx...
		if _, err := tx.Exec(
			"INSERT INTO follows(kind, railcontent_id) VALUES('node', 200)",
		); err != nil {
			return err
		}
		// ...but fn then reports failure, so withTx must roll the insert back.
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("withTx error = %v, want %v", err, sentinel)
	}

	if got := countFollows(t, s); got != 0 {
		t.Errorf("after rolled-back insert, follows has %d rows, want 0", got)
	}
}

func TestStorePing(t *testing.T) {
	s := newTestStore(t)

	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping on open store: %v", err)
	}
}

func TestStorePingAfterClose(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s := NewStore(db)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := s.Ping(context.Background()); err == nil {
		t.Error("Ping after Close succeeded, want error")
	}
}

func TestStoreClose(t *testing.T) {
	// Open the DB directly (not via openMigrated, whose t.Cleanup would also
	// Close it and turn this test's explicit Close into a double-close error).
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s := NewStore(db)

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close the handle is unusable; a query should fail rather than panic.
	if err := s.rawDB().Ping(); err == nil {
		t.Error("Ping after Close succeeded, want error")
	}
}
