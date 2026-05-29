package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Job status values. These mirror the jobs.status CHECK in
// 001_initial_schema.sql; keep the two in sync. New rows default to JobQueued.
const (
	JobQueued   = "queued"
	JobRunning  = "running"
	JobDone     = "done"
	JobFailed   = "failed"
	JobCanceled = "canceled"
)

// Job is one row of the jobs table: a unit of download work the scheduler
// consumes. follow_id is nullable and declared ON DELETE SET NULL, so a job
// outlives the follow that spawned it (its FollowID simply becomes NULL). The
// nullable columns map to sql.Null* so an unstarted job's empty timestamps and
// a successful job's empty error round-trip as SQL NULL rather than zero values.
type Job struct {
	ID            int64          `json:"id"`
	FollowID      sql.NullInt64  `json:"follow_id"`
	RailcontentID int            `json:"railcontent_id"`
	Status        string         `json:"status"`
	Attempts      int            `json:"attempts"`
	Error         sql.NullString `json:"error"`
	CreatedAt     sql.NullTime   `json:"created_at"`
	StartedAt     sql.NullTime   `json:"started_at"`
	FinishedAt    sql.NullTime   `json:"finished_at"`
}

// jobColumns is the canonical column list for SELECTs, kept in one place so
// every scan path agrees with scanJob's field order.
const jobColumns = `id, follow_id, railcontent_id, status, attempts, error,
	created_at, started_at, finished_at`

// scanJob reads one jobs row in jobColumns order from any *sql.Row or *sql.Rows
// (both satisfy this Scan signature).
func scanJob(row interface {
	Scan(dest ...any) error
}) (Job, error) {
	var j Job
	err := row.Scan(
		&j.ID, &j.FollowID, &j.RailcontentID, &j.Status, &j.Attempts, &j.Error,
		&j.CreatedAt, &j.StartedAt, &j.FinishedAt,
	)
	return j, err
}

// EnqueueJob inserts a new download job for the given lesson, in the default
// status='queued', and returns its autoincrement id. followID is the follow
// that spawned the job; pass an invalid sql.NullInt64 to leave it NULL.
func (s *Store) EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO jobs(follow_id, railcontent_id) VALUES(?, ?)`,
			followID, railcontentID,
		)
		if err != nil {
			return fmt.Errorf("enqueue job for lesson %d: %w", railcontentID, err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("last insert id for job: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ClaimNextJob atomically takes the oldest queued job and marks it running. In
// a single transaction it selects the lowest-id job in status='queued', stamps
// started_at, increments attempts (so a fresh claim records attempt #1), and
// re-reads the row to return its post-update state. The bool is false when the
// queue holds no queued job (sql.ErrNoRows), in which case the returned Job is
// the zero value and err is nil.
//
// Select-then-update inside one tx makes the claim atomic: with
// SetMaxOpenConns(1) only one worker runs at a time today, and the same query
// stays correct if a worker pool is added later.
func (s *Store) ClaimNextJob(ctx context.Context) (Job, bool, error) {
	var claimed Job
	var ok bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		j, err := scanJob(tx.QueryRowContext(ctx,
			`SELECT `+jobColumns+` FROM jobs WHERE status = ? ORDER BY id LIMIT 1`,
			JobQueued,
		))
		if err != nil {
			if err == sql.ErrNoRows {
				return nil // queue empty: ok stays false
			}
			return fmt.Errorf("select next queued job: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs
			    SET status = ?,
			        started_at = CURRENT_TIMESTAMP,
			        attempts = attempts + 1
			  WHERE id = ?`,
			JobRunning, j.ID,
		); err != nil {
			return fmt.Errorf("claim job %d: %w", j.ID, err)
		}

		// Re-read inside the same tx so the returned row reflects the new
		// status, attempts, and started_at exactly as persisted.
		claimed, err = scanJob(tx.QueryRowContext(ctx,
			`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, j.ID,
		))
		if err != nil {
			return fmt.Errorf("re-read claimed job %d: %w", j.ID, err)
		}
		ok = true
		return nil
	})
	if err != nil {
		return Job{}, false, err
	}
	return claimed, ok, nil
}

// RequeueStaleRunning moves every job stuck in status='running' back to
// 'queued' and clears its started_at, returning how many rows it touched. The
// daemon calls this once at startup to recover jobs orphaned mid-download by a
// crash or kill, so they are retried rather than lost.
func (s *Store) RequeueStaleRunning(ctx context.Context) (int, error) {
	var n int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, started_at = NULL WHERE status = ?`,
			JobQueued, JobRunning,
		)
		if err != nil {
			return fmt.Errorf("requeue stale running jobs: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected requeuing stale jobs: %w", err)
		}
		n = int(affected)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ActiveJobExists reports whether any job for the given lesson is still queued
// or running. The planner uses it to avoid enqueuing a duplicate job for a
// lesson that already has work outstanding.
func (s *Store) ActiveJobExists(ctx context.Context, railcontentID int) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM jobs WHERE railcontent_id = ? AND status IN (?, ?)`,
		railcontentID, JobQueued, JobRunning,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count active jobs for lesson %d: %w", railcontentID, err)
	}
	return count > 0, nil
}

// ListQueued returns every job still in status='queued', oldest first (by id),
// so the scheduler processes them in enqueue order.
func (s *Store) ListQueued(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE status = ? ORDER BY id`,
		JobQueued,
	)
	if err != nil {
		return nil, fmt.Errorf("list queued jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate jobs: %w", err)
	}
	return jobs, nil
}

