package database

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// seedFollow inserts a node follow directly and returns its id, so jobs tests
// can attach a real follow_id and then exercise the ON DELETE SET NULL path.
func seedFollow(t *testing.T, s *Store, railcontentID int) int64 {
	t.Helper()
	f, err := s.AddNodeFollow(context.Background(), railcontentID, "Course", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	return f.ID
}

// countJobs returns the number of rows in jobs. Used to assert that deleting a
// follow leaves its jobs in place (only nulling their follow_id).
func countJobs(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.rawDB().QueryRow("SELECT count(*) FROM jobs").Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

func TestEnqueueJobThenListQueued(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	followID := seedFollow(t, s, 100)

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: followID, Valid: true}, 409875)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if id == 0 {
		t.Fatal("EnqueueJob returned id 0, want the new autoincrement id")
	}

	queued, err := s.ListQueued(ctx)
	if err != nil {
		t.Fatalf("ListQueued: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("ListQueued returned %d jobs, want 1", len(queued))
	}
	j := queued[0]
	if j.ID != id {
		t.Errorf("queued job id = %d, want %d", j.ID, id)
	}
	if j.RailcontentID != 409875 {
		t.Errorf("queued job railcontent_id = %d, want 409875", j.RailcontentID)
	}
	if j.Status != JobQueued {
		t.Errorf("new job status = %q, want %q", j.Status, JobQueued)
	}
	if j.Attempts != 0 {
		t.Errorf("new job attempts = %d, want 0", j.Attempts)
	}
	if !j.FollowID.Valid || j.FollowID.Int64 != followID {
		t.Errorf("queued job follow_id = %+v, want valid %d", j.FollowID, followID)
	}
	if !j.CreatedAt.Valid {
		t.Error("new job created_at is NULL, want a populated CURRENT_TIMESTAMP")
	}
	if j.StartedAt.Valid || j.FinishedAt.Valid {
		t.Errorf("new job has started/finished set (%+v / %+v), want both NULL", j.StartedAt, j.FinishedAt)
	}
}

// TestEnqueueJobNullFollow proves a job can be enqueued with no owning follow
// (follow_id NULL) — the same shape a job ends up in after its follow is deleted.
func TestEnqueueJobNullFollow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 555)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.FollowID.Valid {
		t.Errorf("FollowID = %+v, want NULL", got.FollowID)
	}
}

func TestMarkJobRunningIncrementsAttempts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	if err := s.MarkJobRunning(ctx, id); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	running, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if running.Status != JobRunning {
		t.Errorf("status = %q, want %q", running.Status, JobRunning)
	}
	if running.Attempts != 1 {
		t.Errorf("attempts = %d after first MarkJobRunning, want 1", running.Attempts)
	}
	if !running.StartedAt.Valid {
		t.Error("started_at is NULL after MarkJobRunning, want set")
	}

	// A retry runs MarkJobRunning again: attempts must climb, not reset.
	if err := s.MarkJobRunning(ctx, id); err != nil {
		t.Fatalf("second MarkJobRunning: %v", err)
	}
	retried, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if retried.Attempts != 2 {
		t.Errorf("attempts = %d after second MarkJobRunning, want 2", retried.Attempts)
	}
}

func TestMarkJobDone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := s.MarkJobRunning(ctx, id); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	if err := s.MarkJobDone(ctx, id); err != nil {
		t.Fatalf("MarkJobDone: %v", err)
	}

	done, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if done.Status != JobDone {
		t.Errorf("status = %q, want %q", done.Status, JobDone)
	}
	if !done.FinishedAt.Valid {
		t.Error("finished_at is NULL after MarkJobDone, want set")
	}
	if done.Error.Valid {
		t.Errorf("error = %+v after MarkJobDone, want NULL", done.Error)
	}

	// A done job is no longer queued.
	queued, err := s.ListQueued(ctx)
	if err != nil {
		t.Fatalf("ListQueued: %v", err)
	}
	if len(queued) != 0 {
		t.Errorf("ListQueued returned %d jobs after MarkJobDone, want 0", len(queued))
	}
}

