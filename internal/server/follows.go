package server

import (
	"net/http"
	"strconv"

	"github.com/elienop/drumdrop/internal/database"
)

// handleListFollows serves GET /api/follows: every follow, ordered as the store
// returns them (added_at then id), mapped to wire shapes. The documented
// ?active query param is accepted but is currently a no-op filter — follows have
// no inactive state yet — so it is ignored.
func (s *Server) handleListFollows(w http.ResponseWriter, r *http.Request) {
	follows, err := s.store.ListFollows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, mapStoreErr(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, followDTO(f))
}

// handleFollowLessons serves GET /api/follows/{id}/lessons: the lessons linked
// to the follow, ordered by railcontent_id. An optional ?status filter keeps
// only lessons in that status (filtered in the handler so the store method stays
// status-agnostic). A non-integer id is a 400. An unknown follow id simply
// yields an empty list, matching the store's follow-id scan.
func (s *Server) handleFollowLessons(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(w, r, "id")
	if !ok {
		return
	}
	lessons, err := s.store.ListLessonsByFollow(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status := r.URL.Query().Get("status"); status != "" {
		lessons = filterLessonsByStatus(lessons, status)
	}
	writeJSON(w, http.StatusOK, lessonDTOs(lessons))
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
