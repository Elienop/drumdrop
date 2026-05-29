package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// fakeWorkerStore is an in-memory Store implementing the worker-side methods.
// It holds a queue of jobs to hand out via ClaimNextJob (oldest first, mirroring
// the real atomic claim: status→running, attempts++), tracks each job's evolving
// state, and records the calls the tests assert on. Planner-only methods panic
// so a stray call is caught.
type fakeWorkerStore struct {
	queue   []database.Job         // jobs still waiting to be claimed (FIFO)
	jobs    map[int64]database.Job // id → latest state of every job seen
	follows map[int64]database.Follow

	// Recorded calls, in invocation order.
	claims         int
	markRunning    []int64
	markDownloaded []markDownloadedCall
	markDone       []int64
	markJobFailed  []int64
	markFailed     []int // railcontent ids marked failed
	markSkipped    []int // railcontent ids marked skipped
	markDownloadng []int // railcontent ids marked downloading

	// Optional fault injection.
	claimErr     error
	getFollowErr error
}

type markDownloadedCall struct {
	id        int
	quality   string
	outputDir string
	videoPath string
	bytes     int64
}

func newFakeWorkerStore(jobs ...database.Job) *fakeWorkerStore {
	s := &fakeWorkerStore{
		jobs:    map[int64]database.Job{},
		follows: map[int64]database.Follow{},
	}
	for _, j := range jobs {
		s.queue = append(s.queue, j)
		s.jobs[j.ID] = j
	}
	return s
}

func (s *fakeWorkerStore) ClaimNextJob(ctx context.Context) (database.Job, bool, error) {
	s.claims++
	if s.claimErr != nil {
		return database.Job{}, false, s.claimErr
	}
	if len(s.queue) == 0 {
		return database.Job{}, false, nil
	}
	j := s.queue[0]
	s.queue = s.queue[1:]
	// Mirror the real claim: attempt #1 (running, attempts=1, started_at set).
	j.Status = database.JobRunning
	j.Attempts++
	j.StartedAt = sql.NullTime{Time: time.Unix(0, 0), Valid: true}
	s.jobs[j.ID] = j
	return j, true, nil
}

func (s *fakeWorkerStore) GetFollow(ctx context.Context, id int64) (database.Follow, error) {
	if s.getFollowErr != nil {
		return database.Follow{}, s.getFollowErr
	}
	f, ok := s.follows[id]
	if !ok {
		return database.Follow{}, errors.New("no such follow")
	}
	return f, nil
}

func (s *fakeWorkerStore) MarkJobRunning(ctx context.Context, id int64) error {
	s.markRunning = append(s.markRunning, id)
	j := s.jobs[id]
	j.Status = database.JobRunning
	j.Attempts++ // re-stamp + increment, mirroring the real store
	s.jobs[id] = j
	return nil
}

func (s *fakeWorkerStore) MarkJobDone(ctx context.Context, id int64) error {
	s.markDone = append(s.markDone, id)
	j := s.jobs[id]
	j.Status = database.JobDone
	s.jobs[id] = j
	return nil
}

func (s *fakeWorkerStore) MarkJobFailed(ctx context.Context, id int64, errMsg string) error {
	s.markJobFailed = append(s.markJobFailed, id)
	j := s.jobs[id]
	j.Status = database.JobFailed
	j.Error = sql.NullString{String: errMsg, Valid: true}
	s.jobs[id] = j
	return nil
}

func (s *fakeWorkerStore) MarkDownloading(ctx context.Context, id int) error {
	s.markDownloadng = append(s.markDownloadng, id)
	return nil
}

func (s *fakeWorkerStore) MarkDownloaded(ctx context.Context, id int, quality, outputDir, videoPath string, bytes int64) error {
	s.markDownloaded = append(s.markDownloaded, markDownloadedCall{
		id:        id,
		quality:   quality,
		outputDir: outputDir,
		videoPath: videoPath,
		bytes:     bytes,
	})
	return nil
}

func (s *fakeWorkerStore) MarkFailed(ctx context.Context, id int, errMsg string) error {
	s.markFailed = append(s.markFailed, id)
	return nil
}

func (s *fakeWorkerStore) MarkSkipped(ctx context.Context, id int, reason string) error {
	s.markSkipped = append(s.markSkipped, id)
	return nil
}

