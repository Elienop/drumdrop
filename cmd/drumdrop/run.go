package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/elienop/drumdrop/internal/config"
	"github.com/elienop/drumdrop/internal/engine"
	"github.com/elienop/drumdrop/internal/musora"
	"github.com/elienop/drumdrop/internal/scheduler"
)

// valueFlags lists the download flags that consume the following argument.
// Used to split positionals from flags so that flags may appear after the id
// (Go's flag.Parse stops at the first non-flag token; the Node reference loops
// over all argv regardless of position — see src/cli.mjs parseArgs).
var valueFlags = map[string]bool{
	"--out":        true,
	"--quality":    true,
	"--limit":      true,
	"--brand":      true,
	"--instructor": true,
	"--interval":   true,
	"--listen":     true,
	"-out":         true,
	"-quality":     true,
	"-limit":       true,
	"-brand":       true,
	"-instructor":  true,
	"-interval":    true,
	"-listen":      true,
}

// splitArgs separates positional arguments from flag tokens, preserving order
// within each group, so flags work regardless of where they sit relative to
// the positional content id. A flag of the form --name=value is self-contained;
// a value flag (e.g. --out dir) carries the following token along with it.
func splitArgs(argv []string) (positionals, flags []string) {
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && valueFlags[a] && i+1 < len(argv) {
				i++
				flags = append(flags, argv[i])
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return positionals, flags
}

// downloadArgs holds the parsed positionals and flag values for the download
// command. Extracted so cmdDownload and its tests drive the same parser.
type downloadArgs struct {
	positionals   []string
	out           string
	quality       string
	limit         int
	whole         bool
	resourcesOnly bool
	dryRun        bool
}

// parseDownloadArgs splits argv into positionals and flags (so flags may appear
// after the positional id — see splitArgs) and parses the download flags. This
// is the exact parse cmdDownload runs; tests call it so a parser regression is
// caught against production code rather than a re-implementation.
func parseDownloadArgs(argv []string) (downloadArgs, error) {
	fs := flag.NewFlagSet("drumdrop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "./downloads", "output directory")
	quality := fs.String("quality", "best", "best|2160|1440|1080|720|480")
	limit := fs.Int("limit", 0, "only the first N lessons")
	whole := fs.Bool("whole-course", false, "walk up to the parent course")
	resourcesOnly := fs.Bool("resources-only", false, "skip video; fetch resources only")
	dryRun := fs.Bool("dry-run", false, "list what would be downloaded")

	// Parse flags from the whole arg list, not just the leading run, so a flag
	// placed after the positional id (e.g. `drumdrop 409875 --dry-run`) is honored.
	positionals, flags := splitArgs(argv)
	if err := fs.Parse(flags); err != nil {
		return downloadArgs{}, err
	}
	// splitArgs routes every non-flag token into positionals and passes only
	// flags to fs.Parse, so fs.Args() is always empty here.
	return downloadArgs{
		positionals:   positionals,
		out:           *out,
		quality:       *quality,
		limit:         *limit,
		whole:         *whole,
		resourcesOnly: *resourcesOnly,
		dryRun:        *dryRun,
	}, nil
}

// cmdDownload resolves a lesson or course and downloads each lesson.
func cmdDownload(argv []string) error {
	args, err := parseDownloadArgs(argv)
	if err != nil {
		return err
	}
	out := &args.out
	quality := &args.quality
	limit := &args.limit
	whole := &args.whole
	resourcesOnly := &args.resourcesOnly
	dryRun := &args.dryRun

	rest := args.positionals
	if len(rest) == 0 {
		fmt.Print(usage)
		os.Exit(1)
	}

	targetID := engine.ExtractID(rest[0])
	if targetID == 0 {
		return fmt.Errorf("could not parse a content id from: %s", rest[0])
	}

	permIDs := engine.PermissionIDs()
	if *whole {
		fmt.Printf("Resolving whole course for id %d …\n", targetID)
	} else {
		fmt.Printf("Resolving target id %d …\n", targetID)
	}
	rootID, lessonItems, err := musora.ResolveLessonIDs(targetID, *whole, permIDs)
	if err != nil {
		return err
	}
	if len(lessonItems) == 0 {
		return fmt.Errorf("no lessons found (content gated, or unknown id)")
	}
	ids := make([]int, len(lessonItems))
	for i, item := range lessonItems {
		ids[i] = item.ID
	}
	if *limit > 0 && *limit < len(ids) {
		ids = ids[:*limit]
	}
	if *limit > 0 {
		fmt.Printf("Found %d lesson(s) under root %d, taking first %d.\n", len(lessonItems), rootID, len(ids))
	} else {
		fmt.Printf("Found %d lesson(s) under root %d.\n", len(lessonItems), rootID)
	}

	// Resolve metadata first (gives titles + the course/series name for foldering).
	resolved := make([]resolvedLesson, 0, len(ids))
	courseTitle := ""
	for i, id := range ids {
		lesson, err := musora.ResolveLesson(id, permIDs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠  resolve %d failed: %v\n", id, err)
		}
		if lesson == nil {
			resolved = append(resolved, resolvedLesson{id: id, failed: true})
			continue
		}
		if courseTitle == "" && len(lesson.ParentContentData) > 0 && lesson.ParentContentData[0].Title != "" {
			courseTitle = lesson.ParentContentData[0].Title
		}
		noVideo := ""
		if lesson.Video.HLSManifestURL == "" {
			noVideo = "  (no video)"
		}
		fmt.Printf("  [%02d/%d] %d  %s%s\n", i+1, len(ids), id, lesson.Title, noVideo)
		resolved = append(resolved, resolvedLesson{id: id, lesson: lesson, index: i + 1})
	}

	if courseTitle == "" {
		courseTitle = fmt.Sprintf("content-%d", rootID)
	}
	outDir := filepath.Join(*out, musora.Sanitize(courseTitle))

	if *dryRun {
		downloadable := 0
		for _, r := range resolved {
			if !r.failed {
				downloadable++
			}
		}
		fmt.Printf("\n[dry-run] would download %d lesson(s) into: %s\n", downloadable, outDir)
		return nil
	}

	// An interrupt (Ctrl-C, SIGTERM) stops the download in progress: yt-dlp runs
	// in its own process group, so it would otherwise outlive drumdrop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	downloaded, failed := downloadResolved(ctx, engine.Downloader{}, *out, musora.Sanitize(courseTitle), resolved, musora.DownloadOpts{
		// AudioLang is env-only (DRUMDROP_AUDIO_LANG, default "en") like the
		// daemon/serve path, so the manual one-shot download prefers the same
		// audio language rather than yt-dlp's default multi-track pick.
		Quality:       *quality,
		AudioLang:     config.AudioLang(),
		ResourcesOnly: *resourcesOnly,
	}, console{stdout: os.Stdout, stderr: os.Stderr})

	fmt.Printf("\nDone → %s\n  downloaded: %d  failed: %d\n", outDir, downloaded, failed)
	if ctx.Err() != nil {
		return errors.New("interrupted: a download in progress was stopped and not placed; its lesson folder is as it was")
	}
	return nil
}

