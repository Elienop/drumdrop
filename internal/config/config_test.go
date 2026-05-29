package config

import (
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