func (s *fakeWorkerStore) RequeueStaleRunning(ctx context.Context) (int, error) {
	return 0, nil
}

// Planner-side Store methods: unused by the Worker, so any call is a bug.
func (s *fakeWorkerStore) ListFollows(ctx context.Context) ([]database.Follow, error) {
	panic("ListFollows: not expected from Worker")
}
func (s *fakeWorkerStore) UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string) error {
	panic("UpsertLesson: not expected from Worker")
}
func (s *fakeWorkerStore) IsDownloaded(ctx context.Context, id int) (bool, error) {
	panic("IsDownloaded: not expected from Worker")
}
func (s *fakeWorkerStore) ActiveJobExists(ctx context.Context, railcontentID int) (bool, error) {
	panic("ActiveJobExists: not expected from Worker")
}
func (s *fakeWorkerStore) EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (int64, error) {
	panic("EnqueueJob: not expected from Worker")
}
func (s *fakeWorkerStore) TouchLastSynced(ctx context.Context, id int64) error {
	panic("TouchLastSynced: not expected from Worker")
}

// fakeResolver returns a canned lesson (or nil/error) per railcontent id.
type fakeResolver struct {
	lessons map[int]*musora.Lesson
	errs    map[int]error
}

func (r fakeResolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	if err := r.errs[id]; err != nil {
		return nil, err
	}
	return r.lessons[id], nil // nil lesson + nil err = unresolvable
}

// fakeDownloader fails the first failsBefore[id] attempts for each lesson, then
// succeeds. It records every Download call's options for assertions.
type fakeDownloader struct {
	failsBefore map[int]int // railcontent id → number of leading failures
	alwaysFail  bool        // every attempt fails

	// writeMP4, when non-nil, is written to the mp4 path DownloadLesson would
	// produce (Dir/<base>/<base>.mp4) on a successful download, so tests can
	// assert the worker stats it and records a real video_path + bytes.
	writeMP4 []byte

	attempts map[int]int // railcontent id → attempts seen so far
	calls    []musora.DownloadOpts
}

func newFakeDownloader() *fakeDownloader {
	return &fakeDownloader{failsBefore: map[int]int{}, attempts: map[int]int{}}
}

func (d *fakeDownloader) Download(l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls = append(d.calls, o)
	d.attempts[l.ID]++
	if d.alwaysFail {
		return errors.New("download blew up")
	}
	if d.attempts[l.ID] <= d.failsBefore[l.ID] {
		return errors.New("transient download error")
	}
	if d.writeMP4 != nil && !o.ResourcesOnly {
		// Mirror DownloadLesson's layout: Dir/<base>/<base>.mp4.
		base := fmt.Sprintf("%02d - %s", o.Index, musora.Sanitize(l.Title))
		dir := filepath.Join(o.Dir, base)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, base+".mp4"), d.writeMP4, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// recordingSleeper captures every sleep duration so retry tests can assert the
// backoff schedule without any real delay.
type recordingSleeper struct {
	durations []time.Duration
}

func (s *recordingSleeper) sleep(d time.Duration) { s.durations = append(s.durations, d) }

// lesson is a tiny helper building a resolvable lesson with the given id/title.
func lesson(id int, title string) *musora.Lesson {
	return &musora.Lesson{ID: id, Title: title}
}

// queuedJob builds a job in its pre-claim state (queued, attempts=0).
func queuedJob(id int64, followID int64, railcontentID int) database.Job {
	return database.Job{
		ID:            id,
		FollowID:      sql.NullInt64{Int64: followID, Valid: true},
		RailcontentID: railcontentID,
		Status:        database.JobQueued,
	}
}

func newTestWorker(store Store, res Resolver, dl Downloader, sleep func(time.Duration)) *Worker {
	w := NewWorker(store, res, dl, DefaultConfig(), "perm", nil)
	w.Cfg.DownloadsDir = "/dl"
	if sleep != nil {
		w.sleep = sleep
	}
	return w
}

func TestWorkerSuccessFirstTry(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	sleeper := &recordingSleeper{}

	w := newTestWorker(store, res, dl, sleeper.sleep)
	processed, err := w.RunOnce(context.Background(), 0)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if processed != 1 {
		t.Errorf("processed = %d, want 1", processed)
	}

	// Success on the first try: no retry, so no sleeps and no re-mark running.
	if len(sleeper.durations) != 0 {
		t.Errorf("sleeps = %v, want none on first-try success", sleeper.durations)
	}
	if len(store.markRunning) != 0 {
		t.Errorf("MarkJobRunning called %d times, want 0 (claim did attempt #1)", len(store.markRunning))
	}
	// attempts stays at 1 (the claim's attempt #1).
	if got := store.jobs[1].Attempts; got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Errorf("job status = %q, want done", got)
	}
	if got, want := store.markDone, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkJobDone = %v, want %v", got, want)
	}
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].id != 100 {
		t.Errorf("MarkDownloaded = %+v, want one call for lesson 100", store.markDownloaded)
	}
	// Output dir folds under the node follow; lessonDir is "01 - <title>".
	wantDir := "/dl/Beginner Course/01 - Lesson A"
	if got := store.markDownloaded[0].outputDir; got != wantDir {
		t.Errorf("lessonDir = %q, want %q", got, wantDir)
	}
	// Quality is the follow's saved quality (no override).
	if got := store.markDownloaded[0].quality; got != "1080" {
		t.Errorf("quality = %q, want 1080", got)
	}
	// One download, options correct (Index 1, ResourcesOnly off).
	if len(dl.calls) != 1 {
		t.Fatalf("download calls = %d, want 1", len(dl.calls))
	}
	if dl.calls[0].Index != 1 || dl.calls[0].Quality != "1080" || dl.calls[0].ResourcesOnly {
		t.Errorf("download opts = %+v, want Index 1 Quality 1080 ResourcesOnly false", dl.calls[0])
	}
}

