package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
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
	// IsPaused, when non-nil and returning true, makes the drain loop stop
	// claiming the NEXT job: the in-flight download finishes but no new one
	// starts, honoring "pause = stop starting new ones" within an active cycle
	// (the daemon's own pause flag only gates whole cycles). nil => never paused
	// (the CLI sync path has no daemon). engine.Build wires it to Daemon.IsPaused.
	IsPaused func() bool
	// sleep waits between retry attempts. It defaults to time.Sleep; tests inject
	// a no-op so retry paths run instantly.
	sleep func(time.Duration)

	// mu guards running. running maps an in-flight job id to the CancelFunc of
	// the per-job context passed into the Downloader, so CancelRunning can kill an
	// active download (yt-dlp dies via the context, see musora.DownloadLesson).
	mu      sync.Mutex
	running map[int64]context.CancelFunc
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

// register records the CancelFunc for an in-flight job so CancelRunning can kill
// its download. unregister removes it once the job finishes.
func (w *Worker) register(jobID int64, cancel context.CancelFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running == nil {
		w.running = map[int64]context.CancelFunc{}
	}
	w.running[jobID] = cancel
}

// unregister drops the in-flight entry for a finished job. It is safe to call
// for a job that was never registered (a benign no-op).
func (w *Worker) unregister(jobID int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.running, jobID)
}