// resolvedLesson is one lesson of a one-shot download: its metadata, or
// failed when it could not be resolved.
type resolvedLesson struct {
	id     int
	lesson *musora.Lesson
	index  int
	failed bool
}

// console is where a one-shot download reports: progress to stdout, failures
// to stderr.
type console struct {
	stdout, stderr io.Writer
}

// downloadResolved downloads each resolved lesson into <out>/<course>/NN -
// Title, one at a time, and counts the outcomes. Each download goes through
// scheduler.DownloadOneShot: it is written in a private folder and placed
// only once it finished, so a failed or interrupted one leaves the lesson's
// folder as it was. Once ctx is done no further lesson starts.
func downloadResolved(ctx context.Context, d scheduler.Downloader, out, course string, lessons []resolvedLesson, opts musora.DownloadOpts, con console) (downloaded, failed int) {
	for _, r := range lessons {
		if r.failed {
			failed++
			continue
		}
		if ctx.Err() != nil {
			fmt.Fprintf(con.stderr, "✖  lesson %d not downloaded: interrupted\n", r.id)
			failed++
			continue
		}
		fmt.Fprintf(con.stdout, "\n▼ [%02d] %s\n", r.index, r.lesson.Title)
		o := opts
		o.Index = r.index
		replaced, err := scheduler.DownloadOneShot(ctx, d, r.lesson, out, course, o)
		for _, p := range replaced {
			fmt.Fprintf(con.stdout, "  ↻ replaced %s\n", p)
		}
		if err != nil {
			failed++
			fmt.Fprintf(con.stderr, "✖  lesson %d failed: %v\n", r.id, err)
			continue
		}
		downloaded++
		fmt.Fprintf(con.stdout, "  ✓ lesson %d\n", r.id)
	}
	return downloaded, failed
}

// stdin is a single shared reader so that bytes buffered by one prompt (e.g. a
// password line that arrives in the same OS pipe write as the email) survive
// into the next prompt. Constructing a fresh bufio.Reader per call would drop
// already-buffered piped input and read EOF on the second prompt.
var stdin = bufio.NewReader(os.Stdin)

// prompt reads a single trimmed line from stdin. For non-interactive use, set
// MUSORA_EMAIL / MUSORA_PASSWORD instead of typing.
func prompt(question string) (string, error) {
	fmt.Print(question)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func cmdLogin() error {
	email := os.Getenv("MUSORA_EMAIL")
	if email == "" {
		var err error
		if email, err = prompt("Musora email: "); err != nil {
			return err
		}
	}
	password := os.Getenv("MUSORA_PASSWORD")
	if password == "" {
		var err error
		if password, err = prompt("Musora password: "); err != nil {
			return err
		}
	}
	if _, err := musora.Login(email, password); err != nil {
		return err
	}
	if err := musora.SaveCreds(email, password); err != nil {
		return err
	}
	fmt.Printf("✓ Logged in as %s. Session + credentials saved.\n", email)
	return nil
}

func cmdWhoami() error {
	cookie := musora.LoadCookie()
	if cookie == "" {
		return fmt.Errorf("not logged in — run `drumdrop login` first")
	}
	ok, err := musora.Me(cookie)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session expired — run `drumdrop login`")
	}
	fmt.Println("Logged in (session valid).")
	return nil
}

func cmdLogout() error {
	for _, p := range []string{config.CookiePath(), config.CredsPath()} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	fmt.Println("✓ Logged out (cleared saved session + credentials).")
	return nil
}
