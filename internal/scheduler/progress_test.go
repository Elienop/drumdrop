package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// recordingSink captures every emitted event for assertions. It is mutex-guarded
// so the daemon's cross-goroutine emits stay race-free under -race.
type recordingSink struct {
	mu     sync.Mutex
	events []ProgressEvent
}

func (s *recordingSink) Emit(e ProgressEvent) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *recordingSink) snapshot() []ProgressEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ProgressEvent, len(s.events))
	copy(out, s.events)
	return out
}

// kinds returns the count of each event Kind seen.
func (s *recordingSink) kinds() map[string]int {
	counts := map[string]int{}
	for _, e := range s.snapshot() {
		counts[e.Kind]++
	}
	return counts
}

// TestProgressNilSinkIsNoop verifies the default (nil Progress) substitutes a
// noopSink so a worker emits unconditionally without panicking and the success
// path still completes — proving the seam is additive.
func TestProgressNilSinkIsNoop(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	// Progress is left nil on purpose.
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Errorf("job status = %q, want done with a nil sink (emits must be no-ops)", got)
	}
}

// TestWorkerEmitsSuccessSequence asserts a first-try success emits exactly
// job_claimed, download_started, download_ok (one each) and carries the lesson's
// identity — and never an attempt_failed or lesson_skipped.
func TestWorkerEmitsSuccessSequence(t *testing.T) {
	job := queuedJob(7, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	sink := &recordingSink{}

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Progress = sink
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	counts := sink.kinds()
	want := map[string]int{"job_claimed": 1, "download_started": 1, "download_ok": 1}
	for k, n := range want {
		if counts[k] != n {
			t.Errorf("%s emitted %d times, want %d (all: %v)", k, counts[k], n, counts)
		}
	}
	if counts["attempt_failed"] != 0 || counts["lesson_skipped"] != 0 {
		t.Errorf("unexpected failure events on success: %v", counts)
	}

	// The events carry the job/lesson identity.
	for _, e := range sink.snapshot() {
		if e.JobID != 7 {
			t.Errorf("%s event JobID = %d, want 7", e.Kind, e.JobID)
		}
	}
	var started ProgressEvent
	for _, e := range sink.snapshot() {
		if e.Kind == "download_started" {
			started = e
		}
	}
	if started.RailcontentID != 100 || started.Title != "Lesson A" {
		t.Errorf("download_started = %+v, want RailcontentID 100 Title \"Lesson A\"", started)
	}
	if started.Attempt != 1 || started.MaxAttempts != w.Cfg.MaxAttempts {
		t.Errorf("download_started attempt = %d/%d, want 1/%d", started.Attempt, started.MaxAttempts, w.Cfg.MaxAttempts)
	}
}

// TestWorkerEmitsRetrySequence asserts a download that fails twice then succeeds
// emits two attempt_failed, three download_started, and one download_ok.
func TestWorkerEmitsRetrySequence(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.failsBefore[100] = 2
	sink := &recordingSink{}

	w := newTestWorker(store, res, dl, (&recordingSleeper{}).sleep)
	w.Progress = sink
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	counts := sink.kinds()
	if counts["download_started"] != 3 {
		t.Errorf("download_started = %d, want 3", counts["download_started"])
	}
	if counts["attempt_failed"] != 2 {
		t.Errorf("attempt_failed = %d, want 2", counts["attempt_failed"])
	}
	if counts["download_ok"] != 1 {
		t.Errorf("download_ok = %d, want 1", counts["download_ok"])
	}
	// The attempt_failed events carry the failing attempt number and an error.
	var failedAttempts []int
	for _, e := range sink.snapshot() {
		if e.Kind == "attempt_failed" {
			failedAttempts = append(failedAttempts, e.Attempt)
			if e.Err == "" {
				t.Errorf("attempt_failed (attempt %d) has empty Err", e.Attempt)
			}
		}
	}
	if len(failedAttempts) != 2 || failedAttempts[0] != 1 || failedAttempts[1] != 2 {
		t.Errorf("attempt_failed attempts = %v, want [1 2]", failedAttempts)
	}
}

// TestWorkerEmitsSkipped asserts an unresolvable lesson emits a single
// lesson_skipped (with a reason) and no download events.
func TestWorkerEmitsSkipped(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	res := fakeResolver{lessons: map[int]*musora.Lesson{}} // (nil, nil) → unresolvable
	dl := newFakeDownloader()
	sink := &recordingSink{}

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Progress = sink
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	counts := sink.kinds()
	if counts["lesson_skipped"] != 1 {
		t.Errorf("lesson_skipped = %d, want 1 (all: %v)", counts["lesson_skipped"], counts)
	}
	if counts["job_claimed"] != 1 {
		t.Errorf("job_claimed = %d, want 1", counts["job_claimed"])
	}
	if counts["download_started"] != 0 || counts["download_ok"] != 0 {
		t.Errorf("no download events expected for an unresolvable lesson: %v", counts)
	}
	for _, e := range sink.snapshot() {
		if e.Kind == "lesson_skipped" {
			if e.RailcontentID != 100 || e.Err == "" {
				t.Errorf("lesson_skipped = %+v, want RailcontentID 100 and a reason", e)
			}
		}
	}
}

// TestDaemonEmitsCycleEvents asserts one RunOnce emits a cycle_started and a
// cycle_done, with the cycle_done carrying the planned/processed counts.
func TestDaemonEmitsCycleEvents(t *testing.T) {
	store := newFakeDaemonStore()
	d := newTestDaemon(store)
	sink := &recordingSink{}
	d.Progress = sink

	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	counts := sink.kinds()
	if counts["cycle_started"] != 1 {
		t.Errorf("cycle_started = %d, want 1 (all: %v)", counts["cycle_started"], counts)
	}
	if counts["cycle_done"] != 1 {
		t.Errorf("cycle_done = %d, want 1 (all: %v)", counts["cycle_done"], counts)
	}
	var done ProgressEvent
	for _, e := range sink.snapshot() {
		if e.Kind == "cycle_done" {
			done = e
		}
	}
	if done.Planned != 1 || done.Processed != 1 {
		t.Errorf("cycle_done planned/processed = %d/%d, want 1/1", done.Planned, done.Processed)
	}
}
