package server

import (
	"sync"

	"github.com/elienop/drumdrop/internal/scheduler"
)

// subBuffer is the per-subscriber channel capacity. Sends are non-blocking: when
// a subscriber's buffer is full the event is dropped for that subscriber so a
// slow SSE client can never stall the single Worker goroutine that calls Emit.
const subBuffer = 64

// Hub is the in-memory progress broadcast hub. The scheduler's Worker and Daemon
// emit ProgressEvents into it (Hub implements scheduler.ProgressSink); each SSE
// connection subscribes for its own buffered fan-out channel. The hub also keeps
// a small last-event-per-kind snapshot so a freshly connected client can be
// seeded with the current state via the SSE "ready" frame.
//
// Hub is safe for concurrent use: Emit is called from the Worker's single
// goroutine while Subscribe/unsubscribe run on HTTP handler goroutines.
type Hub struct {
	mu       sync.Mutex
	subs     map[*subscriber]struct{}
	snapshot map[string]scheduler.ProgressEvent
	seq      uint64
}

// subscriber is one connected consumer's delivery channel.
type subscriber struct {
	ch chan scheduler.ProgressEvent
}

// NewHub returns a ready-to-use Hub with no subscribers.
func NewHub() *Hub {
	return &Hub{
		subs:     make(map[*subscriber]struct{}),
		snapshot: make(map[string]scheduler.ProgressEvent),
	}
}

// Emit records the event in the per-kind snapshot, advances the monotonic seq,
// and fans the event out to every subscriber with a non-blocking send. A
// subscriber whose buffer is full silently misses this event rather than
// blocking the caller. Emit satisfies scheduler.ProgressSink.
func (h *Hub) Emit(e scheduler.ProgressEvent) {
	h.mu.Lock()
	h.seq++
	h.snapshot[e.Kind] = e
	for sub := range h.subs {
		select {
		case sub.ch <- e:
		default:
			// Buffer full: drop for this slow subscriber.
		}
	}
	h.mu.Unlock()
}

// Subscribe registers a new consumer and returns its buffered receive channel
// plus a cancel func. The cancel func unsubscribes and closes the channel; it is
// idempotent so a handler may defer it after an explicit teardown. Once
// canceled, the channel is drained-then-closed and no further events arrive.
func (h *Hub) Subscribe() (<-chan scheduler.ProgressEvent, func()) {
	sub := &subscriber{ch: make(chan scheduler.ProgressEvent, subBuffer)}

	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, sub)
			h.mu.Unlock()
			close(sub.ch)
		})
	}
	return sub.ch, cancel
}

// Snapshot returns the most recent event seen for each Kind, in no particular
// order. The SSE handler uses it to seed a newly connected client's "ready"
// frame so a dashboard reflects current state without waiting for the next live
// event.
func (h *Hub) Snapshot() []scheduler.ProgressEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]scheduler.ProgressEvent, 0, len(h.snapshot))
	for _, e := range h.snapshot {
		out = append(out, e)
	}
	return out
}

// Seq returns the total number of events emitted so far. It is monotonic and
// surfaced for SSE id/Last-Event-ID bookkeeping and observability.
func (h *Hub) Seq() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}
