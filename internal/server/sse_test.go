package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/scheduler"
)

// readFrame reads one SSE frame (lines up to a blank-line terminator) from the
// scanner, returning the joined raw lines. It returns ok=false at EOF.
func readFrame(sc *bufio.Scanner) (string, bool) {
	var lines []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if len(lines) == 0 {
				continue // skip leading blank lines between frames
			}
			return strings.Join(lines, "\n"), true
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

func TestEventsStreamsReadyThenLiveEvent(t *testing.T) {
	hub := NewHub()
	// Seed a snapshot so the ready frame carries known content.
	hub.Emit(scheduler.ProgressEvent{Kind: "cycle_done", Processed: 5})

	store := newTestStore(t)
	api := NewServer(store, Deps{}, hub, Config{}, "test")
	srv := httptest.NewServer(api)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	sc := bufio.NewScanner(resp.Body)

	// First frame: the ready snapshot.
	frame, ok := readFrame(sc)
	if !ok {
		t.Fatalf("no ready frame received")
	}
	if !strings.Contains(frame, "event: ready") {
		t.Fatalf("first frame not a ready event: %q", frame)
	}
	if !strings.Contains(frame, "cycle_done") {
		t.Fatalf("ready frame missing snapshot content: %q", frame)
	}

	// A live event emitted after subscription must arrive as a data frame with
	// an id line carrying the monotonic seq.
	hub.Emit(scheduler.ProgressEvent{Kind: "download_started", RailcontentID: 42})

	frame, ok = readFrame(sc)
	if !ok {
		t.Fatalf("no live frame received")
	}
	if !strings.Contains(frame, "id: ") {
		t.Fatalf("live frame missing id line: %q", frame)
	}
	if !strings.Contains(frame, "download_started") || !strings.Contains(frame, "42") {
		t.Fatalf("live frame missing event content: %q", frame)
	}
}

func TestEventsHonorsContextCancel(t *testing.T) {
	hub := NewHub()
	store := newTestStore(t)
	api := NewServer(store, Deps{}, hub, Config{}, "test")
	srv := httptest.NewServer(api)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	sc := bufio.NewScanner(resp.Body)
	if _, ok := readFrame(sc); !ok {
		t.Fatalf("no ready frame before cancel")
	}

	cancel()
	resp.Body.Close()

	// After cancel, the subscriber must be torn down. Give the handler a moment
	// and confirm the hub has no lingering subscribers.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		n := len(hub.subs)
		hub.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscriber not torn down after context cancel")
}
