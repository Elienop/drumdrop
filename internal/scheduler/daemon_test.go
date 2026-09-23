package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// fakeDaemonStore is a full Store for daemon tests. It records the interleaving
// of planner-side and worker-side calls so tests can assert plan-then-drain
// ordering, counts RequeueStaleRunning calls (must be exactly one at startup),
// and signals on a channel after each drain so a test can cancel deterministically
// once a cycle completes. All access is mutex-guarded because Run executes on the
// test goroutine but assertions read from another.
type fakeDaemonStore struct {
	mu sync.Mutex

	// ordered log of high-level operations, in invocation order.
	ops []string

	requeueCalls int
	planRuns     int // ListFollows calls (once per Plan)
	drainRuns    int // RunOnce drains (counted on the queue-empty claim)

	// One lesson is enqueued per Plan and drained per RunOnce so each cycle does
	// real plan+drain work. queued holds the jobs waiting to be claimed.
	queued []database.Job
	nextID int64

	// cycleDone receives once per completed drain (queue emptied) so a test can
	// wait for N cycles then cancel.
	cycleDone chan struct{}

	// Optional fault injection per call site.
	requeueErr error
	planErr    error
}

func newFakeDaemonStore() *fakeDaemonStore {
	return &fakeDaemonStore{cycleDone: make(chan struct{}, 64)}
}

func (s *fakeDaemonStore) record(op string) {
	s.mu.Lock()
	s.ops = append(s.ops, op)
	s.mu.Unlock()
}

func (s *fakeDaemonStore) snapshotOps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.ops))
	copy(out, s.ops)
	return out
}

// --- worker-side / startup ---

func (s *fakeDaemonStore) RequeueStaleRunning(ctx context.Context) (int, error) {
	s.mu.Lock()
	s.requeueCalls++
	s.ops = append(s.ops, "requeue")
	s.mu.Unlock()
	if s.requeueErr != nil {
		return 0, s.requeueErr
	}
	return 0, nil
}

func (s *fakeDaemonStore) ClaimNextJob(ctx context.Context) (database.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queued) == 0 {
		// Queue drained: this marks the end of one RunOnce drain.
		s.drainRuns++
		s.ops = append(s.ops, "drain-empty")
		select {
		case s.cycleDone <- struct{}{}:
		default:
		}
		return database.Job{}, false, nil
	}
	j := s.queued[0]
	s.queued = s.queued[1:]
	j.Status = database.JobRunning
	j.Attempts++
	s.ops = append(s.ops, "claim")
	return j, true, nil
}

func (s *fakeDaemonStore) GetFollow(ctx context.Context, id int64) (database.Follow, error) {
	return nodeFollow(), nil
}
func (s *fakeDaemonStore) GetLesson(ctx context.Context, id int) (database.Lesson, error) {
	return database.Lesson{}, nil
}
func (s *fakeDaemonStore) MarkJobRunning(ctx context.Context, id int64) error { return nil }
func (s *fakeDaemonStore) ListLessonsWithFiles(ctx context.Context) ([]database.Lesson, error) {
	return nil, nil
}
func (s *fakeDaemonStore) StartDownload(ctx context.Context, jobID int64, id int) error { return nil }
func (s *fakeDaemonStore) ConfirmDownload(ctx context.Context, jobID int64, id int) error {
	return nil
}
func (s *fakeDaemonStore) FinishDownload(ctx context.Context, jobID int64, id int, rec database.DownloadRecord) error {
	s.record("job-done")
	return nil
}
func (s *fakeDaemonStore) FailDownload(ctx context.Context, jobID int64, id int, lm, jm string) error {
	return nil
}
func (s *fakeDaemonStore) SkipDownload(ctx context.Context, jobID int64, id int, r string) error {
	return nil
}
func (s *fakeDaemonStore) CancelDownload(ctx context.Context, jobID int64, id int) error { return nil }

// --- planner-side ---

func (s *fakeDaemonStore) ListFollows(ctx context.Context) ([]database.Follow, error) {
	s.mu.Lock()
	s.planRuns++
	s.ops = append(s.ops, "plan")
	s.mu.Unlock()
	if s.planErr != nil {
		return nil, s.planErr
	}
	return []database.Follow{nodeFollow()}, nil
}

