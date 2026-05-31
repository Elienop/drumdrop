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

func TestListenAddr(t *testing.T) {
	tests := []struct {
		name string
		env  string
		set  bool
		want string
	}{
		{name: "default when unset", want: "127.0.0.1:8080"},
		{name: "env override", env: "0.0.0.0:9000", set: true, want: "0.0.0.0:9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("DRUMDROP_LISTEN", tt.env)
			} else if err := os.Unsetenv("DRUMDROP_LISTEN"); err != nil {
				t.Fatalf("unset DRUMDROP_LISTEN: %v", err)
			}
			if got := ListenAddr(); got != tt.want {
				t.Fatalf("ListenAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIToken(t *testing.T) {
	tests := []struct {
		name string
		env  string
		set  bool
		want string
	}{
		{name: "default empty when unset", want: ""},
		{name: "env override", env: "s3cr3t", set: true, want: "s3cr3t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("DRUMDROP_API_TOKEN", tt.env)
			} else if err := os.Unsetenv("DRUMDROP_API_TOKEN"); err != nil {
				t.Fatalf("unset DRUMDROP_API_TOKEN: %v", err)
			}
			if got := APIToken(); got != tt.want {
				t.Fatalf("APIToken = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCORSOrigin(t *testing.T) {
	tests := []struct {
		name string
		env  string
		set  bool
		want string
	}{
		{name: "default empty when unset", want: ""},
		{name: "env override", env: "https://app.example.com", set: true, want: "https://app.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("DRUMDROP_CORS_ORIGIN", tt.env)
			} else if err := os.Unsetenv("DRUMDROP_CORS_ORIGIN"); err != nil {
				t.Fatalf("unset DRUMDROP_CORS_ORIGIN: %v", err)
			}
			if got := CORSOrigin(); got != tt.want {
				t.Fatalf("CORSOrigin = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInterval(t *testing.T) {
	tests := []struct{ name, env, want string }{
		{"unset default", "", "12h"},
		{"override", "6h", "6h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("DRUMDROP_INTERVAL", tt.env)
			} else if err := os.Unsetenv("DRUMDROP_INTERVAL"); err != nil {
				t.Fatalf("unset: %v", err)
			}
			if got := Interval(); got != tt.want {
				t.Errorf("Interval() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuality(t *testing.T) {
	tests := []struct{ name, env, want string }{
		{"unset default empty", "", ""},
		{"override", "1080", "1080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("DRUMDROP_QUALITY", tt.env)
			} else if err := os.Unsetenv("DRUMDROP_QUALITY"); err != nil {
				t.Fatalf("unset: %v", err)
			}
			if got := Quality(); got != tt.want {
				t.Errorf("Quality() = %q, want %q", got, tt.want)
			}
		})
	}
}
