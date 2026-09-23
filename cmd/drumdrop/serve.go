package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/elienop/drumdrop/internal/config"
	"github.com/elienop/drumdrop/internal/engine"
	"github.com/elienop/drumdrop/internal/server"
)

// version is the build version surfaced by the /healthz probe. It is a var so a
// release build can override it with -ldflags "-X main.version=...".
var version = "dev"

// daemonRunner is the slice of *scheduler.Daemon that gracefulServe needs: the
// startup recovery, and a Run loop that ends only on context cancellation. It
// is an interface so the shutdown-ordering test can substitute a fake without a
// real store or network.
type daemonRunner interface {
	Recover(ctx context.Context)
	Run(ctx context.Context, interval time.Duration) error
}

// cmdServe runs the inbound HTTP API alongside the auto-sync daemon. It refuses
// an unsafe bind (non-loopback without a token), wires the engine + server over
// one store and a shared progress hub, and serves until SIGINT/SIGTERM, then
// shuts down gracefully: drain the HTTP server, stop the daemon, wait for its
// goroutine to return, and close the store last.
func cmdServe(argv []string) error {
	opts, err := parseServeArgs(argv)
	if err != nil {
		return err
	}

	if err := server.GuardListen(opts.listen, config.APIToken()); err != nil {
		return err
	}

	cfg, err := engine.Config(opts.out, opts.quality, opts.resourcesOnly)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", opts.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.listen, err)
	}

	store, err := engine.OpenStore()
	if err != nil {
		ln.Close()
		return err
	}

	hub := server.NewHub()
	planner, worker, daemon := engine.Build(store, cfg, engine.PermissionIDs(), os.Stdout, hub)

	// Kick is the on-demand sync channel: POST /api/sync sends on the server's
	// write end; the daemon's Run select drains the read end. One buffered
	// channel wired to both ends.
	kick := make(chan struct{}, 1)
	daemon.Kick = kick

	srvCfg := server.Config{
		ListenAddr:       opts.listen,
		APIToken:         config.APIToken(),
		CORSOrigin:       config.CORSOrigin(),
		DownloadsDir:     cfg.DownloadsDir,
		HostDownloadsDir: config.HostDownloadsDir(),
		LibraryDir:       cfg.LibraryDir,
	}
	deps := server.Deps{
		Planner:       planner,
		Kick:          kick,
		CancelRunning: worker.CancelRunning,
		Pause:         daemon.Pause,
		Resume:        daemon.Resume,
		IsPaused:      daemon.IsPaused,
	}
	handler := server.NewServer(store, deps, hub, srvCfg, version)
	srv := &http.Server{Handler: handler}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("drumdrop serve: listening on %s, auto-syncing every %s into %s (Ctrl-C to stop)\n",
		opts.listen, opts.interval, cfg.DownloadsDir)

	return gracefulServe(ctx, srv, ln, daemon, opts.interval, store.Close)
}

// gracefulServe runs the daemon loop and the HTTP server together, then tears
// them down in a safe order when ctx is canceled (the signal path). The daemon
// runs in its own goroutine bound to a child context; on shutdown we drain the
// HTTP server first (no new requests), cancel the daemon, WAIT for its goroutine
// to return so no download is mid-flight, and only then run onClose to close the
// store. Returning store.Close while the daemon still held it would race the DB
// out from under an in-flight write.
//
// It is extracted from cmdServe (with the daemon, listener, and onClose injected)
// so the shutdown-ordering test can drive the exact production sequence against
// a fake daemon and an in-process listener.
func gracefulServe(ctx context.Context, srv *http.Server, ln net.Listener, daemon daemonRunner, interval time.Duration, onClose func() error) error {
	// serverCtx is the base context every inbound request descends from. We cancel
	// it at the start of shutdown so long-lived streaming handlers (the SSE
	// /api/events endpoint, which otherwise blocks on its own request context for
	// the full Shutdown timeout) observe the cancellation and return at once,
	// letting srv.Shutdown drain promptly instead of timing out with a misleading
	// DeadlineExceeded. handleEvents selects on this context alongside its request
	// context (see server.handleEvents).
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	baseCtx := server.WithServerContext(serverCtx)
	srv.BaseContext = func(net.Listener) context.Context { return baseCtx }

	// The daemon gets its own cancelable context so we can stop it independently
	// of the HTTP server's shutdown context.
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	defer cancelDaemon()

	// The startup recovery runs to the end before the first request is served
	// (the listener is bound, but nothing accepts until srv.Serve), so no
	// request can race it; Run then skips it.
	daemon.Recover(daemonCtx)

	daemonDone := make(chan struct{})
	go func() {
		defer close(daemonDone)
		// Run only returns on context cancellation (clean shutdown) or a fatal
		// store error; either way the daemon goroutine ends here.
		if err := daemon.Run(daemonCtx, interval); err != nil {
			fmt.Fprintf(os.Stderr, "drumdrop serve: daemon stopped: %v\n", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		// The server stopped on its own (bind/serve failure). Tear the daemon
		// down before returning the error.
		cancelDaemon()
		<-daemonDone
		if cerr := onClose(); cerr != nil && err == nil {
			err = cerr
		}
		return err
	case <-ctx.Done():
		// Signal received: drain the HTTP server, then stop the daemon and wait
		// for its goroutine to return before closing the store.
	}

	// Cancel the base context first so in-flight streaming handlers unblock
	// immediately; then drain. Without this, Shutdown would wait the full timeout
	// for an open SSE stream and return DeadlineExceeded.
	cancelServer()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)

	cancelDaemon()
	<-daemonDone // join the daemon goroutine before touching the store.

	closeErr := onClose()

	if shutdownErr != nil {
		return shutdownErr
	}
	return closeErr
}

// serveOpts holds the parsed serve flags.
type serveOpts struct {
	listen        string
	interval      time.Duration
	out           string
	quality       string
	resourcesOnly bool
}

// parseServeArgs parses the serve flags from argv, honoring flags placed after
// positionals (via splitArgs) and resolving --interval to a validated positive
// duration. --listen defaults to the configured DRUMDROP_LISTEN (loopback when
// unset). It is extracted from cmdServe so the serve test exercises the exact
// production parser rather than a re-implementation.
func parseServeArgs(argv []string) (serveOpts, error) {
	fs := flag.NewFlagSet("drumdrop serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", config.ListenAddr(), "host:port to bind the HTTP API")
	interval := fs.String("interval", config.Interval(), "auto-sync interval (Go duration, e.g. 6h, 30m)")
	out := fs.String("out", "", "output directory (default DRUMDROP_DOWNLOADS_DIR or ./downloads)")
	quality := fs.String("quality", config.Quality(), "override each follow's quality (best|2160|1440|1080|720|480)")
	resourcesOnly := fs.Bool("resources-only", false, "skip video; fetch resources only")

	_, flags := splitArgs(argv)
	if err := fs.Parse(flags); err != nil {
		return serveOpts{}, err
	}
	dur, err := parseInterval(*interval)
	if err != nil {
		return serveOpts{}, err
	}
	return serveOpts{
		listen:        *listen,
		interval:      dur,
		out:           *out,
		quality:       *quality,
		resourcesOnly: *resourcesOnly,
	}, nil
}
