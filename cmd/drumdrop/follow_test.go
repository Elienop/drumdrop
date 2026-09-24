package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
	"github.com/elienop/drumdrop/internal/scheduler"
)

// ---- flag handling -------------------------------------------------------

// TestFollowFlagSplitting proves --brand is honored alongside the existing
// value flags whether it appears before or after the positional target. It
// drives parseFollowArgs — the SAME parser cmdFollow runs — so a real parser
// regression is caught.
func TestFollowFlagSplitting(t *testing.T) {
	if !valueFlags["--brand"] {
		t.Fatal("--brand must be registered in valueFlags so its value is not orphaned")
	}

	parse := func(argv []string) (target, brand, quality, instructor string, err error) {
		args, err := parseFollowArgs(argv)
		if err != nil {
			return "", "", "", "", err
		}
		if len(args.positionals) > 0 {
			target = args.positionals[0]
		}
		return target, args.brand, args.quality, args.instructor, nil
	}

	t.Run("brand value after target is kept", func(t *testing.T) {
		target, brand, quality, _, err := parse([]string{"409875", "--brand", "pianote", "--quality", "1080"})
		if err != nil {
			t.Fatal(err)
		}
		if target != "409875" {
			t.Errorf("target = %q, want 409875", target)
		}
		if brand != "pianote" {
			t.Errorf("brand = %q, want pianote", brand)
		}
		if quality != "1080" {
			t.Errorf("quality = %q, want 1080", quality)
		}
	})

	t.Run("instructor flag", func(t *testing.T) {
		_, _, _, instructor, err := parse([]string{"--instructor", "aaron-edgar"})
		if err != nil {
			t.Fatal(err)
		}
		if instructor != "aaron-edgar" {
			t.Errorf("instructor = %q, want aaron-edgar", instructor)
		}
	})
}

// ---- runSync via the scheduler -------------------------------------------
//
// The full behavioral guarantees (skip already-downloaded, dedupe active jobs,
// retry/backoff, never-abort) are exercised in internal/scheduler's planner and
// worker tests. These CLI tests prove that runSync wires the Planner + Worker
// correctly: dry-run records but neither enqueues nor downloads, and a real run
// plans then drains with --limit capping NEW downloads end to end.

// cliStore is an in-memory scheduler.Store covering both the planner and worker
// sides, just enough to drive runSync's plan-then-drain path with no database.
type cliStore struct {
	follows    []database.Follow
	downloaded map[int]bool

	upserts    []int
	enqueued   []int
	queue      []database.Job
	jobs       map[int64]database.Job
	markedDLed []int
	touched    []int64
	nextJobID  int64
}

func newCLIStore(follows []database.Follow) *cliStore {
	return &cliStore{
		follows:    follows,
		downloaded: map[int]bool{},
		jobs:       map[int64]database.Job{},
	}
}

func (s *cliStore) ListFollows(ctx context.Context) ([]database.Follow, error) {
	return s.follows, nil
}
func (s *cliStore) UpsertLesson(ctx context.Context, id int, title string, parent sql.NullInt64, brand string, position sql.NullInt64, followID sql.NullInt64) error {
	s.upserts = append(s.upserts, id)
	return nil
}
func (s *cliStore) IsDownloaded(ctx context.Context, id int) (bool, error) {
	return s.downloaded[id], nil
}
func (s *cliStore) ShouldSkipEnqueue(ctx context.Context, id int) (bool, error) {
	return s.downloaded[id], nil
}
func (s *cliStore) ActiveJobExists(ctx context.Context, id int) (bool, error) {
	for _, j := range s.jobs {
		if j.RailcontentID == id && (j.Status == database.JobQueued || j.Status == database.JobRunning) {
			return true, nil
		}
	}
	return false, nil
}
func (s *cliStore) EnqueueJob(ctx context.Context, followID sql.NullInt64, id int) (int64, bool, error) {
	// Mirror the real store's atomic dedup: reuse any queued/running job for
	// this lesson and report created=false instead of inserting a duplicate.
	for _, j := range s.jobs {
		if j.RailcontentID == id && (j.Status == database.JobQueued || j.Status == database.JobRunning) {
			return j.ID, false, nil
		}
	}
	s.nextJobID++
	j := database.Job{ID: s.nextJobID, FollowID: followID, RailcontentID: id, Status: database.JobQueued}
	s.queue = append(s.queue, j)
	s.jobs[j.ID] = j
	s.enqueued = append(s.enqueued, id)
	return j.ID, true, nil
}
func (s *cliStore) TouchLastSynced(ctx context.Context, id int64) error {
	s.touched = append(s.touched, id)
	return nil
}
func (s *cliStore) ClaimNextJob(ctx context.Context) (database.Job, bool, error) {
	if len(s.queue) == 0 {
		return database.Job{}, false, nil
	}
	j := s.queue[0]
	s.queue = s.queue[1:]
	j.Status = database.JobRunning
	j.Attempts++
	s.jobs[j.ID] = j
	return j, true, nil
}
func (s *cliStore) GetFollow(ctx context.Context, id int64) (database.Follow, error) {
	for _, f := range s.follows {
		if f.ID == id {
			return f, nil
		}
	}
	return database.Follow{}, sql.ErrNoRows
}