func TestMarkJobFailed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := s.MarkJobRunning(ctx, id); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	if err := s.MarkJobFailed(ctx, id, "yt-dlp exited 1"); err != nil {
		t.Fatalf("MarkJobFailed: %v", err)
	}

	failed, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if failed.Status != JobFailed {
		t.Errorf("status = %q, want %q", failed.Status, JobFailed)
	}
	if !failed.Error.Valid || failed.Error.String != "yt-dlp exited 1" {
		t.Errorf("error = %+v, want %q", failed.Error, "yt-dlp exited 1")
	}
	if !failed.FinishedAt.Valid {
		t.Error("finished_at is NULL after MarkJobFailed, want set")
	}
}

// TestMarkJobMissing asserts the mark helpers report an error when no job row
// matches, rather than silently succeeding on zero rows.
func TestMarkJobMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.MarkJobRunning(ctx, 404); err == nil {
		t.Error("MarkJobRunning on a missing id returned nil error, want error")
	}
	if err := s.MarkJobDone(ctx, 404); err == nil {
		t.Error("MarkJobDone on a missing id returned nil error, want error")
	}
	if err := s.MarkJobFailed(ctx, 404, "x"); err == nil {
		t.Error("MarkJobFailed on a missing id returned nil error, want error")
	}
}

// TestJobRejectsBadStatus guards the jobs.status CHECK at the store boundary: a
// raw insert with an out-of-set status must be rejected so the constraint is
// real, not merely declared. The valid set must all insert cleanly.
func TestJobRejectsBadStatus(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.rawDB().Exec(
		"INSERT INTO jobs(railcontent_id, status) VALUES(1, 'bogus')",
	); err == nil {
		t.Error("inserting a job with status='bogus' succeeded, want CHECK violation")
	}
	for i, st := range []string{
		JobQueued, JobRunning, JobDone, JobFailed, JobCanceled,
	} {
		if _, err := s.rawDB().Exec(
			"INSERT INTO jobs(railcontent_id, status) VALUES(?, ?)", i+1, st,
		); err != nil {
			t.Errorf("inserting a job with valid status %q failed: %v", st, err)
		}
	}
}

// TestJobFollowOnDeleteSetNull is the headline FK test: deleting a follow must
// NOT cascade-delete its jobs; instead the jobs survive with follow_id set to
// NULL (the ON DELETE SET NULL action). This only happens when foreign_keys=ON,
// so a passing assertion here also proves Open's pragma is actually active.
func TestJobFollowOnDeleteSetNull(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	followID := seedFollow(t, s, 100)

	jobID, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: followID, Valid: true}, 409875)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Sanity: the job currently points at the follow.
	before, err := s.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before delete: %v", err)
	}
	if !before.FollowID.Valid || before.FollowID.Int64 != followID {
		t.Fatalf("before delete follow_id = %+v, want valid %d", before.FollowID, followID)
	}

	// Delete the follow the job belongs to.
	if err := s.RemoveFollow(ctx, followID); err != nil {
		t.Fatalf("RemoveFollow: %v", err)
	}

	// The job must still exist...
	if got := countJobs(t, s); got != 1 {
		t.Fatalf("after deleting the follow, jobs has %d rows, want 1 (jobs must survive)", got)
	}
	// ...with its follow_id nulled by ON DELETE SET NULL.
	after, err := s.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after delete: %v", err)
	}
	if after.FollowID.Valid {
		t.Errorf("after deleting the follow, job follow_id = %+v, want NULL "+
			"(proves ON DELETE SET NULL and foreign_keys=ON)", after.FollowID)
	}
	// The job's own identity and payload are untouched.
	if after.ID != jobID || after.RailcontentID != 409875 {
		t.Errorf("job mutated by follow delete: %+v", after)
	}
}

