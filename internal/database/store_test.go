package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// TestConcurrentReadsDuringWrite drives readers against the database while a
// write transaction is deliberately held open. Under WAL with the write mutex
// serializing writers and a >1 connection pool, the readers must complete
// without "database is locked" and without tripping the race detector. The
// in-flight write holds Store.mu (via withTx); the readers go straight to
// QueryContext and must not contend on that mutex.
func TestConcurrentReadsDuringWrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Seed a row so reads have something to scan.
	if _, err := s.AddNodeFollow(ctx, 100, "Seed", "drumeo", "max"); err != nil {
		t.Fatalf("seed AddNodeFollow: %v", err)
	}

	// writeStarted fires once the write tx is open and holding Store.mu;
	// releaseWrite lets the held write commit. This guarantees readers run
	// while the write is genuinely in-flight.
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	writeDone := make(chan error, 1)

	go func() {
		writeDone <- s.withTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO follows(kind, railcontent_id) VALUES('node', 200)",
			); err != nil {
				return err
			}
			close(writeStarted)
			<-releaseWrite
			return nil
		})
	}()

	<-writeStarted

	// readWhileHeld performs one read with a short deadline. It must succeed
	// while the write tx is still open: under WAL with a >1 connection pool the
	// reader uses a separate connection and never blocks on the writer. With a
	// single shared connection the read would block on the held write and time
	// out, which is the regression this test guards against.
	readWhileHeld := func() error {
		rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		var n int
		return s.db.QueryRowContext(rctx,
			"SELECT count(*) FROM follows WHERE kind='node'").Scan(&n)
	}

	const readers = 8
	var wg sync.WaitGroup
	errCh := make(chan error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := readWhileHeld(); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			if strings.Contains(err.Error(), "database is locked") {
				t.Fatalf("read hit lock contention: %v", err)
			}
			t.Fatalf("concurrent read while write in-flight failed: %v", err)
		}
	}

	// All reads completed against the still-open write; now release and verify
	// the write committed cleanly.
	close(releaseWrite)
	if err := <-writeDone; err != nil {
		t.Fatalf("held write returned error: %v", err)
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
