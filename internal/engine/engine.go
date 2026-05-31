// Package engine is the single wiring recipe for drumdrop's download machinery.
// It opens the store, builds the scheduler Config from the shared download flags,
// adapts the musora package to the scheduler's Expander/Resolver/Downloader
// seams, and composes a Planner + Worker + Daemon. Both the CLI (sync/daemon) and
// the HTTP server build their engine through here so the wiring lives in one
// place. Flag parsing and command output stay in the callers.
package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"

	"github.com/elienop/drumdrop/internal/config"
	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
	"github.com/elienop/drumdrop/internal/scheduler"
)

var reDigits = regexp.MustCompile(`\d+`)

// ExtractID accepts a bare numeric id or a Musora URL and returns the last
// numeric run found. Returns 0 when no digits are present.
func ExtractID(input string) int {
	if n, err := strconv.Atoi(input); err == nil {
		return n
	}
	nums := reDigits.FindAllString(input, -1)
	if len(nums) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(nums[len(nums)-1])
	return n
}

// PermissionIDs returns the owner's comma-separated permission id string from
// the environment, passed to the catalog/resolve queries so gated content is
// expanded with the right entitlements.
func PermissionIDs() string { return os.Getenv("DRUMDROP_PERMISSION_IDS") }

// OpenStore prepares the config directory, opens the SQLite database with the
// drumdrop pragmas, runs any pending migrations, and returns a ready Store.
// Every persistence verb opens its store through here and defers store.Close().
func OpenStore() (*database.Store, error) {
	if err := os.MkdirAll(config.ConfigDir(), 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	db, err := database.Open(config.DBPath())
	if err != nil {
		return nil, err
	}
	if err := database.RunMigrations(db); err != nil {
		db.Close()
		return nil, err
	}
	return database.NewStore(db), nil
}

// Config builds a scheduler.Config from the shared download flags. An empty out
// falls back to config.DownloadsDir() (the DRUMDROP_DOWNLOADS_DIR env or
// ./downloads); an empty quality means "use each follow's saved quality".
func Config(out, quality string, resourcesOnly bool) scheduler.Config {
	cfg := scheduler.DefaultConfig()
	cfg.DownloadsDir = out
	if cfg.DownloadsDir == "" {
		cfg.DownloadsDir = config.DownloadsDir()
	}
	cfg.Quality = quality
	cfg.ResourcesOnly = resourcesOnly
	return cfg
}

// Expander adapts the musora package to scheduler.Expander.
type Expander struct{}

func (Expander) Expand(f database.Follow, permIDs string) ([]musora.LessonItem, error) {
	switch f.Kind {
	case "node":
		if !f.RailcontentID.Valid {
			return nil, fmt.Errorf("node follow #%d has no railcontent_id", f.ID)
		}
		_, items, err := musora.ResolveLessonIDs(int(f.RailcontentID.Int64), false, permIDs)
		return items, err
	case "instructor":
		if !f.Slug.Valid {
			return nil, fmt.Errorf("instructor follow #%d has no slug", f.ID)
		}
		refs, err := musora.InstructorLessons(f.Slug.String, f.Brand, permIDs)
		if err != nil {
			return nil, err
		}
		items := make([]musora.LessonItem, 0, len(refs))
		for _, r := range refs {
			items = append(items, musora.LessonItem{ID: r.ID, Title: r.Title})
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unknown follow kind %q", f.Kind)
	}
}

// Resolver adapts musora.ResolveLesson to scheduler.Resolver.
type Resolver struct{}

func (Resolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	return musora.ResolveLesson(id, permIDs)
}

// Downloader adapts musora.DownloadLesson to scheduler.Downloader.
type Downloader struct{}

func (Downloader) Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	return musora.DownloadLesson(ctx, l, o)
}

// Compile-time assertions that the real adapters satisfy the scheduler
// interfaces. They wrap the same musora calls the engine has always used.
var (
	_ scheduler.Expander   = Expander{}
	_ scheduler.Resolver   = Resolver{}
	_ scheduler.Downloader = Downloader{}
)

// Build is the single wiring recipe: it composes a Planner, Worker, and Daemon
// over one store, sharing the config, permission ids, and log writer. The
// returned Daemon embeds the returned Planner and Worker, so callers run either
// the one-shot plan+drain (sync) or the long-running loop (daemon) from the same
// wiring. progress is the structured-event sink shared by the worker and daemon;
// a nil progress leaves both on their default noopSink (the CLI passes nil; the
// HTTP server passes its broadcast hub).
func Build(store *database.Store, cfg scheduler.Config, permIDs string, log io.Writer, progress scheduler.ProgressSink) (*scheduler.Planner, *scheduler.Worker, *scheduler.Daemon) {
	planner := &scheduler.Planner{
		Store:    store,
		Expander: Expander{},
		PermIDs:  permIDs,
		Log:      log,
	}
	worker := scheduler.NewWorker(store, Resolver{}, Downloader{}, cfg, permIDs, log)
	// Set the sink by assignment so NewWorker's positional signature is untouched.
	worker.Progress = progress
	daemon := &scheduler.Daemon{
		Store:    store,
		Planner:  planner,
		Worker:   worker,
		Log:      log,
		Progress: progress,
	}
	// Let the worker's drain loop honor the daemon's pause flag: pausing mid-cycle
	// stops it claiming the next queued job, not just future cycles.
	worker.IsPaused = daemon.IsPaused
	return planner, worker, daemon
}
