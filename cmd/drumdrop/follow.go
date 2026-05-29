package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elienop/drumdrop/internal/config"
	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// openStore prepares the config directory, opens the SQLite database with the
// drumdrop pragmas, runs any pending migrations, and returns a ready Store.
// Every persistence verb calls this and defers store.Close().
func openStore() (*database.Store, error) {
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

// cmdFollow records a node follow (bare id / Musora URL) or an instructor follow
// (leading @slug, or --instructor slug). Both are idempotent: re-following an
// already-followed target reports "already following" rather than erroring.
func cmdFollow(argv []string) error {
	fs := flag.NewFlagSet("drumdrop follow", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	brand := fs.String("brand", "drumeo", "musora brand (drumeo|pianote|guitareo|singeo|playbass)")
	quality := fs.String("quality", "best", "best|2160|1440|1080|720|480")
	instructor := fs.String("instructor", "", "follow an instructor by slug")

	positionals, flags := splitArgs(argv)
	if err := fs.Parse(flags); err != nil {
		return err
	}

	// Determine the slug for an instructor follow: --instructor <slug> wins, else
	// a leading @slug positional.
	slug := *instructor
	if slug == "" && len(positionals) > 0 && strings.HasPrefix(positionals[0], "@") {
		slug = strings.TrimPrefix(positionals[0], "@")
	}

	store, err := openStore()
	if err != nil {
		return err
	}
	defer store.Close()
	ctx := context.Background()

	if slug != "" {
		return followInstructor(ctx, store, slug, *brand, *quality)
	}

	if len(positionals) == 0 {
		return fmt.Errorf("follow: provide a lesson/course id or URL, or @slug / --instructor slug")
	}
	return followNode(ctx, store, positionals[0], *brand, *quality)
}

// followNode resolves a best-effort title for the node id (an empty title is
// acceptable) and records a node follow.
func followNode(ctx context.Context, store *database.Store, target, brand, quality string) error {
	id := extractID(target)
	if id == 0 {
		return fmt.Errorf("could not parse a content id from: %s", target)
	}

	// Best-effort title: the lesson/container's own title, falling back to its
	// parent course name. Failure here is non-fatal — an empty title is fine.
	title := ""
	if lesson, err := musora.ResolveLesson(id, permissionIDs()); err == nil && lesson != nil {
		title = lesson.Title
		if title == "" && len(lesson.ParentContentData) > 0 {
			title = lesson.ParentContentData[0].Title
		}
	}

	f, err := store.AddNodeFollow(ctx, id, title, brand, quality)
	if err != nil {
		if err == database.ErrAlreadyFollowing {
			fmt.Printf("• already following node %d (follow #%d)\n", id, f.ID)
			return nil
		}
		return err
	}
	fmt.Printf("✓ following node %d%s (follow #%d)\n", id, titleSuffix(title), f.ID)
	return nil
}

// followInstructor validates the slug, looks up the instructor's display name,
// and records an instructor follow.
func followInstructor(ctx context.Context, store *database.Store, slug, brand, quality string) error {
	id, name, ok, err := musora.ResolveInstructorID(slug)
	if err != nil {
		return err
	}
	if !ok || id == "" {
		return fmt.Errorf("no instructor found for slug %q", slug)
	}

	f, err := store.AddInstructorFollow(ctx, slug, name, brand, quality)
	if err != nil {
		if err == database.ErrAlreadyFollowing {
			fmt.Printf("• already following @%s (follow #%d)\n", slug, f.ID)
			return nil
		}
		return err
	}
	fmt.Printf("✓ following @%s%s (follow #%d)\n", slug, titleSuffix(name), f.ID)
	return nil
}

// titleSuffix formats a non-empty title as " — title" for the follow confirmation.
func titleSuffix(title string) string {
	if title == "" {
		return ""
	}
	return " — " + title
}

// cmdUnfollow removes a follow by its follows.id. It errors if the id is unknown.
func cmdUnfollow(argv []string) error {
	positionals, _ := splitArgs(argv)
	if len(positionals) == 0 {
		return fmt.Errorf("unfollow: provide a follow id (see `drumdrop follows`)")
	}
	id, err := strconv.ParseInt(positionals[0], 10, 64)
	if err != nil {
		return fmt.Errorf("unfollow: %q is not a valid follow id", positionals[0])
	}

	store, err := openStore()
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.RemoveFollow(context.Background(), id); err != nil {
		return err
	}
	fmt.Printf("✓ unfollowed #%d\n", id)
	return nil
}

// cmdFollows lists every follow with its id, kind, target, title, brand,
// quality, and last-synced timestamp.
func cmdFollows() error {
	store, err := openStore()
	if err != nil {
		return err
	}
	defer store.Close()

	follows, err := store.ListFollows(context.Background())
	if err != nil {
		return err
	}
	printFollows(os.Stdout, follows)
	return nil
}

// printFollows renders the follows table. Split out from cmdFollows so it is
// testable without a real Store.
func printFollows(w io.Writer, follows []database.Follow) {
	if len(follows) == 0 {
		fmt.Fprintln(w, "No follows yet. Add one with `drumdrop follow <id|url>` or `drumdrop follow @<slug>`.")
		return
	}
	fmt.Fprintf(w, "%-4s  %-10s  %-14s  %-8s  %-6s  %-19s  %s\n",
		"ID", "KIND", "TARGET", "BRAND", "QUAL", "LAST SYNCED", "TITLE")
	for _, f := range follows {
		fmt.Fprintf(w, "%-4d  %-10s  %-14s  %-8s  %-6s  %-19s  %s\n",
			f.ID, f.Kind, followTarget(f), f.Brand, f.Quality, lastSynced(f), f.Title)
	}
}

// followTarget renders a follow's natural key: railcontent_id for nodes, @slug
// for instructors.
func followTarget(f database.Follow) string {
	if f.Kind == "instructor" && f.Slug.Valid {
		return "@" + f.Slug.String
	}
	if f.RailcontentID.Valid {
		return strconv.FormatInt(f.RailcontentID.Int64, 10)
	}
	return "-"
}

// lastSynced renders the last-synced timestamp, or "never" when unset.
func lastSynced(f database.Follow) string {
	if !f.LastSyncedAt.Valid {
		return "never"
	}
	return f.LastSyncedAt.Time.Format("2006-01-02 15:04:05")
}

// ---- sync ----------------------------------------------------------------

// expander turns one follow into the ids of the lessons under it. Node follows
// expand via the catalog walk; instructor follows via the instructor-lessons
// query. Defined as an interface so sync is testable offline.
type expander interface {
	Expand(f database.Follow, permIDs string) (ids []int, err error)
}

// resolver fetches the full lesson metadata needed to download. A nil lesson
// (with nil error) means the lesson could not be resolved (gated/missing) and
// must be skipped, matching musora.ResolveLesson's contract.
type resolver interface {
	Resolve(id int, permIDs string) (*musora.Lesson, error)
}

// downloader performs the actual download of a resolved lesson.
type downloader interface {
	Download(l *musora.Lesson, o musora.DownloadOpts) error
}

// syncStore is the subset of the database Store that sync mutates. Declaring it
// as an interface lets tests inject a fake with no real database.
type syncStore interface {
	ListFollows(ctx context.Context) ([]database.Follow, error)
	UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string) error
	IsDownloaded(ctx context.Context, id int) (bool, error)
	EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (int64, error)
	MarkJobRunning(ctx context.Context, id int64) error
	MarkJobDone(ctx context.Context, id int64) error
	MarkJobFailed(ctx context.Context, id int64, errMsg string) error
	MarkDownloading(ctx context.Context, id int) error
	MarkDownloaded(ctx context.Context, id int, quality, outputDir, videoPath string, bytes int64) error
	MarkFailed(ctx context.Context, id int, errMsg string) error
	MarkSkipped(ctx context.Context, id int, reason string) error
	TouchLastSynced(ctx context.Context, id int64) error
}

// syncDeps bundles everything runSync needs so the orchestration is decoupled
// from the network and yt-dlp for testing.
type syncDeps struct {
	store      syncStore
	expander   expander
	resolver   resolver
	downloader downloader
	permIDs    string
}

// syncOpts holds the parsed sync flags.
type syncOpts struct {
	out           string
	quality       string
	limit         int // 0 = unlimited
	dryRun        bool
	resourcesOnly bool
}

// syncSummary accumulates per-run counts for the grand summary.
type syncSummary struct {
	follows  int
	seen     int // lessons discovered (after dedup within a follow's expansion)
	newDL    int // new downloads that succeeded
	skipped  int // already-downloaded or could-not-resolve
	failed   int // download attempts that errored
	limitHit bool
}

// shouldDownload is the pure per-lesson decision: a lesson is downloaded only
// when it is not already downloaded and we are not in dry-run mode. The
// already-downloaded check is the dedup chokepoint; dry-run downloads nothing.
func shouldDownload(isDownloaded, dryRun bool) bool {
	return !isDownloaded && !dryRun
}

// realExpander adapts the musora package to the expander interface.
type realExpander struct{}

func (realExpander) Expand(f database.Follow, permIDs string) ([]int, error) {
	switch f.Kind {
	case "node":
		if !f.RailcontentID.Valid {
			return nil, fmt.Errorf("node follow #%d has no railcontent_id", f.ID)
		}
		_, ids, err := musora.ResolveLessonIDs(int(f.RailcontentID.Int64), false, permIDs)
		return ids, err
	case "instructor":
		if !f.Slug.Valid {
			return nil, fmt.Errorf("instructor follow #%d has no slug", f.ID)
		}
		refs, err := musora.InstructorLessons(f.Slug.String, f.Brand, permIDs)
		if err != nil {
			return nil, err
		}
		ids := make([]int, 0, len(refs))
		for _, r := range refs {
			ids = append(ids, r.ID)
		}
		return ids, nil
	default:
		return nil, fmt.Errorf("unknown follow kind %q", f.Kind)
	}
}

// realResolver adapts musora.ResolveLesson.
type realResolver struct{}

func (realResolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	return musora.ResolveLesson(id, permIDs)
}

// realDownloader adapts musora.DownloadLesson.
type realDownloader struct{}

func (realDownloader) Download(l *musora.Lesson, o musora.DownloadOpts) error {
	return musora.DownloadLesson(l, o)
}

// cmdSync parses the sync flags, wires the real dependencies, and runs sync.
func cmdSync(argv []string) error {
	fs := flag.NewFlagSet("drumdrop sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "./downloads", "output directory")
	quality := fs.String("quality", "", "override each follow's quality (best|2160|1440|1080|720|480)")
	limit := fs.Int("limit", 0, "cap NEW downloads this run (0 = unlimited)")
	dryRun := fs.Bool("dry-run", false, "expand + record, download nothing")
	resourcesOnly := fs.Bool("resources-only", false, "skip video; fetch resources only")

	_, flags := splitArgs(argv)
	if err := fs.Parse(flags); err != nil {
		return err
	}

	store, err := openStore()
	if err != nil {
		return err
	}
	defer store.Close()

	deps := syncDeps{
		store:      store,
		expander:   realExpander{},
		resolver:   realResolver{},
		downloader: realDownloader{},
		permIDs:    permissionIDs(),
	}
	opts := syncOpts{
		out:           *out,
		quality:       *quality,
		limit:         *limit,
		dryRun:        *dryRun,
		resourcesOnly: *resourcesOnly,
	}
	return runSync(context.Background(), deps, opts, os.Stdout)
}

// runSync is the orchestration core, decoupled from process state so tests can
// drive it with fakes. For each follow it expands to lesson ids, upserts each
// (never downgrading an already-downloaded lesson), skips downloaded ones, and
// — unless dry-run — resolves + downloads up to --limit NEW lessons. One bad
// lesson never aborts the run. Each follow is stamped via TouchLastSynced.
func runSync(ctx context.Context, deps syncDeps, opts syncOpts, w io.Writer) error {
	follows, err := deps.store.ListFollows(ctx)
	if err != nil {
		return err
	}
	if len(follows) == 0 {
		fmt.Fprintln(w, "No follows to sync. Add one with `drumdrop follow …`.")
		return nil
	}

	var sum syncSummary
	sum.follows = len(follows)

	for _, f := range follows {
		fmt.Fprintf(w, "\n▶ follow #%d %s %s%s\n", f.ID, f.Kind, followTarget(f), titleSuffix(f.Title))

		ids, err := deps.expander.Expand(f, deps.permIDs)
		if err != nil {
			fmt.Fprintf(w, "  ⚠ expand failed: %v\n", err)
			// Still touch last-synced? No — expansion failure means we did not
			// process this follow; leave its timestamp so it retries next run.
			continue
		}

		folderTitle := f.Title
		if folderTitle == "" {
			folderTitle = followTarget(f)
		}
		outDir := filepath.Join(opts.out, musora.Sanitize(folderTitle))

		quality := opts.quality
		if quality == "" {
			quality = f.Quality
		}

		var parent sql.NullInt64
		if f.Kind == "node" && f.RailcontentID.Valid {
			parent = f.RailcontentID
		}

		fNew, fSkip, fFail := 0, 0, 0
		for i, id := range ids {
			sum.seen++

			if err := deps.store.UpsertLesson(ctx, id, "", parent, f.Brand); err != nil {
				fmt.Fprintf(w, "  ✖ upsert lesson %d: %v\n", id, err)
				fFail++
				sum.failed++
				continue
			}

			done, err := deps.store.IsDownloaded(ctx, id)
			if err != nil {
				fmt.Fprintf(w, "  ✖ check lesson %d: %v\n", id, err)
				fFail++
				sum.failed++
				continue
			}
			if done {
				fmt.Fprintf(w, "  ↳ already downloaded %d\n", id)
				fSkip++
				sum.skipped++
				continue
			}

			if !shouldDownload(done, opts.dryRun) {
				// dry-run: recorded via upsert, downloaded nothing.
				fmt.Fprintf(w, "  · [dry-run] would download %d\n", id)
				fNew++ // would-be new download
				continue
			}

			// Respect --limit on NEW downloads across the whole run.
			if opts.limit > 0 && sum.newDL >= opts.limit {
				sum.limitHit = true
				fmt.Fprintf(w, "  ⏸ limit %d reached; stopping\n", opts.limit)
				break
			}

			if downloadOne(ctx, deps, f, id, i+1, outDir, quality, opts.resourcesOnly, w) {
				fNew++
				sum.newDL++
			} else {
				fFail++
				sum.failed++
			}
		}

		if err := deps.store.TouchLastSynced(ctx, f.ID); err != nil {
			fmt.Fprintf(w, "  ⚠ touch last_synced: %v\n", err)
		}
		fmt.Fprintf(w, "  follow #%d: new %d, skipped %d, failed %d\n", f.ID, fNew, fSkip, fFail)

		if sum.limitHit {
			break
		}
	}

	fmt.Fprintf(w, "\nSync complete — follows %d, seen %d, new %d, skipped %d, failed %d\n",
		sum.follows, sum.seen, sum.newDL, sum.skipped, sum.failed)
	return nil
}

// downloadOne runs the full enqueue→resolve→download→mark pipeline for a single
// lesson and reports whether it counts as a new successful download. It never
// returns an error: a failure is recorded on the lesson + job and logged, so the
// caller can keep going.
func downloadOne(
	ctx context.Context, deps syncDeps, f database.Follow,
	id, index int, outDir, quality string, resourcesOnly bool, w io.Writer,
) (ok bool) {
	jobID, err := deps.store.EnqueueJob(ctx, sql.NullInt64{Int64: f.ID, Valid: true}, id)
	if err != nil {
		fmt.Fprintf(w, "  ✖ enqueue %d: %v\n", id, err)
		return false
	}
	if err := deps.store.MarkJobRunning(ctx, jobID); err != nil {
		fmt.Fprintf(w, "  ✖ start job for %d: %v\n", id, err)
		return false
	}

	lesson, err := deps.resolver.Resolve(id, deps.permIDs)
	if err != nil || lesson == nil {
		reason := "could not resolve (gated or missing)"
		if err != nil {
			reason = err.Error()
		}
		fmt.Fprintf(w, "  ↳ skipping %d: %s\n", id, reason)
		_ = deps.store.MarkSkipped(ctx, id, reason)
		_ = deps.store.MarkJobFailed(ctx, jobID, reason)
		return false
	}

	if err := deps.store.MarkDownloading(ctx, id); err != nil {
		fmt.Fprintf(w, "  ✖ mark downloading %d: %v\n", id, err)
		_ = deps.store.MarkJobFailed(ctx, jobID, err.Error())
		return false
	}

	fmt.Fprintf(w, "  ▼ [%02d] %d %s\n", index, id, lesson.Title)
	dlErr := deps.downloader.Download(lesson, musora.DownloadOpts{
		Dir:           outDir,
		Index:         index,
		Quality:       quality,
		ResourcesOnly: resourcesOnly,
	})
	if dlErr != nil {
		fmt.Fprintf(w, "  ✖ download %d failed: %v\n", id, dlErr)
		_ = deps.store.MarkFailed(ctx, id, dlErr.Error())
		_ = deps.store.MarkJobFailed(ctx, jobID, dlErr.Error())
		return false
	}

	lessonDir := filepath.Join(outDir, fmt.Sprintf("%02d - %s", index, musora.Sanitize(lesson.Title)))
	if err := deps.store.MarkDownloaded(ctx, id, quality, lessonDir, "", 0); err != nil {
		fmt.Fprintf(w, "  ⚠ mark downloaded %d: %v\n", id, err)
	}
	if err := deps.store.MarkJobDone(ctx, jobID); err != nil {
		fmt.Fprintf(w, "  ⚠ mark job done for %d: %v\n", id, err)
	}
	fmt.Fprintf(w, "  ✓ %d\n", id)
	return true
}
