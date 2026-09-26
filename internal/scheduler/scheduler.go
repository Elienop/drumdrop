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

// Downloader performs the actual download of a resolved lesson. ctx cancels the
// in-flight download (killing yt-dlp and its child); a nil ctx preserves the
// uncancelable CLI behaviour.
type Downloader interface {
	Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error
}

// ImageFetcher downloads an image as JPEG bytes, for a plex-tv show's
// poster.jpg and fanart.jpg (musora.FetchJPEG, whose errors say whether to
// ask again).
type ImageFetcher interface {
	FetchJPEG(ctx context.Context, url string) ([]byte, error)
}

// Expander turns one follow into the railcontent ids of the lessons under it.
// Node follows expand via the catalog walk; instructor follows via the
// instructor-lessons query.
type Expander interface {
	Expand(f database.Follow, permIDs string) (items []musora.LessonItem, err error)
}

// Store is the subset of *database.Store the scheduler uses. Declaring it as an
// interface lets tests inject a fake with no real database. Every method
// signature mirrors internal/database exactly; the compile-time assertion below
// guarantees *database.Store satisfies it.
type Store interface {
	// planner
	ListFollows(ctx context.Context) ([]database.Follow, error)
	UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string, position sql.NullInt64, followID sql.NullInt64) error
	IsDownloaded(ctx context.Context, id int) (bool, error)
	ShouldSkipEnqueue(ctx context.Context, id int) (bool, error)
	ActiveJobExists(ctx context.Context, railcontentID int) (bool, error)
	EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (id int64, created bool, err error)
	TouchLastSynced(ctx context.Context, id int64) error
	// worker
	ClaimNextJob(ctx context.Context) (database.Job, bool, error)
	GetFollow(ctx context.Context, id int64) (database.Follow, error)
	GetLesson(ctx context.Context, id int) (database.Lesson, error)
	ListLessonsWithFiles(ctx context.Context) ([]database.Lesson, error)
	MarkJobRunning(ctx context.Context, id int64) error
	// The worker's writes about a job land only while the job and its lesson
	// still exist (database.ErrDownloadAbandoned otherwise, joined with
	// database.ErrLessonDeleted when the delete removes the lesson's files),
	// so nothing a download does can land after a delete removed them; and a
	// download only starts or goes on while its job is running
	// (database.ErrDownloadCanceled otherwise).
	StartDownload(ctx context.Context, jobID int64, id int) error
	ConfirmDownload(ctx context.Context, jobID int64, id int) error
	FinishDownload(ctx context.Context, jobID int64, id int, rec database.DownloadRecord) error
	// FailDownload stores lessonMsg under the lesson (whose menu offers
	// Download), or keptMsg when the lesson still records files from an
	// earlier download and onDisk says they are all on disk (it stays
	// 'downloaded'), and jobMsg on the job (shown in the Queue beside
	// Retry); NotReturnedDownload stores reason in both (so it names no
	// button), or keptMsg under a lesson kept the same way.
	// CancelDownload's onDisk decides the same.
	FailDownload(ctx context.Context, jobID int64, id int, lessonMsg, keptMsg, jobMsg string, onDisk bool) error
	NotReturnedDownload(ctx context.Context, jobID int64, id int, reason, keptMsg string, onDisk bool) error
	CancelDownload(ctx context.Context, jobID int64, id int, onDisk bool) error
	RequeueStaleRunning(ctx context.Context) (int, error)
	// the one-time rename of plex-tv episode files (episodefiles.go): a
	// compare-and-swap of the lesson's library record that a delete's hold
	// or a running job of the lesson refuses (database.ErrLessonDeleting,
	// ErrLessonDownloading, ErrLessonChanged, sql.ErrNoRows).
	SwapLibraryEntries(ctx context.Context, before database.Lesson, entries []string) error
}

// Compile-time assertion that the real store satisfies the scheduler's Store
// interface. If this fails to compile, the interface drifted from
// internal/database — fix the interface to match the store, never the reverse.
var _ Store = (*database.Store)(nil)

// Config holds the tunables shared by the Planner, Worker, and Daemon.
type Config struct {
	// DownloadsDir is the root under which per-follow folders are created.
	DownloadsDir string
	// LibraryDir, when non-empty, is the root the Worker PLACES each finished
	// lesson into (single location) at the lesson's path relative to
	// DownloadsDir: a rename on the same filesystem, a copy across filesystems.
	// Empty places it in DownloadsDir itself. Either way a download is written
	// in its job's private folder under DownloadsDir first (see private.go).
	LibraryDir string
	// Layout selects the library destination layout (lower-cased upstream). ""
	// or "default" keeps today's per-lesson-subfolder layout; "plex-tv" emits
	// Plex's TV-Shows naming when LibraryDir is set. It shapes ONLY the move
	// target; the scratch download layout under DownloadsDir is unchanged.
	Layout string
	// Quality overrides each follow's saved quality when non-empty; empty means
	// use the follow's own quality.
	Quality string
	// AudioLang is the preferred audio-track language (ISO code, e.g. "en") passed
	// to yt-dlp's format selector so multi-audio lessons download that language
	// rather than a dub. Empty disables the preference (yt-dlp's default pick). It
	// is global (env-only, DRUMDROP_AUDIO_LANG) — there is no per-follow override.
	AudioLang string
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