// TestClaimNextJobClaimsOldestRunningAttempts asserts ClaimNextJob takes the
// oldest queued job, flips it to running, records attempt #1 and started_at,
// then hands out the next job on a second claim and reports ok=false when the
// queue is empty.
func TestClaimNextJobClaimsOldestRunningAttempts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 11)
	if err != nil {
		t.Fatalf("EnqueueJob first: %v", err)
	}
	second, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 22)
	if err != nil {
		t.Fatalf("EnqueueJob second: %v", err)
	}

	got, ok, err := s.ClaimNextJob(ctx)
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if !ok {
		t.Fatal("ClaimNextJob ok = false with a queued job present, want true")
	}
	if got.ID != first {
		t.Errorf("claimed job id = %d, want the oldest %d", got.ID, first)
	}
	if got.RailcontentID != 11 {
		t.Errorf("claimed job railcontent_id = %d, want 11", got.RailcontentID)
	}
	if got.Status != JobRunning {
		t.Errorf("claimed job status = %q, want %q", got.Status, JobRunning)
	}
	if got.Attempts != 1 {
		t.Errorf("claimed job attempts = %d, want 1 (claim performs attempt #1)", got.Attempts)
	}
	if !got.StartedAt.Valid {
		t.Error("claimed job started_at is NULL, want a populated CURRENT_TIMESTAMP")
	}

	// The persisted row must match what was returned.
	persisted, err := s.GetJob(ctx, first)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if persisted.Status != JobRunning || persisted.Attempts != 1 || !persisted.StartedAt.Valid {
		t.Errorf("persisted claimed job = %+v, want running/attempts=1/started_at set", persisted)
	}

	// The next claim takes the second-oldest job, not the already-running one.
	next, ok, err := s.ClaimNextJob(ctx)
	if err != nil {
		t.Fatalf("second ClaimNextJob: %v", err)
	}
	if !ok {
		t.Fatal("second ClaimNextJob ok = false, want true")
	}
	if next.ID != second {
		t.Errorf("second claim id = %d, want %d", next.ID, second)
	}

	// Queue now empty: ok=false, zero-value job, nil error.
	empty, ok, err := s.ClaimNextJob(ctx)
	if err != nil {
		t.Fatalf("ClaimNextJob on empty queue: %v", err)
	}
	if ok {
		t.Errorf("ClaimNextJob ok = true on empty queue, want false (job=%+v)", empty)
	}
	if empty != (Job{}) {
		t.Errorf("ClaimNextJob on empty queue returned %+v, want zero Job", empty)
	}
}

// TestRequeueStaleRunning asserts a running (e.g. crash-orphaned) job is moved
// back to queued with its started_at cleared, and the count of moved rows is
// returned.
func TestRequeueStaleRunning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 77); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	claimed, ok, err := s.ClaimNextJob(ctx)
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if !ok {
		t.Fatal("ClaimNextJob ok = false, want true")
	}

	n, err := s.RequeueStaleRunning(ctx)
	if err != nil {
		t.Fatalf("RequeueStaleRunning: %v", err)
	}
	if n != 1 {
		t.Errorf("RequeueStaleRunning returned %d, want 1", n)
	}

	requeued, err := s.GetJob(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if requeued.Status != JobQueued {
		t.Errorf("status after requeue = %q, want %q", requeued.Status, JobQueued)
	}
	if requeued.StartedAt.Valid {
		t.Errorf("started_at after requeue = %+v, want NULL", requeued.StartedAt)
	}

	// A clean queue with nothing running moves zero rows.
	n2, err := s.RequeueStaleRunning(ctx)
	if err != nil {
		t.Fatalf("second RequeueStaleRunning: %v", err)
	}
	if n2 != 0 {
		t.Errorf("RequeueStaleRunning with nothing running returned %d, want 0", n2)
	}
}

// TestActiveJobExists asserts a lesson is "active" while its job is queued or
// running, and inactive once the job reaches a terminal state or for a lesson
// with no job at all — this is what gates the planner's dedupe.
func TestActiveJobExists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const lesson = 909

	// Absent lesson: no job, not active.
	if active, err := s.ActiveJobExists(ctx, lesson); err != nil {
		t.Fatalf("ActiveJobExists (absent): %v", err)
	} else if active {
		t.Error("ActiveJobExists = true for a lesson with no job, want false")
	}

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, lesson)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Queued: active.
	if active, err := s.ActiveJobExists(ctx, lesson); err != nil {
		t.Fatalf("ActiveJobExists (queued): %v", err)
	} else if !active {
		t.Error("ActiveJobExists = false for a queued job, want true")
	}

	// Running: active.
	if err := s.MarkJobRunning(ctx, id); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	if active, err := s.ActiveJobExists(ctx, lesson); err != nil {
		t.Fatalf("ActiveJobExists (running): %v", err)
	} else if !active {
		t.Error("ActiveJobExists = false for a running job, want true")
	}

	// Done: inactive.
	if err := s.MarkJobDone(ctx, id); err != nil {
		t.Fatalf("MarkJobDone: %v", err)
	}
	if active, err := s.ActiveJobExists(ctx, lesson); err != nil {
		t.Fatalf("ActiveJobExists (done): %v", err)
	} else if active {
		t.Error("ActiveJobExists = true after MarkJobDone, want false")
	}

	// Failed: inactive (so the planner can re-enqueue next cycle).
	failID, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, lesson)
	if err != nil {
		t.Fatalf("EnqueueJob (for failed): %v", err)
	}
	if err := s.MarkJobFailed(ctx, failID, "boom"); err != nil {
		t.Fatalf("MarkJobFailed: %v", err)
	}
	if active, err := s.ActiveJobExists(ctx, lesson); err != nil {
		t.Fatalf("ActiveJobExists (failed): %v", err)
	} else if active {
		t.Error("ActiveJobExists = true after MarkJobFailed, want false")
	}
}

