package database

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies every embedded migration that has not yet been applied,
// in ascending filename order, each in its own transaction. It is idempotent:
// when every migration is already recorded in schema_migrations it is a no-op.
//
// Migration files live under migrations/*.sql and are applied once-only; the
// schema_migrations table records each applied version by its filename.
func RunMigrations(db *sql.DB) error {
	if err := ensureMigrationsTable(db); err != nil {
		return err
	}

	pending, err := listPendingMigrations(db)
	if err != nil {
		return err
	}

	return applyPendingMigrations(db, pending)
}

// ensureMigrationsTable creates the schema_migrations bookkeeping table if it
// does not already exist. This is the one place IF NOT EXISTS is appropriate:
// the table predates (and tracks) every versioned migration.
func ensureMigrationsTable(db *sql.DB) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	return nil
}

// listPendingMigrations returns the embedded migration filenames that are not
// yet recorded in schema_migrations, sorted ascending so they apply in order.
func listPendingMigrations(db *sql.DB) ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	applied, err := appliedMigrations(db)
	if err != nil {
		return nil, err
	}

	var pending []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if _, ok := applied[name]; ok {
			continue
		}
		pending = append(pending, name)
	}
	sort.Strings(pending)
	return pending, nil
}

// appliedMigrations returns the set of migration versions already recorded.
func appliedMigrations(db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]struct{})
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

// applyPendingMigrations runs each pending migration in its own transaction,
// then records its version. A failure rolls that migration's transaction back
// and aborts; migrations applied before it stay committed.
func applyPendingMigrations(db *sql.DB, pending []string) error {
	for _, name := range pending {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %q: %w", name, err)
		}

		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %q: %w", name, err)
		}

		if _, err := tx.Exec(
			"INSERT INTO schema_migrations(version) VALUES(?)", name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %q: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %q: %w", name, err)
		}
	}
	return nil
}
