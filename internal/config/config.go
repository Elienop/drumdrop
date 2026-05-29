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
