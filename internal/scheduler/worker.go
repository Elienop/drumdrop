package scheduler

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// Worker drains the jobs table sequentially: it claims one queued job at a
// time, resolves and downloads the lesson, and records the outcome. A failed
// download is retried up to Cfg.MaxAttempts with a growing backoff between
// attempts; after the final attempt the lesson and job are marked failed. A
// single bad job never aborts the run — every per-job failure is recorded and
// the worker moves on to the next claim.
type Worker struct {
	// Store claims jobs, loads follows, and records lesson/job outcomes.
	Store Store
	// Resolver fetches the lesson metadata needed to download.
	Resolver Resolver
	// Downloader performs the actual download.
	Downloader Downloader
	// Cfg holds foldering, quality, and retry tunables.
	Cfg Config
	// PermIDs is the owner's comma-separated permission id string, passed to the
	// resolver so gated content resolves with the right entitlements.
	PermIDs string
	// Log receives human-readable per-job progress. It defaults to io.Discard;
	// the download logic never depends on it.
	Log io.Writer
	// Progress receives structured ProgressEvents beside the human-readable log.
	// It is nil by default; progress() substitutes a noopSink so callers emit
	// unconditionally. engine.Build sets it via assignment after NewWorker.
	Progress ProgressSink
	// sleep waits between retry attempts. It defaults to time.Sleep; tests inject
	// a no-op so retry paths run instantly.
	sleep func(time.Duration)
}

// NewWorker builds a Worker with the given dependencies and config, defaulting
// sleep to time.Sleep and Log to io.Discard so callers need not set them.
func NewWorker(store Store, resolver Resolver, downloader Downloader, cfg Config, permIDs string, log io.Writer) *Worker {
	if log == nil {
		log = io.Discard
	}
	// A zero-value or otherwise non-positive MaxAttempts would skip the per-attempt
	// loop entirely and mark every job failed without a single real download.
	// Clamp it to at least one genuine attempt.
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	return &Worker{
		Store:      store,
		Resolver:   resolver,
		Downloader: downloader,
		Cfg:        cfg,
		PermIDs:    permIDs,
		Log:        log,
		sleep:      time.Sleep,
	}
}

// log returns the configured writer or io.Discard so callers can write without
// a nil check and logic stays independent of logging.
func (w *Worker) log() io.Writer {
	if w.Log == nil {
		return io.Discard
	}
	return w.Log
}

// progress returns the configured sink or a noopSink so callers can emit without
// a nil check and the download logic stays independent of any consumer.
func (w *Worker) progress() ProgressSink {
	if w.Progress == nil {
		return noopSink{}
	}
	return w.Progress
}

// waitBackoff waits the given retry delay but stays responsive to cancellation:
// it returns false (do not retry) the moment ctx is cancelled, leaving the job
// to be re-queued and retried next cycle. A real Worker waits on time.After(d)
// vs ctx.Done(); a Worker with an injected sleeper (tests) calls that sleeper so
// the recorded backoff schedule is still observable, then re-checks ctx so a
// cancelled context still aborts retries. Returns true when the wait completed
// normally and the next attempt should proceed.
func (w *Worker) waitBackoff(ctx context.Context, d time.Duration) bool {
	if w.sleep == nil {
		w.sleep = time.Sleep
	}
	if isTimeSleep(w.sleep) {
		// Production path: race the real delay against cancellation so SIGINT/
		// SIGTERM interrupts a job mid-backoff instead of blocking the full delay.
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			return true
		case <-ctx.Done():
			return false
		}
	}
	// Injected-sleeper path: invoke it so retry tests still record the schedule,
	// then honor cancellation so a cancelled-context test aborts retries promptly.
	w.sleep(d)
	return ctx.Err() == nil
}

// isTimeSleep reports whether f is the real time.Sleep (the default), so the
// production wait can use a cancellable timer while injected sleepers keep their
// recordable, instant behavior.
func isTimeSleep(f func(time.Duration)) bool {
	return reflect.ValueOf(f).Pointer() == reflect.ValueOf(time.Sleep).Pointer()
}

// backoff returns Cfg.Backoff[min(i, len-1)] — the delay schedule clamped to its
// last entry for any further retries. With no backoff configured it returns 0
// so retries happen immediately (used by tests with an injected no-op sleep).
func (w *Worker) backoff(i int) time.Duration {
	b := w.Cfg.Backoff
	if len(b) == 0 {
		return 0
	}
	if i >= len(b) {
		i = len(b) - 1
	}
	return b[i]
}

