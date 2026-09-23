package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/elienop/drumdrop/internal/engine"
	"github.com/elienop/drumdrop/internal/musora"
)

// previewResponse is the GET /api/preview shape: what a follow would resolve to
// before it is created. kind is "node" or "instructor"; root_id is the content
// id a node follow would expand from and is omitted entirely for an instructor
// follow (which has no single root); title is a human label; lesson_count is how
// many downloadable lessons the follow would track; slug is the instructor slug
// an add would store (musora.NormalizeInstructor of what was typed), omitted
// for a node.
type previewResponse struct {
	RootID      *int   `json:"root_id,omitempty"`
	Title       string `json:"title"`
	LessonCount int    `json:"lesson_count"`
	Kind        string `json:"kind"`
	Slug        string `json:"slug,omitempty"`
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
// ?slug= previews an instructor follow (display name, lesson count and the
// normalised slug) from a name, slug or coach-page link. A missing or
// unparseable id, or neither param, is a 400; instructor input that can't be
// normalised, a brand Musora doesn't have, a link whose brand differs from
// ?brand, and an unknown instructor, are 400s too. Resolution
// failures against Musora surface as 502 (detail logged). No answer echoes the
// input back.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case q.Get("slug") != "":
		s.previewInstructor(w, q.Get("slug"), q.Get("brand"))
	case q.Get("id") != "":
		// Parse with engine.ExtractID (accepts a numeric id OR a Drumeo URL) so
		// preview takes the same input as create (POST /api/follows, which also
		// uses ExtractID) — a URL must not 400 on preview when it works on add.
		id := engine.ExtractID(q.Get("id"))
		if id == 0 {
			writeErr(w, http.StatusBadRequest, msgNoContentID)
			return
		}
		s.previewNode(w, id, q.Get("whole") == "true")
	default:
		writeErr(w, http.StatusBadRequest, msgPreviewNothing)
	}
}

// previewNode resolves the node hierarchy to a lesson count and a best-effort
// title (the root's own title, falling back to its parent course name).
func (s *Server) previewNode(w http.ResponseWriter, id int, whole bool) {
	rootID, lessonIDs, err := musora.ResolveLessonIDs(id, whole, engine.PermissionIDs())
	if err != nil {
		fmt.Fprintf(logOut, "drumdrop: preview %d: %v\n", id, err)
		writeErr(w, http.StatusBadGateway, msgPreviewUnreachable)
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

// previewInstructor normalises what was typed for the instructor exactly as an
// add does (musora.NormalizeInstructor), then resolves their display name and
// counts the lessons that reference them in the settled brand. Input or a
// brand that can't be used, and an unknown instructor, are 400s; Musora not
// answering is a 502.
func (s *Server) previewInstructor(w http.ResponseWriter, input, brand string) {
	slug, brand, err := musora.NormalizeInstructor(input, brand)
	if err != nil {
		writeLookupErr(w, "preview instructor", err, msgPreviewUnreachable)
		return
	}
	_, name, ok, err := musora.ResolveInstructorID(slug)
	if err != nil {
		writeLookupErr(w, "preview instructor", err, msgPreviewUnreachable)
		return
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, msgNoInstructor)
		return
	}
	lessons, err := musora.InstructorLessons(slug, brand, engine.PermissionIDs())
	if err != nil {
		writeLookupErr(w, "preview instructor lessons", err, msgPreviewUnreachable)
		return
	}
	writeJSON(w, http.StatusOK, previewResponse{
		Title:       name,
		LessonCount: len(lessons),
		Kind:        "instructor",
		Slug:        slug,
	})
}

// writeLookupErr answers an error from a Musora instructor lookup. Input, a
// brand, or a link and brand that disagree, refused by musora before any
// network call (musora.ErrBadSlug, ErrBadBrand, ErrBrandMismatch), is the
// client's to fix: a 400 saying what is accepted. Anything else is Musora
// failing: a 502 with unreachable, the detail logged under what.
func writeLookupErr(w http.ResponseWriter, what string, err error, unreachable string) {
	switch {
	case errors.Is(err, musora.ErrBadSlug):
		writeErr(w, http.StatusBadRequest, msgBadSlug)
	case errors.Is(err, musora.ErrBadBrand):
		writeErr(w, http.StatusBadRequest, msgBadBrand)
	case errors.Is(err, musora.ErrBrandMismatch):
		writeErr(w, http.StatusBadRequest, msgBrandMismatch)
	default:
		fmt.Fprintf(logOut, "drumdrop: %s: %v\n", what, err)
		writeErr(w, http.StatusBadGateway, unreachable)
	}
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
// persisting the cookie and creds on success. A bad body or a missing
// email/password is a 400; credentials Musora refused are a 422; Musora not
// answering, or answering something unreadable, is a 502; a session that
// couldn't be saved is a 500. Never a 401: the web client takes any 401 for its
// own API token being refused (the auth middleware) and clears it, which would
// log the user out of DrumDrop over a Musora password.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, msgBadBody)
		return
	}
	if req.Email == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, msgLoginMissing)
		return
	}
	_, err := musora.Login(req.Email, req.Password)
	switch {
	case errors.Is(err, musora.ErrLoginRejected):
		writeErr(w, http.StatusUnprocessableEntity, msgLoginRejected)
		return
	case errors.Is(err, musora.ErrSessionNotSaved):
		fmt.Fprintf(logOut, "drumdrop: login: %v\n", err)
		writeErr(w, http.StatusInternalServerError, msgLoginNotSaved)
		return
	case err != nil:
		fmt.Fprintf(logOut, "drumdrop: login: %v\n", err)
		writeErr(w, http.StatusBadGateway, msgLoginUnreachable)
		return
	}
	// Best-effort persist of credentials so the daemon can refresh later; a
	// save failure does not invalidate the (already saved) cookie/login.
	if err := musora.SaveCreds(req.Email, req.Password); err != nil {
		fmt.Fprintf(logOut, "drumdrop: login: save the credentials: %v\n", err)
	}
	writeJSON(w, http.StatusOK, sessionResponse{Connected: true})
}
