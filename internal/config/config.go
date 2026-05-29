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
