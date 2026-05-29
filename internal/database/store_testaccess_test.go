package database

import "database/sql"

// rawDB returns the Store's underlying *sql.DB for in-package tests only. It is
// declared in a _test.go file so it never ships in the package's public API:
// external callers cannot use it to bypass withTx, the single mutation
// chokepoint. Tests use it for direct read-only assertions and for deliberately
// raw inserts that exercise schema constraints (e.g. the jobs.status CHECK).
func (s *Store) rawDB() *sql.DB {
	return s.db
}