func TestWorkerFailTwiceThenSucceed(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.failsBefore[100] = 2 // fail attempts 1 and 2, succeed on attempt 3
	sleeper := &recordingSleeper{}

	w := newTestWorker(store, res, dl, sleeper.sleep)
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// Three download attempts.
	if len(dl.calls) != 3 {
		t.Errorf("download calls = %d, want 3", len(dl.calls))
	}
	// MarkJobRunning re-called for attempts 2 and 3 (not attempt 1 — the claim
	// did that). attempts: 1 (claim) + 2 (re-marks) = 3.
	if got, want := store.markRunning, []int64{1, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkJobRunning = %v, want %v (re-marked for attempts 2 and 3)", got, want)
	}
	if got := store.jobs[1].Attempts; got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
	// Two sleeps, following backoff(attempt-2): backoff(0)=5s, backoff(1)=30s.
	// (The 2m third entry is only reached if MaxAttempts is raised above 3.)
	wantSleeps := []time.Duration{5 * time.Second, 30 * time.Second}
	if !reflect.DeepEqual(sleeper.durations, wantSleeps) {
		t.Errorf("sleeps = %v, want %v", sleeper.durations, wantSleeps)
	}
	// Ends done; failed paths not taken.
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Errorf("job status = %q, want done", got)
	}
	if len(store.markFailed) != 0 || len(store.markJobFailed) != 0 {
		t.Errorf("no failed marks expected on eventual success: markFailed=%v markJobFailed=%v", store.markFailed, store.markJobFailed)
	}
	if got, want := store.markDone, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkJobDone = %v, want %v", got, want)
	}
}

func TestWorkerAlwaysFails(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.alwaysFail = true
	sleeper := &recordingSleeper{}

	w := newTestWorker(store, res, dl, sleeper.sleep)
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// Exactly MaxAttempts (3) download attempts.
	if len(dl.calls) != w.Cfg.MaxAttempts {
		t.Errorf("download calls = %d, want %d", len(dl.calls), w.Cfg.MaxAttempts)
	}
	// Exactly MaxAttempts-1 (2) sleeps.
	if len(sleeper.durations) != w.Cfg.MaxAttempts-1 {
		t.Errorf("sleeps = %d, want %d", len(sleeper.durations), w.Cfg.MaxAttempts-1)
	}
	// attempts: 1 (claim) + 2 (re-marks) = 3.
	if got := store.jobs[1].Attempts; got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
	// Both lesson and job end failed; the success path is not taken.
	if got, want := store.markFailed, []int{100}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkFailed = %v, want %v", got, want)
	}
	if got, want := store.markJobFailed, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkJobFailed = %v, want %v", got, want)
	}
	if got := store.jobs[1].Status; got != database.JobFailed {
		t.Errorf("job status = %q, want failed", got)
	}
	if len(store.markDone) != 0 || len(store.markDownloaded) != 0 {
		t.Errorf("no success marks expected: markDone=%v markDownloaded=%v", store.markDone, store.markDownloaded)
	}
}

