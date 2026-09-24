package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/engine"
	"github.com/elienop/drumdrop/internal/musora"
)

// createFollowRequest is the POST /api/follows body. kind selects the follow
// type; node follows take an id or url (parsed by engine.ExtractID), instructor
// follows take a name, slug or coach-page link in slug (normalised by
// musora.NormalizeInstructor). brand and quality are optional and default to
// the CLI's defaults (drumeo / best); an instructor link's brand fills an empty
// brand.
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
		writeLoadErr(w, err, msgLoadFailed)
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
		writeLoadErr(w, err, msgNoSuchFollow)
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
		writeLoadErr(w, err, msgNoSuchFollow)
		return
	}
	lessons, err := s.store.ListLessonsByFollow(r.Context(), id)
	if err != nil {
		writeLoadErr(w, err, msgLoadFailed)
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
		writeErr(w, http.StatusBadRequest, msgBadBody)
		return
	}

	quality := req.Quality
	if quality == "" {
		quality = "best"
	}
	if !validQuality(quality) {
		writeErr(w, http.StatusBadRequest, msgBadQuality)
		return
	}

	// The brand is stored with the follow and its lessons, and an instructor
	// follow's every sync queries by it: one Musora doesn't have is refused
	// here, not left to fail each sync. An instructor follow settles its brand
	// with its input (a pasted link names one), so it's checked there.
	switch req.Kind {
	case "node":
		brand, err := musora.NodeBrand(req.Brand)
		if err != nil {
			writeLookupErr(w, "add follow", err, msgAddUnreachable)
			return
		}
		s.createNodeFollow(w, r, req, brand, quality)
	case "instructor":
		s.createInstructorFollow(w, r, req, quality)
	default:
		writeErr(w, http.StatusBadRequest, msgBadKind)
	}
}

// createNodeFollow handles a node follow: parse the id from id|url, resolve a
// best-effort title, then AddNodeFollow. A coach-page link is refused: its
// number is the instructor's, not a lesson's or course's.
func (s *Server) createNodeFollow(w http.ResponseWriter, r *http.Request, req createFollowRequest, brand, quality string) {
	target := req.ID
	if target == "" {
		target = req.URL
	}
	if musora.IsCoachLink(target) {
		writeErr(w, http.StatusBadRequest, msgCoachLinkAsNode)
		return
	}
	id := engine.ExtractID(target)
	if id == 0 {
		writeErr(w, http.StatusBadRequest, msgNoContentID)
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

// createInstructorFollow handles an instructor follow: normalise what was typed
// and settle the brand exactly as the preview does (musora.NormalizeInstructor),
// resolve the instructor's display name, then AddInstructorFollow with the
// normalised slug. Musora not answering is a 502; input or a brand that can't
// be used, or an instructor Musora doesn't have, is a 400.
func (s *Server) createInstructorFollow(w http.ResponseWriter, r *http.Request, req createFollowRequest, quality string) {
	if req.Slug == "" {
		writeErr(w, http.StatusBadRequest, msgSlugRequired)
		return
	}
	slug, brand, err := musora.NormalizeInstructor(req.Slug, req.Brand)
	if err != nil {
		writeLookupErr(w, "add follow", err, msgAddUnreachable)
		return
	}
	id, name, ok, err := musora.ResolveInstructorID(slug, brand)
	if err != nil {
		writeLookupErr(w, "add follow: look up instructor", err, msgAddUnreachable)
		return
	}
	if !ok || id == "" {
		writeErr(w, http.StatusBadRequest, msgNoInstructor)
		return
	}

	f, err := s.store.AddInstructorFollow(r.Context(), slug, name, brand, quality)
	s.writeFollowResult(w, f, err)
}

// writeFollowResult finishes a create: ErrAlreadyFollowing → 200 with the
// existing row (idempotent), a nil error → 201 with the new row, any other
// error → 500, its detail logged.
func (s *Server) writeFollowResult(w http.ResponseWriter, f database.Follow, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, followDTO(f))
	case errors.Is(err, database.ErrAlreadyFollowing):
		writeJSON(w, http.StatusOK, followDTO(f))
	default:
		fmt.Fprintf(logOut, "drumdrop: add follow: %v\n", err)
		writeErr(w, http.StatusInternalServerError, msgFollowNotAdded)
	}
}

// handleUpdateFollow serves PATCH /api/follows/{id}: it changes the follow's
// quality preset in place and returns the updated row with 200. Only quality is
// editable — the follow's identity (kind/railcontent_id/slug/brand) is immutable
// — and the change is forward-only (existing lessons are untouched; the new
// quality governs lessons enqueued from now on). The quality is validated
// against the allowed preset set (400 on anything else, reusing validQuality so
// it matches create). An unknown id is a 404 (UpdateFollowQuality answers a
// wrapped sql.ErrNoRows). A malformed body is a 400; a non-integer id is a 400.
func (s *Server) handleUpdateFollow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}

	var req updateFollowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, msgBadBody)
		return
	}
	if !validQuality(req.Quality) {
		writeErr(w, http.StatusBadRequest, msgBadQuality)
		return
	}

	if err := s.store.UpdateFollowQuality(r.Context(), id, req.Quality); err != nil {
		writeStoreErr(w, err, msgEditGone)
		return
	}
	f, err := s.store.GetFollow(r.Context(), id)
	if err != nil {
		writeStoreErr(w, err, msgEditGone)
		return
	}
	writeJSON(w, http.StatusOK, followDTO(f))
}