// CancelRunning cancels the in-flight download for jobID, killing yt-dlp via the
// per-job context, and reports whether a running job by that id was found. The
// worker's own cancel-error-first branch then records the job as canceled and the
// lesson as skipped; CancelRunning itself only fires the cancel.
func (w *Worker) CancelRunning(jobID int64) bool {
	w.mu.Lock()
	cancel, ok := w.running[jobID]
	w.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// progressCallback returns the musora.DownloadOpts.OnProgress closure for one
// download attempt: it translates each yt-dlp DownloadProgress into a
// download_progress ProgressEvent on the worker's sink. yt-dlp renders progress
// many times per second, so the closure coalesces to at most one event per
// second — except the terminal 100% observation, which always passes through so
// a consumer is guaranteed a final frame. The callback is invoked from the
// Downloader on the worker's single goroutine, so the unsynchronised lastEmit is
// safe.
func (w *Worker) progressCallback(job database.Job, lesson *musora.Lesson, attempt int) func(musora.DownloadProgress) {
	var lastEmit time.Time
	var emitted bool
	return func(p musora.DownloadProgress) {
		now := time.Now()
		if emitted && p.Pct < 100 && now.Sub(lastEmit) < time.Second {
			return
		}
		lastEmit = now
		emitted = true
		w.progress().Emit(ProgressEvent{
			Kind:          "download_progress",
			JobID:         job.ID,
			FollowID:      job.FollowID.Int64,
			RailcontentID: job.RailcontentID,
			Title:         lesson.Title,
			Attempt:       attempt,
			MaxAttempts:   w.Cfg.MaxAttempts,
			Pct:           p.Pct,
			Bytes:         p.Downloaded,
			TotalBytes:    p.Total,
			Speed:         p.Speed,
			Time:          now,
		})
	}
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
		// Pause stops the queue from advancing: the in-flight download (if any)
		// already finished this iteration; do not claim the next job. The leftover
		// jobs stay queued and drain on the next cycle after Resume.
		if w.IsPaused != nil && w.IsPaused() {
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
		// The reason goes to the log; the lesson and the event get a sentence
		// written for the user (they reach every client).
		fmt.Fprintf(w.log(), "  ↳ skipping %d: %s\n", id, reason)
		w.progress().Emit(ProgressEvent{
			Kind:          "lesson_skipped",
			JobID:         job.ID,
			FollowID:      job.FollowID.Int64,
			RailcontentID: id,
			Err:           msgNotResolved,
			Time:          time.Now(),
		})
		// Resolve runs before the loop's ctx.Err() guard, so a SIGINT/SIGTERM
		// landing mid-resolve reaches here with ctx already cancelled. Finalize via
		// WithoutCancel (same reasoning as the cancel branch below) so the lesson is
		// not stranded in its prior status with the job stuck 'running'.
		finishCtx := context.WithoutCancel(ctx)
		w.logAbandoned(id, w.Store.SkipDownload(finishCtx, job.ID, id, msgNotResolved))
		return
	}

	outDir := w.outDir(follow, job, lesson)
	quality := qualityFor(w.Cfg, follow)
	// Number the folder by the lesson's recorded position (1-based) so siblings
	// sort the way they appear in the course; fall back to 1 when the position is
	// unknown. The download and the worker's lessonDir MUST share this value so
	// producedVideo and cleanupPartials target the exact folder DownloadLesson
	// writes to. The row also says what the lesson's previous download left in
	// the library, which a plex-tv move replaces; a delete can not change it
	// while this job runs without removing the job (and then nothing here is
	// recorded). A row that can not be read is not "owns nothing": the job fails
	// without downloading, and the next cycle tries again.
	prev, err := w.Store.GetLesson(ctx, id)
	if err != nil {
		w.failBeforeDownload(ctx, job, lesson, fmt.Errorf("the lesson's record could not be read: %w", err))
		return
	}
	// What a download needs to be recorded is checked before it costs one.
	if err := w.checkBeforeDownload(ctx); err != nil {
		w.failBeforeDownload(ctx, job, lesson, err)
		return
	}
	index := 1
	if prev.Position.Valid {
		index = int(prev.Position.Int64)
	}
	// What is in the lesson folder before the first attempt is not this job's
	// to remove if it fails or is stopped (lessonFolder).
	folder := newLessonFolder(lessonDir(outDir, index, lesson.Title), prev)

	// Per-job cancelable context: register its CancelFunc so CancelRunning can kill
	// the in-flight download (yt-dlp dies via the context). Cancelling jobCtx does
	// not affect the outer ctx used for the store writes in the cancel branch.
	jobCtx, cancel := context.WithCancel(ctx)
	w.register(job.ID, cancel)
	defer w.unregister(job.ID)
	defer cancel()

	for attempt := 1; attempt <= w.Cfg.MaxAttempts; attempt++ {
		// Abort promptly on cancellation: leave the job in its current state (it
		// is not downloaded and, after a re-mark, may be running) so the next
		// planner/requeue cycle re-queues and retries it. A later attempt's end
		// was already reported (attempt_failed); the first attempt's is not.
		if err := ctx.Err(); err != nil {
			if attempt == 1 {
				w.ended(job, lesson, msgShutdown)
			}
			return
		}

		// Attempt #1 was already claimed (running, attempts=1). Re-mark running
		// only for retries, then wait the backoff for this retry.
		if attempt > 1 {
			// A job canceled or removed meanwhile is not re-marked; the
			// StartDownload below then stops the download.
			if err := w.Store.MarkJobRunning(ctx, job.ID); err != nil && !errors.Is(err, database.ErrDownloadCanceled) {
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

		if w.stopped(ctx, job, lesson, folder, w.Store.StartDownload(ctx, job.ID, id), "mark downloading") {
			return
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
		derr := w.Downloader.Download(jobCtx, lesson, musora.DownloadOpts{
			Dir:           outDir,
			Root:          w.Cfg.DownloadsDir,
			Index:         index,
			Quality:       quality,
			AudioLang:     w.Cfg.AudioLang,
			ResourcesOnly: w.Cfg.ResourcesOnly,
			OnProgress:    w.progressCallback(job, lesson, attempt),
		})

		// Cancel-error-first: a CancelRunning kill (jobCtx cancelled) must never
		// fall through to the success or failure branches: record the cancel
		// and stop, no retry.
		if jobCtx.Err() != nil || errors.Is(derr, context.Canceled) {
			w.finishCanceled(ctx, job, lesson, folder)
			return
		}

		if derr == nil {
			// The download is complete; nothing is moved yet. A job a delete
			// removed, or one canceled meanwhile, stops here.
			if w.stopped(ctx, job, lesson, folder, w.Store.ConfirmDownload(ctx, job.ID, id), "confirm download") {
				return
			}
			bytes, ok, rerr := w.recordDownload(ctx, job, lesson, follow, prev, quality, index, folder)
			if rerr == nil {
				if ok {
					w.downloaded(job, lesson, attempt, bytes)
				}
				return
			}
			// The download could not be recorded where it belongs (the other
			// lessons' claims could not be read): a failed attempt, retried.
			derr = rerr
		}

		fmt.Fprintf(w.log(), "  ✖ download %d attempt %d/%d failed: %v\n", id, attempt, w.Cfg.MaxAttempts, derr)
		w.progress().Emit(ProgressEvent{
			Kind:          "attempt_failed",
			JobID:         job.ID,
			FollowID:      job.FollowID.Int64,
			RailcontentID: id,
			Title:         lesson.Title,
			Attempt:       attempt,
			MaxAttempts:   w.Cfg.MaxAttempts,
			Err:           msgAttemptFailed,
			Time:          time.Now(),
		})
	}

	// Every attempt failed: record the lesson + job as failed and move on. The
	// next planner cycle re-enqueues the lesson (it is not downloaded, and the
	// job is no longer active), giving it another chance next interval. Each
	// attempt's error was logged above; the lesson records a sentence.
	// WithoutCancel for parity with the cancel/resolve branches: the per-attempt
	// ctx.Err() guard makes a cancelled ctx here practically unreachable, but
	// keeping all terminal writes uncancellable makes "shutdown never strands a
	// job" a single, obvious invariant.
	finishCtx := context.WithoutCancel(ctx)
	switch ferr := w.Store.FailDownload(finishCtx, job.ID, id, msgDownloadFailed); {
	case errors.Is(ferr, database.ErrDownloadAbandoned):
		// A delete or a skip removed the job while it failed: what it wrote goes
		// as the stopper wants, the same as when it stops mid-download.
		w.discardAbandoned(finishCtx, id, folder.path, nil, folder, ferr)
	case errors.Is(ferr, database.ErrDownloadCanceled):
		// Requeued elsewhere: the next run writes the same folder, so leave it.
		fmt.Fprintf(w.log(), "  ⚠ record failure %d: its job was requeued meanwhile: %v\n", id, ferr)
	default:
		if ferr != nil {
			fmt.Fprintf(w.log(), "  ⚠ record failure %d: %v\n", id, ferr)
		}
		w.dropFailedDownload(finishCtx, id, folder)
	}
}

// failBeforeDownload fails a job whose precondition can not pass, without
// downloading anything: its reason is logged, reported as a failed attempt,
// and the lesson is marked failed (the next cycle tries again). The event and
// the lesson carry a sentence written for the user, not the reason.
func (w *Worker) failBeforeDownload(ctx context.Context, job database.Job, lesson *musora.Lesson, reason error) {
	id := job.RailcontentID
	fmt.Fprintf(w.log(), "  ✖ %d not downloaded: %v\n", id, reason)
	w.progress().Emit(ProgressEvent{
		Kind:          "attempt_failed",
		JobID:         job.ID,
		FollowID:      job.FollowID.Int64,
		RailcontentID: id,
		Title:         lesson.Title,
		Attempt:       1,
		MaxAttempts:   w.Cfg.MaxAttempts,
		Err:           msgNotStarted,
		Time:          time.Now(),
	})
	w.logAbandoned(id, w.Store.FailDownload(context.WithoutCancel(ctx), job.ID, id, msgNotStarted))
}

// ended reports the end of a job that stopped without downloading, failing or
// being canceled (a skip or a delete removed it, it was requeued elsewhere, or
// drumdrop is shutting down), so a client never keeps showing it as active.
// msg is a sentence for the user.
func (w *Worker) ended(job database.Job, lesson *musora.Lesson, msg string) {
	w.progress().Emit(ProgressEvent{
		Kind:          "lesson_skipped",
		JobID:         job.ID,
		FollowID:      job.FollowID.Int64,
		RailcontentID: job.RailcontentID,
		Title:         lesson.Title,
		Err:           msg,
		Time:          time.Now(),
	})
}

// logAbandoned logs the outcome of a terminal store write made before anything
// was downloaded (a skip, or a failed precondition), so there is nothing on
// disk for a delete's intent to apply to.
func (w *Worker) logAbandoned(id int, err error) {
	switch {
	case errors.Is(err, database.ErrDownloadAbandoned):
		fmt.Fprintf(w.log(), "  ⊗ %d was removed meanwhile; nothing was recorded\n", id)
	case err != nil:
		fmt.Fprintf(w.log(), "  ⚠ record %d: %v\n", id, err)
	}
}

// stopped reports whether a guarded write's err ends the job: a delete or a
// skip removed it (its end is reported, and what it wrote goes as the stopper
// wants, see discardAbandoned), or it was canceled (recorded as a cancel). Any
// other error is logged under what, and the download goes on.
func (w *Worker) stopped(ctx context.Context, job database.Job, lesson *musora.Lesson, folder lessonFolder, err error, what string) bool {
	switch {
	case errors.Is(err, database.ErrDownloadAbandoned):
		w.ended(job, lesson, msgStopped)
		w.discardAbandoned(ctx, job.RailcontentID, folder.path, nil, folder, err)
		return true
	case errors.Is(err, database.ErrDownloadCanceled):
		w.finishCanceled(ctx, job, lesson, folder)
		return true
	case err != nil:
		fmt.Fprintf(w.log(), "  ⚠ %s %d: %v\n", what, job.RailcontentID, err)
	}
	return false
}

// finishCanceled records a canceled download: its partial files are removed,
// the lesson is left as CancelDownload says, and the job canceled. The write
// uses context.WithoutCancel(ctx): on a user Cancel the outer ctx is still
// alive, but on SIGINT/SIGTERM shutdown it is ALSO cancelled (it is what
// cancelled the job's context), and a plain ctx would make withTx's BeginTx
// fail immediately, stranding the job 'running' and the lesson 'downloading'
// until the next startup requeue. gracefulServe joins the daemon goroutine
// before closing the store, so the DB is still open when it runs. A delete
// that removed the job meanwhile decides what happens to what it wrote
// (discardAbandoned); a job requeued meanwhile (a retry) records nothing.
func (w *Worker) finishCanceled(ctx context.Context, job database.Job, lesson *musora.Lesson, folder lessonFolder) {
	id := job.RailcontentID
	dir := folder.path
	fmt.Fprintf(w.log(), "  ⊗ canceled %d\n", id)
	w.progress().Emit(ProgressEvent{
		Kind:          "lesson_skipped",
		JobID:         job.ID,
		FollowID:      job.FollowID.Int64,
		RailcontentID: id,
		Title:         lesson.Title,
		Err:           "canceled",
		Time:          time.Now(),
	})
	finishCtx := context.WithoutCancel(ctx)
	switch err := w.Store.CancelDownload(finishCtx, job.ID, id); {
	case errors.Is(err, database.ErrDownloadAbandoned):
		w.discardAbandoned(finishCtx, id, dir, nil, folder, err)
		return
	case errors.Is(err, database.ErrDownloadCanceled):
	case err != nil:
		fmt.Fprintf(w.log(), "  ⚠ record cancel %d: %v\n", id, err)
	}
	cleanupPartials(dir)
}

// downloaded reports a recorded download.
func (w *Worker) downloaded(job database.Job, lesson *musora.Lesson, attempt int, bytes int64) {
	fmt.Fprintf(w.log(), "  ✓ %d\n", job.RailcontentID)
	w.progress().Emit(ProgressEvent{
		Kind:          "download_ok",
		JobID:         job.ID,
		FollowID:      job.FollowID.Int64,
		RailcontentID: job.RailcontentID,
		Title:         lesson.Title,
		Attempt:       attempt,
		MaxAttempts:   w.Cfg.MaxAttempts,
		Bytes:         bytes,
		Time:          time.Now(),
	})
}

// outDir is the single source of truth for a job's output directory. An
// instructor follow groups its lessons by their parent course
// (<instructor>/<parent course>), falling back to just <instructor> when a
// lesson has no parent course. Any other real follow (a node) uses outDirFor
// (cfg.DownloadsDir + the follow's folder title). For a NULL/zero follow it
// falls back to the resolved lesson's parent content title, or content-<id>
// when even that is missing, so an orphaned job still lands somewhere sensible.
func (w *Worker) outDir(f database.Follow, job database.Job, lesson *musora.Lesson) string {
	if hasFollow(f) && f.Kind == "instructor" {
		base := filepath.Join(w.Cfg.DownloadsDir, musora.Sanitize(folderTitle(f)))
		if parent := lessonParentTitle(lesson); parent != "" {
			return filepath.Join(base, musora.Sanitize(parent))
		}
		return base
	}
	if hasFollow(f) {
		return outDirFor(w.Cfg, f)
	}
	title := lessonParentTitle(lesson)
	if title == "" {
		title = fmt.Sprintf("content-%d", job.RailcontentID)
	}
	return filepath.Join(w.Cfg.DownloadsDir, musora.Sanitize(title))
}

// plexShow is the single top-level show name for the plex-tv layout. It mirrors
// outDir's grouping decision but FLATTENS it to one segment (Plex TV misreads a
// nested <Instructor>/<Course> as two levels): an instructor follow uses the
// lesson's parent course, falling back to the instructor name for a course-less
// lesson; any other real follow uses its folder title; a NULL/zero follow uses
// the lesson's parent title, or content-<id> when even that is missing. The raw
// (un-Sanitized) title is returned — moveToLibraryPlexTV Sanitizes it into the
// path segment, matching how outDir feeds folderTitle through Sanitize.
func plexShow(f database.Follow, job database.Job, lesson *musora.Lesson) string {
	if hasFollow(f) && f.Kind == "instructor" {
		if parent := lessonParentTitle(lesson); parent != "" {
			return parent
		}
		return folderTitle(f)
	}
	if hasFollow(f) {
		return folderTitle(f)
	}
	if title := lessonParentTitle(lesson); title != "" {
		return title
	}
	return fmt.Sprintf("content-%d", job.RailcontentID)
}

// hasFollow reports whether the loaded follow carries enough identity to drive
// foldering — a saved title or its natural key. A zero Follow (NULL/missing job
// follow) has none of these and triggers the lesson-parent-title fallback.
func hasFollow(f database.Follow) bool {
	return f.Title != "" || f.Slug.Valid || f.RailcontentID.Valid
}

// lessonDir is the single source of truth for a lesson's on-disk folder: the
// output dir joined with "NN - Sanitized title", where NN is the lesson's
// position. DownloadLesson builds the same name from DownloadOpts.Index, so the
// index passed here MUST match DownloadOpts.Index for producedVideo to find the
// file and for cleanupPartials to target the right dir.
func lessonDir(outDir string, index int, title string) string {
	return filepath.Join(outDir, fmt.Sprintf("%02d - %s", index, musora.Sanitize(title)))
}

// cleanupPartials removes leftover partial-download artifacts from a lesson's
// dir, so a killed download leaves no half-written files behind, and none is
// moved into the library. Only names of the lesson's own base (the folder's
// name) in the partial shapes go (isPartialName): a finished subtitle ("<base>.fr.vtt") or
// a title with dots in it is a kept file, never a partial. A missing dir is
// tolerated (nothing to clean); any other read/remove error is ignored:
// cleanup is best-effort and must not block the cancel path.
func cleanupPartials(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // missing dir or unreadable: nothing to clean
	}
	base := filepath.Base(dir)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if name := e.Name(); isPartialName(name, base) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// isPartialName reports whether name is one of the partial-download artifacts
// for the lesson base, yt-dlp's or drumdrop's own:
//   - "<base>….part", "<base>….ytdl", and "<base>….part-Frag<N>…" (a download
//     in progress, and its fragments);
//   - "<base>[ [Label]].f<digits>.<ext>" (one format of a merge, before
//     ffmpeg joins them);
//   - "<base>[ [Label]].temp.<ext>" (ffmpeg's output while it merges or fixes
//     up the video, before yt-dlp renames it over "<base>.<ext>");
//   - "<base>….drumdrop-part" and "<base>….drumdrop-episode" (a file drumdrop
//     writes itself, the nfo or the poster, before it takes its place).
//
// Each is left only by a run that died (or was killed) before finishing it.
func isPartialName(name, base string) bool {
	rest, ok := strings.CutPrefix(name, base)
	if !ok {
		return false
	}
	for _, suffix := range []string{".part", ".ytdl", musora.TempSuffix, episodeTempSuffix} {
		if strings.HasSuffix(rest, suffix) {
			return true
		}
	}
	if strings.Contains(rest, ".part-Frag") {
		return true
	}
	if strings.HasPrefix(rest, " [") {
		end := strings.Index(rest, "]")
		if end < 0 {
			return false
		}
		rest = rest[end+1:]
	}
	if ext, ok := strings.CutPrefix(rest, ".temp."); ok {
		return ext != "" && !strings.Contains(ext, ".")
	}
	after, ok := strings.CutPrefix(rest, ".f")
	if !ok {
		return false
	}
	digits, ext, ok := strings.Cut(after, ".")
	if !ok || digits == "" || ext == "" || strings.Contains(ext, ".") {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// producedVideo returns the path and total size of the mp4(s) DownloadLesson
// writes for a successfully downloaded lesson. A regular lesson produces a
// single "<base>.mp4"; a song produces one bracket-tagged version file per
// recording ("<base> [Original].mp4", "<base> [Drumless].mp4"). Both share the
// lessonDir's own "NN - Sanitized title" base name (DownloadLesson uses base for
// the folder and every file), so producedVideo collects every regular file
// named "<base>...mp4" and returns the FIRST in sorted order as videoPath (for
// deterministic metadata) and the SUM of all their sizes as bytes.
//
// On ResourcesOnly (no video is produced) or when no matching mp4 exists (a
// video-less song, or a different container extension) it returns "" and 0 so
// FinishDownload records no video metadata rather than a path that does not
// exist. ReadDir + string prefix/suffix matching is used (not filepath.Glob) so
// glob metacharacters surviving Sanitize in the base can never break the match.
func (w *Worker) producedVideo(lessonDir string) (videoPath string, bytes int64) {
	if w.Cfg.ResourcesOnly {
		return "", 0
	}
	base := filepath.Base(lessonDir)
	entries, err := os.ReadDir(lessonDir)
	if err != nil {
		return "", 0
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Matches "<base>.mp4" (regular lesson) and "<base> [Tag].mp4" (song version
		// files), while rejecting yt-dlp fragments ("<base> [..].fNNN.mp4") and
		// unrelated "<base> X.mp4" strays.
		if isLessonVideoName(name, base) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", 0
	}
	sort.Strings(names)
	for _, name := range names {
		if info, err := os.Stat(filepath.Join(lessonDir, name)); err == nil {
			bytes += info.Size()
		}
	}
	return filepath.Join(lessonDir, names[0]), bytes
}

// lessonParentTitle returns the first parent content title of a lesson, used as
// the folder name when a job has no follow to fold under.
func lessonParentTitle(l *musora.Lesson) string {
	if l != nil && len(l.ParentContentData) > 0 {
		return l.ParentContentData[0].Title
	}
	return ""
}
