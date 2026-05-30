package server

import (
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
			writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, mapStoreErr(err), "lesson not found")
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