// TestListJobsByStatus asserts ListJobsByStatus returns only the jobs in the
// requested status, in id (enqueue) order, and an empty slice when none match.
func TestListJobsByStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	queued1, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob queued1: %v", err)
	}
	running, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 2)
	if err != nil {
		t.Fatalf("EnqueueJob running: %v", err)
	}
	queued2, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 3)
	if err != nil {
		t.Fatalf("EnqueueJob queued2: %v", err)
	}

	// Move the middle job to running.
	if err := s.MarkJobRunning(ctx, running); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}

	gotQueued, err := s.ListJobsByStatus(ctx, JobQueued)
	if err != nil {
		t.Fatalf("ListJobsByStatus(queued): %v", err)
	}
	if len(gotQueued) != 2 {
		t.Fatalf("ListJobsByStatus(queued) returned %d jobs, want 2", len(gotQueued))
	}
	if gotQueued[0].ID != queued1 || gotQueued[1].ID != queued2 {
		t.Errorf("queued ids = [%d, %d], want [%d, %d] in id order",
			gotQueued[0].ID, gotQueued[1].ID, queued1, queued2)
	}

	gotRunning, err := s.ListJobsByStatus(ctx, JobRunning)
	if err != nil {
		t.Fatalf("ListJobsByStatus(running): %v", err)
	}
	if len(gotRunning) != 1 || gotRunning[0].ID != running {
		t.Errorf("ListJobsByStatus(running) = %+v, want one job id %d", gotRunning, running)
	}

	// A status with no rows yields an empty slice and no error.
	gotDone, err := s.ListJobsByStatus(ctx, JobDone)
	if err != nil {
		t.Fatalf("ListJobsByStatus(done): %v", err)
	}
	if len(gotDone) != 0 {
		t.Errorf("ListJobsByStatus(done) returned %d jobs, want 0", len(gotDone))
	}
}

// TestListJobsOrderAndLimit asserts ListJobs returns jobs most-recent first (by
// id DESC), regardless of status, honoring an explicit limit and falling back to
// the default cap when limit <= 0.
func TestListJobsOrderAndLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var ids []int64
	for i := 0; i < 3; i++ {
		id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, i+1)
		if err != nil {
			t.Fatalf("EnqueueJob %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	// limit <= 0 falls back to the default and returns all rows, newest first.
	all, err := s.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs(0): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListJobs(0) returned %d jobs, want 3", len(all))
	}
	if all[0].ID != ids[2] || all[1].ID != ids[1] || all[2].ID != ids[0] {
		t.Errorf("ListJobs ids = [%d, %d, %d], want descending [%d, %d, %d]",
			all[0].ID, all[1].ID, all[2].ID, ids[2], ids[1], ids[0])
	}

	// An explicit limit caps the page to the most recent rows.
	page, err := s.ListJobs(ctx, 2)
	if err != nil {
		t.Fatalf("ListJobs(2): %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("ListJobs(2) returned %d jobs, want 2", len(page))
	}
	if page[0].ID != ids[2] || page[1].ID != ids[1] {
		t.Errorf("ListJobs(2) ids = [%d, %d], want [%d, %d]",
			page[0].ID, page[1].ID, ids[2], ids[1])
	}
}

// TestCancelJobQueued asserts a queued job is moved to status='canceled' with
// finished_at stamped.
func TestCancelJobQueued(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	if err := s.CancelJob(ctx, id); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != JobCanceled {
		t.Errorf("status = %q, want %q", got.Status, JobCanceled)
	}
	if !got.FinishedAt.Valid {
		t.Error("finished_at is NULL after CancelJob, want set")
	}
}

// TestCancelJobRunning asserts a running job can also be canceled.
func TestCancelJobRunning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := s.MarkJobRunning(ctx, id); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}

	if err := s.CancelJob(ctx, id); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != JobCanceled {
		t.Errorf("status = %q, want %q", got.Status, JobCanceled)
	}
	if !got.FinishedAt.Valid {
		t.Error("finished_at is NULL after CancelJob, want set")
	}
}

