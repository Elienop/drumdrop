package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/elienop/drumdrop/internal/scheduler"
)

// cmdDaemon runs the unattended auto-sync loop: a periodic Planner that enqueues
// jobs for newly discovered lessons and a sequential Worker that drains the queue
// one download at a time with retry. With --once it runs a single plan+drain
// cycle and exits (the cron-friendly / testing path). Otherwise it loops every
// --interval until SIGINT/SIGTERM, then shuts down cleanly after the in-flight
// download finishes.
func cmdDaemon(argv []string) error {
	opts, err := parseDaemonArgs(argv)
	if err != nil {
		return err
	}

	store, err := openStore()
	if err != nil {
		return err
	}
	defer store.Close()

	cfg := schedulerConfig(opts.out, opts.quality, opts.resourcesOnly)
	permIDs := permissionIDs()
	planner := &scheduler.Planner{
		Store:    store,
		Expander: realExpander{},
		PermIDs:  permIDs,
		Log:      os.Stdout,
	}
	worker := scheduler.NewWorker(store, realResolver{}, realDownloader{}, cfg, permIDs, os.Stdout)
	daemon := &scheduler.Daemon{
		Store:   store,
		Planner: planner,
		Worker:  worker,
		Log:     os.Stdout,
	}

	if opts.once {
		fmt.Printf("drumdrop daemon: one cycle into %s\n", cfg.DownloadsDir)
		return daemon.RunOnce(context.Background())
	}

	// Graceful shutdown: SIGINT/SIGTERM cancels the context; the daemon stops
	// claiming new jobs and returns after the in-flight download finishes.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	interval := fs.String("interval", "12h", "re-check interval (Go duration, e.g. 6h, 30m)")
	once := fs.Bool("once", false, "run one plan+drain cycle then exit")
	out := fs.String("out", "", "output directory (default DRUMDROP_DOWNLOADS_DIR or ./downloads)")
	quality := fs.String("quality", "", "override each follow's quality (best|2160|1440|1080|720|480)")
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