func TestWorkerUnresolvableLessonSkipped(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	// Resolve returns (nil, nil) — gated/missing lesson.
	res := fakeResolver{lessons: map[int]*musora.Lesson{}}
	dl := newFakeDownloader()
	sleeper := &recordingSleeper{}

	w := newTestWorker(store, res, dl, sleeper.sleep)
	processed, err := w.RunOnce(context.Background(), 0)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if processed != 1 {
		t.Errorf("processed = %d, want 1", processed)
	}

	// Skipped + job failed; nothing downloaded, no retries/sleeps.
	if got, want := store.markSkipped, []int{100}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkSkipped = %v, want %v", got, want)
	}
	if got, want := store.markJobFailed, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkJobFailed = %v, want %v", got, want)
	}
	if len(dl.calls) != 0 {
		t.Errorf("download calls = %d, want 0 (unresolvable)", len(dl.calls))
	}
	if len(store.markRunning) != 0 || len(sleeper.durations) != 0 {
		t.Errorf("no re-mark/sleep on unresolvable: markRunning=%v sleeps=%v", store.markRunning, sleeper.durations)
	}
	if len(store.markFailed) != 0 {
		t.Errorf("MarkFailed should not be called for an unresolvable lesson (it is skipped): %v", store.markFailed)
	}
}

func TestWorkerNeverAbortsOnFailingJob(t *testing.T) {
	// Job 1 always fails; job 2 succeeds. Both must be processed in one RunOnce.
	store := newFakeWorkerStore(
		queuedJob(1, nodeFollow().ID, 100),
		queuedJob(2, nodeFollow().ID, 200),
	)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{
		100: lesson(100, "Bad"),
		200: lesson(200, "Good"),
	}}
	dl := &fakeDownloader{
		failsBefore: map[int]int{100: 99}, // lesson 100 never succeeds
		attempts:    map[int]int{},
	}
	sleeper := &recordingSleeper{}

	w := newTestWorker(store, res, dl, sleeper.sleep)
	processed, err := w.RunOnce(context.Background(), 0)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if processed != 2 {
		t.Errorf("processed = %d, want 2 (failing job must not stop the next)", processed)
	}

	// Job 1 failed, job 2 done.
	if got := store.jobs[1].Status; got != database.JobFailed {
		t.Errorf("job 1 status = %q, want failed", got)
	}
	if got := store.jobs[2].Status; got != database.JobDone {
		t.Errorf("job 2 status = %q, want done", got)
	}
	if got, want := store.markFailed, []int{100}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkFailed = %v, want %v", got, want)
	}
	if got, want := store.markDone, []int64{2}; !reflect.DeepEqual(got, want) {
		t.Errorf("MarkJobDone = %v, want %v", got, want)
	}
}

func TestWorkerLimitCapsProcessed(t *testing.T) {
	store := newFakeWorkerStore(
		queuedJob(1, nodeFollow().ID, 100),
		queuedJob(2, nodeFollow().ID, 200),
		queuedJob(3, nodeFollow().ID, 300),
	)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{
		100: lesson(100, "A"),
		200: lesson(200, "B"),
		300: lesson(300, "C"),
	}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	processed, err := w.RunOnce(context.Background(), 2)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if processed != 2 {
		t.Errorf("processed = %d, want 2 (capped by limit)", processed)
	}
	// Only two claims consumed; the third job is left queued.
	if len(dl.calls) != 2 {
		t.Errorf("download calls = %d, want 2", len(dl.calls))
	}
	if got := store.jobs[3].Status; got != database.JobQueued {
		t.Errorf("job 3 status = %q, want still queued", got)
	}
}

func TestWorkerCanceledContextReturnsNil(t *testing.T) {
	store := newFakeWorkerStore(queuedJob(1, nodeFollow().ID, 100))
	store.follows[nodeFollow().ID] = nodeFollow()
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "A")}}
	dl := newFakeDownloader()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	processed, err := w.RunOnce(ctx, 0)
	if err != nil {
		t.Errorf("RunOnce on cancelled ctx = %v, want nil", err)
	}
	if processed != 0 {
		t.Errorf("processed = %d, want 0 (no claim on a cancelled context)", processed)
	}
	if store.claims != 0 {
		t.Errorf("ClaimNextJob called %d times, want 0", store.claims)
	}
}