// TestCancelJobTerminal asserts canceling a job already in a terminal state
// returns ErrJobNotActive and leaves the row untouched.
func TestCancelJobTerminal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := s.MarkJobRunning(ctx, id); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	if err := s.MarkJobDone(ctx, id); err != nil {
		t.Fatalf("MarkJobDone: %v", err)
	}

	if err := s.CancelJob(ctx, id); !errors.Is(err, ErrJobNotActive) {
		t.Fatalf("CancelJob on a done job err = %v, want ErrJobNotActive", err)
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != JobDone {
		t.Errorf("status = %q after failed cancel, want unchanged %q", got.Status, JobDone)
	}
}

// TestCancelJobMissing asserts canceling an unknown id reports sql.ErrNoRows so
// the handler can distinguish 404 from the 409 terminal case.
func TestCancelJobMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CancelJob(ctx, 404); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CancelJob on a missing id err = %v, want sql.ErrNoRows", err)
	}
}

// TestRetryJobFailed asserts retrying a failed job resets the job back to
// queued (clearing attempts/timestamps/error) AND resets its lesson back to
// pending (clearing the lesson error) so the worker re-downloads it.
func TestRetryJobFailed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const lesson = 500
	if err := s.UpsertLesson(ctx, lesson, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, lesson)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := s.MarkJobRunning(ctx, id); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	if err := s.MarkJobFailed(ctx, id, "yt-dlp exited 1"); err != nil {
		t.Fatalf("MarkJobFailed: %v", err)
	}
	if err := s.MarkFailed(ctx, lesson, "yt-dlp exited 1"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if err := s.RetryJob(ctx, id); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}

	job, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != JobQueued {
		t.Errorf("job status = %q, want %q", job.Status, JobQueued)
	}
	if job.Attempts != 0 {
		t.Errorf("job attempts = %d after retry, want 0", job.Attempts)
	}
	if job.StartedAt.Valid || job.FinishedAt.Valid {
		t.Errorf("job started/finished = %+v / %+v after retry, want both NULL", job.StartedAt, job.FinishedAt)
	}
	if job.Error.Valid {
		t.Errorf("job error = %+v after retry, want NULL", job.Error)
	}

	les, err := s.GetLesson(ctx, lesson)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if les.Status != StatusPending {
		t.Errorf("lesson status = %q after retry, want %q", les.Status, StatusPending)
	}
	if les.Error.Valid {
		t.Errorf("lesson error = %+v after retry, want NULL", les.Error)
	}

	// The requeue must reuse the existing job, not add a second one — so the
	// lesson has exactly one active job (otherwise the worker would double up).
	if active, err := s.ActiveJobExists(ctx, lesson); err != nil {
		t.Fatalf("ActiveJobExists: %v", err)
	} else if !active {
		t.Error("ActiveJobExists = false after retry, want true (job requeued)")
	}
	if got := countJobs(t, s); got != 1 {
		t.Errorf("jobs row count = %d after retry, want 1 (requeue must not insert a new job)", got)
	}
}

// TestRetryJobCanceled asserts a canceled job is also retryable.
func TestRetryJobCanceled(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const lesson = 600
	if err := s.UpsertLesson(ctx, lesson, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, lesson)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := s.CancelJob(ctx, id); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	if err := s.RetryJob(ctx, id); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}

	job, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != JobQueued {
		t.Errorf("job status = %q after retry, want %q", job.Status, JobQueued)
	}
}

// TestRetryJobActive asserts retrying a job that is still queued or running
// returns ErrJobNotTerminal and leaves it untouched.
func TestRetryJobActive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const lesson = 700
	if err := s.UpsertLesson(ctx, lesson, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, lesson)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	if err := s.RetryJob(ctx, id); !errors.Is(err, ErrJobNotTerminal) {
		t.Fatalf("RetryJob on a queued job err = %v, want ErrJobNotTerminal", err)
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != JobQueued {
		t.Errorf("status = %q after failed retry, want unchanged %q", got.Status, JobQueued)
	}
}

