package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Lesson status values. These mirror the lessons.status CHECK in
// 001_initial_schema.sql; keep the two in sync. New rows default to
// StatusPending.
const (
	StatusPending     = "pending"
	StatusDownloading = "downloading"
	StatusDownloaded  = "downloaded"
	StatusFailed      = "failed"
	StatusSkipped     = "skipped"
)

// Lesson is one row of the lessons table: a single Musora lesson keyed by its
// railcontent_id (the natural dedup key). The nullable columns map to sql.Null*
// so an undownloaded lesson's empty paths/bytes/error and a node-less instructor
// lesson's empty parent round-trip as SQL NULL rather than zero values.
type Lesson struct {
	RailcontentID       int            `json:"railcontent_id"`
	Title               string         `json:"title"`
	ParentRailcontentID sql.NullInt64  `json:"parent_railcontent_id"`
	Brand               string         `json:"brand"`
	Position            sql.NullInt64  `json:"position"`
	Status              string         `json:"status"`
	Quality             sql.NullString `json:"quality"`
	OutputDir           sql.NullString `json:"output_dir"`
	VideoPath           sql.NullString `json:"video_path"`
	Bytes               sql.NullInt64  `json:"bytes"`
	Error               sql.NullString `json:"error"`
	FollowID            sql.NullInt64  `json:"follow_id"`
	FirstSeenAt         sql.NullTime   `json:"first_seen_at"`
	DownloadedAt        sql.NullTime   `json:"downloaded_at"`
	UpdatedAt           sql.NullTime   `json:"updated_at"`
	// LibraryEntries is the raw library_entries column: a JSON array of the
	// entries the plex-tv move placed in a season folder for this lesson, each
	// relative to the library folder, or NULL when there is no record. Read it
	// through PlacedEntries.
	LibraryEntries sql.NullString `json:"library_entries"`
	// Deleting is set while a delete is removing the lesson's files (see
	// BeginLessonDelete): no job may be enqueued or retried for it meanwhile.
	Deleting bool `json:"deleting"`
}

// lessonColumns is the canonical column list for SELECTs, kept in one place so
// every scan path agrees with scanLesson's field order.
const lessonColumns = `railcontent_id, title, parent_railcontent_id, brand, position, status,
	quality, output_dir, video_path, bytes, error, follow_id,
	first_seen_at, downloaded_at, updated_at, library_entries, deleting`

// scanLesson reads one lessons row in lessonColumns order from any *sql.Row or
// *sql.Rows (both satisfy this Scan signature).
func scanLesson(row interface {
	Scan(dest ...any) error
}) (Lesson, error) {
	var l Lesson
	err := row.Scan(
		&l.RailcontentID, &l.Title, &l.ParentRailcontentID, &l.Brand, &l.Position, &l.Status,
		&l.Quality, &l.OutputDir, &l.VideoPath, &l.Bytes, &l.Error, &l.FollowID,
		&l.FirstSeenAt, &l.DownloadedAt, &l.UpdatedAt, &l.LibraryEntries, &l.Deleting,
	)
	return l, err
}

// getLessonTx reads lesson id inside tx (a wrapped sql.ErrNoRows if unknown).
func getLessonTx(ctx context.Context, tx *sql.Tx, id int) (Lesson, error) {
	l, err := scanLesson(tx.QueryRowContext(ctx,
		`SELECT `+lessonColumns+` FROM lessons WHERE railcontent_id = ?`, id,
	))
	if err != nil {
		return Lesson{}, fmt.Errorf("get lesson %d: %w", id, err)
	}
	return l, nil
}

// lessonDeletingTx returns ErrLessonDeleting while lesson id's files are being
// deleted (an unknown id is not being deleted).
func lessonDeletingTx(ctx context.Context, tx *sql.Tx, id int) error {
	var deleting int
	err := tx.QueryRowContext(ctx,
		`SELECT deleting FROM lessons WHERE railcontent_id = ?`, id,
	).Scan(&deleting)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("check lesson %d: %w", id, err)
	case deleting == 1:
		return fmt.Errorf("lesson %d: %w", id, ErrLessonDeleting)
	}
	return nil
}

