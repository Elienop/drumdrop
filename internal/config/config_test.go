package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDirRespectsEnv(t *testing.T) {
	t.Setenv("DRUMDROP_CONFIG_DIR", "/tmp/x")
	if got := ConfigDir(); got != "/tmp/x" {
		t.Fatalf("ConfigDir = %q, want /tmp/x", got)
	}
	if got, want := SecretKeyPath(), filepath.Join("/tmp/x", "secret.key"); got != want {
		t.Fatalf("SecretKeyPath = %q, want %q", got, want)
	}
}

func TestDownloadsDir(t *testing.T) {
	// Set: the env value wins verbatim.
	t.Setenv("DRUMDROP_DOWNLOADS_DIR", "/data/archive")
	if got, want := DownloadsDir(), "/data/archive"; got != want {
		t.Fatalf("DownloadsDir (env set) = %q, want %q", got, want)
	}

	// Unset: fall back to ./downloads. t.Setenv has registered a cleanup that
	// restores the prior value, so unsetting here is safe within the test.
	if err := os.Unsetenv("DRUMDROP_DOWNLOADS_DIR"); err != nil {
		t.Fatalf("unset DRUMDROP_DOWNLOADS_DIR: %v", err)
	}
	if got, want := DownloadsDir(), "./downloads"; got != want {
		t.Fatalf("DownloadsDir (env unset) = %q, want %q", got, want)
	}
}