// RunOnce drains queued jobs one at a time until the queue is empty, the limit
// is reached, or the context is cancelled. It returns the number of jobs
// processed. A store error from ClaimNextJob is fatal and returned; per-job
// failures are swallowed inside execute so one bad job never stops the drain.
//
// limit > 0 caps how many jobs are processed this run; limit <= 0 drains the
// whole queue. Cancellation returns nil (not an error): the in-flight job, if
// any, has already finished before the next claim.
func (w *Worker) RunOnce(ctx context.Context, limit int) (processed int, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return processed, nil
		}

		job, ok, err := w.Store.ClaimNextJob(ctx)
		if err != nil {
			return processed, fmt.Errorf("claim next job: %w", err)
		}
		if !ok {
			break // queue empty
		}

		w.progress().Emit(ProgressEvent{
			Kind:          "job_claimed",
			JobID:         job.ID,
			FollowID:      job.FollowID.Int64,
			RailcontentID: job.RailcontentID,
			Time:          time.Now(),
		})

		w.execute(ctx, job)
		processed++

		if limit > 0 && processed >= limit {
			break
		}
	}
	return processed, nil
}

// execute downloads the lesson for one already-claimed job, retrying on failure
// and recording the final outcome. It never returns an error: an unresolvable
// lesson is skipped, and a download that fails every attempt is marked failed —
// in both cases the job is marked failed so RunOnce can continue draining.
//
// Attempts accounting: ClaimNextJob already performed attempt #1 (status
// running, attempts=1), so this method does NOT re-mark running for attempt 1.
// Only attempts >= 2 call MarkJobRunning (which re-stamps and increments
// attempts). A job that succeeds on the first try ends at attempts==1; one that
// needs all three tries ends at attempts==3.
func (w *Worker) execute(ctx context.Context, job database.Job) {
	id := job.RailcontentID

	// Load the follow for quality + folder title. A NULL/missing follow id (a
	// job whose follow was deleted, or a manually enqueued job) falls back to a
	// zero Follow, which outDirFor/qualityFor handle via Cfg defaults and the
	// lesson's parent title below.
	var follow database.Follow
	if job.FollowID.Valid {
		f, err := w.Store.GetFollow(ctx, job.FollowID.Int64)
		if err != nil {
			fmt.Fprintf(w.log(), "  ⚠ job %d: follow %d not found, using defaults: %v\n", job.ID, job.FollowID.Int64, err)
		} else {
			follow = f
		}
	}

	lesson, err := w.Resolver.Resolve(id, w.PermIDs)
	if err != nil || lesson == nil {
		reason := "could not resolve (gated or missing)"
		if err != nil {
			reason = err.Error()
		}
		fmt.Fprintf(w.log(), "  ↳ skipping %d: %s\n", id, reason)
		w.progress().Emit(ProgressEvent{
			Kind:          "lesson_skipped",
			JobID:         job.ID,
			FollowID:      job.FollowID.Int64,
			RailcontentID: id,
			Err:           reason,
			Time:          time.Now(),
		})
		_ = w.Store.MarkSkipped(ctx, id, reason)
		_ = w.Store.MarkJobFailed(ctx, job.ID, reason)
		return
	}

	outDir := w.outDir(follow, job, lesson)
	quality := qualityFor(w.Cfg, follow)

	var lastErr error
	for attempt := 1; attempt <= w.Cfg.MaxAttempts; attempt++ {
		// Abort promptly on cancellation: leave the job in its current state (it
		// is not downloaded and, after a re-mark, may be running) so the next
		// planner/requeue cycle re-queues and retries it.
		if err := ctx.Err(); err != nil {
			return
		}

		// Attempt #1 was already claimed (running, attempts=1). Re-mark running
		// only for retries, then wait the backoff for this retry.
		if attempt > 1 {
			if err := w.Store.MarkJobRunning(ctx, job.ID); err != nil {
				fmt.Fprintf(w.log(), "  ⚠ job %d: re-mark running failed: %v\n", job.ID, err)
			}
			// First retry (attempt 2) waits Backoff[0]; backoff() clamps to the
			// last entry for any further retries. See the doc comment: Backoff[i]
			// is the delay before attempt i+2. A cancellation mid-backoff aborts
			// the retry early, leaving the job to be re-queued next cycle.
			if !w.waitBackoff(ctx, w.backoff(attempt-2)) {
				return
			}
		}

		if err := w.Store.MarkDownloading(ctx, id); err != nil {
			fmt.Fprintf(w.log(), "  ⚠ mark downloading %d: %v\n", id, err)
		}

		fmt.Fprintf(w.log(), "  ▼ [%02d/%d] %d %s\n", attempt, w.Cfg.MaxAttempts, id, lesson.Title)
		w.progress().Emit(ProgressEvent{
			Kind:          "download_started",
			JobID:         job.ID,
			FollowID:      job.FollowID.Int64,
			RailcontentID: id,
			Title:         lesson.Title,
			Attempt:       attempt,
			MaxAttempts:   w.Cfg.MaxAttempts,
			Time:          time.Now(),
		})
		derr := w.Downloader.Download(lesson, musora.DownloadOpts{
			Dir:           outDir,
			Index:         1,
			Quality:       quality,
			ResourcesOnly: w.Cfg.ResourcesOnly,
		})
		if derr == nil {
			lessonDir := filepath.Join(outDir, fmt.Sprintf("%02d - %s", 1, musora.Sanitize(lesson.Title)))
			videoPath, bytes := w.producedVideo(lessonDir)
			if err := w.Store.MarkDownloaded(ctx, id, quality, lessonDir, videoPath, bytes); err != nil {
				fmt.Fprintf(w.log(), "  ⚠ mark downloaded %d: %v\n", id, err)
			}
			if err := w.Store.MarkJobDone(ctx, job.ID); err != nil {
				fmt.Fprintf(w.log(), "  ⚠ mark job done %d: %v\n", job.ID, err)
			}
			fmt.Fprintf(w.log(), "  ✓ %d\n", id)
			w.progress().Emit(ProgressEvent{
				Kind:          "download_ok",
				JobID:         job.ID,
				FollowID:      job.FollowID.Int64,
				RailcontentID: id,
				Title:         lesson.Title,
				Attempt:       attempt,
				MaxAttempts:   w.Cfg.MaxAttempts,
				Bytes:         bytes,
				Time:          time.Now(),
			})
			return
		}

		lastErr = derr
		fmt.Fprintf(w.log(), "  ✖ download %d attempt %d/%d failed: %v\n", id, attempt, w.Cfg.MaxAttempts, derr)
		w.progress().Emit(ProgressEvent{
			Kind:          "attempt_failed",
			JobID:         job.ID,
			FollowID:      job.FollowID.Int64,
			RailcontentID: id,
			Title:         lesson.Title,
			Attempt:       attempt,
			MaxAttempts:   w.Cfg.MaxAttempts,
			Err:           derr.Error(),
			Time:          time.Now(),
		})
	}

	// Every attempt failed: record the lesson + job as failed and move on. The
	// next planner cycle re-enqueues the lesson (it is not downloaded, and the
	// job is no longer active), giving it another chance next interval.
	msg := "download failed"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	_ = w.Store.MarkFailed(ctx, id, msg)
	_ = w.Store.MarkJobFailed(ctx, job.ID, msg)
}

