package scheduler

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// Daemon composes a Planner and a Worker into the unattended download loop. Each
// cycle plans (enqueues jobs for newly discovered lessons) then drains the queue
// (downloads them, one at a time, with retry). It is the long-running process the
// future HTTP API + SSE will attach to.
//
// The Store field is held separately (not only via Planner/Worker) so the daemon
// can reclaim jobs orphaned by a crash at startup via RequeueStaleRunning.
type Daemon struct {
	// Store is used at startup for crash recovery (RequeueStaleRunning). It is
	// the same store the Planner and Worker already hold.
	Store Store
	// Planner enqueues jobs for newly discovered lessons.
	Planner *Planner
	// Worker drains the jobs table, downloading one lesson at a time with retry.
	Worker *Worker
	// Log receives a one-line summary per cycle plus startup/shutdown notices. It
	// defaults to io.Discard; the loop logic never depends on it.
	Log io.Writer
	// Progress receives structured per-cycle ProgressEvents beside the log. It is
	// nil by default; progress() substitutes a noopSink. engine.Build sets it via
	// assignment after construction.
	Progress ProgressSink
	// Kick requests one immediate cycle out of the regular interval. A receive on
	// it runs RunOnce just like a ticker tick. It is nil by default: a nil channel
	// blocks forever in the select, so the plain CLI daemon never kicks. The serve
	// entrypoint creates a buffered channel and sends on it for POST /api/sync.
	Kick <-chan struct{}

	// paused gates the scheduling loop: while set, the immediate startup cycle,
	// every ticker tick, and every kick skip RunOnce entirely (a kick received
	// while paused is dropped, not buffered for resume). It is read/written
	// atomically because Pause/Resume are called from the HTTP handler goroutine
	// while Run executes on the daemon goroutine.
	paused atomic.Bool
}

// Pause stops the daemon from starting new cycles. While paused, the startup
// cycle, ticker ticks, and kicks all skip RunOnce; an in-flight cycle is not
// interrupted (pause takes effect at the next scheduling decision). Idempotent.
func (d *Daemon) Pause() { d.paused.Store(true) }

// Resume re-enables scheduling so the next tick (or kick) runs a cycle again.
// Idempotent. A kick dropped while paused is not replayed; the next tick covers it.
func (d *Daemon) Resume() { d.paused.Store(false) }

// IsPaused reports whether scheduling is currently paused.
func (d *Daemon) IsPaused() bool { return d.paused.Load() }

// log returns the configured writer or io.Discard so callers can write without a
// nil check and logic stays independent of logging.
func (d *Daemon) log() io.Writer {
	if d.Log == nil {
		return io.Discard
	}
	return d.Log
}

// progress returns the configured sink or a noopSink so callers can emit without
// a nil check and the loop logic stays independent of any consumer.
func (d *Daemon) progress() ProgressSink {
	if d.Progress == nil {
		return noopSink{}
	}
	return d.Progress
}

// RunOnce runs one full cycle: plan, then drain. It plans first so any newly
// discovered lessons are queued before the worker drains, letting a single cycle
// download brand-new content. It logs a one-line summary (planned, processed) and
// returns the first fatal error encountered (a planner or worker store failure);
// per-lesson and per-job failures are recorded internally and never returned.
//
// RunOnce is used by both `sync` and `daemon --once`, and is the body of each
// Run tick.
func (d *Daemon) RunOnce(ctx context.Context) error {
	d.progress().Emit(ProgressEvent{Kind: "cycle_started", Time: time.Now()})

	planned, perr := d.Planner.Plan(ctx, 0)
	processed, werr := d.Worker.RunOnce(ctx, 0)

	fmt.Fprintf(d.log(), "cycle: planned %d, processed %d\n", planned, processed)
	d.progress().Emit(ProgressEvent{
		Kind:      "cycle_done",
		Planned:   planned,
		Processed: processed,
		Time:      time.Now(),
	})

	// Return the first fatal error; a planning failure is reported even if the
	// worker (which may still have drained pre-existing jobs) also failed.
	if perr != nil {
		return fmt.Errorf("plan: %w", perr)
	}
	if werr != nil {
		return fmt.Errorf("drain: %w", werr)
	}
	return nil
}

// Run is the long-running daemon loop. It first reclaims any jobs left running by
// a previous crash (RequeueStaleRunning), runs one cycle immediately, then runs a
// cycle on every interval tick until the context is cancelled.
//
// A cycle error is logged and the loop CONTINUES — a transient store hiccup must
// not kill the daemon, and the failing lesson/job is re-tried next planner cycle.
// Only context cancellation (a SIGINT/SIGTERM-derived ctx) ends the loop, and Run
// then returns nil for a clean shutdown.
func (d *Daemon) Run(ctx context.Context, interval time.Duration) error {
	// Crash recovery: any job still marked running was orphaned by a previous
	// process that died mid-download. Re-queue them so this run picks them up.
	if reclaimed, err := d.Store.RequeueStaleRunning(ctx); err != nil {
		fmt.Fprintf(d.log(), "startup: requeue stale running failed: %v\n", err)
	} else {
		fmt.Fprintf(d.log(), "startup: requeued %d stale running job(s)\n", reclaimed)
	}
	// A delete runs inside one request of this process, so a lesson still
	// marked as being deleted was left by a process that died mid-delete: end
	// it, or the lesson could never be downloaded again.
	if ended, err := d.Store.ClearStaleDeletes(ctx); err != nil {
		fmt.Fprintf(d.log(), "startup: clear stale deletes failed: %v\n", err)
	} else if ended > 0 {
		fmt.Fprintf(d.log(), "startup: ended %d delete(s) a previous run left unfinished\n", ended)
	}

	// Run one cycle immediately so the daemon does useful work without waiting a
	// full interval on startup — unless paused, in which case the next un-paused
	// tick covers it.
	if !d.IsPaused() {
		if err := d.RunOnce(ctx); err != nil {
			fmt.Fprintf(d.log(), "cycle error (continuing): %v\n", err)
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Clean shutdown: the in-flight job (if any) finished before the last
			// claim; we simply stop scheduling further cycles.
			return nil
		case <-ticker.C:
			// Skip the cycle entirely while paused; the next un-paused tick runs.
			if d.IsPaused() {
				continue
			}
			if err := d.RunOnce(ctx); err != nil {
				fmt.Fprintf(d.log(), "cycle error (continuing): %v\n", err)
			}
		case <-d.Kick:
			// On-demand sync: run one immediate cycle out of band. A nil Kick
			// channel blocks forever here, so this case never fires for the plain
			// CLI daemon. A kick received while paused is dropped (not replayed on
			// resume) — the next un-paused tick covers any work it would have done.
			if d.IsPaused() {
				continue
			}
			if err := d.RunOnce(ctx); err != nil {
				fmt.Fprintf(d.log(), "cycle error (continuing): %v\n", err)
			}
		}
	}
}
