package config

import (
	"os"
	"path/filepath"
)

// ConfigDir is resolved at call time so tests can override via DRUMDROP_CONFIG_DIR.
func ConfigDir() string {
	if d := os.Getenv("DRUMDROP_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "drumdrop")
}

func SecretKeyPath() string { return filepath.Join(ConfigDir(), "secret.key") }
func CredsPath() string     { return filepath.Join(ConfigDir(), "credentials.enc") }
func CookiePath() string    { return filepath.Join(ConfigDir(), "session.cookie") }

// DBPath is the path to the SQLite database file.
func DBPath() string { return filepath.Join(ConfigDir(), "drumdrop.db") }

// DownloadsDir is the root directory under which the daemon writes downloads.
// It is resolved at call time so it can be overridden via DRUMDROP_DOWNLOADS_DIR;
// when unset it defaults to ./downloads (relative to the process working dir).
// sync --out still overrides this per run.
func DownloadsDir() string {
	if d := os.Getenv("DRUMDROP_DOWNLOADS_DIR"); d != "" {
		return d
	}
	return "./downloads"
}

// LibraryDir is the root directory the daemon MOVES finished lessons into,
// read at call time from DRUMDROP_LIBRARY_DIR. Empty (the default) disables the
// move entirely — byte-for-byte the prior behavior, lessons stay in the downloads
// dir. When set, each finished lesson folder is moved into this dir (single
// location) at the same path relative to DownloadsDir, so a Plex library owns the
// file and never sees the in-progress partials in the downloads scratch dir.
func LibraryDir() string { return os.Getenv("DRUMDROP_LIBRARY_DIR") }

// HostDownloadsDir is the host path that the container's DownloadsDir is
// bind-mounted from, read at call time from DRUMDROP_HOST_DOWNLOADS_DIR. It is
// used only to rewrite the stored (container) download paths in the API so the
// UI's "Copy path" resolves on the host. Empty (the default) leaves paths as the
// container sees them.
func HostDownloadsDir() string { return os.Getenv("DRUMDROP_HOST_DOWNLOADS_DIR") }

// ListenAddr is the host:port the HTTP API binds to. It is resolved at call
// time via DRUMDROP_LISTEN; when unset it defaults to loopback 127.0.0.1:8080.
func ListenAddr() string {
	if a := os.Getenv("DRUMDROP_LISTEN"); a != "" {
		return a
	}
	return "127.0.0.1:8080"
}

// APIToken is the bearer token required by the HTTP API, read at call time from
// DRUMDROP_API_TOKEN. An empty result means no token is configured (allowed only
// on loopback binds; enforced at startup by the server's listen guard).
func APIToken() string { return os.Getenv("DRUMDROP_API_TOKEN") }

// CORSOrigin is the allowed CORS origin for the HTTP API, read at call time from
// DRUMDROP_CORS_ORIGIN. An empty result (the default) disables CORS headers.
func CORSOrigin() string { return os.Getenv("DRUMDROP_CORS_ORIGIN") }

// Interval is the auto-sync interval used by serve/daemon, read at call time
// from DRUMDROP_INTERVAL; when unset it defaults to "12h". It returns the raw
// string (a Go duration) so the existing --interval parsing/validation applies.
func Interval() string {
	if v := os.Getenv("DRUMDROP_INTERVAL"); v != "" {
		return v
	}
	return "12h"
}

// Quality overrides each follow's quality for serve/daemon/sync, read at call
// time from DRUMDROP_QUALITY; an empty result (the default, when unset) means
// "use each follow's own quality" — unchanged behavior.
func Quality() string { return os.Getenv("DRUMDROP_QUALITY") }