// outDir is the single source of truth for a job's output directory. With a
// real follow it uses outDirFor (cfg.DownloadsDir + the follow's folder title).
// For a NULL/zero follow it falls back to the resolved lesson's parent content
// title, or content-<id> when even that is missing, so an orphaned job still
// lands somewhere sensible.
func (w *Worker) outDir(f database.Follow, job database.Job, lesson *musora.Lesson) string {
	if hasFollow(f) {
		return outDirFor(w.Cfg, f)
	}
	title := lessonParentTitle(lesson)
	if title == "" {
		title = fmt.Sprintf("content-%d", job.RailcontentID)
	}
	return filepath.Join(w.Cfg.DownloadsDir, musora.Sanitize(title))
}

// hasFollow reports whether the loaded follow carries enough identity to drive
// foldering — a saved title or its natural key. A zero Follow (NULL/missing job
// follow) has none of these and triggers the lesson-parent-title fallback.
func hasFollow(f database.Follow) bool {
	return f.Title != "" || f.Slug.Valid || f.RailcontentID.Valid
}

// producedVideo returns the path and size of the mp4 DownloadLesson writes for a
// successfully downloaded lesson. The video lives at lessonDir/<base>.mp4 where
// <base> is lessonDir's own "NN - Sanitized title" base name (DownloadLesson
// uses base for both the folder and the file). On ResourcesOnly (no video is
// produced) or any os.Stat error (e.g. a different container extension) it
// returns "" and 0 so MarkDownloaded records no video metadata rather than a
// path that does not exist.
func (w *Worker) producedVideo(lessonDir string) (videoPath string, bytes int64) {
	if w.Cfg.ResourcesOnly {
		return "", 0
	}
	p := filepath.Join(lessonDir, filepath.Base(lessonDir)+".mp4")
	info, err := os.Stat(p)
	if err != nil {
		return "", 0
	}
	return p, info.Size()
}

// lessonParentTitle returns the first parent content title of a lesson, used as
// the folder name when a job has no follow to fold under.
func lessonParentTitle(l *musora.Lesson) string {
	if l != nil && len(l.ParentContentData) > 0 {
		return l.ParentContentData[0].Title
	}
	return ""
}
