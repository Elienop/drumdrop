package database

import (
	"context"
	"database/sql"
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
}

// lessonColumns is the canonical column list for SELECTs, kept in one place so
// every scan path agrees with scanLesson's field order.
const lessonColumns = `railcontent_id, title, parent_railcontent_id, brand, status,
	quality, output_dir, video_path, bytes, error, follow_id,
	first_seen_at, downloaded_at, updated_at`

// scanLesson reads one lessons row in lessonColumns order from any *sql.Row or
// *sql.Rows (both satisfy this Scan signature).
func scanLesson(row interface {
	Scan(dest ...any) error
}) (Lesson, error) {
	var l Lesson
	err := row.Scan(
		&l.RailcontentID, &l.Title, &l.ParentRailcontentID, &l.Brand, &l.Status,
		&l.Quality, &l.OutputDir, &l.VideoPath, &l.Bytes, &l.Error, &l.FollowID,
		&l.FirstSeenAt, &l.DownloadedAt, &l.UpdatedAt,
	)
	return l, err
}

// UpsertLesson records (or refreshes) a lesson's descriptive fields keyed on its
// railcontent_id. On conflict it updates only title, parent, and updated_at — it
// deliberately does NOT touch status, download metadata, or follow_id. This is
// half of the dedup mechanism: a re-sync that re-discovers an already-downloaded
// lesson must never downgrade it back to pending and trigger a redundant
// re-download. Leaving follow_id untouched is first-follow-wins: the lesson stays
// attributed to the follow that first discovered it even if a later follow also
// covers it. New rows take the table default status='pending'.
func (s *Store) UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string, followID sql.NullInt64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO lessons(railcontent_id, title, parent_railcontent_id, brand, follow_id)
			 VALUES(?, ?, ?, ?, ?)
			 ON CONFLICT(railcontent_id) DO UPDATE SET
			     title                 = excluded.title,
			     parent_railcontent_id = excluded.parent_railcontent_id,
			     updated_at            = CURRENT_TIMESTAMP`,
			railcontentID, title, parent, brand, followID,
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

// MarkDownloading transitions a lesson to status='downloading'. It returns an
// error if no lesson row matched so the caller learns the id was unknown.
func (s *Store) MarkDownloading(ctx context.Context, id int) error {
	return s.updateStatus(ctx,
		`UPDATE lessons
		    SET status = ?, updated_at = CURRENT_TIMESTAMP
		  WHERE railcontent_id = ?`,
		StatusDownloading, id,
	)
}

// MarkDownloaded records a successful download: it sets status='downloaded',
// stores the quality/paths/byte count, stamps downloaded_at, and clears any
// prior error. It returns an error if no lesson row matched.
func (s *Store) MarkDownloaded(ctx context.Context, id int, quality, outputDir, videoPath string, bytes int64) error {
	return s.updateStatus(ctx,
		`UPDATE lessons
		    SET status = ?,
		        quality = ?,
		        output_dir = ?,
		        video_path = ?,
		        bytes = ?,
		        error = NULL,
		        downloaded_at = CURRENT_TIMESTAMP,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE railcontent_id = ?`,
		StatusDownloaded, quality, outputDir, videoPath, bytes, id,
	)
}

// MarkFailed records a failed download attempt: status='failed' with the error
// message. It returns an error if no lesson row matched.
func (s *Store) MarkFailed(ctx context.Context, id int, errMsg string) error {
	return s.updateStatus(ctx,
		`UPDATE lessons
		    SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP
		  WHERE railcontent_id = ?`,
		StatusFailed, errMsg, id,
	)
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