// UpsertLesson records (or refreshes) a lesson's descriptive fields keyed on its
// railcontent_id. On conflict it updates only title, parent, and updated_at — it
// deliberately does NOT touch status, download metadata, or follow_id. This is
// half of the dedup mechanism: a re-sync that re-discovers an already-downloaded
// lesson must never downgrade it back to pending and trigger a redundant
// re-download. Leaving follow_id untouched is first-follow-wins: the lesson stays
// attributed to the follow that first discovered it even if a later follow also
// covers it. New rows take the table default status='pending'.
//
// position is the lesson's sequence within its follow (the "NN - " folder
// prefix). On conflict it is first-write-wins via COALESCE(lessons.position,
// excluded.position): a lesson shared by two follows keeps the first number, and
// a prior NULL is filled in by a later numbered upsert. (title stays
// last-write-wins.)
func (s *Store) UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string, position sql.NullInt64, followID sql.NullInt64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO lessons(railcontent_id, title, parent_railcontent_id, brand, position, follow_id)
			 VALUES(?, ?, ?, ?, ?, ?)
			 ON CONFLICT(railcontent_id) DO UPDATE SET
			     title                 = excluded.title,
			     parent_railcontent_id = excluded.parent_railcontent_id,
			     position              = COALESCE(lessons.position, excluded.position),
			     updated_at            = CURRENT_TIMESTAMP`,
			railcontentID, title, parent, brand, position, followID,
		)
		if err != nil {
			return fmt.Errorf("upsert lesson %d: %w", railcontentID, err)
		}
		return nil
	})
}

// GetLesson returns the lesson with the given railcontent_id, or sql.ErrNoRows
// (wrapped) if none exists.
func (s *Store) GetLesson(ctx context.Context, id int) (Lesson, error) {
	l, err := scanLesson(s.db.QueryRowContext(ctx,
		`SELECT `+lessonColumns+` FROM lessons WHERE railcontent_id = ?`, id,
	))
	if err != nil {
		return Lesson{}, fmt.Errorf("get lesson %d: %w", id, err)
	}
	return l, nil
}

// IsDownloaded reports whether the lesson with the given railcontent_id has
// status='downloaded'. An unknown id is not an error: it simply reports false,
// so sync can treat "never seen" and "seen but not yet downloaded" alike.
func (s *Store) IsDownloaded(ctx context.Context, id int) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM lessons WHERE railcontent_id = ? AND status = ?`,
		id, StatusDownloaded,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check downloaded for lesson %d: %w", id, err)
	}
	return n > 0, nil
}

// ShouldSkipEnqueue reports whether the planner must NOT enqueue a download job
// for the lesson with the given railcontent_id: true when its status is
// 'downloaded' (already have it) OR 'skipped' (intentionally passed over, e.g.
// locked/missing content — re-enqueuing would loop forever), or while a delete
// is removing its files. A 'failed' lesson is deliberately NOT skipped so it is
// retried. An unknown id is not an error: it reports false, so a never-seen
// lesson enqueues normally.
func (s *Store) ShouldSkipEnqueue(ctx context.Context, id int) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM lessons WHERE railcontent_id = ? AND (status IN (?, ?) OR deleting = 1)`,
		id, StatusDownloaded, StatusSkipped,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check should-skip-enqueue for lesson %d: %w", id, err)
	}
	return n > 0, nil
}

// MarkSkipped records that a lesson was intentionally skipped (e.g. locked or
// missing content): status='skipped' with the reason recorded in error. It
// returns an error if no lesson row matched.
func (s *Store) MarkSkipped(ctx context.Context, id int, reason string) error {
	return s.updateStatus(ctx,
		`UPDATE lessons
		    SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP
		  WHERE railcontent_id = ?`,
		StatusSkipped, reason, id,
	)
}

// UnskipLesson is the inverse of MarkSkipped: a guarded UPDATE that resets a
// skipped lesson back to pending and clears its error, ONLY while it is still
// skipped. It tolerates zero rows as a benign no-op and returns nil — an already-pending/terminal lesson (or an unknown id) is left
// untouched rather than erroring. It executes directly rather than through
// updateStatus (which treats 0 rows as "no such lesson").
func (s *Store) UnskipLesson(ctx context.Context, id int) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE lessons
			    SET status = ?, error = NULL, updated_at = CURRENT_TIMESTAMP
			  WHERE railcontent_id = ? AND status = ?`,
			StatusPending, id, StatusSkipped,
		)
		if err != nil {
			return fmt.Errorf("unskip lesson %d: %w", id, err)
		}
		// Zero rows affected (not skipped, or unknown id) is intentional: only a
		// skipped lesson is reset here, and any other state is a no-op.
		return nil
	})
}

