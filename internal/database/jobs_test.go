package database

import (
	"context"
	"database/sql"
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
	if err := s.DB().QueryRow("SELECT count(*) FROM jobs").Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

func TestEnqueueJobThenListQueued(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	followID := seedFollow(t, s, 100)

	id, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: followID, Valid: true}, 409875)
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

	id, err := s.EnqueueJob(ctx, sql.NullInt64{}, 555)
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

	id, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
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

	id, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
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

	id, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
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

	if _, err := s.DB().Exec(
		"INSERT INTO jobs(railcontent_id, status) VALUES(1, 'bogus')",
	); err == nil {
		t.Error("inserting a job with status='bogus' succeeded, want CHECK violation")
	}
	for i, st := range []string{
		JobQueued, JobRunning, JobDone, JobFailed, JobCanceled,
	} {
		if _, err := s.DB().Exec(
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

	jobID, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: followID, Valid: true}, 409875)
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

	first, err := s.EnqueueJob(ctx, sql.NullInt64{}, 11)
	if err != nil {
		t.Fatalf("EnqueueJob first: %v", err)
	}
	second, err := s.EnqueueJob(ctx, sql.NullInt64{}, 22)
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

	if _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 77); err != nil {
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

	id, err := s.EnqueueJob(ctx, sql.NullInt64{}, lesson)
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
	failID, err := s.EnqueueJob(ctx, sql.NullInt64{}, lesson)
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

// TestListQueuedOrderAndFilter asserts ListQueued returns only queued jobs, in
// enqueue (id) order, and excludes jobs that have moved on to running/done.
func TestListQueuedOrderAndFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob first: %v", err)
	}
	second, err := s.EnqueueJob(ctx, sql.NullInt64{}, 2)
	if err != nil {
		t.Fatalf("EnqueueJob second: %v", err)
	}
	third, err := s.EnqueueJob(ctx, sql.NullInt64{}, 3)
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
