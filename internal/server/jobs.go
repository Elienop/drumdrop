package server

import (
	"net/http"

	"github.com/elienop/drumdrop/internal/database"
)

// handleListJobs serves GET /api/jobs. With ?state it returns every job in that
// status, oldest first (via ListJobsByStatus); otherwise it returns a page of
// the most recent jobs across all statuses, honoring the optional ?limit query
// param. A non-integer ?limit is a 400.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if state := q.Get("state"); state != "" {
		jobs, err := s.store.ListJobsByStatus(r.Context(), state)
		if err != nil {
			writeLoadErr(w, err, msgLoadFailed)
			return
		}
		writeJSON(w, http.StatusOK, jobDTOs(jobs))
		return
	}

	limit, ok := queryInt(w, q.Get("limit"), "limit")
	if !ok {
		return
	}
	jobs, err := s.store.ListJobs(r.Context(), limit)
	if err != nil {
		writeLoadErr(w, err, msgLoadFailed)
		return
	}
	writeJSON(w, http.StatusOK, jobDTOs(jobs))
}

// handleGetJob serves GET /api/jobs/{id}: the single job keyed by its id, or 404
// if no job has that id. A non-integer id is a 400.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	j, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		writeLoadErr(w, err, msgNoSuchJob)
		return
	}
	writeJSON(w, http.StatusOK, jobDTO(j))
}

// handleCancelJob serves POST /api/jobs/{id}/cancel: it cancels a queued or
// running job and returns the (re-fetched) job with 200. It reads the job first
// so an unknown id maps cleanly to 404.
//
// A RUNNING job has a live yt-dlp process: a bare DB status flip would leave it
// downloading (the original bug). So for a running job we ask the worker, via
// deps.CancelRunning, to kill the process; the worker's cancel-first branch then
// finalizes the job to canceled + the lesson to skipped ASYNCHRONOUSLY and emits
// a lesson_skipped SSE event the UI refetches on. The handler does not block on
// that, so the re-fetched job may still read "running" — that is expected. When
// no worker is attached (CancelRunning nil) or the job is not in-flight in this
// process (returns false), we fall back to the DB CancelJob flip.
//
// A QUEUED job has no process to kill, so the DB flip is sufficient. CancelJob
// on a terminal job yields ErrJobNotActive, which writeStoreErr turns into 409.
// A non-integer id is a 400.
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	j, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, msgCancelGone)
		return
	}

	// For a running job, prefer killing the in-flight process via the worker; it
	// owns the canceled/skipped finalization. Only fall back to the DB flip when
	// no worker handled it.
	handledByWorker := j.Status == database.JobRunning &&
		s.deps.CancelRunning != nil && s.deps.CancelRunning(id)
	if !handledByWorker {
		if err := s.store.CancelJob(r.Context(), id); err != nil {
			writeStoreErr(w, err, msgCancelGone)
			return
		}
	}

	j, err = s.store.GetJob(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, msgCancelGone)
		return
	}
	writeJSON(w, http.StatusOK, jobDTO(j))
}

// handlePause serves POST /api/pause: it pauses the daemon's sync cycles and
// returns {"paused":true} with 200. When no daemon is attached (deps.Pause nil)
// it returns 503.
func (s *Server) handlePause(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Pause == nil {
		writeErr(w, http.StatusServiceUnavailable, msgNoDaemon)
		return
	}
	s.deps.Pause()
	writeJSON(w, http.StatusOK, map[string]bool{"paused": true})
}

// handleResume serves POST /api/resume: it resumes the daemon's sync cycles and
// returns {"paused":false} with 200. When no daemon is attached (deps.Resume
// nil) it returns 503.
func (s *Server) handleResume(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Resume == nil {
		writeErr(w, http.StatusServiceUnavailable, msgNoDaemon)
		return
	}
	s.deps.Resume()
	// Kick a cycle so lessons left queued while paused drain now, rather than
	// waiting for the next scheduled tick (up to the full sync interval away).
	s.kick()
	writeJSON(w, http.StatusOK, map[string]bool{"paused": false})
}

// handleRetryJob serves POST /api/jobs/{id}/retry: it requeues a failed or
// canceled job (and resets its lesson to pending) and returns the requeued job
// with 202. It reads the job first so an unknown id maps cleanly to 404. A job
// that is not in a retryable terminal status yields ErrJobNotTerminal, which
// writeStoreErr turns into 409. A non-integer id is a 400.
func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.store.GetJob(r.Context(), id); err != nil {
		writeStoreErr(w, err, msgRetryGone)
		return
	}
	if err := s.store.RetryJob(r.Context(), id); err != nil {
		writeStoreErr(w, err, msgRetryGone)
		return
	}
	// The job is queued: start a sync now (owner ruling 2026-09-24 (m)).
	s.kick()
	j, err := s.store.GetJob(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, msgRetryGone)
		return
	}
	writeJSON(w, http.StatusAccepted, jobDTO(j))
}

// lessonStatuses and jobStatuses are the known enum sets used to fill the
// summary maps with zero counts for statuses that have no rows, so the wire
// shape carries a stable bucket set regardless of what the database holds.
var (
	lessonStatuses = []string{
		database.StatusPending,
		database.StatusDownloading,
		database.StatusDownloaded,
		database.StatusFailed,
		database.StatusSkipped,
	}
	jobStatuses = []string{
		database.JobQueued,
		database.JobRunning,
		database.JobDone,
		database.JobFailed,
		database.JobCanceled,
	}
)

// handleSummary serves GET /api/summary: the dashboard rollup of lesson counts
// by status, job counts by state, and the total follow count. The store's count
// methods omit statuses with no rows, so the handler seeds every known enum to
// zero first and then overlays the observed counts.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	lessonCounts, err := s.store.CountLessonsByStatus(ctx)
	if err != nil {
		writeLoadErr(w, err, msgLoadFailed)
		return
	}
	jobCounts, err := s.store.CountJobsByState(ctx)
	if err != nil {
		writeLoadErr(w, err, msgLoadFailed)
		return
	}
	follows, err := s.store.ListFollows(ctx)
	if err != nil {
		writeLoadErr(w, err, msgLoadFailed)
		return
	}

	writeJSON(w, http.StatusOK, SummaryDTO{
		Follows: len(follows),
		Lessons: fillCounts(lessonStatuses, lessonCounts),
		Jobs:    fillCounts(jobStatuses, jobCounts),
		Paused:  s.deps.IsPaused != nil && s.deps.IsPaused(),
	})
}

// fillCounts returns a map carrying every status in the known enum set, seeded
// to zero, then overlaid with the observed counts. Counts for statuses outside
// the known set are preserved so an unexpected value is never silently dropped.
func fillCounts(known []string, observed map[string]int) map[string]int {
	out := make(map[string]int, len(known)+len(observed))
	for _, status := range known {
		out[status] = 0
	}
	for status, n := range observed {
		out[status] = n
	}
	return out
}
