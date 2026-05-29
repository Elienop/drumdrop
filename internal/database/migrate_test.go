package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// openMigrated opens a fresh temp-file DB and runs all migrations against it.
func openMigrated(t *testing.T) *sql.DB {
	t.Helper()
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
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return db
}

// objectExists reports whether sqlite_master has an object of the given type
// (e.g. "table" or "index") with the given name.
func objectExists(t *testing.T, db *sql.DB, objType, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?",
		objType, name,
	).Scan(&n)
	if err != nil {
		t.Fatalf("query sqlite_master for %s %q: %v", objType, name, err)
	}
	return n > 0
}

func TestRunMigrationsCreatesSchema(t *testing.T) {
	db := openMigrated(t)

	for _, tbl := range []string{"follows", "lessons", "jobs", "schema_migrations"} {
		if !objectExists(t, db, "table", tbl) {
			t.Errorf("table %q does not exist after migrations", tbl)
		}
	}

	for _, idx := range []string{
		"idx_follows_node",
		"idx_follows_instructor",
		"idx_lessons_status",
		"idx_lessons_parent",
		"idx_jobs_status",
	} {
		if !objectExists(t, db, "index", idx) {
			t.Errorf("index %q does not exist after migrations", idx)
		}
	}
}

func TestRunMigrationsCheckConstraints(t *testing.T) {
	db := openMigrated(t)

	// follows.kind CHECK rejects values outside ('node','instructor').
	if _, err := db.Exec(
		"INSERT INTO follows(kind, railcontent_id) VALUES('bogus', 1)",
	); err == nil {
		t.Error("inserting follows with kind='bogus' succeeded, want CHECK violation")
	}

	// lessons.status CHECK rejects values outside the allowed set.
	if _, err := db.Exec(
		"INSERT INTO lessons(railcontent_id, status) VALUES(1, 'nope')",
	); err == nil {
		t.Error("inserting lessons with status='nope' succeeded, want CHECK violation")
	}

	// jobs.status CHECK rejects values outside the allowed set.
	if _, err := db.Exec(
		"INSERT INTO jobs(railcontent_id, status) VALUES(1, 'huh')",
	); err == nil {
		t.Error("inserting jobs with status='huh' succeeded, want CHECK violation")
	}

	// A valid row still inserts cleanly (guards against an over-broad CHECK).
	if _, err := db.Exec(
		"INSERT INTO follows(kind, railcontent_id) VALUES('node', 42)",
	); err != nil {
		t.Errorf("inserting a valid follows row failed: %v", err)
	}
}

func TestRunMigrationsIdempotent(t *testing.T) {
	db := openMigrated(t)

	// A second run must be a no-op (no error, no duplicate application).
	if err := RunMigrations(db); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("schema_migrations has %d rows, want 1", count)
	}

	var version string
	if err := db.QueryRow("SELECT version FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != "001_initial_schema.sql" {
		t.Errorf("recorded version = %q, want %q", version, "001_initial_schema.sql")
	}
}

func TestFollowsPartialUnique(t *testing.T) {
	db := openMigrated(t)

	// Two node rows with the same railcontent_id collide on idx_follows_node.
	if _, err := db.Exec(
		"INSERT INTO follows(kind, railcontent_id) VALUES('node', 100)",
	); err != nil {
		t.Fatalf("first node insert: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO follows(kind, railcontent_id) VALUES('node', 100)",
	); err == nil {
		t.Error("second node row with railcontent_id=100 succeeded, want unique violation")
	}

	// Two instructor rows with the same slug collide on idx_follows_instructor.
	if _, err := db.Exec(
		"INSERT INTO follows(kind, slug) VALUES('instructor', 'aaron-edgar')",
	); err != nil {
		t.Fatalf("first instructor insert: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO follows(kind, slug) VALUES('instructor', 'aaron-edgar')",
	); err == nil {
		t.Error("second instructor row with slug='aaron-edgar' succeeded, want unique violation")
	}

	// A node row and an instructor row do NOT collide even though the partial
	// indexes are separate: a node carrying a slug-less row and an instructor
	// carrying a railcontent_id-less row live in disjoint partial indexes.
	if _, err := db.Exec(
		"INSERT INTO follows(kind, railcontent_id) VALUES('node', 200)",
	); err != nil {
		t.Fatalf("node row insert: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO follows(kind, slug) VALUES('instructor', 'mike-johnston')",
	); err != nil {
		t.Fatalf("instructor row insert (should not collide with node): %v", err)
	}
}
