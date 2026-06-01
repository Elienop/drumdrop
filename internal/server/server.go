package server

import (
	"io/fs"
	"net/http"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/scheduler"
	webui "github.com/elienop/drumdrop/web"
)

// Config carries the server's networking settings, resolved once at startup
// from the config env helpers (DRUMDROP_LISTEN/_API_TOKEN/_CORS_ORIGIN). The
// zero value is valid: no token, no CORS, and the listen guard left to the
// caller. Auth and CORS tasks consume these fields.
type Config struct {
	ListenAddr string
	APIToken   string
	CORSOrigin string
	// DownloadsDir is the container-side download root the worker writes under (the
	// stored output_dir/video_path prefix). HostDownloadsDir, when set, is the host
	// path it is bind-mounted from; the lesson DTO rewrites the former prefix to the
	// latter so the UI's "Copy path" resolves on the host. When HostDownloadsDir is
	// empty (the default), lesson paths are returned exactly as stored.
	DownloadsDir     string
	HostDownloadsDir string
	// LibraryDir is the root the worker mirrors finished lessons into (the Plex/
	// media library hardlink tree), matching scheduler.Config.LibraryDir. The
	// delete-files path removes a lesson's library mirror at
	// filepath.Join(LibraryDir, rel(DownloadsDir, output_dir)). Empty (the
	// default) means no library is configured: only the downloads copy is removed.
	LibraryDir string
}

// Deps bundles the engine handles the write/sync handlers need beyond the
// store: the Planner for dry-run sync and the daemon kick channel for on-demand
// sync. It is populated by the serve entrypoint; the read and health endpoints
// do not use it, so the zero value is valid (a nil Planner/Kick degrades the
// sync endpoint to a clear error rather than a panic).
type Deps struct {
	// Planner backs POST /api/sync?dry_run=true, reporting how many lessons a real
	// sync would enqueue without touching the queue.
	Planner *scheduler.Planner
	// Kick is the buffered channel the daemon's Run select drains for an immediate
	// out-of-band cycle. POST /api/sync does a non-blocking send on it. Nil when
	// no daemon is attached (e.g. tests of read-only endpoints).
	Kick chan<- struct{}
	// CancelRunning kills the in-flight download for a running job, returning true
	// when the job was found in this process's worker registry (the worker then
	// finalizes the job to canceled + the lesson to skipped asynchronously). It is
	// nil when no worker is attached; handleCancelJob then falls back to the DB
	// status flip. These are plain func handles (not the *Daemon/*Worker structs)
	// so server tests stay injectable without a real worker/daemon — mirroring Kick.
	CancelRunning func(jobID int64) bool
	// Pause/Resume gate the daemon's sync cycles; IsPaused reports the current
	// flag. All three are nil when no daemon is attached: the pause/resume
	// endpoints then return 503 and the summary reports paused=false.
	Pause    func()
	Resume   func()
	IsPaused func() bool
}

// Server holds the shared state behind drumdrop's inbound HTTP API: the database
// store the handlers read and write, the engine deps the write/sync handlers
// use, the in-memory progress Hub the SSE endpoint subscribes to, the resolved
// networking Config, and the routing mux. Hub and Deps are wired in by later
// tasks and are nil-tolerant until then; the health endpoints need only store
// and version.
type Server struct {
	store   *database.Store
	deps    Deps
	hub     *Hub
	cfg     Config
	version string
	mux     *http.ServeMux
	web     fs.FS // embedded SPA dist tree (empty in the default !webui build)
}

// NewServer assembles the routing mux over the shared store, engine deps,
// progress hub, and config, and returns it as an http.Handler. hub and deps may
// be nil/zero until the tasks that use them land. version is surfaced by the
// /healthz probe.
func NewServer(store *database.Store, deps Deps, hub *Hub, cfg Config, version string) http.Handler {
	s := &Server{
		store:   store,
		deps:    deps,
		hub:     hub,
		cfg:     cfg,
		version: version,
		mux:     http.NewServeMux(),
		web:     webui.DistFS(),
	}
	s.routes()
	return s.withMiddleware(s.mux)
}

// routes registers the HTTP handlers on the server's mux. Health endpoints are
// unauthenticated; the /api/* routes are guarded by the auth middleware
// NewServer wraps around this mux (see withMiddleware).
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)

	s.mux.HandleFunc("GET /api/follows", s.handleListFollows)
	s.mux.HandleFunc("POST /api/follows", s.handleCreateFollow)
	s.mux.HandleFunc("GET /api/follows/{id}", s.handleGetFollow)
	s.mux.HandleFunc("PATCH /api/follows/{id}", s.handleUpdateFollow)
	s.mux.HandleFunc("DELETE /api/follows/{id}", s.handleDeleteFollow)
	s.mux.HandleFunc("GET /api/follows/{id}/lessons", s.handleFollowLessons)

	s.mux.HandleFunc("GET /api/lessons", s.handleListLessons)
	s.mux.HandleFunc("GET /api/lessons/{id}", s.handleGetLesson)
	s.mux.HandleFunc("DELETE /api/lessons/{id}", s.handleDeleteLesson)
	s.mux.HandleFunc("POST /api/lessons/{id}/download", s.handleDownloadLesson)
	s.mux.HandleFunc("POST /api/lessons/{id}/skip", s.handleSkipLesson)
	s.mux.HandleFunc("POST /api/lessons/{id}/unskip", s.handleUnskipLesson)

	s.mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	s.mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	s.mux.HandleFunc("POST /api/jobs/{id}/cancel", s.handleCancelJob)
	s.mux.HandleFunc("POST /api/jobs/{id}/retry", s.handleRetryJob)

	s.mux.HandleFunc("GET /api/summary", s.handleSummary)

	s.mux.HandleFunc("POST /api/sync", s.handleSync)

	s.mux.HandleFunc("POST /api/pause", s.handlePause)
	s.mux.HandleFunc("POST /api/resume", s.handleResume)

	s.mux.HandleFunc("GET /api/preview", s.handlePreview)
	s.mux.HandleFunc("GET /api/session", s.handleGetSession)
	s.mux.HandleFunc("POST /api/session", s.handleLogin)

	s.mux.HandleFunc("GET /api/events", s.handleEvents)

	// Catch-all: serve the embedded SPA. Registered last; guards /api//healthz/
	// /readyz itself (see spaHandler) so it never shadows the API.
	s.mux.Handle("/", spaHandler(s.web))
}

// handleHealthz is the liveness probe: it always reports ok plus the build
// version, without touching the database. Unauthenticated.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.version,
	})
}

// handleReadyz is the readiness probe: it pings the database and reports ok on
// success or 503 on failure. The raw ping error is not echoed (it can carry
// driver/SQL internals); callers see a clean "database unavailable" message.
// Unauthenticated.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
