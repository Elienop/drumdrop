package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/engine"
	"github.com/elienop/drumdrop/internal/musora"
)

// createFollowRequest is the POST /api/follows body. kind selects the follow
// type; node follows take an id or url (parsed by engine.ExtractID), instructor
// follows take a slug. brand and quality are optional and default to the CLI's
// defaults (drumeo / best).
type createFollowRequest struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	URL     string `json:"url"`
	Slug    string `json:"slug"`
	Brand   string `json:"brand"`
	Quality string `json:"quality"`
}

// handleListFollows serves GET /api/follows: every follow, ordered as the store
// returns them (added_at then id), mapped to wire shapes. The documented
// ?active query param is accepted but is currently a no-op filter — follows have
// no inactive state yet — so it is ignored.
func (s *Server) handleListFollows(w http.ResponseWriter, r *http.Request) {
	follows, err := s.store.ListFollows(r.Context())
	if err != nil {
		writeStoreErr(w, err, "follows not found")
		return
	}
	writeJSON(w, http.StatusOK, followDTOs(follows))
}

// handleGetFollow serves GET /api/follows/{id}: the single follow, or 404 if no
// follow has that id. A non-integer id is a 400.
func (s *Server) handleGetFollow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	f, err := s.store.GetFollow(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "follow not found")
		return
	}
	writeJSON(w, http.StatusOK, followDTO(f))
}

// handleFollowLessons serves GET /api/follows/{id}/lessons: the lessons linked
// to the follow, ordered by railcontent_id. An optional ?status filter keeps
// only lessons in that status (filtered in the handler so the store method stays
// status-agnostic). A non-integer id is a 400. It reads the follow first so an
// unknown id maps to 404 (mirroring handleDeleteFollow/handleSkipLesson) rather
// than a misleading 200 with an empty list.
func (s *Server) handleFollowLessons(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.store.GetFollow(r.Context(), id); err != nil {
		writeStoreErr(w, err, "follow not found")
		return
	}
	lessons, err := s.store.ListLessonsByFollow(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "lessons not found")
		return
	}
	if status := r.URL.Query().Get("status"); status != "" {
		lessons = filterLessonsByStatus(lessons, status)
	}
	writeJSON(w, http.StatusOK, s.viewLessons(lessons))
}

// handleCreateFollow serves POST /api/follows. It mirrors the CLI follow path:
// a node follow resolves its title best-effort via musora.ResolveLesson (a
// failure is non-fatal — the follow is still created with an empty title); an
// instructor follow resolves its display name via musora.ResolveInstructorID
// and 400s when the slug matches no instructor. An already-followed target is
// idempotent: it returns 200 with the existing row rather than 409. A bad kind,
// an id with no digits, or a missing slug is a 400.
// allowedQualities is the set the Add-follow UI offers. The create handler
// rejects anything else (400) so a malformed value can't be persisted and then
// silently degrade to an uncapped download — FormatSelector falls back to best
// on a non-numeric quality, so without this guard "1080p" would download full-res.
var allowedQualities = map[string]struct{}{
	"best": {}, "2160": {}, "1440": {}, "1080": {}, "720": {}, "480": {},
}

func validQuality(q string) bool {
	_, ok := allowedQualities[q]
	return ok
}

func (s *Server) handleCreateFollow(w http.ResponseWriter, r *http.Request) {
	var req createFollowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	brand := req.Brand
	if brand == "" {
		brand = "drumeo"
	}
	quality := req.Quality
	if quality == "" {
		quality = "best"
	}
	if !validQuality(quality) {
		writeErr(w, http.StatusBadRequest, "quality must be one of: best, 2160, 1440, 1080, 720, 480")
		return
	}

	switch req.Kind {
	case "node":
		s.createNodeFollow(w, r, req, brand, quality)
	case "instructor":
		s.createInstructorFollow(w, r, req, brand, quality)
	default:
		writeErr(w, http.StatusBadRequest, "kind must be node or instructor")
	}
}

// createNodeFollow handles a node follow: parse the id from id|url, resolve a
// best-effort title, then AddNodeFollow.
func (s *Server) createNodeFollow(w http.ResponseWriter, r *http.Request, req createFollowRequest, brand, quality string) {
	target := req.ID
	if target == "" {
		target = req.URL
	}
	id := engine.ExtractID(target)
	if id == 0 {
		writeErr(w, http.StatusBadRequest, "could not parse a content id from id or url")
		return
	}

	// Best-effort title: the lesson/container's own title, falling back to its
	// parent course name. A resolve failure is non-fatal — an empty title is fine.
	title := ""
	if lesson, err := musora.ResolveLesson(id, engine.PermissionIDs()); err == nil && lesson != nil {
		title = lesson.Title
		if title == "" && len(lesson.ParentContentData) > 0 {
			title = lesson.ParentContentData[0].Title
		}
	}

	f, err := s.store.AddNodeFollow(r.Context(), id, title, brand, quality)
	s.writeFollowResult(w, f, err)
}

// createInstructorFollow handles an instructor follow: validate the slug,
// resolve the instructor's display name, then AddInstructorFollow.
func (s *Server) createInstructorFollow(w http.ResponseWriter, r *http.Request, req createFollowRequest, brand, quality string) {
	if req.Slug == "" {
		writeErr(w, http.StatusBadRequest, "slug is required for an instructor follow")
		return
	}
	id, name, ok, err := musora.ResolveInstructorID(req.Slug)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not resolve instructor")
		return
	}
	if !ok || id == "" {
		writeErr(w, http.StatusBadRequest, "no instructor found for that slug")
		return
	}

	f, err := s.store.AddInstructorFollow(r.Context(), req.Slug, name, brand, quality)
	s.writeFollowResult(w, f, err)
}

