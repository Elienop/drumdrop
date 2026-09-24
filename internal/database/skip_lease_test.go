package database

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

// abandonedIntent is what abandoned_jobs records for job id ("" when no row).
func abandonedIntent(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var intent string
	err := s.rawDB().QueryRow(`SELECT intent FROM abandoned_jobs WHERE job_id = ?`, id).Scan(&intent)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("read the intent of job %d: %v", id, err)
	}
	return intent
}

// TestSkipLessonSticks proves Skip in one transaction marks the lesson skipped
// with its reason and stops every download of it: its queued job is removed
// (nothing to kill), its running one is removed and returned to kill, with
// "discard" recorded so the worker records nothing and removes what it wrote;
// the planner then leaves the lesson alone.
func TestSkipLessonSticks(t *testing.T) {
	ctx := context.Background()
	t.Run("queued", func(t *testing.T) {
		s := newTestStore(t)
		queued := seedJob(t, s, 1, sql.NullInt64{})
		kill, err := s.SkipLesson(ctx, 1, "not for me")
		if err != nil || len(kill) != 0 {
			t.Fatalf("SkipLesson = %v, %v, want nothing to kill", kill, err)
		}
		if _, err := s.GetJob(ctx, queued); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("the queued job survived the skip (err=%v)", err)
		}
		if got := abandonedIntent(t, s, queued); got != "" {
			t.Errorf("a job no worker held recorded intent %q", got)
		}
		l := mustLesson(t, s, 1)
		if l.Status != StatusSkipped || l.Error.String != "not for me" {
			t.Errorf("lesson = %s/%v, want skipped with the reason", l.Status, l.Error)
		}
		if skip, err := s.ShouldSkipEnqueue(ctx, 1); err != nil || !skip {
			t.Errorf("ShouldSkipEnqueue after Skip = %v, %v, want true", skip, err)
		}
	})
	t.Run("mid-download", func(t *testing.T) {
		s := newTestStore(t)
		running := claimed(t, s, 1, sql.NullInt64{})
		if err := s.StartDownload(ctx, running, 1); err != nil {
			t.Fatalf("StartDownload: %v", err)
		}
		kill, err := s.SkipLesson(ctx, 1, "")
		if err != nil || !reflect.DeepEqual(kill, []int64{running}) {
			t.Fatalf("SkipLesson = %v, %v, want [%d] to kill", kill, err, running)
		}
		if got := abandonedIntent(t, s, running); got != intentDiscard {
			t.Errorf("intent = %q, want %q", got, intentDiscard)
		}
		err = s.FinishDownload(ctx, running, 1, DownloadRecord{OutputDir: "/dl/C/01 - L"})
		if !errors.Is(err, ErrDownloadAbandoned) || errors.Is(err, ErrLessonDeleted) {
			t.Fatalf("FinishDownload after Skip = %v, want abandoned, not a delete", err)
		}
		if l := mustLesson(t, s, 1); l.Status != StatusSkipped || l.OutputDir.Valid {
			t.Errorf("the skipped lesson was recorded: %+v", l)
		}
	})
	t.Run("refused while deleting", func(t *testing.T) {
		s := newTestStore(t)
		seedJob(t, s, 1, sql.NullInt64{})
		seedFiles(t, s, 1, "/dl/C/01 - L", "", sql.NullString{})
		if _, _, err := s.BeginLessonDelete(ctx, 1); err != nil {
			t.Fatalf("BeginLessonDelete: %v", err)
		}
		if _, err := s.SkipLesson(ctx, 1, "x"); !errors.Is(err, ErrLessonDeleting) {
			t.Errorf("SkipLesson during a delete = %v, want ErrLessonDeleting", err)
		}
		if l := mustLesson(t, s, 1); l.Status != StatusDownloaded {
			t.Errorf("a refused skip changed the lesson to %s", l.Status)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.SkipLesson(ctx, 404, ""); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("SkipLesson(unknown) = %v, want sql.ErrNoRows", err)
		}
	})
}

// TestDeleteLeaseLapsesAndRenews proves the deleting mark is a lease: a delete
// the process died in stops holding the lesson once its lease lapses, with no
// sweep; a renewal keeps a live delete holding it; and a renewal after the
// delete ended does not bring the mark back.
func TestDeleteLeaseLapsesAndRenews(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedJob(t, s, 1, sql.NullInt64{})
	if _, _, err := s.BeginLessonDelete(ctx, 1); err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	mustExec(t, s, `UPDATE lessons SET deleting_until = datetime('now', '-1 second') WHERE railcontent_id = 1`)
	if l := mustLesson(t, s, 1); l.Deleting {
		t.Error("a lapsed lease still reads as deleting")
	}
	if skip, err := s.ShouldSkipEnqueue(ctx, 1); err != nil || skip {
		t.Errorf("ShouldSkipEnqueue on a lapsed lease = %v, %v, want false", skip, err)
	}
	if err := s.RenewLessonDelete(ctx, 1); err != nil {
		t.Fatalf("RenewLessonDelete: %v", err)
	}
	if l := mustLesson(t, s, 1); !l.Deleting {
		t.Error("a renewed lease does not hold the lesson")
	}
	if _, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1); !errors.Is(err, ErrLessonDeleting) {
		t.Errorf("EnqueueJob under a renewed lease = %v, want ErrLessonDeleting", err)
	}
	if err := s.EndLessonDelete(ctx, 1); err != nil {
		t.Fatalf("EndLessonDelete: %v", err)
	}
	if err := s.RenewLessonDelete(ctx, 1); err != nil {
		t.Fatalf("RenewLessonDelete after the end: %v", err)
	}
	if l := mustLesson(t, s, 1); l.Deleting {
		t.Error("a renewal after the delete ended marked the lesson again")
	}
}

