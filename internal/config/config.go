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
