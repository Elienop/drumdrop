package server

import (
	"net/http"

	"github.com/elienop/drumdrop/internal/database"
)

// Config carries the server's networking settings, resolved once at startup
// from the config env helpers (DRUMDROP_LISTEN/_API_TOKEN/_CORS_ORIGIN). The
// zero value is valid: no token, no CORS, and the listen guard left to the
// caller. Auth and CORS tasks consume these fields.
type Config struct {
	ListenAddr string
	APIToken   string
	CORSOrigin string
}

// Hub is the in-memory progress broadcast hub the SSE endpoint subscribes to.
// It is a placeholder here so the Server can hold a *Hub field; the SSE task
// gives it its subscriber set, channels, and scheduler.ProgressSink behavior. A
// nil *Hub is tolerated until the SSE endpoint lands.
type Hub struct{}

// Deps bundles the engine handles the write/sync handlers need beyond the
// store: the Planner for dry-run sync, the daemon kick channel for on-demand
// sync, and the owner permission ids for follow resolution. It is empty for now
// and populated as the write/sync endpoints land; the read and health endpoints
// do not use it, so the zero value is valid.
type Deps struct{}

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
	}
	s.routes()
	return s.mux
}

// routes registers the HTTP handlers on the server's mux. Health endpoints are
// unauthenticated; the /api/* routes and their auth wrapper are added by later
// tasks.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
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
// success or 503 with the error on failure. Unauthenticated.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
