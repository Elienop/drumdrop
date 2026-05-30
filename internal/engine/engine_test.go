package engine

import (
	"io"
	"testing"

	"github.com/elienop/drumdrop/internal/scheduler"
)

func TestExtractID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"bare number", "409875", 409875},
		{"url with two ids takes last", "https://app.musora.com/drumeo/lessons/course/409875/409918", 409918},
		{"url with single id", "https://app.musora.com/drumeo/lessons/409918", 409918},
		{"no digits", "https://app.musora.com/drumeo/lessons", 0},
		{"empty", "", 0},
		{"mixed alphanumeric", "lesson-42-final", 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractID(tt.input); got != tt.want {
				t.Fatalf("ExtractID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestPermissionIDs(t *testing.T) {
	t.Setenv("DRUMDROP_PERMISSION_IDS", "1,2,3")
	if got := PermissionIDs(); got != "1,2,3" {
		t.Fatalf("PermissionIDs() = %q, want %q", got, "1,2,3")
	}
	t.Setenv("DRUMDROP_PERMISSION_IDS", "")
	if got := PermissionIDs(); got != "" {
		t.Fatalf("PermissionIDs() = %q, want empty", got)
	}
}

// TestConfigDefaults verifies the flag → scheduler.Config mapping: an empty out
// falls back to DownloadsDir() (env-driven), an empty quality stays empty (use
// each follow's saved quality), and resourcesOnly flows through. The retry
// tunables come from scheduler.DefaultConfig.
func TestConfigDefaults(t *testing.T) {
	t.Setenv("DRUMDROP_DOWNLOADS_DIR", "/tmp/dd")

	cfg := Config("", "", false)
	if cfg.DownloadsDir != "/tmp/dd" {
		t.Errorf("DownloadsDir = %q, want /tmp/dd (from env fallback)", cfg.DownloadsDir)
	}
	if cfg.Quality != "" {
		t.Errorf("Quality = %q, want empty", cfg.Quality)
	}
	if cfg.ResourcesOnly {
		t.Error("ResourcesOnly = true, want false")
	}
	if cfg.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3 (from DefaultConfig)", cfg.MaxAttempts)
	}

	cfg = Config("/explicit/out", "best", true)
	if cfg.DownloadsDir != "/explicit/out" {
		t.Errorf("DownloadsDir = %q, want /explicit/out (out wins over env)", cfg.DownloadsDir)
	}
	if cfg.Quality != "best" {
		t.Errorf("Quality = %q, want best", cfg.Quality)
	}
	if !cfg.ResourcesOnly {
		t.Error("ResourcesOnly = false, want true")
	}
}

// TestAdaptersSatisfyInterfaces guards that the real adapters keep implementing
// the scheduler seams after the hoist.
func TestAdaptersSatisfyInterfaces(t *testing.T) {
	var _ scheduler.Expander = Expander{}
	var _ scheduler.Resolver = Resolver{}
	var _ scheduler.Downloader = Downloader{}
}

// TestBuildWires verifies Build returns a planner, worker, and daemon wired over
// the same store, permission ids, log, and config, with the daemon composing the
// returned planner and worker.
func TestBuildWires(t *testing.T) {
	cfg := Config("/tmp/x", "best", false)
	var log io.Writer = io.Discard

	planner, worker, daemon := Build(nil, cfg, "perm", log)
	if planner == nil || worker == nil || daemon == nil {
		t.Fatal("Build returned a nil component")
	}
	if planner.PermIDs != "perm" {
		t.Errorf("planner.PermIDs = %q, want perm", planner.PermIDs)
	}
	if planner.Expander == nil {
		t.Error("planner.Expander not wired")
	}
	if worker.PermIDs != "perm" {
		t.Errorf("worker.PermIDs = %q, want perm", worker.PermIDs)
	}
	if worker.Cfg.DownloadsDir != "/tmp/x" {
		t.Errorf("worker.Cfg.DownloadsDir = %q, want /tmp/x", worker.Cfg.DownloadsDir)
	}
	if daemon.Planner != planner {
		t.Error("daemon.Planner is not the returned planner")
	}
	if daemon.Worker != worker {
		t.Error("daemon.Worker is not the returned worker")
	}
}
