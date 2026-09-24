package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/elienop/drumdrop/internal/database"
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
			writeLoadErr(w, err, msgLoadFailed)
			return
		}
		writeJSON(w, http.StatusOK, s.viewLessons(lessons))
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
		writeLoadErr(w, err, msgLoadFailed)
		return
	}
	writeJSON(w, http.StatusOK, s.viewLessons(lessons))
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
		writeLoadErr(w, err, msgNoSuchLesson)
		return
	}
	writeJSON(w, http.StatusOK, s.viewLesson(l))
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
// needed. Any status is queued, a downloaded lesson's too: that is how the
// owner retries a failed re-download, which leaves the lesson downloaded and
// which syncs never queue again (owner ruling 2026-09-24 (h)). A non-integer
// id is a 400.
func (s *Server) handleDownloadLesson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	lesson, err := s.store.GetLesson(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, msgDownloadGone)
		return
	}

	jobID, created, err := s.store.EnqueueJob(r.Context(), lesson.FollowID, id)
	if err != nil {
		writeStoreErr(w, err, msgDownloadGone)
		return
	}
	// A job is queued (or already was): start a sync now, not at the next
	// interval (owner ruling 2026-09-24 (m)).
	s.kick()
	job, err := s.store.GetJob(r.Context(), jobID)
	if err != nil {
		writeStoreErr(w, err, msgDownloadJobGone)
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
// returns the updated lesson with 200. Skip sticks: in the same transaction it
// removes the lesson's queued, running and canceled jobs (SkipLesson), then it
// kills a running download, whose worker then records nothing and removes what
// that download wrote (its earlier, recorded files stay); syncs leave a
// skipped lesson alone. It cancels rather than refusing while a download runs:
// the user asked for the lesson not to be downloaded, and a refusal would only
// send them to Cancel first. While a delete holds the lesson it answers 409
// (msgSkipDeleting): the delete skips it anyway once the files are gone. An
// unknown id is a 404; an empty or missing body skips with no reason; a
// malformed body or a non-integer id is a 400.
func (s *Server) handleSkipLesson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}

	var req skipLessonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, msgBadBody)
		return
	}

	running, err := s.store.SkipLesson(r.Context(), id, req.Reason)
	switch {
	case errors.Is(err, database.ErrLessonDeleting):
		writeErr(w, http.StatusConflict, msgSkipDeleting)
		return
	case err != nil:
		writeStoreErr(w, err, msgSkipGone)
		return
	}
	s.killRunning(running)
	l, err := s.store.GetLesson(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, msgSkipGone)
		return
	}
	writeJSON(w, http.StatusOK, s.viewLesson(l))
}

// handleUnskipLesson serves POST /api/lessons/{id}/unskip: it resets a skipped
// lesson back to pending (clearing its error) and returns the updated lesson
// with 200. It reads the lesson first so an unknown id maps cleanly to 404
// (UnskipLesson itself tolerates a non-skipped lesson as a benign no-op rather
// than erroring). A non-integer id is a 400. It mirrors handleSkipLesson.
//
// Un-skip queues no job itself: a sync's planning does, for a pending lesson
// its follow lists. So when it reset a skipped lesson, it starts a sync now
// rather than at the next interval (owner ruling 2026-09-24 (m)).
func (s *Server) handleUnskipLesson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}

	before, err := s.store.GetLesson(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, msgUnskipGone)
		return
	}
	if err := s.store.UnskipLesson(r.Context(), id); err != nil {
		writeStoreErr(w, err, msgUnskipGone)
		return
	}
	if before.Status == database.StatusSkipped {
		s.kick()
	}
	l, err := s.store.GetLesson(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, msgUnskipGone)
		return
	}
	writeJSON(w, http.StatusOK, s.viewLesson(l))
}

// handleDeleteLesson serves DELETE /api/lessons/{id}: it removes a downloaded
// lesson's files (both the downloads copy and the library mirror, via the shared
// primitive) and tombstone-skips the row (status='skipped', error='deleted',
// paths cleared), returning the updated lesson with 200. It is a tombstone, not
// a row delete, because the follow is still active and the next sync would
// otherwise re-discover and re-download it — ShouldSkipEnqueue already skips
// 'skipped', and un-skip can bring it back later.
//
// It first marks the lesson deleting and removes its queued, running and
// canceled jobs (BeginLessonDelete), then kills a running download: no step of
// a download already under way can record anything once the delete answers,
// and no new one can start until it ends; a download that was under way
// removes what it wrote, whatever the lesson's record says. From there the
// delete runs to the end even if the client goes away, renews its hold on the
// lesson while it runs, and always ends it (holdDelete). File removal
// uses the RAW stored paths (container paths), NOT the host-mapped DTO
// values, and follows the lesson's record (see removeLessonFiles). An unknown
// id is a 404, a non-integer id a 400, a lesson already being deleted a 409.
// When a file could not be removed, the lesson records only what is left, the
// detail goes to the server log, and the client gets a fixed 500.
func (s *Server) handleDeleteLesson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}

	l, running, err := s.store.BeginLessonDelete(r.Context(), id)
	if errors.Is(err, database.ErrLessonDeleting) {
		writeErr(w, http.StatusConflict, msgLessonDeleting)
		return
	}
	if err != nil {
		writeStoreErr(w, err, msgLessonGone)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	hold := s.holdDelete(ctx, id)
	defer s.endDelete(ctx, hold)
	s.killRunning(running)

	c, err := s.claims(ctx)
	if err == nil {
		err = s.deleteLessonFiles(ctx, c, hold, l)
	}
	switch {
	case errors.Is(err, errNoClaims):
		writeErr(w, http.StatusInternalServerError, msgLessonNoClaims)
		return
	case errors.Is(err, errFilesKept):
		writeErr(w, http.StatusInternalServerError, msgLessonFilesKept)
		return
	case errors.Is(err, errRecordNotUpdated):
		writeErr(w, http.StatusInternalServerError, msgLessonNotSaved)
		return
	case errors.Is(err, database.ErrLessonChanged):
		writeErr(w, http.StatusConflict, msgLessonChanged)
		return
	case err != nil:
		writeStoreErr(w, err, msgLessonGone)
		return
	}
	updated, err := s.store.GetLesson(ctx, id)
	if err != nil {
		writeStoreErr(w, err, msgLessonGone)
		return
	}
	writeJSON(w, http.StatusOK, s.viewLesson(updated))
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
		writeErr(w, http.StatusBadRequest, msgBadQueryNumber(name))
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
		writeErr(w, http.StatusBadRequest, msgBadPathNumber(name))
		return 0, false
	}
	return id, true
}
