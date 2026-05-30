// Package scheduler implements drumdrop's unattended download machinery: a
// periodic Planner that enqueues jobs for newly discovered lessons and a
// sequential Worker that drains the jobs table one download at a time with
// automatic retry. A Daemon composes the two into a long-running loop.
//
// The package depends only on internal/database, internal/musora (for the
// Lesson/DownloadOpts types), and the standard library. All side effects —
// catalog expansion, lesson resolution, downloading, and persistence — flow
// through the interfaces below, so the package is fully testable offline with
// fakes plus an injected sleep.
package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// Resolver fetches the full lesson metadata needed to download. A nil lesson
// returned with a nil error means the lesson could not be resolved (gated or
// missing) and must be skipped, matching musora.ResolveLesson's contract.
type Resolver interface {
	Resolve(id int, permIDs string) (*musora.Lesson, error)
}

// Downloader performs the actual download of a resolved lesson.
type Downloader interface {
	Download(l *musora.Lesson, o musora.DownloadOpts) error
}

// Expander turns one follow into the railcontent ids of the lessons under it.
// Node follows expand via the catalog walk; instructor follows via the
// instructor-lessons query.
type Expander interface {
	Expand(f database.Follow, permIDs string) (ids []int, err error)
}

// Store is the subset of *database.Store the scheduler uses. Declaring it as an
// interface lets tests inject a fake with no real database. Every method
// signature mirrors internal/database exactly; the compile-time assertion below
// guarantees *database.Store satisfies it.
type Store interface {
	// planner
	ListFollows(ctx context.Context) ([]database.Follow, error)
	UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string, followID sql.NullInt64) error
	IsDownloaded(ctx context.Context, id int) (bool, error)
	ActiveJobExists(ctx context.Context, railcontentID int) (bool, error)
	EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (id int64, created bool, err error)
	TouchLastSynced(ctx context.Context, id int64) error
	// worker
	ClaimNextJob(ctx context.Context) (database.Job, bool, error)
	GetFollow(ctx context.Context, id int64) (database.Follow, error)
	MarkJobRunning(ctx context.Context, id int64) error
	MarkJobDone(ctx context.Context, id int64) error
	MarkJobFailed(ctx context.Context, id int64, errMsg string) error
	MarkDownloading(ctx context.Context, id int) error
	MarkDownloaded(ctx context.Context, id int, quality, outputDir, videoPath string, bytes int64) error
	MarkFailed(ctx context.Context, id int, errMsg string) error
	MarkSkipped(ctx context.Context, id int, reason string) error
	RequeueStaleRunning(ctx context.Context) (int, error)
}

// Compile-time assertion that the real store satisfies the scheduler's Store
// interface. If this fails to compile, the interface drifted from
// internal/database — fix the interface to match the store, never the reverse.
var _ Store = (*database.Store)(nil)

// Config holds the tunables shared by the Planner, Worker, and Daemon.
type Config struct {
	// DownloadsDir is the root under which per-follow folders are created.
	DownloadsDir string
	// Quality overrides each follow's saved quality when non-empty; empty means
	// use the follow's own quality.
	Quality string
	// ResourcesOnly skips the video and fetches only attached resources.
	ResourcesOnly bool
	// MaxAttempts is the total number of download attempts per job (>=1).
	MaxAttempts int
	// Backoff[i] is the delay before attempt i+2 (i.e. between retries). The
	// last entry is reused for any further attempts.
	Backoff []time.Duration
}

// DefaultConfig returns the standard tunables: 3 attempts with a 5s/30s/2m
// backoff schedule. DownloadsDir and Quality are left to the caller (Quality
// empty = use each follow's saved quality).
func DefaultConfig() Config {
	return Config{
		MaxAttempts: 3,
		Backoff: []time.Duration{
			5 * time.Second,
			30 * time.Second,
			2 * time.Minute,
		},
	}
}

// folderTitle is the human-facing folder name for a follow: its saved Title
// when present, otherwise its natural key (@slug for instructors, the
// railcontent_id for nodes). It mirrors cmd/drumdrop's followTarget fallback so
// the Planner, Worker, and CLI all agree on foldering.
func folderTitle(f database.Follow) string {
	if f.Title != "" {
		return f.Title
	}
	if f.Kind == "instructor" && f.Slug.Valid {
		return "@" + f.Slug.String
	}
	if f.RailcontentID.Valid {
		return strconv.FormatInt(f.RailcontentID.Int64, 10)
	}
	return "-"
}

// outDirFor is the single source of truth for a follow's output directory:
// cfg.DownloadsDir joined with the sanitized folder title.
func outDirFor(cfg Config, f database.Follow) string {
	return filepath.Join(cfg.DownloadsDir, musora.Sanitize(folderTitle(f)))
}

// qualityFor resolves the effective quality for a follow: the config override
// when non-empty, otherwise the follow's own saved quality.
func qualityFor(cfg Config, f database.Follow) string {
	if cfg.Quality != "" {
		return cfg.Quality
	}
	return f.Quality
}
