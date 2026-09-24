package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/elienop/drumdrop/internal/config"
	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/engine"
	"github.com/elienop/drumdrop/internal/musora"
	"github.com/elienop/drumdrop/internal/scheduler"
)

// errCoachLinkAsNode is what followNode answers for a coach-page link.
var errCoachLinkAsNode = errors.New("follow: that's a coach page, not a lesson or course; follow the instructor with `drumdrop follow @<link>` or --instructor <link>")

// followArgs holds the parsed positionals and flag values for the follow
// command. Extracted so cmdFollow and its tests drive the same parser.
type followArgs struct {
	positionals []string
	brand       string
	quality     string
	instructor  string
}

// parseFollowArgs splits argv into positionals and flags (so flags may appear
// after the positional target — see splitArgs) and parses the follow flags. This
// is the exact parse cmdFollow runs; tests call it so a parser regression is
// caught against production code rather than a re-implementation.
func parseFollowArgs(argv []string) (followArgs, error) {
	fs := flag.NewFlagSet("drumdrop follow", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	// brand defaults to "" so an instructor follow can tell a --brand given
	// from none, when a pasted coach-page link names its own.
	brand := fs.String("brand", "", "musora brand (drumeo|pianote|guitareo|singeo|playbass); default drumeo, or a coach link's")
	quality := fs.String("quality", "best", "best|2160|1440|1080|720|480")
	instructor := fs.String("instructor", "", "follow an instructor by name, slug or coach-page link")

	positionals, flags := splitArgs(argv)
	if err := fs.Parse(flags); err != nil {
		return followArgs{}, err
	}
	return followArgs{
		positionals: positionals,
		brand:       *brand,
		quality:     *quality,
		instructor:  *instructor,
	}, nil
}

// instructorInput returns what was typed for an instructor follow, and whether
// this is one: --instructor wins, else a leading @ on the first positional.
// The @ stays in what it returns: the normaliser drops one, as it does for the
// web's preview and add, so "@@jared-falk" is refused on every path. A bare
// "@" is an instructor follow with nothing typed, which the normaliser
// refuses, rather than a node follow of "@".
func instructorInput(args followArgs) (string, bool) {
	if args.instructor != "" {
		return args.instructor, true
	}
	if len(args.positionals) > 0 && strings.HasPrefix(args.positionals[0], "@") {
		return args.positionals[0], true
	}
	return "", false
}

// cmdFollow records a node follow (bare id / Musora URL) or an instructor follow
// (leading @, or --instructor). Both are idempotent: re-following an
// already-followed target reports "already following" rather than erroring.
func cmdFollow(argv []string) error {
	args, err := parseFollowArgs(argv)
	if err != nil {
		return err
	}
	input, isInstructor := instructorInput(args)
	if !isInstructor && len(args.positionals) == 0 {
		return fmt.Errorf("follow: provide a lesson/course id or URL, or @slug / --instructor slug")
	}

	store, err := engine.OpenStore()
	if err != nil {
		return err
	}
	defer store.Close()
	ctx := context.Background()

	if isInstructor {
		return followInstructor(ctx, store, input, args.brand, args.quality)
	}
	return followNode(ctx, store, args.positionals[0], args.brand, args.quality)
}

// followNode resolves a best-effort title for the node id (an empty title is
// acceptable) and records a node follow. The brand is settled as the web's
// add settles it (musora.NodeBrand): any case, empty for the default, and one
// Musora doesn't have is refused before Musora is asked, since every lesson of
// the follow is stored with it. A coach-page link
// is refused too: its number is the instructor's, not a lesson's or course's.
func followNode(ctx context.Context, store *database.Store, target, brand, quality string) error {
	brand, err := musora.NodeBrand(brand)
	if err != nil {
		return fmt.Errorf("follow: --brand must be drumeo, pianote, guitareo, singeo or playbass: %w", err)
	}
	if musora.IsCoachLink(target) {
		return errCoachLinkAsNode
	}
	id := engine.ExtractID(target)
	if id == 0 {
		return fmt.Errorf("could not parse a content id from: %s", target)
	}

	// Best-effort title: the lesson/container's own title, falling back to its
	// parent course name. Failure here is non-fatal — an empty title is fine.
	title := ""
	if lesson, err := musora.ResolveLesson(id, engine.PermissionIDs()); err == nil && lesson != nil {
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

// followInstructor normalises what was typed and settles the brand exactly as
// the web's preview and add do (musora.NormalizeInstructor), looks up the
// instructor's display name, and records an instructor follow of the
// normalised slug.
func followInstructor(ctx context.Context, store *database.Store, input, brand, quality string) error {
	slug, brand, err := musora.NormalizeInstructor(input, brand)
	if errors.Is(err, musora.ErrBadSlug) {
		return fmt.Errorf("follow: enter an instructor's name, like @'Jared Falk', slug, like @jared-falk, or coach-page link: %w", err)
	}
	if err != nil {
		return fmt.Errorf("follow: %w", err)
	}
	id, name, ok, err := musora.ResolveInstructorID(slug, brand)
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

	store, err := engine.OpenStore()
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
	store, err := engine.OpenStore()
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
//
// sync is the one-shot equivalent of the daemon's per-cycle work: build a
// scheduler Planner + Worker over the real store and adapters (via engine.Build),
// plan once (record + enqueue new lessons), then drain the queue once (download
// them, with retry). The behavioral guarantees — skip already-downloaded, dedupe
// active jobs, cap NEW downloads via --limit, never abort on one bad lesson, and
// dry-run records but downloads nothing — live in and are tested by the scheduler
// package.
//
// sync runs no startup recovery (Daemon.Recover), on purpose: it may run while
// a daemon or serve works on the same database and folders, and it can not
// tell that process's download from a crashed one. Worker.SweepPrivate keeps
// only the private folders of jobs its own worker is running, which for sync is
// none, so it would remove the other process's download in progress. The jobs
// table is no substitute: a job canceled or removed meanwhile still uses its
// folder until its worker has undone its placement, and a queued one can be
// claimed between the check and the removal. Requeueing running jobs would
// requeue the other process's. What a crash left is cleared by the next daemon
// or serve start. An interrupt (Ctrl-C, SIGTERM) is not a crash: it stops the
// download in progress, and the worker removes its private folder.

// cmdSync parses the sync flags, builds a scheduler Planner + Worker over the
// real store and adapters, and runs one plan+drain cycle (the daemon's per-cycle
// work, one-shot). --dry-run records what would be downloaded but enqueues and
// downloads nothing; otherwise --limit caps the number of NEW downloads.
func cmdSync(argv []string) error {
	fs := flag.NewFlagSet("drumdrop sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "", "output directory (default DRUMDROP_DOWNLOADS_DIR or ./downloads)")
	quality := fs.String("quality", config.Quality(), "override each follow's quality (best|2160|1440|1080|720|480)")
	limit := fs.Int("limit", 0, "cap NEW downloads this run (0 = unlimited)")
	dryRun := fs.Bool("dry-run", false, "expand + record, download nothing")
	resourcesOnly := fs.Bool("resources-only", false, "skip video; fetch resources only")

	_, flags := splitArgs(argv)
	if err := fs.Parse(flags); err != nil {
		return err
	}

	store, err := engine.OpenStore()
	if err != nil {
		return err
	}
	defer store.Close()

	cfg, err := engine.Config(*out, *quality, *resourcesOnly)
	if err != nil {
		return err
	}
	planner, worker, _ := engine.Build(store, cfg, engine.PermissionIDs(), os.Stdout, nil)

	// yt-dlp runs in its own process group, so a Ctrl-C at the terminal never
	// reaches it: without this, drumdrop would die and leave it running on its
	// own, writing into the job's private folder.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runSync(ctx, planner, worker, *dryRun, *limit, os.Stdout)
}

// runSync executes one sync cycle, decoupled from flag parsing so tests can drive
// it with fakes. Dry-run reports what would be downloaded but enqueues and
// downloads nothing (a queued job would otherwise be drained later by a daemon).
// A real run plans (records + enqueues new lessons, deduped) then drains the
// queue, downloading up to limit NEW lessons.
func runSync(ctx context.Context, planner *scheduler.Planner, worker *scheduler.Worker, dryRun bool, limit int, w io.Writer) error {
	if dryRun {
		enqueued, err := planner.PlanDryRun(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "\n[dry-run] %d new lesson(s) would be queued; downloaded nothing\n", enqueued)
		return nil
	}

	planned, err := planner.Plan(ctx, limit)
	if err != nil {
		return err
	}
	processed, err := worker.RunOnce(ctx, limit)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		// The stopped job stays marked running (Worker.shuttingDown), and only
		// a daemon or serve start requeues it.
		fmt.Fprintf(w, "\nSync interrupted — queued %d, processed %d\n", planned, processed)
		return errors.New("sync interrupted: a download in progress was stopped; it starts over the next time daemon or serve starts")
	}
	fmt.Fprintf(w, "\nSync complete — queued %d, downloaded %d\n", planned, processed)
	return nil
}
