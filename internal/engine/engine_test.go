package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
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

	cfg := mustConfig(t, "", "", false)
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
	if cfg.AudioLang != "en" {
		t.Errorf("AudioLang = %q, want en (DRUMDROP_AUDIO_LANG unset defaults to en)", cfg.AudioLang)
	}

	t.Setenv("DRUMDROP_AUDIO_LANG", "es")
	if cfg := mustConfig(t, "", "", false); cfg.AudioLang != "es" {
		t.Errorf("AudioLang = %q, want es (from DRUMDROP_AUDIO_LANG)", cfg.AudioLang)
	}

	cfg = mustConfig(t, "/explicit/out", "best", true)
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
	cfg := mustConfig(t, "/tmp/x", "best", false)
	var log io.Writer = io.Discard

	sink := buildTestSink{}
	planner, worker, daemon := Build(nil, cfg, "perm", log, sink)
	if planner == nil || worker == nil || daemon == nil {
		t.Fatal("Build returned a nil component")
	}
	// The progress sink is shared by assignment with both the worker and daemon.
	if worker.Progress != sink {
		t.Error("worker.Progress is not the sink passed to Build")
	}
	if daemon.Progress != sink {
		t.Error("daemon.Progress is not the sink passed to Build")
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

// buildTestSink is a no-op ProgressSink used to assert Build wires it through to
// the worker and daemon by assignment. It is comparable so the test can check
// pointer/value identity against what it passed in.
type buildTestSink struct{}

func (buildTestSink) Emit(scheduler.ProgressEvent) {}

func mustConfig(t *testing.T, out, quality string, resourcesOnly bool) scheduler.Config {
	t.Helper()
	cfg, err := Config(out, quality, resourcesOnly)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	return cfg
}

// TestConfigRefusesALibraryThatIsTheDownloadsFolder proves every command
// (they all build their config here) refuses to start when
// DRUMDROP_LIBRARY_DIR is the downloads folder reached by another path, where
// a library move would delete a lesson's only copy. The same path spelled the
// same way is allowed: every move is then a no-op.
func TestConfigRefusesALibraryThatIsTheDownloadsFolder(t *testing.T) {
	downloads := filepath.Join(t.TempDir(), "dl")
	if err := os.Mkdir(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "lib")
	if err := os.Symlink(downloads, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRUMDROP_LIBRARY_DIR", alias)
	if _, err := Config(downloads, "", false); err == nil || !strings.Contains(err.Error(), "is the downloads folder") {
		t.Errorf("Config(library aliasing downloads) err = %v, want the refusal", err)
	}
	t.Setenv("DRUMDROP_LIBRARY_DIR", downloads)
	if _, err := Config(downloads, "", false); err != nil {
		t.Errorf("Config(library == downloads, same path) = %v, want nil", err)
	}
}

// TestConfigMakesTheRootsAbsolute proves a relative downloads or library
// folder (the ./downloads default, or DRUMDROP_LIBRARY_DIR=lib) is resolved
// once against the working directory, so every path recorded from it is
// absolute and a later command run from elsewhere still finds the lessons.
func TestConfigMakesTheRootsAbsolute(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	t.Setenv("DRUMDROP_LIBRARY_DIR", "lib")
	cfg := mustConfig(t, "downloads", "", false)
	if want := filepath.Join(tmp, "downloads"); cfg.DownloadsDir != want {
		t.Errorf("DownloadsDir = %q, want %q", cfg.DownloadsDir, want)
	}
	if want := filepath.Join(tmp, "lib"); cfg.LibraryDir != want {
		t.Errorf("LibraryDir = %q, want %q", cfg.LibraryDir, want)
	}
	t.Setenv("DRUMDROP_LIBRARY_DIR", "")
	if cfg := mustConfig(t, "downloads", "", false); cfg.LibraryDir != "" {
		t.Errorf("unset library = %q, want it left unset", cfg.LibraryDir)
	}
}