// GetJob returns the job with the given id, or sql.ErrNoRows (wrapped) if none
// exists. Used by tests and callers that need to inspect a job's full state.
func (s *Store) GetJob(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id,
	))
	if err != nil {
		return Job{}, fmt.Errorf("get job %d: %w", id, err)
	}
	return j, nil
}

// MarkJobRunning transitions a job to status='running', stamps started_at with
// CURRENT_TIMESTAMP, and increments attempts. Incrementing here (rather than at
// enqueue) means attempts counts actual run starts, so a retried job records
// each attempt. It returns an error if no job row matched.
//
// The "Job" suffix disambiguates from the lessons store's status mutators
// (MarkDownloading/MarkFailed/…), which share the same *Store receiver.
func (s *Store) MarkJobRunning(ctx context.Context, id int64) error {
	return s.updateJob(ctx,
		`UPDATE jobs
		    SET status = ?,
		        started_at = CURRENT_TIMESTAMP,
		        attempts = attempts + 1
		  WHERE id = ?`,
		JobRunning, id,
	)
}

// MarkJobDone transitions a job to status='done' and stamps finished_at. It
// returns an error if no job row matched.
func (s *Store) MarkJobDone(ctx context.Context, id int64) error {
	return s.updateJob(ctx,
		`UPDATE jobs
		    SET status = ?, finished_at = CURRENT_TIMESTAMP
		  WHERE id = ?`,
		JobDone, id,
	)
}

// MarkJobFailed transitions a job to status='failed', records the error
// message, and stamps finished_at. It returns an error if no job row matched.
func (s *Store) MarkJobFailed(ctx context.Context, id int64, errMsg string) error {
	return s.updateJob(ctx,
		`UPDATE jobs
		    SET status = ?, error = ?, finished_at = CURRENT_TIMESTAMP
		  WHERE id = ?`,
		JobFailed, errMsg, id,
	)
}

// updateJob runs a status-mutating UPDATE through withTx and fails if it
// touched zero rows (the job id was unknown). All Mark* helpers funnel through
// here so the "no such job" behavior is defined in exactly one place.
func (s *Store) updateJob(ctx context.Context, query string, args ...any) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("update job status: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected updating job status: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("no job matched the status update")
		}
		return nil
	})
}