func (s *fakeDaemonStore) UpsertLesson(ctx context.Context, id int, title string, parent sql.NullInt64, brand string, position sql.NullInt64, followID sql.NullInt64) error {
	return nil
}
func (s *fakeDaemonStore) IsDownloaded(ctx context.Context, id int) (bool, error) { return false, nil }
func (s *fakeDaemonStore) ShouldSkipEnqueue(ctx context.Context, id int) (bool, error) {
	return false, nil
}
func (s *fakeDaemonStore) ActiveJobExists(ctx context.Context, id int) (bool, error) {
	return false, nil
}

func (s *fakeDaemonStore) EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.queued = append(s.queued, database.Job{
		ID:            s.nextID,
		FollowID:      followID,
		RailcontentID: railcontentID,
		Status:        database.JobQueued,
	})
	s.ops = append(s.ops, "enqueue")
	return s.nextID, true, nil
}

func (s *fakeDaemonStore) TouchLastSynced(ctx context.Context, id int64) error { return nil }

// daemonExpander hands every node follow a single fresh lesson id per Plan, so
// each cycle has one job to enqueue and drain.
type daemonExpander struct{ next int }

func (e *daemonExpander) Expand(f database.Follow, permIDs string) ([]musora.LessonItem, error) {
	e.next++
	return []musora.LessonItem{{ID: 1000 + e.next}}, nil
}

// daemonResolver resolves any id to a trivially downloadable lesson.
type daemonResolver struct{}

func (daemonResolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	return &musora.Lesson{ID: id, Title: "L"}, nil
}

// daemonDownloader always succeeds.
type daemonDownloader struct{}

func (daemonDownloader) Download(_ context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	return nil
}

func newTestDaemon(store *fakeDaemonStore) *Daemon {
	planner := &Planner{Store: store, Expander: &daemonExpander{}, PermIDs: "perm"}
	worker := NewWorker(store, daemonResolver{}, daemonDownloader{}, DefaultConfig(), "perm", nil)
	worker.Cfg.DownloadsDir = "/dl"
	worker.sleep = func(time.Duration) {} // no real delays
	return &Daemon{Store: store, Planner: planner, Worker: worker}
}

func TestDaemonRunOncePlansThenDrains(t *testing.T) {
	store := newFakeDaemonStore()
	d := newTestDaemon(store)

	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	ops := store.snapshotOps()
	// The first "plan" must precede the first "claim"/"drain-empty": planning has
	// to enqueue before the worker drains, or a brand-new lesson would wait a
	// whole cycle.
	planIdx, drainIdx := -1, -1
	for i, op := range ops {
		switch op {
		case "plan":
			if planIdx == -1 {
				planIdx = i
			}
		case "claim", "drain-empty":
			if drainIdx == -1 {
				drainIdx = i
			}
		}
	}
	if planIdx == -1 || drainIdx == -1 {
		t.Fatalf("missing plan/drain in ops: %v", ops)
	}
	if planIdx > drainIdx {
		t.Errorf("plan (idx %d) must come before drain (idx %d): %v", planIdx, drainIdx, ops)
	}

	// One cycle: planned 1, processed 1.
	if store.planRuns != 1 {
		t.Errorf("planRuns = %d, want 1", store.planRuns)
	}
	if len(ops) == 0 || !containsOp(ops, "job-done") {
		t.Errorf("expected the enqueued lesson to be downloaded (job-done): %v", ops)
	}
	// RunOnce alone never reclaims stale jobs — that is Run's startup-only job.
	if store.requeueCalls != 0 {
		t.Errorf("RequeueStaleRunning called %d times in RunOnce, want 0", store.requeueCalls)
	}
}

func TestDaemonRunOncePlanErrorIsFatal(t *testing.T) {
	store := newFakeDaemonStore()
	store.planErr = errors.New("db down")
	d := newTestDaemon(store)

	if err := d.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce: want a fatal error from a failed plan, got nil")
	}
}

func TestDaemonRunReclaimsOnceAndStopsOnCancel(t *testing.T) {
	store := newFakeDaemonStore()
	d := newTestDaemon(store)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, 5*time.Millisecond) }()

	// Wait for the immediate (startup) cycle to complete its drain.
	waitCycle(t, store.cycleDone)

	// Cancel and require a prompt nil return.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}

	// RequeueStaleRunning is a startup-only crash-recovery step: exactly once.
	store.mu.Lock()
	reclaims := store.requeueCalls
	store.mu.Unlock()
	if reclaims != 1 {
		t.Errorf("RequeueStaleRunning called %d times, want exactly 1 (startup only)", reclaims)
	}

	// requeue must be the very first recorded op, before any plan/drain.
	ops := store.snapshotOps()
	if len(ops) < 1 || ops[0] != "requeue" {
		t.Errorf("first ops = %v, want requeue first", ops)
	}
}