// handleDeleteFollow serves DELETE /api/follows/{id}: 204 on success, 404 if no
// follow has that id (also when another request removed it meanwhile), 400 for
// a non-integer id.
//
// Without ?files=true (the default: delete is opt-in) the files stay: one
// transaction removes the jobs of the follow's lessons, the lessons and the
// follow (RemoveFollowCascade), then the running downloads are killed; what
// they wrote is kept, as asked. Jobs the follow queued for another follow's
// lessons are left alone.
//
// With ?files=true:
//  1. Mark every lesson of the follow deleting and remove their queued,
//     running and canceled jobs (BeginFollowDelete), then kill the running
//     downloads (deps.CancelRunning, nil-safe when no worker is attached).
//     From here on no download that was under way can record anything, and no
//     new one can start for those lessons; what a stopped download wrote is
//     removed by the worker. The delete runs to the end even if the client
//     goes away, renews its hold on the lessons while it runs, and always
//     ends it (holdDelete).
//  2. Remove the files of every lesson that records any (whatever its
//     status), following each lesson's record exactly as the lesson delete
//     does, and tombstone each lesson whose files are all gone. If a lesson's
//     files could not all be removed, that lesson records only what is left,
//     the follow and every lesson row are kept, the detail is logged, and the
//     client gets a fixed 500: deleting the rows would leave those files
//     tracked by nothing.
//  3. Cascade-delete the follow's jobs + lessons + the follow row (one tx). It
//     refuses (409) if a lesson records files again by then (one the planner
//     found after step 1 and downloaded meanwhile), and answers 404 if another
//     request removed the follow meanwhile.
func (s *Server) handleDeleteFollow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.store.GetFollow(r.Context(), id); err != nil {
		writeStoreErr(w, err, msgFollowGone)
		return
	}

	if !deleteFilesRequested(r) {
		s.removeFollowKeepingFiles(w, r, id)
		return
	}

	// 1. Stop every download of this follow's lessons, for good.
	lessons, running, err := s.store.BeginFollowDelete(r.Context(), id)
	if errors.Is(err, database.ErrLessonDeleting) {
		writeErr(w, http.StatusConflict, msgFollowDeleting)
		return
	}
	if err != nil {
		writeStoreErr(w, err, msgFollowGone)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	ids := make([]int, 0, len(lessons))
	for _, l := range lessons {
		ids = append(ids, l.RailcontentID)
	}
	hold := s.holdDelete(ctx, ids...)
	defer s.endDelete(ctx, hold)
	s.killRunning(running)

	// 2. Remove every lesson's files, then its paths.
	if !s.deleteFollowFiles(ctx, w, hold, lessons) {
		return
	}
	// 3. Cascade, unless a lesson records files again by now.
	running, err = s.store.RemoveFilelessFollowCascade(ctx, id)
	switch {
	case errors.Is(err, database.ErrFollowHasFiles):
		writeErr(w, http.StatusConflict, msgFollowNewFiles)
		return
	case isNotFound(err):
		writeErr(w, http.StatusNotFound, msgFollowGoneLate)
		return
	case err != nil:
		fmt.Fprintf(logOut, "drumdrop: remove follow %d: %v\n", id, err)
		writeErr(w, http.StatusInternalServerError, msgFollowNotSaved)
		return
	}
	s.killRunning(running)
	w.WriteHeader(http.StatusNoContent)
}

// removeFollowKeepingFiles removes follow id and its lessons' rows, keeping
// every file, and kills the downloads it stopped.
func (s *Server) removeFollowKeepingFiles(w http.ResponseWriter, r *http.Request, id int64) {
	running, err := s.store.RemoveFollowCascade(r.Context(), id)
	switch {
	case errors.Is(err, database.ErrLessonDeleting):
		writeErr(w, http.StatusConflict, msgFollowDeleting)
		return
	case isNotFound(err):
		writeErr(w, http.StatusNotFound, msgFollowGone)
		return
	case err != nil:
		fmt.Fprintf(logOut, "drumdrop: remove follow %d: %v\n", id, err)
		writeErr(w, http.StatusInternalServerError, msgFollowNotRemoved)
		return
	}
	s.killRunning(running)
	w.WriteHeader(http.StatusNoContent)
}

// deleteFollowFiles removes the files of every lesson that has any
// (Lesson.HasFiles), and tombstones each one whose files are all gone. The
// lessons are as BeginFollowDelete read them, and nothing can change them
// meanwhile: no download can record for a lesson being deleted. It writes the
// error response and returns false when any lesson's files could not all be
// removed (that lesson records what is left) or a store step failed.
func (s *Server) deleteFollowFiles(ctx context.Context, w http.ResponseWriter, hold *deleteHold, lessons []database.Lesson) bool {
	c, err := s.claims(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, msgFollowNoClaims)
		return false
	}
	kept, changed := 0, 0
	for _, l := range lessons {
		if !l.HasFiles() {
			continue // no files recorded: the cascade removes the row
		}
		switch err := s.deleteLessonFiles(ctx, c, hold, l); {
		case errors.Is(err, errFilesKept):
			kept++
		case errors.Is(err, database.ErrLessonChanged):
			changed++
		case errors.Is(err, errRecordNotUpdated):
			writeErr(w, http.StatusInternalServerError, msgFollowNotSaved)
			return false
		case err != nil:
			// The lesson's row is gone: another request removed the follow.
			writeStoreErr(w, err, msgFollowGoneLate)
			return false
		default:
			// Its files are gone: it claims nothing any more, so the next
			// lesson's ownership checks must not count it.
			c.Forget(l.RailcontentID)
		}
	}
	switch {
	case kept > 0:
		writeErr(w, http.StatusInternalServerError, msgFollowFilesKept)
		return false
	case changed > 0:
		writeErr(w, http.StatusConflict, msgFollowLessonChanged)
		return false
	}
	return true
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
		writeErr(w, http.StatusBadRequest, msgBadPathNumber(name))
		return 0, false
	}
	return id, true
}