// TestAbandonedJobsAreBounded proves abandoned_jobs holds only rows a worker
// may still need: a row is consumed by the first write that reads it (a later
// read keeps the files, the safe side), a row nobody read lapses after
// abandonedTTL, a job no worker held gets no row, and a row answers only for
// the job AND lesson it names, so a reused job id can not inherit it.
func TestAbandonedJobsAreBounded(t *testing.T) {
	ctx := context.Background()
	t.Run("consumed on read", func(t *testing.T) {
		s := newTestStore(t)
		running := claimed(t, s, 1, sql.NullInt64{})
		mustExec(t, s, `DELETE FROM jobs WHERE id = ?`, running)
		mustExec(t, s, `INSERT INTO abandoned_jobs(job_id, railcontent_id, intent) VALUES(?, 1, 'delete')`, running)
		if err := s.CancelDownload(ctx, running, 1, true); !errors.Is(err, ErrLessonDeleted) {
			t.Fatalf("first read = %v, want the delete", err)
		}
		if got := abandonedIntent(t, s, running); got != "" {
			t.Errorf("the row survived its read: %q", got)
		}
		err := s.CancelDownload(ctx, running, 1, true)
		if !errors.Is(err, ErrDownloadAbandoned) || errors.Is(err, ErrLessonDeleted) {
			t.Errorf("second read = %v, want abandoned, keeping the files", err)
		}
	})
	t.Run("lapses", func(t *testing.T) {
		s := newTestStore(t)
		old := claimed(t, s, 1, sql.NullInt64{})
		if _, err := s.SkipLesson(ctx, 1, ""); err != nil {
			t.Fatalf("SkipLesson: %v", err)
		}
		mustExec(t, s, `UPDATE abandoned_jobs SET recorded_at = datetime('now', '-8 days') WHERE job_id = ?`, old)
		fresh := claimed(t, s, 2, sql.NullInt64{})
		if _, err := s.SkipLesson(ctx, 2, ""); err != nil {
			t.Fatalf("SkipLesson: %v", err)
		}
		if got := abandonedIntent(t, s, old); got != "" {
			t.Errorf("a row older than the TTL survived the next insert: %q", got)
		}
		if got := abandonedIntent(t, s, fresh); got != intentDiscard {
			t.Errorf("the fresh row = %q, want %q", got, intentDiscard)
		}
	})
	t.Run("never-started canceled job", func(t *testing.T) {
		s := newTestStore(t)
		queued := seedJob(t, s, 1, sql.NullInt64{})
		if err := s.CancelJob(ctx, queued); err != nil {
			t.Fatalf("CancelJob: %v", err)
		}
		if kill, err := s.SkipLesson(ctx, 1, ""); err != nil || len(kill) != 0 {
			t.Errorf("SkipLesson = %v, %v, want nothing to kill", kill, err)
		}
		if got := abandonedIntent(t, s, queued); got != "" {
			t.Errorf("a job no worker held recorded %q", got)
		}
	})
	t.Run("matched on the lesson too", func(t *testing.T) {
		s := newTestStore(t)
		seedJob(t, s, 9, sql.NullInt64{})
		running := claimed(t, s, 1, sql.NullInt64{})
		mustExec(t, s, `DELETE FROM jobs WHERE id = ?`, running)
		// A row left for the same job id but another lesson (a job id reused
		// after a table rebuild) must not answer for this job.
		mustExec(t, s, `INSERT INTO abandoned_jobs(job_id, railcontent_id, intent) VALUES(?, 9, 'delete')`, running)
		err := s.FinishDownload(ctx, running, 1, DownloadRecord{OutputDir: "/dl/C/01 - L"})
		if !errors.Is(err, ErrDownloadAbandoned) || errors.Is(err, ErrLessonDeleted) {
			t.Errorf("FinishDownload = %v, want abandoned, keeping the files", err)
		}
	})
}

// TestStoppingAPendingLessonKeepsItPending (L5) proves removing a queued job
// leaves its lesson as it was unless it was 'downloading': a follow delete
// that then fails leaves a pending lesson pending, so the planner queues it
// again, instead of marking it skipped or canceled for good.
func TestStoppingAPendingLessonKeepsItPending(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := seedFollow(t, s, 10)
	seedJob(t, s, 1, sql.NullInt64{Int64: f, Valid: true})
	if _, _, err := s.BeginFollowDelete(ctx, f); err != nil {
		t.Fatalf("BeginFollowDelete: %v", err)
	}
	if err := s.EndLessonDelete(ctx, 1); err != nil {
		t.Fatalf("EndLessonDelete: %v", err)
	}
	l := mustLesson(t, s, 1)
	if l.Status != StatusPending || l.Error.Valid {
		t.Errorf("lesson = %s/%v, want pending with no error", l.Status, l.Error)
	}
	if skip, err := s.ShouldSkipEnqueue(ctx, 1); err != nil || skip {
		t.Errorf("ShouldSkipEnqueue = %v, %v, want false (queued again)", skip, err)
	}
}

// TestUpdateFollowQualityUnknownIsNotFound proves an unknown follow is a
// wrapped sql.ErrNoRows, which the API answers with 404.
func TestUpdateFollowQualityUnknownIsNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpdateFollowQuality(context.Background(), 404, "720"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UpdateFollowQuality(unknown) = %v, want sql.ErrNoRows", err)
	}
}