func TestDaemonRunDrivesMultipleCyclesBeforeCancel(t *testing.T) {
	store := newFakeDaemonStore()
	d := newTestDaemon(store)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, 5*time.Millisecond) }()

	// The immediate cycle plus at least one ticker-driven cycle = >=2 cycles.
	waitCycle(t, store.cycleDone) // startup/immediate cycle
	waitCycle(t, store.cycleDone) // first ticker cycle

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}

	store.mu.Lock()
	plans := store.planRuns
	drains := store.drainRuns
	store.mu.Unlock()
	if plans < 2 {
		t.Errorf("planRuns = %d, want >= 2 (immediate + >=1 ticker cycle)", plans)
	}
	if drains < 2 {
		t.Errorf("drainRuns = %d, want >= 2", drains)
	}
}

func TestDaemonRunContinuesAfterCycleError(t *testing.T) {
	// A cycle error (here: every Plan fails) must NOT kill the daemon. Run keeps
	// ticking until the context is cancelled, then returns nil.
	store := newFakeDaemonStore()
	store.planErr = errors.New("transient plan failure")
	d := newTestDaemon(store)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, 2*time.Millisecond) }()

	// Even with failing plans, ListFollows is attempted each cycle; wait until at
	// least two cycles have been attempted to prove the loop kept going.
	deadline := time.After(2 * time.Second)
	for {
		store.mu.Lock()
		plans := store.planRuns
		store.mu.Unlock()
		if plans >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("daemon stopped ticking after a cycle error")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil despite per-cycle errors", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}
}

func TestDaemonRunKickTriggersExtraCycle(t *testing.T) {
	// A send on the Kick channel must run one immediate extra cycle without
	// waiting for the interval ticker. The interval is set long enough that the
	// ticker cannot account for the second cycle, so the only way planRuns
	// reaches 2 is the kick.
	store := newFakeDaemonStore()
	d := newTestDaemon(store)
	kick := make(chan struct{}, 1)
	d.Kick = kick

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, time.Hour) }()

	// Wait for the immediate (startup) cycle to drain.
	waitCycle(t, store.cycleDone)

	// Kick and wait for the kick-driven cycle to drain.
	kick <- struct{}{}
	waitCycle(t, store.cycleDone)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}

	store.mu.Lock()
	plans := store.planRuns
	store.mu.Unlock()
	// Startup cycle + kick cycle = 2; the hour-long ticker cannot have fired.
	if plans != 2 {
		t.Errorf("planRuns = %d, want exactly 2 (startup + kick, no ticker tick)", plans)
	}
}

func TestDaemonPauseSkipsCyclesThenResumes(t *testing.T) {
	// A paused daemon must not run any cycle: its ticker ticks and any kick are
	// dropped while paused, so no Plan/drain happens. Resuming lets the next tick
	// (or kick) run a cycle again.
	store := newFakeDaemonStore()
	d := newTestDaemon(store)
	d.Pause()
	if !d.IsPaused() {
		t.Fatal("IsPaused() = false after Pause(), want true")
	}
	kick := make(chan struct{}, 1)
	d.Kick = kick

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, 2*time.Millisecond) }()

	// Let the immediate (startup) cycle and several ticker ticks fire while paused.
	// A kick while paused must also be dropped.
	kick <- struct{}{}
	time.Sleep(40 * time.Millisecond)

	store.mu.Lock()
	pausedPlans := store.planRuns
	store.mu.Unlock()
	if pausedPlans != 0 {
		t.Errorf("planRuns = %d while paused, want 0 (no cycles run)", pausedPlans)
	}

	// Resume: the next tick must run a cycle.
	d.Resume()
	if d.IsPaused() {
		t.Fatal("IsPaused() = true after Resume(), want false")
	}
	waitCycle(t, store.cycleDone)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}

	store.mu.Lock()
	plans := store.planRuns
	store.mu.Unlock()
	if plans < 1 {
		t.Errorf("planRuns = %d after Resume, want >= 1", plans)
	}
}

// waitCycle blocks until one cycle signals on ch, failing the test on timeout.
func waitCycle(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a cycle to complete")
	}
}

func containsOp(ops []string, want string) bool {
	for _, op := range ops {
		if op == want {
			return true
		}
	}
	return false
}