// GetLesson returns a zero lesson (no recorded position) so the worker's
// numbering falls back to 1; the CLI tests assert behaviour that predates real
// positions, so a zero row keeps their expectations intact.
func (s *cliStore) GetLesson(ctx context.Context, id int) (database.Lesson, error) {
	return database.Lesson{}, nil
}
func (s *cliStore) MarkJobRunning(ctx context.Context, id int64) error { return nil }
func (s *cliStore) ListLessonsWithFiles(ctx context.Context) ([]database.Lesson, error) {
	return nil, nil
}
func (s *cliStore) setJobStatus(id int64, status string) {
	j := s.jobs[id]
	j.Status = status
	s.jobs[id] = j
}
func (s *cliStore) StartDownload(ctx context.Context, jobID int64, id int) error { return nil }
func (s *cliStore) FinishDownload(ctx context.Context, jobID int64, id int, rec database.DownloadRecord) error {
	s.markedDLed = append(s.markedDLed, id)
	s.downloaded[id] = true
	s.setJobStatus(jobID, database.JobDone)
	return nil
}
func (s *cliStore) FailDownload(ctx context.Context, jobID int64, id int, lessonMsg, jobMsg string) error {
	s.setJobStatus(jobID, database.JobFailed)
	return nil
}
func (s *cliStore) SkipDownload(ctx context.Context, jobID int64, id int, reason string) error {
	s.setJobStatus(jobID, database.JobFailed)
	return nil
}
func (s *cliStore) CancelDownload(ctx context.Context, jobID int64, id int) error {
	if s.jobs[jobID].Status == database.JobRunning {
		s.setJobStatus(jobID, database.JobCanceled)
	}
	return nil
}
func (s *cliStore) RequeueStaleRunning(ctx context.Context) (int, error) { return 0, nil }
func (s *cliStore) ConfirmDownload(ctx context.Context, jobID int64, id int) error {
	return nil
}

// cliExpander returns a fixed id list per follow id.
type cliExpander struct{ ids map[int64][]int }

func (e cliExpander) Expand(f database.Follow, permIDs string) ([]musora.LessonItem, error) {
	ids := e.ids[f.ID]
	items := make([]musora.LessonItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, musora.LessonItem{ID: id})
	}
	return items, nil
}

// cliResolver returns a stub lesson for every id without touching the network.
type cliResolver struct{}

func (cliResolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	return &musora.Lesson{ID: id, Title: "L"}, nil
}

// cliDownloader records every download and never touches yt-dlp.
type cliDownloader struct{ calls []int }

func (d *cliDownloader) Download(_ context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls = append(d.calls, l.ID)
	return nil
}

func cliNodeFollow(id int64, rc int) database.Follow {
	return database.Follow{
		ID:            id,
		Kind:          "node",
		RailcontentID: sql.NullInt64{Int64: int64(rc), Valid: true},
		Title:         "Course",
		Brand:         "drumeo",
		Quality:       "best",
	}
}

// newSyncHarness is a planner and a worker over store, downloading into a
// folder of the test's own: the worker writes real folders there (D66).
func newSyncHarness(t *testing.T, store *cliStore, exp cliExpander, dl *cliDownloader) (*scheduler.Planner, *scheduler.Worker) {
	t.Helper()
	planner := &scheduler.Planner{Store: store, Expander: exp, PermIDs: "perm"}
	cfg := scheduler.DefaultConfig()
	cfg.DownloadsDir = t.TempDir()
	worker := scheduler.NewWorker(store, cliResolver{}, dl, cfg, "perm", nil)
	return planner, worker
}

