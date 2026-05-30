package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/server"
)

// TestServeArgs drives the production parseServeArgs so a parser regression is
// caught against real code. It covers defaults (from config), the
// space-separated --listen/--interval forms (which must survive splitArgs), the
// equals form, mixed flags, and rejection of an invalid interval.
func TestServeArgs(t *testing.T) {
	if !valueFlags["--listen"] || !valueFlags["-listen"] {
		t.Fatal("--listen/-listen must be registered in valueFlags so its value is not orphaned")
	}

	t.Run("defaults from config", func(t *testing.T) {
		t.Setenv("DRUMDROP_LISTEN", "")
		opts, err := parseServeArgs(nil)
		if err != nil {
			t.Fatalf("parseServeArgs(nil) error: %v", err)
		}
		if opts.listen != "127.0.0.1:8080" {
			t.Errorf("listen = %q, want 127.0.0.1:8080 default", opts.listen)
		}
		if opts.interval != 12*time.Hour {
			t.Errorf("interval = %v, want 12h default", opts.interval)
		}
	})

	t.Run("space-separated --listen keeps its value", func(t *testing.T) {
		opts, err := parseServeArgs([]string{"--listen", "0.0.0.0:9000"})
		if err != nil {
			t.Fatalf("parseServeArgs([--listen 0.0.0.0:9000]) error: %v", err)
		}
		if opts.listen != "0.0.0.0:9000" {
			t.Errorf("listen = %q, want 0.0.0.0:9000", opts.listen)
		}
	})

	t.Run("interval and other flags mixed", func(t *testing.T) {
		opts, err := parseServeArgs(
			[]string{"--listen", "127.0.0.1:7000", "--interval", "1h", "--out", "/tmp/dd", "--quality", "1080", "--resources-only"})
		if err != nil {
			t.Fatalf("parseServeArgs(mixed) error: %v", err)
		}
		if opts.listen != "127.0.0.1:7000" {
			t.Errorf("listen = %q, want 127.0.0.1:7000", opts.listen)
		}
		if opts.interval != time.Hour {
			t.Errorf("interval = %v, want 1h", opts.interval)
		}
		if opts.out != "/tmp/dd" {
			t.Errorf("out = %q, want /tmp/dd", opts.out)
		}
		if opts.quality != "1080" {
			t.Errorf("quality = %q, want 1080", opts.quality)
		}
		if !opts.resourcesOnly {
			t.Error("resourcesOnly = false, want true")
		}
	})

	t.Run("equals form still works", func(t *testing.T) {
		opts, err := parseServeArgs([]string{"--interval=90s"})
		if err != nil {
			t.Fatalf("parseServeArgs([--interval=90s]) error: %v", err)
		}
		if opts.interval != 90*time.Second {
			t.Errorf("interval = %v, want 90s", opts.interval)
		}
	})

	t.Run("invalid interval is rejected", func(t *testing.T) {
		for _, in := range []string{"0", "nonsense", "-5m"} {
			if _, err := parseServeArgs([]string{"--interval", in}); err == nil {
				t.Errorf("parseServeArgs([--interval %q]): want error, got nil", in)
			}
		}
	})
}

// fakeDaemon records when its Run goroutine starts and returns, so the
// shutdown-ordering test can assert the daemon goroutine is joined before the
// store is closed.
type fakeDaemon struct {
	started  chan struct{}
	returned chan struct{}
}

func (d *fakeDaemon) Run(ctx context.Context, _ time.Duration) error {
	close(d.started)
	<-ctx.Done() // mirror the real daemon: only ctx cancellation ends Run.
	close(d.returned)
	return nil
}

// TestGracefulServeShutdownOrdering verifies the shutdown sequence: when the
// server context is canceled (the signal path), gracefulServe shuts the HTTP
// server down, cancels the daemon, WAITS for the daemon goroutine to return,
// and only THEN runs onClose (store.Close). A close that fires before the
// daemon goroutine has returned would race the store out from under an
// in-flight download, so we assert ordering explicitly.
func TestGracefulServeShutdownOrdering(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.NewServeMux()}

	daemon := &fakeDaemon{started: make(chan struct{}), returned: make(chan struct{})}

	var mu sync.Mutex
	closedWhileRunning := false
	closeCalled := false
	onClose := func() error {
		mu.Lock()
		defer mu.Unlock()
		closeCalled = true
		select {
		case <-daemon.returned:
			// Good: daemon goroutine already returned before Close.
		default:
			closedWhileRunning = true
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- gracefulServe(ctx, srv, ln, daemon, 50*time.Millisecond, onClose) }()

	// Wait for the daemon goroutine to actually start before signaling shutdown.
	select {
	case <-daemon.started:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon Run never started")
	}

	cancel() // simulate SIGINT/SIGTERM

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("gracefulServe returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gracefulServe did not return after cancel")
	}

	mu.Lock()
	defer mu.Unlock()
	if !closeCalled {
		t.Fatal("onClose was never called")
	}
	if closedWhileRunning {
		t.Fatal("onClose ran before the daemon goroutine returned (store closed under a live daemon)")
	}
}

// newServeTestStore opens a fresh temp-file store with migrations applied so the
// shutdown test can build a real server handler (NewServer needs a *Store).
func newServeTestStore(t *testing.T) *database.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	store := database.NewStore(db)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestGracefulServeShutdownWithSSEClient verifies that an open SSE stream does
// not pin the shutdown for the full 10s timeout. handleEvents blocks in a select
// until its request context is done; plain http.Server.Shutdown never cancels
// request contexts, so without the BaseContext wiring the drain would hang until
// the timeout and return a misleading DeadlineExceeded. With gracefulServe
// installing a server-closing BaseContext that the SSE select also watches, the
// signal path must return promptly and with a nil error.
func TestGracefulServeShutdownWithSSEClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	hub := server.NewHub()
	handler := server.NewServer(newServeTestStore(t), server.Deps{}, hub, server.Config{}, "test")
	srv := &http.Server{Handler: handler}

	daemon := &fakeDaemon{started: make(chan struct{}), returned: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gracefulServe(ctx, srv, ln, daemon, time.Hour, func() error { return nil }) }()

	select {
	case <-daemon.started:
	case <-time.After(2 * time.Second):
		t.Fatal("daemon Run never started")
	}

	// Open an SSE stream and read the ready frame so the handler is parked in its
	// select with the connection counted as in-flight by http.Server.
	url := "http://" + ln.Addr().String() + "/api/events"
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	gotReady := false
	for sc.Scan() {
		if sc.Text() == "" {
			gotReady = true
			break
		}
	}
	if !gotReady {
		t.Fatal("no ready frame from SSE stream before shutdown")
	}

	cancel() // simulate SIGINT/SIGTERM

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("gracefulServe returned error: %v (want nil; shutdown should not time out with an SSE client)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gracefulServe did not return within 5s with an SSE client connected (shutdown likely blocked on the full timeout)")
	}
}