// TestRetryJobMissing asserts retrying an unknown id reports sql.ErrNoRows.
func TestRetryJobMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.RetryJob(ctx, 404); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("RetryJob on a missing id err = %v, want sql.ErrNoRows", err)
	}
}

// TestListQueuedOrderAndFilter asserts ListQueued returns only queued jobs, in
// enqueue (id) order, and excludes jobs that have moved on to running/done.
func TestListQueuedOrderAndFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob first: %v", err)
	}
	second, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 2)
	if err != nil {
		t.Fatalf("EnqueueJob second: %v", err)
	}
	third, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 3)
	if err != nil {
		t.Fatalf("EnqueueJob third: %v", err)
	}

	// Move the middle job out of the queue.
	if err := s.MarkJobRunning(ctx, second); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}

	queued, err := s.ListQueued(ctx)
	if err != nil {
		t.Fatalf("ListQueued: %v", err)
	}
	if len(queued) != 2 {
		t.Fatalf("ListQueued returned %d jobs, want 2 (running one excluded)", len(queued))
	}
	if queued[0].ID != first || queued[1].ID != third {
		t.Errorf("ListQueued ids = [%d, %d], want [%d, %d] in enqueue order",
			queued[0].ID, queued[1].ID, first, third)
	}
}

// TestCountJobsByState asserts the helper groups jobs by status and returns one
// entry per present status with the correct count, omitting states with no rows
// (the handler fills zeros for the known enum set).
func TestCountJobsByState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Enqueue three jobs (all queued), then move two to terminal states.
	var ids []int64
	for _, rc := range []int{101, 102, 103} {
		id, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, rc)
		if err != nil {
			t.Fatalf("EnqueueJob %d: %v", rc, err)
		}
		ids = append(ids, id)
	}
	if err := s.MarkJobDone(ctx, ids[0]); err != nil {
		t.Fatalf("MarkJobDone: %v", err)
	}
	if err := s.MarkJobFailed(ctx, ids[1], "boom"); err != nil {
		t.Fatalf("MarkJobFailed: %v", err)
	}

	counts, err := s.CountJobsByState(ctx)
	if err != nil {
		t.Fatalf("CountJobsByState: %v", err)
	}

	want := map[string]int{
		JobDone:   1,
		JobFailed: 1,
		JobQueued: 1,
	}
	if len(counts) != len(want) {
		t.Fatalf("CountJobsByState returned %d states (%v), want %d", len(counts), counts, len(want))
	}
	for state, n := range want {
		if counts[state] != n {
			t.Errorf("count[%q] = %d, want %d", state, counts[state], n)
		}
	}
	if _, ok := counts[JobRunning]; ok {
		t.Errorf("count includes %q with no rows, want it omitted", JobRunning)
	}
}

// TestCountJobsByStateEmpty asserts an empty jobs table yields an empty
// (non-nil) map and no error.
func TestCountJobsByStateEmpty(t *testing.T) {
	s := newTestStore(t)

	counts, err := s.CountJobsByState(context.Background())
	if err != nil {
		t.Fatalf("CountJobsByState: %v", err)
	}
	if counts == nil {
		t.Fatal("CountJobsByState returned nil map, want empty non-nil map")
	}
	if len(counts) != 0 {
		t.Errorf("CountJobsByState on empty table returned %v, want empty", counts)
	}
}

