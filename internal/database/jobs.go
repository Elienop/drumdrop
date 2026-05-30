package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrJobNotActive is returned by CancelJob when the target job exists but is no
// longer cancelable (it has reached a terminal status — done, failed, or
// canceled). The API layer maps it to 409 Conflict, distinct from the
// sql.ErrNoRows 404 returned for an unknown id.
var ErrJobNotActive = errors.New("job not active")

// ErrJobNotTerminal is returned by RetryJob when the target job exists but is
// not in a retryable terminal status (only failed or canceled jobs can be
// retried). The API layer maps it to 409 Conflict, distinct from the
// sql.ErrNoRows 404 returned for an unknown id.
var ErrJobNotTerminal = errors.New("job not terminal")

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
// Select-then-update inside one tx makes the claim atomic, and the
// running-transition UPDATE is itself guarded by `status = JobQueued`: if a
// concurrent worker has already moved the row out of 'queued' since this tx
// selected it, the UPDATE touches zero rows. That lost race is treated as a
// no-claim (ok=false, nil) rather than a half-claimed job, so adding a worker
// pool later cannot let two workers both believe they claimed the same job.
// With SetMaxOpenConns(1) only one worker runs at a time today, so the guard is
// dormant; it exists so the same query stays correct under future concurrency.
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

		n, err := markRunningTx(ctx, tx, j.ID, JobQueued)
		if err != nil {
			return fmt.Errorf("claim job %d: %w", j.ID, err)
		}
		if n != 1 {
			// Lost race: another worker moved this row out of 'queued'
			// between our SELECT and UPDATE. Leave ok=false so the caller
			// treats it as "nothing claimed" rather than half-claiming a row
			// that is now owned by someone else.
			return nil
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

// markRunningTx applies the running-transition UPDATE for job id inside tx:
// status -> 'running', started_at -> CURRENT_TIMESTAMP, attempts incremented. It
// returns the number of rows affected (1 on success, 0 when no row matched) so
// callers can detect a lost claim race or an unknown id. This is the single
// place the running transition lives; ClaimNextJob and MarkJobRunning both go
// through it.
//
// guardStatus narrows the WHERE clause: a non-empty value requires the row to
// still be in that status for the UPDATE to fire (ClaimNextJob passes JobQueued
// so a row another worker already moved out of 'queued' is left untouched). An
// empty guardStatus matches the row by id alone, which is what MarkJobRunning
// needs to re-stamp an already-running job on a retry.
func markRunningTx(ctx context.Context, tx *sql.Tx, id int64, guardStatus string) (int64, error) {
	const setClause = `UPDATE jobs
		    SET status = ?,
		        started_at = CURRENT_TIMESTAMP,
		        attempts = attempts + 1
		  WHERE id = ?`

	var (
		res sql.Result
		err error
	)
	if guardStatus == "" {
		res, err = tx.ExecContext(ctx, setClause, JobRunning, id)
	} else {
		res, err = tx.ExecContext(ctx, setClause+` AND status = ?`, JobRunning, id, guardStatus)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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

// ListJobsByStatus returns every job in the given status, oldest first (by id),
// so the API can render a status-filtered queue in enqueue order. A status with
// no matching rows yields an empty slice and no error.
func (s *Store) ListJobsByStatus(ctx context.Context, status string) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE status = ? ORDER BY id`,
		status,
	)
	if err != nil {
		return nil, fmt.Errorf("list jobs by status %q: %w", status, err)
	}
	return scanJobs(rows)
}

// defaultJobListLimit caps a ListJobs call when the caller passes a non-positive
// limit, so an unbounded query can never be issued by accident.
const defaultJobListLimit = 100

// ListJobs returns a page of jobs across all statuses, most recent first (by id
// DESC). A limit <= 0 falls back to defaultJobListLimit.
func (s *Store) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = defaultJobListLimit
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM jobs ORDER BY id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list jobs (limit %d): %w", limit, err)
	}
	return scanJobs(rows)
}

// CountJobsByState returns the number of jobs in each status, keyed by status.
// Only statuses with at least one job appear in the map; a status with no rows
// is absent rather than present with a zero count, so the caller fills in the
// missing entries for the known enum set. An empty jobs table yields an empty
// (non-nil) map.
func (s *Store) CountJobsByState(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, count(*) FROM jobs GROUP BY status`,
	)
	if err != nil {
		return nil, fmt.Errorf("count jobs by state: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan job state count: %w", err)
		}
		counts[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job state counts: %w", err)
	}
	return counts, nil
}

// scanJobs drains a jobs *sql.Rows into a slice and closes it, so the listing
// methods share one scan/iterate/close path.
func scanJobs(rows *sql.Rows) ([]Job, error) {
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

// MarkJobRunning re-stamps an already-claimed job for a retry: status='running',
// started_at=CURRENT_TIMESTAMP, attempts incremented. The FIRST attempts
// increment is performed by ClaimNextJob when the job is taken off the queue
// (attempts 0 -> 1); MarkJobRunning is only called for subsequent retries, so in
// the production flow it runs against an already-running job and pushes attempts
// to 2 or higher. It returns an error if no job row matched.
//
// It shares markRunningTx with ClaimNextJob but passes an empty guard so the
// re-stamp matches by id alone, independent of the job's current status. The
// "Job" suffix disambiguates from the lessons store's status mutators
// (MarkDownloading/MarkFailed/…), which share the same *Store receiver.
func (s *Store) MarkJobRunning(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		n, err := markRunningTx(ctx, tx, id, "")
		if err != nil {
			return fmt.Errorf("mark job %d running: %w", id, err)
		}
		if n == 0 {
			return fmt.Errorf("no job matched the status update")
		}
		return nil
	})
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

// CancelJob moves a queued or running job to status='canceled' and stamps
// finished_at. It distinguishes the two failure modes the API needs to surface
// differently: an unknown id yields a wrapped sql.ErrNoRows (404), while a job
// already in a terminal status yields ErrJobNotActive (409). The cancel UPDATE
// is guarded by `status IN (queued,running)` so it cannot resurrect or restamp
// a finished job; a prior GetJob inside the same tx tells us which sentinel to
// return when that guard touches zero rows.
func (s *Store) CancelJob(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		j, err := scanJob(tx.QueryRowContext(ctx,
			`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id,
		))
		if err != nil {
			return fmt.Errorf("cancel job %d: %w", id, err)
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE jobs
			    SET status = ?, finished_at = CURRENT_TIMESTAMP
			  WHERE id = ? AND status IN (?, ?)`,
			JobCanceled, id, JobQueued, JobRunning,
		)
		if err != nil {
			return fmt.Errorf("cancel job %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected canceling job %d: %w", id, err)
		}
		if n == 0 {
			// The row exists (the SELECT found it) but the guard excluded it,
			// so it is in a terminal status: report 409, not 404.
			return fmt.Errorf("cancel job %d (status %q): %w", id, j.Status, ErrJobNotActive)
		}
		return nil
	})
}