func TestWorkerClaimErrorIsFatal(t *testing.T) {
	store := newFakeWorkerStore()
	store.claimErr = errors.New("db down")
	res := fakeResolver{}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	_, err := w.RunOnce(context.Background(), 0)
	if err == nil {
		t.Fatal("RunOnce: want a fatal error from ClaimNextJob, got nil")
	}
}

func TestWorkerNullFollowFallsBackToParentTitle(t *testing.T) {
	// Job with no follow (FollowID NULL): outDir falls back to the lesson's
	// parent content title, sanitized, under the configured DownloadsDir.
	job := database.Job{ID: 1, RailcontentID: 100, Status: database.JobQueued}
	store := newFakeWorkerStore(job)

	les := lesson(100, "Orphan Lesson")
	les.ParentContentData = []struct {
		Title string `json:"title"`
	}{{Title: "Some Course"}}
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: les}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	wantDir := "/dl/Some Course/01 - Orphan Lesson"
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != wantDir {
		t.Errorf("lessonDir = %+v, want %q", store.markDownloaded, wantDir)
	}
	// No follow quality available and no override → empty quality.
	if got := store.markDownloaded[0].quality; got != "" {
		t.Errorf("quality = %q, want empty (no follow, no override)", got)
	}
}

func TestWorkerNullFollowFallsBackToContentID(t *testing.T) {
	// No follow AND no parent title: the folder is content-<id>.
	job := database.Job{ID: 1, RailcontentID: 777, Status: database.JobQueued}
	store := newFakeWorkerStore(job)
	res := fakeResolver{lessons: map[int]*musora.Lesson{777: lesson(777, "Lone")}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	wantDir := "/dl/content-777/01 - Lone"
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != wantDir {
		t.Errorf("lessonDir = %+v, want %q", store.markDownloaded, wantDir)
	}
}

func TestWorkerGetFollowErrorFallsBackToDefaults(t *testing.T) {
	// The job references a follow, but GetFollow fails (e.g. the follow row was
	// deleted between enqueue and claim). The worker must not abort: it logs,
	// uses a zero follow, and folds under the lesson's parent title.
	job := queuedJob(1, 42, 100)
	store := newFakeWorkerStore(job)
	store.getFollowErr = errors.New("follow gone")

	les := lesson(100, "Resilient")
	les.ParentContentData = []struct {
		Title string `json:"title"`
	}{{Title: "Recovered Course"}}
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: les}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Errorf("job status = %q, want done despite GetFollow failure", got)
	}
	wantDir := "/dl/Recovered Course/01 - Resilient"
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != wantDir {
		t.Errorf("lessonDir = %+v, want %q", store.markDownloaded, wantDir)
	}
}

func TestWorkerQualityAndResourcesOnlyOverride(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow() // saved quality 1080

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "A")}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.Quality = "best"     // override wins over the follow's 1080
	w.Cfg.ResourcesOnly = true // flows into DownloadOpts

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if len(dl.calls) != 1 {
		t.Fatalf("download calls = %d, want 1", len(dl.calls))
	}
	if dl.calls[0].Quality != "best" || !dl.calls[0].ResourcesOnly {
		t.Errorf("download opts = %+v, want Quality best ResourcesOnly true", dl.calls[0])
	}
	if got := store.markDownloaded[0].quality; got != "best" {
		t.Errorf("MarkDownloaded quality = %q, want best", got)
	}
}

func TestWorkerBackoffClampsToLastEntry(t *testing.T) {
	// With MaxAttempts=5 and the default 3-entry backoff [5s,30s,2m], an
	// always-failing download retries before attempts 2..5, so four sleeps fire:
	// backoff(0)=5s, backoff(1)=30s, backoff(2)=2m, backoff(3)→clamped to the last
	// entry=2m. This exercises both the third (2m) entry and the clamp.
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.alwaysFail = true
	sleeper := &recordingSleeper{}

	w := newTestWorker(store, res, dl, sleeper.sleep)
	w.Cfg.MaxAttempts = 5 // raise above the 3-entry backoff to reach the clamp

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// Five attempts, four sleeps following the clamped schedule.
	if len(dl.calls) != 5 {
		t.Errorf("download calls = %d, want 5", len(dl.calls))
	}
	wantSleeps := []time.Duration{
		5 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		2 * time.Minute, // backoff(3) clamped to the last entry
	}
	if !reflect.DeepEqual(sleeper.durations, wantSleeps) {
		t.Errorf("sleeps = %v, want %v", sleeper.durations, wantSleeps)
	}
}

