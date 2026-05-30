package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

// handleListLessons serves GET /api/lessons. With ?status it returns every
// lesson in that status (via ListByStatus, ordered by railcontent_id);
// otherwise it returns a page ordered by updated_at DESC then railcontent_id,
// honoring the optional ?limit and ?offset query params. A non-integer ?limit
// or ?offset is a 400.
func (s *Server) handleListLessons(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if status := q.Get("status"); status != "" {
		lessons, err := s.store.ListByStatus(r.Context(), status)
		if err != nil {
			writeStoreErr(w, err, "lessons not found")
			return
		}
		writeJSON(w, http.StatusOK, lessonDTOs(lessons))
		return
	}

	limit, ok := queryInt(w, q.Get("limit"), "limit")
	if !ok {
		return
	}
	offset, ok := queryInt(w, q.Get("offset"), "offset")
	if !ok {
		return
	}
	lessons, err := s.store.ListLessons(r.Context(), limit, offset)
	if err != nil {
		writeStoreErr(w, err, "lessons not found")
		return
	}
	writeJSON(w, http.StatusOK, lessonDTOs(lessons))
}

// handleGetLesson serves GET /api/lessons/{id}: the single lesson keyed by its
// railcontent_id, or 404 if no lesson has that id. A non-integer id is a 400.
func (s *Server) handleGetLesson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	l, err := s.store.GetLesson(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "lesson not found")
		return
	}
	writeJSON(w, http.StatusOK, lessonDTO(l))
}

// skipLessonRequest is the optional POST /api/lessons/{id}/skip body. reason is
// recorded in the lesson's error column; an absent or empty reason skips the
// lesson with no recorded reason.
type skipLessonRequest struct {
	Reason string `json:"reason"`
}

// handleDownloadLesson serves POST /api/lessons/{id}/download: it manually
// enqueues a download job for the lesson keyed by railcontent_id. An unknown id
// is a 404. EnqueueJob dedups atomically: if the lesson already has a
// queued-or-running job that existing job is returned with 200; otherwise a new
// job is enqueued (inheriting the lesson's follow_id) and returned with 202. The
// created bool carries the dedup signal, so no separate active-job lookup is
// needed. A non-integer id is a 400.
func (s *Server) handleDownloadLesson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	lesson, err := s.store.GetLesson(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "lesson not found")
		return
	}

	jobID, created, err := s.store.EnqueueJob(r.Context(), lesson.FollowID, id)
	if err != nil {
		writeStoreErr(w, err, "lesson not found")
		return
	}
	job, err := s.store.GetJob(r.Context(), jobID)
	if err != nil {
		writeStoreErr(w, err, "job not found")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, jobDTO(job))
}

// handleSkipLesson serves POST /api/lessons/{id}/skip: it marks the lesson
// skipped, recording the optional {reason} in the lesson's error column, and
// returns the updated lesson with 200. It reads the lesson first so an unknown
// id maps cleanly to 404 (MarkSkipped's own miss error is not a wrapped
// sql.ErrNoRows). An empty or missing body is allowed and skips with no reason;
// a malformed body is a 400. A non-integer id is a 400.
func (s *Server) handleSkipLesson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}

	var req skipLessonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if _, err := s.store.GetLesson(r.Context(), id); err != nil {
		writeStoreErr(w, err, "lesson not found")
		return
	}
	if err := s.store.MarkSkipped(r.Context(), id, req.Reason); err != nil {
		writeStoreErr(w, err, "lesson not found")
		return
	}
	l, err := s.store.GetLesson(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "lesson not found")
		return
	}
	writeJSON(w, http.StatusOK, lessonDTO(l))
}

// queryInt parses an optional integer query param. An empty value is treated as
// "not supplied" and yields 0 with ok=true (the store applies its own defaults);
// a non-integer value writes a 400 and returns ok=false.
func queryInt(w http.ResponseWriter, raw, name string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid "+name+": "+raw)
		return 0, false
	}
	return v, true
}

// pathInt parses a {name} path value as an int, writing a 400 and returning
// ok=false when it is missing or not an integer. It is the int counterpart to
// pathInt64, used for railcontent_id routes.
func pathInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.PathValue(name)
	id, err := strconv.Atoi(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid "+name+": "+raw)
		return 0, false
	}
	return id, true
}