// RetryJob requeues a failed or canceled job so the worker downloads it again.
// In one transaction it resets the job (status='queued', attempts=0,
// started_at/finished_at/error cleared) AND resets its lesson back to
// status='pending' with the lesson error cleared, so the planner/worker treat
// it as fresh work. Resetting the existing job row IS the requeue — no new job
// is inserted, so this never conflicts with ActiveJobExists.
//
// As with CancelJob, an unknown id yields wrapped sql.ErrNoRows (404) and a job
// that is not in a retryable terminal status (failed/canceled) yields
// ErrJobNotTerminal (409); the guarded UPDATE plus the prior SELECT pick the
// right sentinel when zero rows are touched.
func (s *Store) RetryJob(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		j, err := scanJob(tx.QueryRowContext(ctx,
			`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id,
		))
		if err != nil {
			return fmt.Errorf("retry job %d: %w", id, err)
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE jobs
			    SET status = ?, attempts = 0,
			        started_at = NULL, finished_at = NULL, error = NULL
			  WHERE id = ? AND status IN (?, ?)`,
			JobQueued, id, JobFailed, JobCanceled,
		)
		if err != nil {
			return fmt.Errorf("retry job %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected retrying job %d: %w", id, err)
		}
		if n == 0 {
			// Row exists but the guard excluded it: it is queued or running.
			return fmt.Errorf("retry job %d (status %q): %w", id, j.Status, ErrJobNotTerminal)
		}

		// Reset the lesson so the worker re-downloads it. The lesson is
		// expected to exist (the job was enqueued for it); if it has been
		// removed, leave the requeued job to fail/skip on its own rather than
		// aborting the retry.
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons
			    SET status = ?, error = NULL, updated_at = CURRENT_TIMESTAMP
			  WHERE railcontent_id = ?`,
			StatusPending, j.RailcontentID,
		); err != nil {
			return fmt.Errorf("reset lesson %d for retry: %w", j.RailcontentID, err)
		}
		return nil
	})
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