func TestWorkerCancelDuringBackoffAbortsRetries(t *testing.T) {
	// A context cancelled while waiting out a retry backoff must abort the
	// remaining attempts promptly, leaving the job to be re-queued next cycle
	// rather than burning through every attempt.
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.alwaysFail = true // force a retry so the backoff wait is reached

	ctx, cancel := context.WithCancel(context.Background())
	// The first backoff wait cancels the context, simulating a SIGINT arriving
	// mid-backoff; waitBackoff must then return false and stop retrying.
	sleeper := &recordingSleeper{}
	cancelling := func(d time.Duration) {
		sleeper.sleep(d)
		cancel()
	}

	w := newTestWorker(store, res, dl, cancelling)
	w.Cfg.MaxAttempts = 5

	processed, err := w.RunOnce(ctx, 0)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if processed != 1 {
		t.Errorf("processed = %d, want 1 (the one claimed job)", processed)
	}

	// Attempt 1 ran, then one backoff wait fired and cancelled: no further
	// attempts, so exactly one sleep and two download calls (attempt 1 + the
	// attempt-2 download is never reached after the abort).
	if len(sleeper.durations) != 1 {
		t.Errorf("sleeps = %v, want exactly 1 before the cancel aborts retries", sleeper.durations)
	}
	if len(dl.calls) != 1 {
		t.Errorf("download calls = %d, want 1 (retries aborted by cancel during backoff)", len(dl.calls))
	}
	// The job is left untouched by the failed-paths: it is neither marked done nor
	// marked failed, so the next cycle re-queues it.
	if len(store.markDone) != 0 {
		t.Errorf("MarkJobDone called %v, want none on cancellation", store.markDone)
	}
	if len(store.markFailed) != 0 || len(store.markJobFailed) != 0 {
		t.Errorf("no failed marks expected on cancellation: markFailed=%v markJobFailed=%v", store.markFailed, store.markJobFailed)
	}
}

func TestWorkerMarkDownloadedRecordsVideoMetadata(t *testing.T) {
	// On a successful real download the worker stats the produced mp4 and records
	// a non-empty video_path and non-zero byte count (the write-only-columns fix).
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes") // 14 bytes

	// Point DownloadsDir at a real temp dir so the produced mp4 can be stat'd.
	tmp := t.TempDir()
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = tmp

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	if len(store.markDownloaded) != 1 {
		t.Fatalf("MarkDownloaded calls = %d, want 1", len(store.markDownloaded))
	}
	got := store.markDownloaded[0]
	wantDir := filepath.Join(tmp, "Beginner Course", "01 - Lesson A")
	wantVideo := filepath.Join(wantDir, "01 - Lesson A.mp4")
	if got.outputDir != wantDir {
		t.Errorf("outputDir = %q, want %q", got.outputDir, wantDir)
	}
	if got.videoPath != wantVideo {
		t.Errorf("videoPath = %q, want %q", got.videoPath, wantVideo)
	}
	if got.bytes != int64(len(dl.writeMP4)) {
		t.Errorf("bytes = %d, want %d", got.bytes, len(dl.writeMP4))
	}
}

func TestWorkerMarkDownloadedNoVideoWhenResourcesOnly(t *testing.T) {
	// ResourcesOnly produces no mp4, so MarkDownloaded must record an empty
	// video_path and zero bytes even though the download succeeded.
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("should not be written in resources-only")

	tmp := t.TempDir()
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = tmp
	w.Cfg.ResourcesOnly = true

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if len(store.markDownloaded) != 1 {
		t.Fatalf("MarkDownloaded calls = %d, want 1", len(store.markDownloaded))
	}
	got := store.markDownloaded[0]
	if got.videoPath != "" || got.bytes != 0 {
		t.Errorf("videoPath/bytes = %q/%d, want empty/0 on ResourcesOnly", got.videoPath, got.bytes)
	}
}
