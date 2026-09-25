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
		"idx_lessons_follow_id",
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
	if count != 5 {
		t.Errorf("schema_migrations has %d rows, want 5", count)
	}

	// Versions are recorded in ascending filename order — the application order
	// the runner guarantees.
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("query versions: %v", err)
	}
	defer rows.Close()
	var versions []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate versions: %v", err)
	}
	want := []string{"001_initial_schema.sql", "002_lessons_follow_id.sql", "003_lessons_position.sql", "004_lessons_library_entries.sql", "005_lessons_canceled_note.sql"}
	if len(versions) != len(want) {
		t.Fatalf("recorded versions = %v, want %v", versions, want)
	}
	for i := range want {
		if versions[i] != want[i] {
			t.Errorf("recorded version[%d] = %q, want %q", i, versions[i], want[i])
		}
	}
}

// TestMigration002LinksLessonsToFollows verifies the follow_id column + its
// foreign key behave: a lesson can record a follow_id, and deleting that follow
// sets the lesson's follow_id back to NULL (ON DELETE SET NULL) rather than
// cascading the lesson away.
func TestMigration002LinksLessonsToFollows(t *testing.T) {
	db := openMigrated(t)

	res, err := db.Exec(
		"INSERT INTO follows(kind, railcontent_id) VALUES('node', 4242)",
	)
	if err != nil {
		t.Fatalf("insert follow: %v", err)
	}
	followID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("follow last id: %v", err)
	}

	if _, err := db.Exec(
		"INSERT INTO lessons(railcontent_id, follow_id) VALUES(1, ?)", followID,
	); err != nil {
		t.Fatalf("insert lesson with follow_id: %v", err)
	}

	// Deleting the follow detaches the lesson (SET NULL), keeping the lesson row.
	if _, err := db.Exec("DELETE FROM follows WHERE id = ?", followID); err != nil {
		t.Fatalf("delete follow: %v", err)
	}

	var (
		exists   int
		followFK sql.NullInt64
	)
	if err := db.QueryRow(
		"SELECT count(*), max(follow_id) FROM lessons WHERE railcontent_id = 1",
	).Scan(&exists, &followFK); err != nil {
		t.Fatalf("read lesson after follow delete: %v", err)
	}
	if exists != 1 {
		t.Fatalf("lesson row count = %d after follow delete, want 1 (no cascade)", exists)
	}
	if followFK.Valid {
		t.Errorf("lesson follow_id = %+v after follow delete, want NULL (ON DELETE SET NULL)", followFK)
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

// TestMigrationGivesOldCanceledNotesASentence proves migration 005 rewrites
// the bare 'canceled' an older version left on a skipped lesson to the
// sentence a stopped download leaves now (stoppedNote), and touches nothing
// else: another skip reason, or the word on a lesson that is not skipped.
func TestMigrationGivesOldCanceledNotesASentence(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := ensureMigrationsTable(db); err != nil {
		t.Fatal(err)
	}
	old := []string{"001_initial_schema.sql", "002_lessons_follow_id.sql", "003_lessons_position.sql", "004_lessons_library_entries.sql"}
	if err := applyPendingMigrations(db, old); err != nil {
		t.Fatalf("apply the migrations before 005: %v", err)
	}
	rows := []struct {
		id            int
		status, error string
		want          string
	}{
		{1, StatusSkipped, "canceled", stoppedNote},
		{2, StatusSkipped, "not wanted", "not wanted"},
		{3, StatusFailed, "canceled", "canceled"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO lessons(railcontent_id, status, error) VALUES(?, ?, ?)`, r.id, r.status, r.error); err != nil {
			t.Fatalf("seed lesson %d: %v", r.id, err)
		}
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	for _, r := range rows {
		var got string
		if err := db.QueryRow(`SELECT error FROM lessons WHERE railcontent_id = ?`, r.id).Scan(&got); err != nil {
			t.Fatalf("read lesson %d: %v", r.id, err)
		}
		if got != r.want {
			t.Errorf("lesson %d (%s, %q): error = %q, want %q", r.id, r.status, r.error, got, r.want)
		}
	}
}
