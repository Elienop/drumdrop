package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/elienop/drumdrop/internal/engine"
	"github.com/elienop/drumdrop/internal/musora"
)

// previewResponse is the GET /api/preview shape: what a follow would resolve to
// before it is created. kind is "node" or "instructor"; root_id is the content
// id a node follow would expand from and is omitted entirely for an instructor
// follow (which has no single root); title is a human label; lesson_count is how
// many downloadable lessons the follow would track.
type previewResponse struct {
	RootID      *int   `json:"root_id,omitempty"`
	Title       string `json:"title"`
	LessonCount int    `json:"lesson_count"`
	Kind        string `json:"kind"`
}

// sessionResponse is the GET /api/session shape: whether the saved cookie is
// still accepted by Musora.
type sessionResponse struct {
	Connected bool `json:"connected"`
}

// loginRequest is the POST /api/session body.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handlePreview serves GET /api/preview: a read-only dry resolve of what a
// follow would track, so the UI can show a count/title before the user commits.
// ?id=N(&whole=bool) previews a node follow (root id, lesson count, title);
// ?slug= previews an instructor follow (display name + lesson count). A missing
// or unparseable id, or neither param, is a 400; an unknown instructor is a 400.
// Resolution failures against Musora surface as 502.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case q.Get("slug") != "":
		s.previewInstructor(w, q.Get("slug"), q.Get("brand"))
	case q.Get("id") != "":
		id, err := strconv.Atoi(q.Get("id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid id: "+q.Get("id"))
			return
		}
		s.previewNode(w, id, q.Get("whole") == "true")
	default:
		writeErr(w, http.StatusBadRequest, "id or slug is required")
	}
}

// previewNode resolves the node hierarchy to a lesson count and a best-effort
// title (the root's own title, falling back to its parent course name).
func (s *Server) previewNode(w http.ResponseWriter, id int, whole bool) {
	rootID, lessonIDs, err := musora.ResolveLessonIDs(id, whole, engine.PermissionIDs())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not resolve content")
		return
	}

	title := ""
	if lesson, err := musora.ResolveLesson(rootID, engine.PermissionIDs()); err == nil && lesson != nil {
		title = lesson.Title
		if title == "" && len(lesson.ParentContentData) > 0 {
			title = lesson.ParentContentData[0].Title
		}
	}

	writeJSON(w, http.StatusOK, previewResponse{
		RootID:      &rootID,
		Title:       title,
		LessonCount: len(lessonIDs),
		Kind:        "node",
	})
}

// previewInstructor resolves the instructor's display name and counts the
// lessons that reference them in the given brand (defaulting to drumeo). An
// unknown slug is a 400.
func (s *Server) previewInstructor(w http.ResponseWriter, slug, brand string) {
	if brand == "" {
		brand = "drumeo"
	}
	_, name, ok, err := musora.ResolveInstructorID(slug)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not resolve instructor")
		return
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, "no instructor found for that slug")
		return
	}
	lessons, err := musora.InstructorLessons(slug, brand, engine.PermissionIDs())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not resolve instructor lessons")
		return
	}
	writeJSON(w, http.StatusOK, previewResponse{
		Title:       name,
		LessonCount: len(lessons),
		Kind:        "instructor",
	})
}

// handleGetSession serves GET /api/session: reports whether the saved cookie is
// still accepted by Musora (a network failure is reported as disconnected — the
// endpoint is cosmetic and must not 500 the dashboard).
func (s *Server) handleGetSession(w http.ResponseWriter, _ *http.Request) {
	connected, err := musora.Me(musora.LoadCookie())
	if err != nil {
		connected = false
	}
	writeJSON(w, http.StatusOK, sessionResponse{Connected: connected})
}

// handleLogin serves POST /api/session: logs in with the supplied credentials,
// persisting the cookie and creds on success. A missing email/password is a
// 400; a rejected login is a 401.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "email and password are required")
		return
	}
	if _, err := musora.Login(req.Email, req.Password); err != nil {
		writeErr(w, http.StatusUnauthorized, "login failed")
		return
	}
	// Best-effort persist of credentials so the daemon can refresh later; a
	// save failure does not invalidate the (already saved) cookie/login.
	_ = musora.SaveCreds(req.Email, req.Password)
	writeJSON(w, http.StatusOK, sessionResponse{Connected: true})
}
