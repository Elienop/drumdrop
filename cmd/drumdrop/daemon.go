package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/elienop/drumdrop/internal/config"
	"github.com/elienop/drumdrop/internal/engine"
)

// cmdDaemon runs the unattended auto-sync loop: a periodic Planner that enqueues
// jobs for newly discovered lessons and a sequential Worker that drains the queue
// one download at a time with retry. With --once it runs a single plan+drain
// cycle and exits (the cron-friendly / testing path). Otherwise it loops every
// --interval until SIGINT/SIGTERM. Either way SIGINT/SIGTERM stops the
// in-flight download, whose job is left running. Only the start of serve or of
// a looping daemon queues it again (Daemon.Recover); --once never does, so
// after a stopped --once run the job stays running, and the lesson is not
// queued again, until one of those starts.
func cmdDaemon(argv []string) error {
	opts, err := parseDaemonArgs(argv)
	if err != nil {
		return err
	}

	store, err := engine.OpenStore()
	if err != nil {
		return err
	}
	defer store.Close()

	cfg, err := engine.Config(opts.out, opts.quality, opts.resourcesOnly)
	if err != nil {
		return err
	}
	_, _, daemon := engine.Build(store, cfg, engine.PermissionIDs(), os.Stdout, nil)

	// Graceful shutdown, --once included: SIGINT/SIGTERM cancels the context,
	// which stops the in-flight download (yt-dlp runs in its own process group,
	// so a Ctrl-C at the terminal never reaches it: without this, drumdrop would
	// die and leave it running on its own, writing into the job's private
	// folder). The worker then removes that folder, leaves the job running, and
	// claims nothing more. A --once run never requeues it (RunOnce does not call
	// Daemon.Recover): the next serve or looping daemon start does.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if opts.once {
		fmt.Printf("drumdrop daemon: one cycle into %s\n", cfg.DownloadsDir)
		return daemon.RunOnce(ctx)
	}

	fmt.Printf("drumdrop daemon: auto-syncing every %s into %s (Ctrl-C to stop)\n", opts.interval, cfg.DownloadsDir)
	if err := daemon.Run(ctx, opts.interval); err != nil {
		return err
	}
	fmt.Println("drumdrop daemon: stopped cleanly.")
	return nil
}

// daemonOpts holds the parsed daemon flags.
type daemonOpts struct {
	interval      time.Duration
	once          bool
	out           string
	quality       string
	resourcesOnly bool
}

// parseDaemonArgs parses the daemon flags from argv, honoring flags placed after
// positionals (via splitArgs) and resolving --interval to a validated positive
// duration. It is extracted from cmdDaemon so the daemon test can exercise the
// exact production parser rather than a re-implementation.
func parseDaemonArgs(argv []string) (daemonOpts, error) {
	fs := flag.NewFlagSet("drumdrop daemon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	interval := fs.String("interval", config.Interval(), "re-check interval (Go duration, e.g. 6h, 30m)")
	once := fs.Bool("once", false, "run one plan+drain cycle then exit")
	out := fs.String("out", "", "output directory (default DRUMDROP_DOWNLOADS_DIR or ./downloads)")
	quality := fs.String("quality", config.Quality(), "override each follow's quality (best|2160|1440|1080|720|480)")
	resourcesOnly := fs.Bool("resources-only", false, "skip video; fetch resources only")

	_, flags := splitArgs(argv)
	if err := fs.Parse(flags); err != nil {
		return daemonOpts{}, err
	}
	dur, err := parseInterval(*interval)
	if err != nil {
		return daemonOpts{}, err
	}
	return daemonOpts{
		interval:      dur,
		once:          *once,
		out:           *out,
		quality:       *quality,
		resourcesOnly: *resourcesOnly,
	}, nil
}

// parseInterval parses the --interval flag as a Go duration and rejects
// non-positive values, which would make the ticker panic or spin.
func parseInterval(s string) (time.Duration, error) {
	dur, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --interval %q: %w", s, err)
	}
	if dur <= 0 {
		return 0, fmt.Errorf("--interval must be positive, got %s", dur)
	}
	return dur, nil
}
