package database

import (
	"path/filepath"
	"testing"
)

func TestOpenSetsPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}
}

func TestOpenBadPath(t *testing.T) {
	// A path whose parent directory does not exist should fail when the
	// connection is first used (pragmas exercise the handle).
	bad := filepath.Join(t.TempDir(), "no-such-dir", "test.db")
	if _, err := Open(bad); err == nil {
		t.Fatalf("Open(%q) = nil error, want error", bad)
	}
}