// updateStatus runs a status-mutating UPDATE through withTx and fails if it
// touched zero rows (the lesson id was unknown). All Mark* helpers funnel
// through here so the "no such lesson" behavior is defined in exactly one place.
func (s *Store) updateStatus(ctx context.Context, query string, args ...any) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("update lesson status: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected updating lesson status: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("no lesson matched the status update")
		}
		return nil
	})
}

// ListByStatus returns every lesson with the given status, ordered by
// railcontent_id for a deterministic result.
func (s *Store) ListByStatus(ctx context.Context, status string) ([]Lesson, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+lessonColumns+` FROM lessons WHERE status = ? ORDER BY railcontent_id`,
		status,
	)
	if err != nil {
		return nil, fmt.Errorf("list lessons by status %q: %w", status, err)
	}
	return scanLessons(rows)
}

// ListLessonsByFollow returns every lesson attributed to the given follow id,
// ordered by railcontent_id for a deterministic result. A follow with no
// lessons yields an empty slice and no error.
func (s *Store) ListLessonsByFollow(ctx context.Context, followID int64) ([]Lesson, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+lessonColumns+` FROM lessons WHERE follow_id = ? ORDER BY railcontent_id`,
		followID,
	)
	if err != nil {
		return nil, fmt.Errorf("list lessons by follow %d: %w", followID, err)
	}
	return scanLessons(rows)
}

// defaultLessonListLimit caps a paged ListLessons call when the caller passes a
// non-positive limit, so an unbounded query can never be issued by accident.
const defaultLessonListLimit = 100

// ListLessons returns a page of lessons ordered by updated_at DESC then
// railcontent_id (most recently touched first, stable within the same
// timestamp). A limit <= 0 falls back to defaultLessonListLimit; offset pages
// through the result.
func (s *Store) ListLessons(ctx context.Context, limit, offset int) ([]Lesson, error) {
	if limit <= 0 {
		limit = defaultLessonListLimit
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+lessonColumns+` FROM lessons
		  ORDER BY updated_at DESC, railcontent_id
		  LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list lessons (limit %d offset %d): %w", limit, offset, err)
	}
	return scanLessons(rows)
}

// CountLessonsByStatus returns the number of lessons in each status, keyed by
// status. Only statuses with at least one lesson appear in the map; a status
// with no rows is absent rather than present with a zero count, so the caller
// fills in the missing entries for the known enum set. An empty lessons table
// yields an empty (non-nil) map.
func (s *Store) CountLessonsByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, count(*) FROM lessons GROUP BY status`,
	)
	if err != nil {
		return nil, fmt.Errorf("count lessons by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan lesson status count: %w", err)
		}
		counts[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lesson status counts: %w", err)
	}
	return counts, nil
}

// scanLessons drains a lessons *sql.Rows into a slice and closes it, so the
// listing methods share one scan/iterate/close path.
func scanLessons(rows *sql.Rows) ([]Lesson, error) {
	defer rows.Close()

	var lessons []Lesson
	for rows.Next() {
		l, err := scanLesson(rows)
		if err != nil {
			return nil, fmt.Errorf("scan lesson: %w", err)
		}
		lessons = append(lessons, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lessons: %w", err)
	}
	return lessons, nil
}
