package server

import (
	"sync"
	"testing"

	"github.com/elienop/drumdrop/internal/scheduler"
)

// Hub must satisfy scheduler.ProgressSink so engine.Build can hand it to the
// worker/daemon.
var _ scheduler.ProgressSink = (*Hub)(nil)

func TestHubSubscribeReceivesEvents(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	defer cancel()

	h.Emit(scheduler.ProgressEvent{Kind: "download_started", RailcontentID: 7})

	got := <-ch
	if got.Kind != "download_started" || got.RailcontentID != 7 {
		t.Fatalf("got %+v, want download_started/7", got)
	}
}

func TestHubSeqIsMonotonic(t *testing.T) {
	h := NewHub()

	if got := h.Seq(); got != 0 {
		t.Fatalf("fresh hub Seq() = %d, want 0", got)
	}
	h.Emit(scheduler.ProgressEvent{Kind: "cycle_started"})
	first := h.Seq()
	h.Emit(scheduler.ProgressEvent{Kind: "cycle_done"})
	second := h.Seq()

	if first != 1 {
		t.Fatalf("Seq after one emit = %d, want 1", first)
	}
	if second != first+1 {
		t.Fatalf("Seq after two emits = %d, want %d", second, first+1)
	}
}

func TestHubUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()

	cancel()
	// A second cancel must be a safe no-op (the SSE handler defers it after
	// teardown that may also have triggered it).
	cancel()

	h.Emit(scheduler.ProgressEvent{Kind: "cycle_started"})

	if _, ok := <-ch; ok {
		t.Fatalf("channel still delivered after unsubscribe")
	}
}

func TestHubSlowSubscriberDoesNotBlockEmit(t *testing.T) {
	h := NewHub()
	// Subscribe but never drain. Emit far more than the buffer; once full,
	// further events must be dropped rather than block.
	_, cancel := h.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10_000; i++ {
			h.Emit(scheduler.ProgressEvent{Kind: "download_progress"})
		}
		close(done)
	}()

	<-done // would deadlock if Emit blocked on the full subscriber.
}

func TestHubSnapshotCachesLastPerKind(t *testing.T) {
	h := NewHub()

	h.Emit(scheduler.ProgressEvent{Kind: "download_started", RailcontentID: 1})
	h.Emit(scheduler.ProgressEvent{Kind: "download_started", RailcontentID: 2})
	h.Emit(scheduler.ProgressEvent{Kind: "cycle_done", Processed: 3})

	snap := h.Snapshot()
	byKind := map[string]scheduler.ProgressEvent{}
	for _, e := range snap {
		byKind[e.Kind] = e
	}
	if got := byKind["download_started"]; got.RailcontentID != 2 {
		t.Fatalf("download_started snapshot RailcontentID = %d, want 2 (last wins)", got.RailcontentID)
	}
	if got := byKind["cycle_done"]; got.Processed != 3 {
		t.Fatalf("cycle_done snapshot Processed = %d, want 3", got.Processed)
	}
}

func TestHubConcurrentSubscribeEmitUnsubscribe(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				ch, cancel := h.Subscribe()
				go func() {
					for range ch {
					}
				}()
				h.Emit(scheduler.ProgressEvent{Kind: "download_progress"})
				cancel()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 2000; j++ {
			h.Emit(scheduler.ProgressEvent{Kind: "cycle_started"})
			_ = h.Snapshot()
			_ = h.Seq()
		}
	}()

	wg.Wait()
}
