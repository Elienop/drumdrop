package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// syncRequest is the optional POST /api/sync body. dry_run flips the endpoint
// from "kick the daemon" to "report what a sync would enqueue" without touching
// the queue. An absent or empty body means a real (non-dry-run) sync.
type syncRequest struct {
	DryRun bool `json:"dry_run"`
}

// handleSync serves POST /api/sync. With {"dry_run":true} it runs the planner's
// dry run synchronously and returns {"would_enqueue":N} — the number of lessons
// a real sync would queue, having enqueued nothing. Otherwise it requests one
// out-of-band daemon cycle via a non-blocking send on the kick channel and
// returns 202 {"triggered":true}; if the kick buffer is already full a cycle is
// pending, so it still reports 202 rather than failing. An empty or missing body
// is a real sync; a malformed body is a 400.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, msgBadBody)
		return
	}

	if req.DryRun {
		if s.deps.Planner == nil {
			writeErr(w, http.StatusServiceUnavailable, msgNoPlanner)
			return
		}
		would, err := s.deps.Planner.PlanDryRun(r.Context())
		if err != nil {
			fmt.Fprintf(logOut, "drumdrop: dry-run sync: %v\n", err)
			writeErr(w, http.StatusInternalServerError, msgDryRunFailed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"would_enqueue": would})
		return
	}

	if s.deps.Kick == nil {
		writeErr(w, http.StatusServiceUnavailable, msgNoDaemon)
		return
	}
	s.kick()
	writeJSON(w, http.StatusAccepted, map[string]bool{"triggered": true})
}

// kick asks the daemon for one cycle now, out of the interval: a non-blocking
// send on the kick channel, so a request never waits on it. A full buffer
// means a cycle is already pending, and it covers this request too, so
// presses close together share one cycle. With no daemon attached (a nil
// Kick: server tests, and the CLI, which serves nothing) it does nothing.
// While syncs are paused the daemon drops a kick it receives (Daemon.Run), so
// what a press queued waits for Resume, which kicks again.
func (s *Server) kick() {
	if s.deps.Kick == nil {
		return
	}
	select {
	case s.deps.Kick <- struct{}{}:
	default:
	}
}
