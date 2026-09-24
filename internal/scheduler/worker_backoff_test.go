package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// hookSink records every event, and runs on for each one first, on the
// worker's goroutine (as the real sink is called).
type hookSink struct {
	recordingSink
	on func(ProgressEvent)
}

func (s *hookSink) Emit(e ProgressEvent) {
	s.on(e)
	s.recordingSink.Emit(e)
}

// backoffWorker is a worker over one job (1, lesson 100) whose download always
// fails, with a real 30-second backoff before each retry: a stop that waited
// the backoff out would take that long. stop runs 100 ms after the first
// attempt's failure is reported, from another goroutine (as a Cancel request
// or a signal does), so while the worker waits out the backoff.
func backoffWorker(t *testing.T, stop func(w *Worker)) (*Worker, *fakeWorkerStore, *fakeDownloader, *hookSink) {
	t.Helper()
	store := newFakeWorkerStore(queuedJob(1, nodeFollow().ID, 100))
	store.follows[nodeFollow().ID] = nodeFollow()
	dl := newFakeDownloader()
	dl.alwaysFail = true
	w := newTestWorker(t, store, fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}, dl, time.Sleep)
	w.Cfg.MaxAttempts = 3
	w.Cfg.Backoff = []time.Duration{30 * time.Second}
	sink := &hookSink{}
	sink.on = func(e ProgressEvent) {
		if e.Kind == "attempt_failed" && e.Attempt == 1 {
			go func() {
				time.Sleep(100 * time.Millisecond)
				stop(w)
			}()
		}
	}
	w.Progress = sink
	return w, store, dl, sink
}

// runPromptly runs w over its queue and fails unless it returns within two
// seconds, far short of the backoff.
func runPromptly(t *testing.T, ctx context.Context, w *Worker) {
	t.Helper()
	start := time.Now()
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the run took %v: the stop waited the backoff out", took)
	}
}

// TestCancelDuringABackoffLandsAtOnce (round-5 code Low 5) proves a Cancel
// that lands while a failed download waits to retry is recorded at once, not
// when the backoff ends: no further attempt runs, the job is canceled, and the
// job's last event says so.
func TestCancelDuringABackoffLandsAtOnce(t *testing.T) {
	w, store, dl, sink := backoffWorker(t, func(w *Worker) {
		if !w.CancelRunning(1) {
			t.Error("CancelRunning found no running job")
		}
	})
	runPromptly(t, context.Background(), w)
	if dl.attempts[100] != 1 {
		t.Errorf("attempts = %d, want 1 (no retry after the Cancel)", dl.attempts[100])
	}
	if len(store.markJobCanceled) != 1 || store.markJobCanceled[0] != 1 {
		t.Errorf("canceled jobs = %v, want job 1", store.markJobCanceled)
	}
	if len(store.markJobFailed) != 0 {
		t.Errorf("failed jobs = %v, want none", store.markJobFailed)
	}
	events := sink.snapshot()
	if last := events[len(events)-1]; last.Kind != "lesson_skipped" || last.Err != msgCanceled {
		t.Errorf("last event = %+v, want lesson_skipped %q", last, msgCanceled)
	}
}

// TestShutdownDuringABackoffRequeues proves a shutdown that lands while a
// failed download waits to retry stops at once and leaves the job running,
// for the next start to requeue: it is never canceled, skipped or failed, and
// its end is reported once (the failed attempt).
func TestShutdownDuringABackoffRequeues(t *testing.T) {
	ctx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	w, store, dl, sink := backoffWorker(t, func(*Worker) { shutdown() })
	runPromptly(t, ctx, w)
	if dl.attempts[100] != 1 {
		t.Errorf("attempts = %d, want 1", dl.attempts[100])
	}
	if len(store.markJobCanceled) != 0 || len(store.markSkipped) != 0 || len(store.markFailed) != 0 {
		t.Errorf("canceled %v, skipped %v, failed %v; want the job left as it is", store.markJobCanceled, store.markSkipped, store.markFailed)
	}
	if got := store.jobs[1].Status; got != database.JobRunning {
		t.Errorf("job status = %q, want running (requeued at the next start)", got)
	}
	assertEndsOnce(t, &sink.recordingSink, 1)
}
