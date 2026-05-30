package server

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// withMiddleware wraps the routing handler with CORS and bearer-token auth. The
// health probes (/healthz, /readyz) are exempt; everything else under /api is
// guarded. CORS headers are applied (and OPTIONS preflight short-circuited) when
// cfg.CORSOrigin is set, before the auth check so browsers can read the 401.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.CORSOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", s.cfg.CORSOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		if s.authExempt(r.URL.Path) || s.authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized")
	})
}

// authExempt reports whether the path bypasses the auth check. The liveness and
// readiness probes are always reachable so orchestrators can poll them.
func (s *Server) authExempt(path string) bool {
	return path == "/healthz" || path == "/readyz"
}

// authorized reports whether the request carries a valid credential. When no API
// token is configured and the listen address is loopback, every request is
// allowed (single-user local mode); otherwise a constant-time match against the
// Bearer token or ?access_token query value is required.
func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.APIToken == "" {
		return isLoopbackAddr(s.cfg.ListenAddr)
	}
	return constantTimeMatch(requestToken(r), s.cfg.APIToken)
}

// requestToken extracts the caller's token from the Authorization: Bearer header
// or, falling back, the access_token query parameter (used by EventSource, which
// cannot set headers).
func requestToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if t, ok := strings.CutPrefix(h, "Bearer "); ok {
			return t
		}
	}
	return r.URL.Query().Get("access_token")
}

// constantTimeMatch reports whether got equals want without leaking timing
// information about how many leading bytes matched.
func constantTimeMatch(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// GuardListen refuses an unsafe bind: serving on a non-loopback address without
// an API token would expose the unauthenticated API to the network. It returns
// an error in that case so serve can refuse to start; loopback binds and any
// bind with a token configured are allowed.
func GuardListen(addr, token string) error {
	if token != "" || isLoopbackAddr(addr) {
		return nil
	}
	return fmt.Errorf("refusing to bind %q without an API token: set DRUMDROP_API_TOKEN or listen on loopback", addr)
}

// isLoopbackAddr reports whether the host portion of a listen address is a
// loopback target: a 127.0.0.0/8 / ::1 IP, "localhost", or an empty host (a bare
// ":port" binds all interfaces in net/http but is treated as local-only intent
// here, matching the plan).
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port present; treat the whole string as the host.
		host = addr
	}
	if host == "" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