// writeFollowResult finishes a create: ErrAlreadyFollowing → 200 with the
// existing row (idempotent), a nil error → 201 with the new row, any other
// error → 500.
func (s *Server) writeFollowResult(w http.ResponseWriter, f database.Follow, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, followDTO(f))
	case errors.Is(err, database.ErrAlreadyFollowing):
		writeJSON(w, http.StatusOK, followDTO(f))
	default:
		writeErr(w, http.StatusInternalServerError, "could not create follow")
	}
}

// handleUpdateFollow serves PATCH /api/follows/{id}: it changes the follow's
// quality preset in place and returns the updated row with 200. Only quality is
// editable — the follow's identity (kind/railcontent_id/slug/brand) is immutable
// — and the change is forward-only (existing lessons are untouched; the new
// quality governs lessons enqueued from now on). The quality is validated
// against the allowed preset set (400 on anything else, reusing validQuality so
// it matches create). It reads the follow first so an unknown id maps cleanly to
// 404. A malformed body is a 400; a non-integer id is a 400.
func (s *Server) handleUpdateFollow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}

	var req updateFollowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validQuality(req.Quality) {
		writeErr(w, http.StatusBadRequest, "quality must be one of: best, 2160, 1440, 1080, 720, 480")
		return
	}

	if _, err := s.store.GetFollow(r.Context(), id); err != nil {
		writeStoreErr(w, err, "follow not found")
		return
	}
	if err := s.store.UpdateFollowQuality(r.Context(), id, req.Quality); err != nil {
		writeStoreErr(w, err, "follow not found")
		return
	}
	f, err := s.store.GetFollow(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, "follow not found")
		return
	}
	writeJSON(w, http.StatusOK, followDTO(f))
}

// handleDeleteFollow serves DELETE /api/follows/{id}: 204 on success, 404 if no
// follow has that id, 400 for a non-integer id. It reads the follow first so an
// unknown id maps cleanly to 404 (RemoveFollowCascade's own miss error is not a
// wrapped sql.ErrNoRows).
//
// Order of operations:
//  1. Cancel the follow's running job(s) so a live download actually stops
//     (mirrors handleCancelJob: deps.CancelRunning kills the yt-dlp process; the
//     worker finalizes the job/lesson asynchronously). nil-safe when no worker.
//  2. With ?files=true, remove the follow's downloaded lessons' files (the
//     downloads copy AND the library mirror) BEFORE the rows are deleted, since
//     the lesson rows carry the on-disk paths. Default (?files absent/false)
//     keeps the files — delete is opt-in.
//  3. Cascade-delete the follow's jobs + lessons + the follow row (one tx).
//
// The cancel→files→cascade ordering is best-effort: the worker's async
// canceled/skipped finalization may still be in flight, so a file delete can
// race a worker still writing the lesson dir, and the cascade's job delete makes
// the worker's later MarkJobCanceled a benign 0-row no-op. The handler does not
// block on the worker (mirroring handleCancelJob).
func (s *Server) handleDeleteFollow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.store.GetFollow(r.Context(), id); err != nil {
		writeStoreErr(w, err, "follow not found")
		return
	}

	// 1. Cancel any running job(s) for this follow so a live download stops. No
	// by-follow jobs query exists, so list running jobs and filter by follow_id.
	if s.deps.CancelRunning != nil {
		running, err := s.store.ListJobsByStatus(r.Context(), database.JobRunning)
		if err != nil {
			writeStoreErr(w, err, "follow not found")
			return
		}
		for _, j := range running {
			if j.FollowID.Valid && j.FollowID.Int64 == id {
				s.deps.CancelRunning(j.ID)
			}
		}
	}

	// 2. With ?files=true, remove the downloaded lessons' files before the rows go.
	if deleteFilesRequested(r) {
		lessons, err := s.store.ListLessonsByFollow(r.Context(), id)
		if err != nil {
			writeStoreErr(w, err, "follow not found")
			return
		}
		for _, l := range lessons {
			if l.Status == database.StatusDownloaded && l.OutputDir.Valid {
				// Best-effort: use the RAW stored container path (not the host-mapped
				// DTO). A failure is logged-by-being-ignored — file cleanup must not
				// block removing the follow records.
				_ = removeLessonFiles(s.cfg.DownloadsDir, s.cfg.LibraryDir, l.OutputDir.String)
			}
		}
	}

	// 3. Cascade-delete jobs + lessons + the follow.
	if err := s.store.RemoveFollowCascade(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete follow")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteFilesRequested reports whether the request asked to also delete on-disk
// files via ?files=. A missing or unparseable value is false (files kept), so
// delete is strictly opt-in.
func deleteFilesRequested(r *http.Request) bool {
	v, err := strconv.ParseBool(r.URL.Query().Get("files"))
	return err == nil && v
}

// filterLessonsByStatus returns the lessons whose Status equals status,
// preserving order.
func filterLessonsByStatus(lessons []database.Lesson, status string) []database.Lesson {
	out := lessons[:0:0]
	for _, l := range lessons {
		if l.Status == status {
			out = append(out, l)
		}
	}
	return out
}

// pathInt64 parses a {name} path value as an int64, writing a 400 and returning
// ok=false when it is missing or not an integer.
func pathInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid "+name+": "+raw)
		return 0, false
	}
	return id, true
}
