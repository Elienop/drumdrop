package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/elienop/drumdrop/internal/config"
	"github.com/elienop/drumdrop/internal/musora"
)

var reDigits = regexp.MustCompile(`\d+`)

// extractID accepts a bare numeric id or a Musora URL and returns the last
// numeric run found. Returns 0 when no digits are present.
func extractID(input string) int {
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

func permissionIDs() string { return os.Getenv("DRUMDROP_PERMISSION_IDS") }

// valueFlags lists the download flags that consume the following argument.
// Used to split positionals from flags so that flags may appear after the id
// (Go's flag.Parse stops at the first non-flag token; the Node reference loops
// over all argv regardless of position — see src/cli.mjs parseArgs).
var valueFlags = map[string]bool{
	"--out":     true,
	"--quality": true,
	"--limit":   true,
	"-out":      true,
	"-quality":  true,
	"-limit":    true,
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

// cmdDownload resolves a lesson or course and downloads each lesson.
func cmdDownload(argv []string) error {
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
		return err
	}
	// splitArgs routes every non-flag token into positionals and passes only
	// flags to fs.Parse, so fs.Args() is always empty here.
	rest := positionals
	if len(rest) == 0 {
		fmt.Print(usage)
		os.Exit(1)
	}

	targetID := extractID(rest[0])
	if targetID == 0 {
		return fmt.Errorf("could not parse a content id from: %s", rest[0])
	}

	permIDs := permissionIDs()
	if *whole {
		fmt.Printf("Resolving whole course for id %d …\n", targetID)
	} else {
		fmt.Printf("Resolving target id %d …\n", targetID)
	}
	rootID, lessonIDs, err := musora.ResolveLessonIDs(targetID, *whole, permIDs)
	if err != nil {
		return err
	}
	if len(lessonIDs) == 0 {
		return fmt.Errorf("no lessons found (content gated, or unknown id)")
	}
	ids := lessonIDs
	if *limit > 0 && *limit < len(ids) {
		ids = ids[:*limit]
	}
	if *limit > 0 {
		fmt.Printf("Found %d lesson(s) under root %d, taking first %d.\n", len(lessonIDs), rootID, len(ids))
	} else {
		fmt.Printf("Found %d lesson(s) under root %d.\n", len(lessonIDs), rootID)
	}

	// Resolve metadata first (gives titles + the course/series name for foldering).
	type resolvedLesson struct {
		id     int
		lesson *musora.Lesson
		index  int
		failed bool
	}
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

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	downloaded, failed := 0, 0
	for _, r := range resolved {
		if r.failed {
			failed++
			continue
		}
		fmt.Printf("\n▼ [%02d] %s\n", r.index, r.lesson.Title)
		err := musora.DownloadLesson(r.lesson, musora.DownloadOpts{
			Dir:           outDir,
			Index:         r.index,
			Quality:       *quality,
			ResourcesOnly: *resourcesOnly,
		})
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "✖  lesson %d failed: %v\n", r.id, err)
			continue
		}
		downloaded++
		fmt.Printf("  ✓ lesson %d\n", r.id)
	}

	fmt.Printf("\nDone → %s\n  downloaded: %d  failed: %d\n", outDir, downloaded, failed)
	return nil
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
