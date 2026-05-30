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
			writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, mapStoreErr(err), "job not found")
		return
	}
	writeJSON(w, http.StatusOK, jobDTO(j))
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jobCounts, err := s.store.CountJobsByState(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	follows, err := s.store.ListFollows(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, SummaryDTO{
		Follows: len(follows),
		Lessons: fillCounts(lessonStatuses, lessonCounts),
		Jobs:    fillCounts(jobStatuses, jobCounts),
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
