package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/elienop/drumdrop/internal/scheduler"
)

// pingInterval is how often the SSE stream writes a ": ping" comment frame to
// keep idle connections (and any intervening proxies) from timing out.
const pingInterval = 20 * time.Second

// serverCtxKey is the context key under which the serve entrypoint stashes the
// server-closing context on each request's base context (see WithServerContext).
type ctxKey int

const serverCtxKey ctxKey = iota

// WithServerContext returns base annotated so handlers can recover the
// server-closing context via serverClosingContext. The serve entrypoint sets
// http.Server.BaseContext to return this, so every request descends from a
// context the entrypoint cancels at shutdown start. The long-lived SSE handler
// selects on that context to unblock promptly instead of pinning Shutdown for
// the full timeout.
func WithServerContext(base context.Context) context.Context {
	return context.WithValue(base, serverCtxKey, base)
}

// serverClosingContext recovers the server-closing context stashed by
// WithServerContext, or nil when none was set (e.g. httptest servers that do not
// install the BaseContext). A nil context is never ready, so a nil-guarded
// select case is a no-op.
func serverClosingContext(ctx context.Context) context.Context {
	c, _ := ctx.Value(serverCtxKey).(context.Context)
	return c
}

// handleEvents streams the scheduler's progress events to the client as
// Server-Sent Events. It first writes a "ready" frame carrying the hub's current
// per-kind snapshot so a freshly connected dashboard reflects state without
// waiting for the next live event, then forwards each emitted event as an
// id/data frame and a periodic keep-alive ping. The stream ends when the client
// disconnects (request context done). Auth is handled by the middleware, which
// honors the ?access_token query value EventSource clients pass.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		writeErr(w, http.StatusServiceUnavailable, msgNoProgressStream)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	rc := http.NewResponseController(w)

	ch, cancel := s.hub.Subscribe()
	defer cancel()

	// Seed the client with the current snapshot as a single "ready" frame so it
	// can render immediately.
	if snap := s.hub.Snapshot(); len(snap) > 0 {
		if data, err := json.Marshal(snap); err == nil {
			fmt.Fprintf(w, "event: ready\ndata: %s\n\n", data)
			_ = rc.Flush()
		}
	} else {
		// Always announce readiness even when there is nothing cached yet so the
		// client knows the stream is live.
		fmt.Fprint(w, "event: ready\ndata: []\n\n")
		_ = rc.Flush()
	}

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	ctx := r.Context()
	// srvDone fires when the server enters shutdown, so an open stream tears down
	// at once rather than pinning srv.Shutdown for the full timeout. It is nil
	// (a never-ready select case) when no BaseContext was installed.
	var srvDone <-chan struct{}
	if sc := serverClosingContext(ctx); sc != nil {
		srvDone = sc.Done()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-srvDone:
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !s.writeEvent(w, rc, e) {
				return
			}
		}
	}
}

// writeEvent serializes one ProgressEvent as an SSE id/data frame and flushes
// it. It reports false when the connection can no longer be written, signaling
// the stream loop to tear down.
func (s *Server) writeEvent(w http.ResponseWriter, rc *http.ResponseController, e scheduler.ProgressEvent) bool {
	data, err := json.Marshal(e)
	if err != nil {
		// A ProgressEvent is plain data; a marshal failure is not recoverable per
		// event, so skip it but keep the stream open.
		return true
	}
	if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", s.hub.Seq(), data); err != nil {
		return false
	}
	return rc.Flush() == nil
}