// TestEnqueueJobDedupReturnsExisting proves the new dedup contract: a second
// EnqueueJob for a lesson that already has a queued (or running) job inserts no
// new row and returns (existingID, created=false). The first call must report
// created=true.
func TestEnqueueJobDedupReturnsExisting(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, created, err := s.EnqueueJob(ctx, sql.NullInt64{}, 909)
	if err != nil {
		t.Fatalf("EnqueueJob first: %v", err)
	}
	if !created {
		t.Fatal("first EnqueueJob created = false, want true")
	}

	// Still queued: a second enqueue must dedup to the same job.
	second, created, err := s.EnqueueJob(ctx, sql.NullInt64{}, 909)
	if err != nil {
		t.Fatalf("EnqueueJob second: %v", err)
	}
	if created {
		t.Error("second EnqueueJob created = true, want false (dedup)")
	}
	if second != first {
		t.Errorf("second EnqueueJob id = %d, want existing %d", second, first)
	}

	// Move the job to running; an enqueue must still dedup against it.
	if err := s.MarkJobRunning(ctx, first); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}
	third, created, err := s.EnqueueJob(ctx, sql.NullInt64{}, 909)
	if err != nil {
		t.Fatalf("EnqueueJob third: %v", err)
	}
	if created {
		t.Error("third EnqueueJob (running) created = true, want false (dedup)")
	}
	if third != first {
		t.Errorf("third EnqueueJob id = %d, want existing %d", third, first)
	}

	// Exactly one row for this lesson.
	var n int
	if err := s.rawDB().QueryRow(
		"SELECT count(*) FROM jobs WHERE railcontent_id = 909",
	).Scan(&n); err != nil {
		t.Fatalf("count jobs for 909: %v", err)
	}
	if n != 1 {
		t.Errorf("jobs for lesson 909 = %d, want 1 (no duplicate)", n)
	}
}

// TestEnqueueJobAfterTerminalEnqueuesFresh proves dedup is scoped to active
// (queued/running) jobs only: once the prior job reaches a terminal status it no
// longer blocks a new enqueue, so a fresh job (created=true) is inserted.
func TestEnqueueJobAfterTerminalEnqueuesFresh(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 808)
	if err != nil {
		t.Fatalf("EnqueueJob first: %v", err)
	}
	if err := s.MarkJobDone(ctx, first); err != nil {
		t.Fatalf("MarkJobDone: %v", err)
	}

	second, created, err := s.EnqueueJob(ctx, sql.NullInt64{}, 808)
	if err != nil {
		t.Fatalf("EnqueueJob second: %v", err)
	}
	if !created {
		t.Error("second EnqueueJob after terminal created = false, want true")
	}
	if second == first {
		t.Errorf("second EnqueueJob reused terminal job %d, want a fresh id", first)
	}
}

// TestEnqueueJobConcurrentDedup is the TOCTOU regression test: N goroutines call
// EnqueueJob for the SAME lesson concurrently. The atomic check+insert inside
// withTx must leave EXACTLY ONE queued row, and every non-winning caller must
// return created=false with the winner's id. Run with -race, this also proves
// the path is free of data races. Before the fix (lock-free ActiveJobExists
// outside withTx) two enqueuers could both insert, producing duplicates.
func TestEnqueueJobConcurrentDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const goroutines = 16
	const railcontentID = 707

	var (
		wg          sync.WaitGroup
		start       = make(chan struct{})
		createdN    atomic.Int64
		ids         = make([]int64, goroutines)
		createdFlag = make([]bool, goroutines)
		errs        = make([]error, goroutines)
	)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			<-start // line everyone up so the inserts genuinely race
			id, created, err := s.EnqueueJob(ctx, sql.NullInt64{}, railcontentID)
			ids[i] = id
			createdFlag[i] = created
			errs[i] = err
			if created {
				createdN.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d EnqueueJob: %v", i, err)
		}
	}

	// Exactly one fresh insert.
	if got := createdN.Load(); got != 1 {
		t.Errorf("created=true count = %d, want exactly 1", got)
	}

	// Exactly one queued row in the table.
	var n int
	if err := s.rawDB().QueryRow(
		"SELECT count(*) FROM jobs WHERE railcontent_id = ?", railcontentID,
	).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 1 {
		t.Fatalf("jobs for lesson %d = %d, want exactly 1 (no duplicate enqueued)", railcontentID, n)
	}

	// Find the winner's id, then assert every non-winner returned created=false
	// with that same id.
	var winnerID int64
	if err := s.rawDB().QueryRow(
		"SELECT id FROM jobs WHERE railcontent_id = ?", railcontentID,
	).Scan(&winnerID); err != nil {
		t.Fatalf("select winner id: %v", err)
	}
	for i := 0; i < goroutines; i++ {
		if ids[i] != winnerID {
			t.Errorf("goroutine %d returned id %d, want winner %d", i, ids[i], winnerID)
		}
		if !createdFlag[i] && ids[i] != winnerID {
			t.Errorf("goroutine %d non-winner id = %d, want winner %d", i, ids[i], winnerID)
		}
	}
}