// TestRunSyncDryRunDownloadsNothing: dry-run records every lesson but enqueues no
// jobs, downloads nothing, marks nothing downloaded, and does not stamp follows.
func TestRunSyncDryRunDownloadsNothing(t *testing.T) {
	store := newCLIStore([]database.Follow{cliNodeFollow(1, 100)})
	exp := cliExpander{ids: map[int64][]int{1: {11, 12, 13}}}
	dl := &cliDownloader{}
	planner, worker := newSyncHarness(t, store, exp, dl)

	var buf bytes.Buffer
	if err := runSync(context.Background(), planner, worker, true /* dryRun */, 0, &buf); err != nil {
		t.Fatal(err)
	}

	if len(store.upserts) != 3 {
		t.Errorf("dry-run should still upsert all ids; upserts=%v", store.upserts)
	}
	if len(dl.calls) != 0 {
		t.Errorf("dry-run must download nothing; downloaded=%v", dl.calls)
	}
	if len(store.enqueued) != 0 {
		t.Errorf("dry-run must enqueue no jobs; enqueued=%v", store.enqueued)
	}
	if len(store.markedDLed) != 0 {
		t.Errorf("dry-run must mark nothing downloaded; markedDLed=%v", store.markedDLed)
	}
	if len(store.touched) != 0 {
		t.Errorf("dry-run must not stamp last_synced; touched=%v", store.touched)
	}
	if !strings.Contains(buf.String(), "3 new lesson(s) would be queued") {
		t.Errorf("dry-run summary should report the would-be count; got:\n%s", buf.String())
	}
}

// TestRunSyncLimitCapsNewDownloads: --limit caps the number of NEW downloads
// across the whole run, end to end through the Planner + Worker. Already
// downloaded lessons are skipped (dedup) and never handed to the downloader.
func TestRunSyncLimitCapsNewDownloads(t *testing.T) {
	store := newCLIStore([]database.Follow{cliNodeFollow(1, 100), cliNodeFollow(2, 200)})
	store.downloaded[11] = true // already downloaded → skipped, not re-downloaded
	exp := cliExpander{ids: map[int64][]int{
		1: {11, 12, 13},
		2: {21, 22},
	}}
	dl := &cliDownloader{}
	planner, worker := newSyncHarness(t, store, exp, dl)

	var buf bytes.Buffer
	if err := runSync(context.Background(), planner, worker, false, 2 /* limit */, &buf); err != nil {
		t.Fatal(err)
	}

	if len(dl.calls) != 2 {
		t.Errorf("downloaded %d lessons, want exactly 2 (the --limit cap); calls=%v", len(dl.calls), dl.calls)
	}
	for _, id := range dl.calls {
		if id == 11 {
			t.Errorf("already-downloaded lesson 11 must not be downloaded; calls=%v", dl.calls)
		}
	}
	if !strings.Contains(buf.String(), "Sync complete") {
		t.Errorf("real run should print a completion summary; got:\n%s", buf.String())
	}
}

// interruptingDownloader is a download an interrupt stops: it writes a partial
// file in its folder, the interrupt lands (cancel), and yt-dlp, killed,
// returns the context's error.
type interruptingDownloader struct {
	cancel context.CancelFunc
	dirs   []string
}

func (d *interruptingDownloader) Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	d.dirs = append(d.dirs, o.Dir)
	if err := os.WriteFile(filepath.Join(o.Dir, "partial.mp4.part"), []byte("partial"), 0o644); err != nil {
		return err
	}
	d.cancel()
	return ctx.Err()
}

// TestRunSyncInterruptStopsAndSaysSo: an interrupt stops the download in
// progress, starts no other, leaves no private folder behind, and sync reports
// it as an interruption, not as a completed sync.
func TestRunSyncInterruptStopsAndSaysSo(t *testing.T) {
	store := newCLIStore([]database.Follow{cliNodeFollow(1, 100)})
	exp := cliExpander{ids: map[int64][]int{1: {11, 12}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dl := &interruptingDownloader{cancel: cancel}
	planner := &scheduler.Planner{Store: store, Expander: exp, PermIDs: "perm"}
	cfg := scheduler.DefaultConfig()
	cfg.DownloadsDir = t.TempDir()
	worker := scheduler.NewWorker(store, cliResolver{}, dl, cfg, "perm", nil)

	var buf bytes.Buffer
	err := runSync(ctx, planner, worker, false, 0, &buf)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("runSync = %v, want an interruption error", err)
	}
	if strings.Contains(buf.String(), "Sync complete") {
		t.Errorf("an interrupted sync reported itself complete:\n%s", buf.String())
	}
	if len(dl.dirs) != 1 {
		t.Fatalf("downloads started = %d, want 1 (none after the interrupt)", len(dl.dirs))
	}
	if _, err := os.Stat(dl.dirs[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stopped download's private folder %q is still there (err=%v)", dl.dirs[0], err)
	}
	if len(store.markedDLed) != 0 {
		t.Errorf("an interrupted download was recorded: %v", store.markedDLed)
	}
}
