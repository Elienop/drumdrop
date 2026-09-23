package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	lessons map[int]database.Lesson // railcontent id → stored lesson row (for GetLesson)

	// Recorded calls, in invocation order.
	claims          int
	markRunning     []int64
	markDownloaded  []markDownloadedCall
	markDone        []int64
	markJobFailed   []int64
	markJobCanceled []int64
	markFailed      []int // railcontent ids marked failed
	markSkipped     []int // railcontent ids marked skipped
	markDownloadng  []int // railcontent ids marked downloading

	// ctx.Err() observed at each SkipDownload/CancelDownload call, so the
	// shutdown-finalize test can assert those writes do NOT ride a cancelled ctx.
	markSkippedCtxErr     []error
	markJobCanceledCtxErr []error

	// Optional fault injection.
	claimErr     error
	getFollowErr error
	// gone marks jobs a delete that removes the lesson's files removed: every
	// guarded write for them returns database.ErrDownloadAbandoned joined with
	// database.ErrDiscardDownload and database.ErrLessonDeleted, and records
	// nothing. skipped marks jobs a Skip removed (ErrDownloadAbandoned with
	// ErrDiscardDownload). kept marks jobs a delete that keeps the files
	// removed (ErrDownloadAbandoned alone).
	gone    map[int64]bool
	skipped map[int64]bool
	kept    map[int64]bool
	// getLessonErr, when set, is what GetLesson answers.
	getLessonErr error
	// finishErr, when set, is what FinishDownload answers (nothing recorded).
	finishErr error
	// requeued marks jobs another process requeued meanwhile: CancelDownload
	// answers database.ErrDownloadCanceled and records nothing.
	requeued map[int64]bool
	// canceled marks jobs canceled in the database while the worker holds
	// them: StartDownload and ConfirmDownload return
	// database.ErrDownloadCanceled; the terminal writes land.
	canceled map[int64]bool
	confirms []int64
	// onConfirm, when set, runs after a ConfirmDownload passed: a delete or a
	// cancel landing while the download is being moved.
	onConfirm func()
	// withFiles is what ListLessonsWithFiles returns (or withFilesErr).
	withFiles    []database.Lesson
	withFilesErr error
}

type markDownloadedCall struct {
	id        int
	quality   string
	outputDir string
	videoPath string
	bytes     int64
	entries   []string
}

func newFakeWorkerStore(jobs ...database.Job) *fakeWorkerStore {
	s := &fakeWorkerStore{
		jobs:    map[int64]database.Job{},
		follows: map[int64]database.Follow{},
		lessons: map[int]database.Lesson{},
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

// GetLesson returns the stored lesson row for an id (its Position drives the
// worker's folder numbering). An unknown id returns a zero lesson with no error,
// so the worker falls back to index 1 — never panicking on a download path.
func (s *fakeWorkerStore) GetLesson(ctx context.Context, id int) (database.Lesson, error) {
	if s.getLessonErr != nil {
		return database.Lesson{}, s.getLessonErr
	}
	return s.lessons[id], nil
}

func (s *fakeWorkerStore) MarkJobRunning(ctx context.Context, id int64) error {
	s.markRunning = append(s.markRunning, id)
	j := s.jobs[id]
	j.Status = database.JobRunning
	j.Attempts++ // re-stamp + increment, mirroring the real store
	s.jobs[id] = j
	return nil
}

func (s *fakeWorkerStore) markJobDone(id int64) {
	s.markDone = append(s.markDone, id)
	j := s.jobs[id]
	j.Status = database.JobDone
	s.jobs[id] = j
}

func (s *fakeWorkerStore) markJobFailedAs(id int64, errMsg string) {
	s.markJobFailed = append(s.markJobFailed, id)
	j := s.jobs[id]
	j.Status = database.JobFailed
	j.Error = sql.NullString{String: errMsg, Valid: true}
	s.jobs[id] = j
}

// abandoned mirrors the store's job guard: a job a delete removed takes no
// write, and says what the delete wanted.
func (s *fakeWorkerStore) abandoned(jobID int64) error {
	switch {
	case s.gone[jobID]:
		return fmt.Errorf("job %d: %w: %w: %w", jobID, database.ErrDownloadAbandoned, database.ErrDiscardDownload, database.ErrLessonDeleted)
	case s.skipped[jobID]:
		return fmt.Errorf("job %d: %w: %w", jobID, database.ErrDownloadAbandoned, database.ErrDiscardDownload)
	case s.kept[jobID]:
		return fmt.Errorf("job %d: %w", jobID, database.ErrDownloadAbandoned)
	}
	return nil
}

// running mirrors the guard of the writes that start or confirm a download:
// abandoned, or canceled while the worker held the job.
func (s *fakeWorkerStore) running(jobID int64) error {
	if err := s.abandoned(jobID); err != nil {
		return err
	}
	if s.canceled[jobID] {
		return fmt.Errorf("job %d: %w", jobID, database.ErrDownloadCanceled)
	}
	return nil
}

func (s *fakeWorkerStore) ConfirmDownload(ctx context.Context, jobID int64, id int) error {
	s.confirms = append(s.confirms, jobID)
	if err := s.running(jobID); err != nil {
		return err
	}
	if s.onConfirm != nil {
		s.onConfirm()
	}
	return nil
}

func (s *fakeWorkerStore) ListLessonsWithFiles(ctx context.Context) ([]database.Lesson, error) {
	return s.withFiles, s.withFilesErr
}

func (s *fakeWorkerStore) StartDownload(ctx context.Context, jobID int64, id int) error {
	if err := s.running(jobID); err != nil {
		return err
	}
	s.markDownloadng = append(s.markDownloadng, id)
	return nil
}

func (s *fakeWorkerStore) FinishDownload(ctx context.Context, jobID int64, id int, rec database.DownloadRecord) error {
	if err := s.abandoned(jobID); err != nil {
		return err
	}
	if s.finishErr != nil {
		return s.finishErr
	}
	s.markDownloaded = append(s.markDownloaded, markDownloadedCall{
		id:        id,
		quality:   rec.Quality,
		outputDir: rec.OutputDir,
		videoPath: rec.VideoPath,
		bytes:     rec.Bytes,
		entries:   rec.LibraryEntries,
	})
	s.markJobDone(jobID)
	return nil
}

func (s *fakeWorkerStore) FailDownload(ctx context.Context, jobID int64, id int, errMsg string) error {
	if err := s.abandoned(jobID); err != nil {
		return err
	}
	s.markFailed = append(s.markFailed, id)
	s.markJobFailedAs(jobID, errMsg)
	return nil
}

func (s *fakeWorkerStore) SkipDownload(ctx context.Context, jobID int64, id int, reason string) error {
	if err := s.abandoned(jobID); err != nil {
		return err
	}
	s.markSkipped = append(s.markSkipped, id)
	s.markSkippedCtxErr = append(s.markSkippedCtxErr, ctx.Err())
	s.markJobFailedAs(jobID, reason)
	return nil
}

// CancelDownload mirrors the store's guarded cancel: it only transitions a
// running job and is a benign no-op otherwise, so worker cancel-branch tests
// see the same status semantics as production.
func (s *fakeWorkerStore) CancelDownload(ctx context.Context, jobID int64, id int) error {
	if err := s.abandoned(jobID); err != nil {
		return err
	}
	if s.requeued[jobID] {
		return fmt.Errorf("job %d is queued: %w", jobID, database.ErrDownloadCanceled)
	}
	s.markSkipped = append(s.markSkipped, id)
	s.markSkippedCtxErr = append(s.markSkippedCtxErr, ctx.Err())
	s.markJobCanceled = append(s.markJobCanceled, jobID)
	s.markJobCanceledCtxErr = append(s.markJobCanceledCtxErr, ctx.Err())
	j := s.jobs[jobID]
	if j.Status == database.JobRunning {
		j.Status = database.JobCanceled
		s.jobs[jobID] = j
	}
	return nil
}

func (s *fakeWorkerStore) RequeueStaleRunning(ctx context.Context) (int, error) {
	return 0, nil
}

// Planner-side Store methods: unused by the Worker, so any call is a bug.
func (s *fakeWorkerStore) ListFollows(ctx context.Context) ([]database.Follow, error) {
	panic("ListFollows: not expected from Worker")
}
func (s *fakeWorkerStore) UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string, position sql.NullInt64, followID sql.NullInt64) error {
	panic("UpsertLesson: not expected from Worker")
}
func (s *fakeWorkerStore) IsDownloaded(ctx context.Context, id int) (bool, error) {
	panic("IsDownloaded: not expected from Worker")
}
func (s *fakeWorkerStore) ShouldSkipEnqueue(ctx context.Context, id int) (bool, error) {
	panic("ShouldSkipEnqueue: not expected from Worker")
}
func (s *fakeWorkerStore) ActiveJobExists(ctx context.Context, railcontentID int) (bool, error) {
	panic("ActiveJobExists: not expected from Worker")
}
func (s *fakeWorkerStore) EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (int64, bool, error) {
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

// cancelingResolver cancels the provided context (simulating a SIGINT/SIGTERM
// landing during the network resolve) and then returns an error, so a test can
// drive the resolve-failure branch with the outer ctx already dead.
type cancelingResolver struct{ cancel context.CancelFunc }

func (r cancelingResolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	r.cancel()
	return nil, errors.New("resolve failed during shutdown")
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

	// afterWrite, when set, runs on the lesson folder once writeMP4's files are
	// written, so a test can shape the finished download (an unreadable file, a
	// read-only folder) before the worker moves it.
	afterWrite func(dir string)

	// onProgress, when non-empty, is replayed through the opts' OnProgress
	// callback (if the worker set one) before the download resolves, simulating
	// yt-dlp's per-render progress lines.
	onProgress []musora.DownloadProgress

	attempts map[int]int // railcontent id → attempts seen so far
	calls    []musora.DownloadOpts
}

func newFakeDownloader() *fakeDownloader {
	return &fakeDownloader{failsBefore: map[int]int{}, attempts: map[int]int{}}
}

func (d *fakeDownloader) Download(_ context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls = append(d.calls, o)
	d.attempts[l.ID]++
	if o.OnProgress != nil {
		for _, p := range d.onProgress {
			o.OnProgress(p)
		}
	}
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
		// A song (soundslice slug, no HLS) produces TWO bracket-tagged version
		// files plus an nfo and a resources/ PDF — exactly the layout the real
		// DownloadLesson writes for the soundslice path — instead of the single
		// "<base>.mp4" a regular lesson produces.
		if l.SoundsliceSlug() != "" && l.Video.HLSManifestURL == "" {
			for _, tag := range []string{"Original", "Drumless"} {
				p := filepath.Join(dir, fmt.Sprintf("%s [%s].mp4", base, tag))
				if err := os.WriteFile(p, d.writeMP4, 0o644); err != nil {
					return err
				}
			}
			if err := os.WriteFile(filepath.Join(dir, base+".nfo"), []byte("<movie/>"), 0o644); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Join(dir, "resources"), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "resources", "song.pdf"), []byte("pdf-bytes"), 0o644); err != nil {
				return err
			}
		} else {
			if err := os.WriteFile(filepath.Join(dir, base+".mp4"), d.writeMP4, 0o644); err != nil {
				return err
			}
			// DownloadLesson always writes the <movie> nfo last.
			if err := os.WriteFile(filepath.Join(dir, base+".nfo"), []byte("<movie/>"), 0o644); err != nil {
				return err
			}
		}
		if d.afterWrite != nil {
			d.afterWrite(dir)
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

// TestNewWorkerClampsMaxAttempts verifies a non-positive MaxAttempts is clamped
// to 1, so a zero-value Config still makes one real download attempt instead of
// skipping the attempt loop entirely and marking every job failed without trying.
// producedVideo must find a song's bracket-tagged version files when no plain
// "<base>.mp4" exists: it returns the FIRST (sorted) version path and the SUM
// of every "<base>...mp4" file's bytes, so FinishDownload records real metadata
// for a multi-version song download.
func TestProducedVideoSongVersions(t *testing.T) {
	w := &Worker{}
	tmp := t.TempDir()
	base := filepath.Base(tmp) // producedVideo derives base from lessonDir's own name
	// Two version files (no plain <base>.mp4), distinct sizes, plus a sidecar.
	files := map[string][]byte{
		base + " [Original].mp4": []byte("aaaa"),   // 4 bytes
		base + " [Drumless].mp4": []byte("bbbbbb"), // 6 bytes
		base + ".nfo":            []byte("<nfo/>"), // not a video, ignored
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	gotPath, gotBytes := w.producedVideo(tmp)
	wantPath := filepath.Join(tmp, base+" [Drumless].mp4") // sorted-first ('D' < 'O')
	if gotPath != wantPath {
		t.Errorf("videoPath = %q, want %q (first sorted version file)", gotPath, wantPath)
	}
	if gotBytes != 10 {
		t.Errorf("bytes = %d, want 10 (sum of both versions)", gotBytes)
	}
}

// producedVideo still returns the plain "<base>.mp4" for a regular lesson, and
// sums it in too when version files happen to coexist.
func TestProducedVideoRegularLesson(t *testing.T) {
	w := &Worker{}
	tmp := t.TempDir()
	base := filepath.Base(tmp)
	if err := os.WriteFile(filepath.Join(tmp, base+".mp4"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotPath, gotBytes := w.producedVideo(tmp)
	if gotPath != filepath.Join(tmp, base+".mp4") || gotBytes != 5 {
		t.Errorf("producedVideo = %q/%d, want plain mp4 / 5", gotPath, gotBytes)
	}
}

// producedVideo must reject yt-dlp fragment files ("<base> [Tag].fNNN.mp4") and
// unrelated strays ("<base> X.mp4") so neither inflates the recorded byte count
// nor is mistaken for the episode video — only the real "<base> [Tag].mp4" (or
// "<base>.mp4") counts.
func TestProducedVideoRejectsStrayFiles(t *testing.T) {
	w := &Worker{}
	tmp := t.TempDir()
	base := filepath.Base(tmp)
	files := map[string][]byte{
		base + " Extra.mp4":           []byte("stray-not-a-version"), // unrelated stray
		base + " [Original].f137.mp4": []byte("fragment-file-bytes"), // yt-dlp fragment
		base + " [Original].mp4":      []byte("real77"),              // the real version (6 bytes)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gotPath, gotBytes := w.producedVideo(tmp)
	if gotPath != filepath.Join(tmp, base+" [Original].mp4") {
		t.Errorf("videoPath = %q, want the real [Original].mp4 (strays/fragments excluded)", gotPath)
	}
	if gotBytes != 6 {
		t.Errorf("bytes = %d, want 6 (only the real version, not strays/fragments)", gotBytes)
	}
}

// No video of any shape -> "" / 0 (e.g. a video-less song or ResourcesOnly).
func TestProducedVideoNone(t *testing.T) {
	w := &Worker{}
	tmp := t.TempDir()
	base := filepath.Base(tmp)
	if err := os.WriteFile(filepath.Join(tmp, base+".nfo"), []byte("<nfo/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, b := w.producedVideo(tmp); p != "" || b != 0 {
		t.Errorf("producedVideo = %q/%d, want empty/0 when no mp4", p, b)
	}

	// ResourcesOnly short-circuits even if an mp4 somehow exists.
	wRO := &Worker{}
	wRO.Cfg.ResourcesOnly = true
	if err := os.WriteFile(filepath.Join(tmp, base+".mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, b := wRO.producedVideo(tmp); p != "" || b != 0 {
		t.Errorf("producedVideo (ResourcesOnly) = %q/%d, want empty/0", p, b)
	}
}

func TestNewWorkerClampsMaxAttempts(t *testing.T) {
	for _, in := range []int{0, -3} {
		w := NewWorker(nil, nil, nil, Config{MaxAttempts: in}, "", nil)
		if w.Cfg.MaxAttempts != 1 {
			t.Errorf("NewWorker(MaxAttempts=%d) -> Cfg.MaxAttempts=%d, want 1", in, w.Cfg.MaxAttempts)
		}
	}

	// Behavioral: a zero-value MaxAttempts still yields exactly one real attempt
	// that completes the job (not an immediate no-attempt failure).
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()

	w := NewWorker(store, res, dl, Config{MaxAttempts: 0, DownloadsDir: "/dl"}, "perm", nil)
	w.sleep = (&recordingSleeper{}).sleep
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if len(dl.calls) != 1 {
		t.Errorf("download calls = %d, want 1 (clamped to one attempt)", len(dl.calls))
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Errorf("job status = %q, want done", got)
	}
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
		t.Errorf("jobs done = %v, want %v", got, want)
	}
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].id != 100 {
		t.Errorf("lessons downloaded = %+v, want one call for lesson 100", store.markDownloaded)
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
	// AudioLang opt-out passthrough: an unset Cfg.AudioLang (the value config
	// produces for DRUMDROP_AUDIO_LANG=any/all) must reach the downloader as ""
	// so FormatSelector keeps yt-dlp's historical no-preference pick.
	if dl.calls[0].AudioLang != "" {
		t.Errorf("AudioLang = %q, want \"\" (no preference flows through unchanged)", dl.calls[0].AudioLang)
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
		t.Errorf("jobs done = %v, want %v", got, want)
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
		t.Errorf("lessons failed = %v, want %v", got, want)
	}
	if got, want := store.markJobFailed, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("jobs failed = %v, want %v", got, want)
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
		t.Errorf("lessons skipped = %v, want %v", got, want)
	}
	if got, want := store.markJobFailed, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("jobs failed = %v, want %v", got, want)
	}
	if len(dl.calls) != 0 {
		t.Errorf("download calls = %d, want 0 (unresolvable)", len(dl.calls))
	}
	if len(store.markRunning) != 0 || len(sleeper.durations) != 0 {
		t.Errorf("no re-mark/sleep on unresolvable: markRunning=%v sleeps=%v", store.markRunning, sleeper.durations)
	}
	if len(store.markFailed) != 0 {
		t.Errorf("an unresolvable lesson was marked failed, want skipped: %v", store.markFailed)
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
		t.Errorf("lessons failed = %v, want %v", got, want)
	}
	if got, want := store.markDone, []int64{2}; !reflect.DeepEqual(got, want) {
		t.Errorf("jobs done = %v, want %v", got, want)
	}
}

// TestWorkerStopsClaimingWhenPaused proves pause halts the queue mid-cycle: the
// in-flight job finishes but the worker does not claim the next one while
// IsPaused reports true. This is the "pause = stop starting new ones" guarantee
// at the per-job level — the daemon's pause flag alone only gates whole cycles,
// so a drain already in progress would otherwise run the whole queue.
func TestWorkerStopsClaimingWhenPaused(t *testing.T) {
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
	// Become paused once the first download has completed: the loop's pre-claim
	// check then fires before job 2 is claimed.
	w.IsPaused = func() bool { return len(dl.calls) >= 1 }

	processed, err := w.RunOnce(context.Background(), 0)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if processed != 1 {
		t.Errorf("processed = %d, want 1 (paused after the first job)", processed)
	}
	if len(dl.calls) != 1 {
		t.Errorf("download calls = %d, want 1 (no new job claimed while paused)", len(dl.calls))
	}
	if got := store.jobs[2].Status; got != database.JobQueued {
		t.Errorf("job 2 status = %q, want still queued (not claimed while paused)", got)
	}
	if got := store.jobs[3].Status; got != database.JobQueued {
		t.Errorf("job 3 status = %q, want still queued", got)
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
	w.Cfg.AudioLang = "en"     // flows into DownloadOpts

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if len(dl.calls) != 1 {
		t.Fatalf("download calls = %d, want 1", len(dl.calls))
	}
	if dl.calls[0].Quality != "best" || !dl.calls[0].ResourcesOnly || dl.calls[0].AudioLang != "en" {
		t.Errorf("download opts = %+v, want Quality best ResourcesOnly true AudioLang en", dl.calls[0])
	}
	if got := store.markDownloaded[0].quality; got != "best" {
		t.Errorf("recorded quality = %q, want best", got)
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
		t.Errorf("jobs done %v, want none on cancellation", store.markDone)
	}
	if len(store.markFailed) != 0 || len(store.markJobFailed) != 0 {
		t.Errorf("no failed marks expected on cancellation: markFailed=%v markJobFailed=%v", store.markFailed, store.markJobFailed)
	}
}

func TestWorkerFinishDownloadRecordsVideoMetadata(t *testing.T) {
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
		t.Fatalf("FinishDownload calls = %d, want 1", len(store.markDownloaded))
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

func TestWorkerFinishDownloadNoVideoWhenResourcesOnly(t *testing.T) {
	// ResourcesOnly produces no mp4, so FinishDownload must record an empty
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
		t.Fatalf("FinishDownload calls = %d, want 1", len(store.markDownloaded))
	}
	got := store.markDownloaded[0]
	if got.videoPath != "" || got.bytes != 0 {
		t.Errorf("videoPath/bytes = %q/%d, want empty/0 on ResourcesOnly", got.videoPath, got.bytes)
	}
}

// blockingDownloader blocks inside Download until its ctx is cancelled, then
// returns ctx.Err() (context.Canceled). started is closed on the first call so a
// test can wait until the download is genuinely in flight before cancelling.
type blockingDownloader struct {
	started chan struct{}
	calls   int
}

func newBlockingDownloader() *blockingDownloader {
	return &blockingDownloader{started: make(chan struct{})}
}

func (d *blockingDownloader) Download(ctx context.Context, _ *musora.Lesson, _ musora.DownloadOpts) error {
	d.calls++
	if d.calls == 1 {
		close(d.started)
	}
	<-ctx.Done()
	return ctx.Err()
}

// TestWorkerCancelRunningSkipsAndCancelsJob verifies the cancel-error-first
// branch: a running download whose job is cancelled via CancelRunning is recorded
// as a skipped lesson + a canceled job, has its partial files cleaned up, is NOT
// retried, and never falls through to the success branch.
func TestWorkerCancelRunningSkipsAndCancelsJob(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newBlockingDownloader()

	tmp := t.TempDir()
	// Pre-create the lesson dir with a partial file so cleanupPartials has work.
	lessonDirPath := filepath.Join(tmp, "Beginner Course", "01 - Lesson A")
	if err := os.MkdirAll(lessonDirPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	partial := filepath.Join(lessonDirPath, "01 - Lesson A.mp4.part")
	if err := os.WriteFile(partial, []byte("half a file"), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.MaxAttempts = 5 // ensure cancel beats retries
	w.Cfg.DownloadsDir = tmp

	done := make(chan struct{})
	go func() {
		_, _ = w.RunOnce(context.Background(), 0)
		close(done)
	}()

	// Wait until the download is in flight, then cancel the running job.
	<-dl.started
	if !w.CancelRunning(1) {
		t.Fatal("CancelRunning(1) = false, want true (job is running)")
	}
	<-done

	if dl.calls != 1 {
		t.Errorf("download calls = %d, want 1 (no retry after cancel)", dl.calls)
	}
	if got, want := store.markSkipped, []int{100}; !reflect.DeepEqual(got, want) {
		t.Errorf("markSkipped = %v, want %v", got, want)
	}
	if got, want := store.markJobCanceled, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("markJobCanceled = %v, want %v", got, want)
	}
	if got := store.jobs[1].Status; got != database.JobCanceled {
		t.Errorf("job status = %q, want canceled", got)
	}
	// Success/failure branches must NOT have run.
	if len(store.markDownloaded) != 0 || len(store.markDone) != 0 {
		t.Errorf("success branch ran: markDownloaded=%+v markDone=%v", store.markDownloaded, store.markDone)
	}
	if len(store.markFailed) != 0 || len(store.markJobFailed) != 0 {
		t.Errorf("failure branch ran: markFailed=%v markJobFailed=%v", store.markFailed, store.markJobFailed)
	}
	// Partial file removed; the directory itself is left in place.
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Errorf("partial file still present (stat err = %v), want removed", err)
	}
}

// TestWorkerKillByADeleteDiscardsWhatItWrote proves a download killed by a
// delete that removes the lesson's files (its job already removed, with that
// intent) goes as the delete wants: its lesson folder is removed whole, not
// just its partial files, and no cancel is recorded. That holds for a folder
// the download created, and for one the lesson's own row recorded before (the
// delete takes that folder anyway); a folder that held files no row of the
// lesson recorded is kept (TestWorkerKeepsWhatTheLessonFolderHeldBefore).
func TestWorkerKillByADeleteDiscardsWhatItWrote(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(fmt.Sprintf("recorded=%v", recorded), func(t *testing.T) {
			store := newFakeWorkerStore(queuedJob(1, nodeFollow().ID, 100))
			store.follows[nodeFollow().ID] = nodeFollow()
			store.gone = map[int64]bool{}
			dl := newBlockingDownloader()
			tmp := t.TempDir()
			dir := filepath.Join(tmp, "Beginner Course", "01 - Lesson A")
			if recorded {
				store.lessons[100] = database.Lesson{RailcontentID: 100, OutputDir: sql.NullString{String: dir, Valid: true}}
				seedSeason(t, dir, "01 - Lesson A.mp4", "sheet.pdf")
			}
			w := newTestWorker(store, fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}, dl, func(time.Duration) {})
			w.Cfg.DownloadsDir = tmp

			done := make(chan struct{})
			go func() {
				_, _ = w.RunOnce(context.Background(), 0)
				close(done)
			}()
			<-dl.started
			seedSeason(t, dir, "01 - Lesson A.mp4.part", "sheet.pdf") // what the download wrote
			store.gone[1] = true                                      // the delete removed the job, then killed the download
			if !w.CancelRunning(1) {
				t.Fatal("CancelRunning(1) = false, want true (job is running)")
			}
			<-done

			assertExist(t, false, dir)
			if len(store.markJobCanceled) != 0 || len(store.markDownloaded) != 0 {
				t.Errorf("recorded cancel %v, download %+v; want nothing recorded", store.markJobCanceled, store.markDownloaded)
			}
		})
	}
}

// TestWorkerCancelDuringShutdownFinalizesWithLiveCtx simulates SIGINT/SIGTERM
// arriving while a download is in flight: cancelling the OUTER ctx (not just the
// per-job ctx) makes the download return context.Canceled and runs the cancel-
// first branch with the outer ctx already dead. The finalization writes
// (CancelDownload, for the lesson and the job) must NOT ride that cancelled ctx — they use
// context.WithoutCancel so the real store's BeginTx still commits, otherwise the
// job/lesson would be stranded 'running'/'downloading'. The fake records the
// ctx.Err() it saw for each write; both must be nil.
func TestWorkerCancelDuringShutdownFinalizesWithLiveCtx(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newBlockingDownloader()

	ctx, cancel := context.WithCancel(context.Background())
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.MaxAttempts = 5 // ensure the cancel path beats retries
	w.Cfg.DownloadsDir = t.TempDir()

	done := make(chan struct{})
	go func() {
		_, _ = w.RunOnce(ctx, 0)
		close(done)
	}()

	// Once the download is in flight, cancel the OUTER ctx (shutdown). That cancels
	// jobCtx too, so the download returns context.Canceled and the cancel branch
	// runs while ctx itself is dead.
	<-dl.started
	cancel()
	<-done

	// The cancel branch ran: lesson skipped + job canceled.
	if got, want := store.markSkipped, []int{100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("markSkipped = %v, want %v", got, want)
	}
	if got, want := store.markJobCanceled, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("markJobCanceled = %v, want %v", got, want)
	}
	// Crucially, neither finalization write saw a cancelled context.
	for i, e := range store.markSkippedCtxErr {
		if e != nil {
			t.Errorf("CancelDownload (lesson) call %d saw ctx.Err()=%v, want nil (must use WithoutCancel)", i, e)
		}
	}
	for i, e := range store.markJobCanceledCtxErr {
		if e != nil {
			t.Errorf("CancelDownload (job) call %d saw ctx.Err()=%v, want nil (must use WithoutCancel)", i, e)
		}
	}
}

// TestWorkerResolveFailureDuringShutdownFinalizesWithLiveCtx is the resolve-branch
// twin of the cancel-branch shutdown test: Resolve runs before the loop's
// ctx.Err() guard, so a shutdown landing mid-resolve takes the resolve-failure
// branch with the outer ctx already cancelled. SkipDownload must
// still commit (WithoutCancel), else the job is stranded 'running' until the next
// startup requeue.
func TestWorkerResolveFailureDuringShutdownFinalizesWithLiveCtx(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	ctx, cancel := context.WithCancel(context.Background())
	res := cancelingResolver{cancel: cancel}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// The lesson was skipped (resolve failed) and the download never ran.
	if got, want := store.markSkipped, []int{100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("markSkipped = %v, want %v", got, want)
	}
	if len(dl.calls) != 0 {
		t.Errorf("download calls = %d, want 0 (resolve failed before any download)", len(dl.calls))
	}
	// The finalization write did NOT ride the cancelled ctx.
	for i, e := range store.markSkippedCtxErr {
		if e != nil {
			t.Errorf("CancelDownload (lesson) call %d saw ctx.Err()=%v, want nil (must use WithoutCancel)", i, e)
		}
	}
}

// TestWorkerCancelRunningUnknownJob verifies CancelRunning returns false when no
// job by that id is currently registered as running.
func TestWorkerCancelRunningUnknownJob(t *testing.T) {
	w := newTestWorker(newFakeWorkerStore(), fakeResolver{}, newFakeDownloader(), func(time.Duration) {})
	if w.CancelRunning(999) {
		t.Error("CancelRunning(999) = true, want false (no such running job)")
	}
}

// TestWorkerUsesLessonPositionForNumbering proves the worker folders a node
// follow's lesson under its REAL stored position (not the old hardcoded 1): a
// lesson at position 2 lands in "<course>/02 - <title>", and both the
// DownloadOpts.Index and the recorded output dir share that value.
func TestWorkerUsesLessonPositionForNumbering(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100, Position: sql.NullInt64{Int64: 2, Valid: true}}

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	wantDir := "/dl/Beginner Course/02 - Lesson A"
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != wantDir {
		t.Errorf("lessonDir = %+v, want %q", store.markDownloaded, wantDir)
	}
	if len(dl.calls) != 1 {
		t.Fatalf("download calls = %d, want 1", len(dl.calls))
	}
	if dl.calls[0].Index != 2 {
		t.Errorf("DownloadOpts.Index = %d, want 2 (lesson's real position)", dl.calls[0].Index)
	}
}

// TestWorkerNullPositionFallsBackToOne proves a lesson with no recorded position
// still numbers as "01 - <title>" (the historical default), so a position-less
// lesson keeps working.
func TestWorkerNullPositionFallsBackToOne(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100} // Position invalid

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	wantDir := "/dl/Beginner Course/01 - Lesson A"
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != wantDir {
		t.Errorf("lessonDir = %+v, want %q", store.markDownloaded, wantDir)
	}
	if len(dl.calls) != 1 || dl.calls[0].Index != 1 {
		t.Errorf("DownloadOpts.Index = %+v, want 1 (NULL position fallback)", dl.calls)
	}
}

// TestWorkerInstructorFollowGroupsByParentCourse proves an instructor follow
// groups each lesson under "<instructor>/<parent course>/NN - <title>" instead
// of the old flat outDirFor dir. producedVideo must find the written mp4 at that
// exact path (real position 3 here).
func TestWorkerInstructorFollowGroupsByParentCourse(t *testing.T) {
	job := queuedJob(1, instructorFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[instructorFollow().ID] = instructorFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100, Position: sql.NullInt64{Int64: 3, Valid: true}}

	les := lesson(100, "Paradiddle Power")
	les.ParentContentData = []struct {
		Title string `json:"title"`
	}{{Title: "Hand Technique"}}
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: les}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("video-bytes")

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	tmp := t.TempDir()
	w.Cfg.DownloadsDir = tmp
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	wantDir := filepath.Join(tmp, "Mike Johnston", "Hand Technique", "03 - Paradiddle Power")
	if len(store.markDownloaded) != 1 {
		t.Fatalf("FinishDownload calls = %+v, want 1", store.markDownloaded)
	}
	if got := store.markDownloaded[0].outputDir; got != wantDir {
		t.Errorf("lessonDir = %q, want %q", got, wantDir)
	}
	// producedVideo found the written mp4 at the grouped path.
	wantVideo := filepath.Join(wantDir, "03 - Paradiddle Power.mp4")
	if got := store.markDownloaded[0].videoPath; got != wantVideo {
		t.Errorf("videoPath = %q, want %q", got, wantVideo)
	}
	if got := store.markDownloaded[0].bytes; got != int64(len("video-bytes")) {
		t.Errorf("bytes = %d, want %d", got, len("video-bytes"))
	}
}

// TestWorkerMovesToLibraryOnSuccess proves that with Cfg.LibraryDir set, a
// successful download is MOVED into the library at the lesson's path relative to
// DownloadsDir: the finished .mp4 lives under <library>/<...>/NN - title, the
// scratch downloads lesson dir is gone, and output_dir/video_path record the
// library location.
func TestWorkerMovesToLibraryOnSuccess(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes")

	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "dl")
	library := filepath.Join(tmp, "lib")
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = downloads
	w.Cfg.LibraryDir = library

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Fatalf("job status = %q, want done", got)
	}

	// The lesson folder is in the library at the same relative path, with its .mp4.
	libDir := filepath.Join(library, "Beginner Course", "01 - Lesson A")
	wantVideo := filepath.Join(libDir, "01 - Lesson A.mp4")
	got, err := os.ReadFile(wantVideo)
	if err != nil {
		t.Fatalf("moved video missing: %v", err)
	}
	if string(got) != string(dl.writeMP4) {
		t.Errorf("moved video content = %q, want %q", got, dl.writeMP4)
	}
	// The scratch downloads lesson dir is gone (moved, not copied).
	srcDir := filepath.Join(downloads, "Beginner Course", "01 - Lesson A")
	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Errorf("downloads scratch lesson dir still present (stat err = %v), want moved away", err)
	}
	// output_dir / video_path record the LIBRARY location.
	if len(store.markDownloaded) != 1 {
		t.Fatalf("markDownloaded calls = %d, want 1", len(store.markDownloaded))
	}
	if got := store.markDownloaded[0].outputDir; got != libDir {
		t.Errorf("outputDir = %q, want library path %q", got, libDir)
	}
	if got := store.markDownloaded[0].videoPath; got != wantVideo {
		t.Errorf("videoPath = %q, want library path %q", got, wantVideo)
	}
}

// TestWorkerNoLibraryDirKeepsInDownloads proves that with an empty Cfg.LibraryDir
// the worker writes nothing to any library and behaves exactly as before: job
// done, the file stays in downloads, and output_dir is the downloads path.
func TestWorkerNoLibraryDirKeepsInDownloads(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes")

	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "dl")
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = downloads
	// Cfg.LibraryDir stays empty: feature off.

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// Behavior unchanged: job done, the download landed under the downloads dir.
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Errorf("job status = %q, want done", got)
	}
	srcDir := filepath.Join(downloads, "Beginner Course", "01 - Lesson A")
	if _, err := os.Stat(filepath.Join(srcDir, "01 - Lesson A.mp4")); err != nil {
		t.Errorf("download video missing: %v", err)
	}
	// output_dir is the downloads path (no move happened).
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != srcDir {
		t.Errorf("outputDir = %+v, want downloads path %q", store.markDownloaded, srcDir)
	}
	// No sibling "lib" tree was created — only the downloads dir exists under tmp.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read tmp: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "dl" {
			t.Errorf("unexpected dir %q under tmp, want only the downloads dir (no library written)", e.Name())
		}
	}
}

// TestWorkerMoveToLibraryFailureNonFatal proves a move failure never fails the
// job: when both the rename and the copy-tree fallback fail (the library parent
// is occupied by a regular file, so MkdirAll of the parent fails), the job is
// still Done and output_dir stays the downloads path (the file is still there).
func TestWorkerMoveToLibraryFailureNonFatal(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes")

	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "dl")
	// Make the LibraryDir itself a regular file so MkdirAll of the destination
	// parent under it always fails -> moveToLibrary errors (non-fatal path).
	library := filepath.Join(tmp, "lib")
	if err := os.WriteFile(library, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write library-as-file: %v", err)
	}

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = downloads
	w.Cfg.LibraryDir = library

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}

	// Job still done despite the move failure (non-fatal).
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Errorf("job status = %q, want done (move failure must not fail the job)", got)
	}
	// The file stays in downloads, and output_dir records the downloads path.
	srcDir := filepath.Join(downloads, "Beginner Course", "01 - Lesson A")
	if _, err := os.Stat(filepath.Join(srcDir, "01 - Lesson A.mp4")); err != nil {
		t.Errorf("download video missing after failed move: %v", err)
	}
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != srcDir {
		t.Errorf("outputDir = %+v, want downloads path %q (kept on move failure)", store.markDownloaded, srcDir)
	}
}

// TestWorkerInstructorFollowNoParentCourse proves a lesson under an instructor
// follow with no parent course falls back to just "<instructor>/NN - <title>".
func TestWorkerInstructorFollowNoParentCourse(t *testing.T) {
	job := queuedJob(1, instructorFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[instructorFollow().ID] = instructorFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100, Position: sql.NullInt64{Int64: 4, Valid: true}}

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Solo Lesson")}}
	dl := newFakeDownloader()

	w := newTestWorker(store, res, dl, func(time.Duration) {})
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	wantDir := "/dl/Mike Johnston/04 - Solo Lesson"
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != wantDir {
		t.Errorf("lessonDir = %+v, want %q", store.markDownloaded, wantDir)
	}
}

// TestWorkerPlexTvLayoutOnSuccess proves that with Cfg.Layout="plex-tv" and a
// LibraryDir set, a successful download is moved into the Plex TV layout:
// <library>/<Show>/Season 01/<Show> - s01eNN - Title.mp4, with output_dir = the
// Season folder and video_path = the episode mp4. The show is the node follow's
// course title (flat, no NN-subfolder).
func TestWorkerPlexTvLayoutOnSuccess(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100, Position: sql.NullInt64{Int64: 5, Valid: true}}

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes")

	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "dl")
	library := filepath.Join(tmp, "lib")
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = downloads
	w.Cfg.LibraryDir = library
	w.Cfg.Layout = "plex-tv"

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Fatalf("job status = %q, want done", got)
	}

	seasonDir := filepath.Join(library, "Beginner Course", "Season 01")
	wantVideo := filepath.Join(seasonDir, "Beginner Course - s01e05 - Lesson A.mp4")
	got, err := os.ReadFile(wantVideo)
	if err != nil {
		t.Fatalf("plex-tv video missing: %v", err)
	}
	if string(got) != string(dl.writeMP4) {
		t.Errorf("moved video content = %q, want %q", got, dl.writeMP4)
	}
	// The scratch downloads lesson dir is gone.
	srcDir := filepath.Join(downloads, "Beginner Course", "05 - Lesson A")
	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Errorf("downloads scratch lesson dir still present (stat err = %v), want moved away", err)
	}
	// output_dir is the SEASON folder; video_path is the episode mp4.
	if len(store.markDownloaded) != 1 {
		t.Fatalf("markDownloaded calls = %d, want 1", len(store.markDownloaded))
	}
	if got := store.markDownloaded[0].outputDir; got != seasonDir {
		t.Errorf("outputDir = %q, want season dir %q", got, seasonDir)
	}
	if got := store.markDownloaded[0].videoPath; got != wantVideo {
		t.Errorf("videoPath = %q, want %q", got, wantVideo)
	}
	if got := store.markDownloaded[0].bytes; got != int64(len(dl.writeMP4)) {
		t.Errorf("bytes = %d, want %d", got, len(dl.writeMP4))
	}
}

// TestWorkerPlexTvLayoutOnSuccessSong proves the end-to-end plex-tv flow for a
// SONG: both bracket-tagged version files land flat in the season dir sharing
// the episode base (so Plex merges them as one episode with two versions), the
// song's resources/ PDF survives into the library renamed with the episode-base
// prefix, and FinishDownload records a videoPath that exists on disk.
func TestWorkerPlexTvLayoutOnSuccessSong(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100, Position: sql.NullInt64{Int64: 5, Valid: true}}

	// A song: soundslice slug, no HLS -> the fake writes the two version files.
	song := &musora.Lesson{ID: 100, Title: "Even Flow", Soundslice: []musora.SoundsliceRef{{Slug: "169230"}}}
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: song}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes")

	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "dl")
	library := filepath.Join(tmp, "lib")
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = downloads
	w.Cfg.LibraryDir = library
	w.Cfg.Layout = "plex-tv"

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Fatalf("job status = %q, want done", got)
	}

	seasonDir := filepath.Join(library, "Beginner Course", "Season 01")
	episodeBase := "Beginner Course - s01e05 - Even Flow"
	// Both version files landed flat in the season dir under the shared base.
	for _, tag := range []string{" [Original].mp4", " [Drumless].mp4"} {
		p := filepath.Join(seasonDir, episodeBase+tag)
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("missing song version file %s: %v", episodeBase+tag, err)
			continue
		}
		if string(got) != string(dl.writeMP4) {
			t.Errorf("%s content = %q, want %q", episodeBase+tag, got, dl.writeMP4)
		}
	}
	// The song's resources/ PDF survived into the library, renamed with prefix.
	pdf := filepath.Join(seasonDir, episodeBase+" resources", "song.pdf")
	if _, err := os.Stat(pdf); err != nil {
		t.Errorf("song PDF lost in plex-tv move: %v", err)
	}
	// The scratch downloads lesson dir is gone.
	srcDir := filepath.Join(downloads, "Beginner Course", "05 - Even Flow")
	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Errorf("downloads scratch lesson dir still present (stat err = %v), want moved away", err)
	}
	// FinishDownload recorded a videoPath that exists on disk (one of the versions).
	if len(store.markDownloaded) != 1 {
		t.Fatalf("markDownloaded calls = %d, want 1", len(store.markDownloaded))
	}
	vp := store.markDownloaded[0].videoPath
	if vp == "" {
		t.Fatal("videoPath is empty, want one of the moved version files")
	}
	if _, err := os.Stat(vp); err != nil {
		t.Errorf("recorded videoPath does not exist on disk: %q (%v)", vp, err)
	}
}

// TestWorkerPlexTvWritesEpisodeNFO proves that in plex-tv layout, after the move,
// the worker writes (overwrites) the episode nfo at the moved <episodeBase>.nfo
// path: a Kodi/Plex <episodedetails> doc with the episode title, season, episode,
// aired date, and instructor actor. The fakeDownloader writes only the .mp4 (no
// pre-existing nfo), so the worker's write creates it fresh — which is the same
// path the move would have renamed a download-time <movie> nfo to.
func TestWorkerPlexTvWritesEpisodeNFO(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100, Position: sql.NullInt64{Int64: 5, Valid: true}}

	// Enrich the resolver lesson so the nfo carries an aired date + actor.
	rich := lesson(100, "Lesson A")
	rich.PublishedOn = "2024-06-11T15:00:00.000000Z"
	rich.Instructors = []musora.Instructor{{Name: "El Estepario Siberiano"}}
	res := fakeResolver{lessons: map[int]*musora.Lesson{100: rich}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes")

	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "dl")
	library := filepath.Join(tmp, "lib")
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = downloads
	w.Cfg.LibraryDir = library
	w.Cfg.Layout = "plex-tv"

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Fatalf("job status = %q, want done", got)
	}

	seasonDir := filepath.Join(library, "Beginner Course", "Season 01")
	nfoPath := filepath.Join(seasonDir, "Beginner Course - s01e05 - Lesson A.nfo")
	got, err := os.ReadFile(nfoPath)
	if err != nil {
		t.Fatalf("episode nfo missing: %v", err)
	}
	xml := string(got)
	for _, want := range []string{
		"<episodedetails>",
		"<title>Lesson A</title>",
		"<showtitle>Beginner Course</showtitle>",
		"<season>1</season>",
		"<episode>5</episode>",
		"<aired>2024-06-11</aired>",
		`<actor><name>El Estepario Siberiano</name><role>Instructor</role></actor>`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("episode nfo missing %q\n%s", want, xml)
		}
	}
	if strings.Contains(xml, "<movie>") {
		t.Errorf("episode nfo unexpectedly a <movie>\n%s", xml)
	}
}

// TestWorkerPlexTvLayoutNoLibraryKeepsInDownloads proves plex-tv has no effect
// without a LibraryDir: the file stays in the default downloads layout.
func TestWorkerPlexTvLayoutNoLibraryKeepsInDownloads(t *testing.T) {
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()

	res := fakeResolver{lessons: map[int]*musora.Lesson{100: lesson(100, "Lesson A")}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes")

	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "dl")
	w := newTestWorker(store, res, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = downloads
	w.Cfg.Layout = "plex-tv" // but no LibraryDir

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	srcDir := filepath.Join(downloads, "Beginner Course", "01 - Lesson A")
	if _, err := os.Stat(filepath.Join(srcDir, "01 - Lesson A.mp4")); err != nil {
		t.Errorf("download video missing: %v", err)
	}
	if len(store.markDownloaded) != 1 || store.markDownloaded[0].outputDir != srcDir {
		t.Errorf("outputDir = %+v, want downloads path %q (plex-tv no-op without library)", store.markDownloaded, srcDir)
	}
}

// TestPlexShow exercises the flattened show resolution for each follow shape:
// node -> course title; instructor with a parent course -> the parent course
// (flat, NOT <instructor>/<course>); course-less instructor -> instructor name;
// no follow -> parent title else content-<id>.
func TestPlexShow(t *testing.T) {
	withParent := func(l *musora.Lesson, title string) *musora.Lesson {
		l.ParentContentData = []struct {
			Title string `json:"title"`
		}{{Title: title}}
		return l
	}

	tests := []struct {
		name   string
		follow database.Follow
		job    database.Job
		lesson *musora.Lesson
		want   string
	}{
		{
			name:   "node follow uses course title",
			follow: nodeFollow(),
			job:    queuedJob(1, nodeFollow().ID, 100),
			lesson: lesson(100, "L"),
			want:   "Beginner Course",
		},
		{
			name:   "instructor with parent course flattens to the course",
			follow: instructorFollow(),
			job:    queuedJob(1, instructorFollow().ID, 100),
			lesson: withParent(lesson(100, "L"), "Hand Technique"),
			want:   "Hand Technique",
		},
		{
			name:   "course-less instructor falls back to instructor name",
			follow: instructorFollow(),
			job:    queuedJob(1, instructorFollow().ID, 100),
			lesson: lesson(100, "L"),
			want:   "Mike Johnston",
		},
		{
			name:   "no follow uses parent title",
			follow: database.Follow{},
			job:    database.Job{ID: 1, RailcontentID: 100},
			lesson: withParent(lesson(100, "L"), "Some Course"),
			want:   "Some Course",
		},
		{
			name:   "no follow no parent uses content id",
			follow: database.Follow{},
			job:    database.Job{ID: 1, RailcontentID: 777},
			lesson: lesson(777, "L"),
			want:   "content-777",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := plexShow(tc.follow, tc.job, tc.lesson); got != tc.want {
				t.Errorf("plexShow = %q, want %q", got, tc.want)
			}
		})
	}
}

// songJobWorker sets up a worker for one song (lesson 100, "Even Flow", episode
// 5 of the node follow) downloading into tmp/dl with a library at tmp/lib in the
// given layout, and returns it with its store, the downloader and a log buffer.
func songJobWorker(t *testing.T, layout string) (*Worker, *fakeWorkerStore, *fakeDownloader, *bytes.Buffer, string) {
	t.Helper()
	job := queuedJob(1, nodeFollow().ID, 100)
	store := newFakeWorkerStore(job)
	store.follows[nodeFollow().ID] = nodeFollow()
	store.lessons[100] = database.Lesson{RailcontentID: 100, Position: sql.NullInt64{Int64: 5, Valid: true}}
	song := &musora.Lesson{ID: 100, Title: "Even Flow", Soundslice: []musora.SoundsliceRef{{Slug: "169230"}}}
	dl := newFakeDownloader()
	dl.writeMP4 = []byte("fake mp4 bytes")

	tmp := t.TempDir()
	w := newTestWorker(store, fakeResolver{lessons: map[int]*musora.Lesson{100: song}}, dl, func(time.Duration) {})
	w.Cfg.DownloadsDir = filepath.Join(tmp, "dl")
	w.Cfg.LibraryDir = filepath.Join(tmp, "lib")
	w.Cfg.Layout = layout
	var logBuf bytes.Buffer
	w.Log = &logBuf
	return w, store, dl, &logBuf, tmp
}

// runSongJob runs the worker once and checks the job still succeeded (the move
// is non-fatal) and recorded exactly one download, which it returns.
func runSongJob(t *testing.T, w *Worker, store *fakeWorkerStore) markDownloadedCall {
	t.Helper()
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if got := store.jobs[1].Status; got != database.JobDone {
		t.Fatalf("job status = %q, want done (a move failure must not fail the job)", got)
	}
	if len(store.markDownloaded) != 1 {
		t.Fatalf("markDownloaded calls = %d, want 1", len(store.markDownloaded))
	}
	return store.markDownloaded[0]
}

// assertRecordedWhole checks the recorded folder holds both versions and the
// resources PDF under the given names, and that the recorded video exists.
func assertRecordedWhole(t *testing.T, rec markDownloadedCall, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(rec.outputDir, name)); err != nil {
			t.Errorf("recorded folder %s is missing %s: %v", rec.outputDir, name, err)
		}
	}
	if _, err := os.Stat(rec.videoPath); err != nil {
		t.Errorf("recorded video %q does not exist: %v", rec.videoPath, err)
	}
}

// TestWorkerDefaultMoveCopyFailsRecordsDownloads covers D52's first case at the
// worker: the default-layout copy fails part-way. The lesson is recorded in
// downloads, where it is whole, and the library holds no copy of it.
func TestWorkerDefaultMoveCopyFailsRecordsDownloads(t *testing.T) {
	skipWithoutPermissionChecks(t)
	w, store, dl, _, tmp := songJobWorker(t, "")
	forceCopyFallback(t)
	dl.afterWrite = func(dir string) { makeUnreadable(t, filepath.Join(dir, "05 - Even Flow [Original].mp4")) }

	rec := runSongJob(t, w, store)
	srcDir := filepath.Join(tmp, "dl", "Beginner Course", "05 - Even Flow")
	if rec.outputDir != srcDir {
		t.Errorf("outputDir = %q, want the downloads folder %q", rec.outputDir, srcDir)
	}
	assertRecordedWhole(t, rec, "05 - Even Flow [Drumless].mp4", "05 - Even Flow [Original].mp4", "resources/song.pdf")
	if names := readDirNames(t, filepath.Join(tmp, "lib", "Beginner Course")); len(names) != 0 {
		t.Errorf("library holds %v, want no copy of a lesson recorded in downloads", names)
	}
}

// TestWorkerDefaultMoveSourceNotRemovableRecordsLibrary covers D52's third case
// at the worker (default layout): the copy is complete but downloads cannot be
// cleared. The worker records the library folder (it used to drop it on any
// error), and the log names the downloads leftover.
func TestWorkerDefaultMoveSourceNotRemovableRecordsLibrary(t *testing.T) {
	skipWithoutPermissionChecks(t)
	w, store, dl, logBuf, tmp := songJobWorker(t, "")
	forceCopyFallback(t)
	dl.afterWrite = func(dir string) { makeUndeletable(t, filepath.Join(dir, "resources")) }

	rec := runSongJob(t, w, store)
	libDir := filepath.Join(tmp, "lib", "Beginner Course", "05 - Even Flow")
	if rec.outputDir != libDir {
		t.Errorf("outputDir = %q, want the complete library copy %q", rec.outputDir, libDir)
	}
	assertRecordedWhole(t, rec, "05 - Even Flow [Drumless].mp4", "05 - Even Flow [Original].mp4", "resources/song.pdf")
	srcDir := filepath.Join(tmp, "dl", "Beginner Course", "05 - Even Flow")
	if !strings.Contains(logBuf.String(), fmt.Sprintf("leftover at %q", srcDir)) {
		t.Errorf("log %q does not name the downloads leftover %q", logBuf.String(), srcDir)
	}
}

// TestWorkerPlexTvMoveCopyFailsRecordsScratch covers D52's second case at the
// worker: the plex-tv copy fails part-way. The move is undone, the lesson is
// recorded in scratch WITH its video (it used to lose it), and the season folder
// holds nothing of the episode.
func TestWorkerPlexTvMoveCopyFailsRecordsScratch(t *testing.T) {
	skipWithoutPermissionChecks(t)
	w, store, dl, _, tmp := songJobWorker(t, LayoutPlexTV)
	forceCopyFallback(t)
	dl.afterWrite = func(dir string) { makeUnreadable(t, filepath.Join(dir, "05 - Even Flow [Original].mp4")) }

	rec := runSongJob(t, w, store)
	srcDir := filepath.Join(tmp, "dl", "Beginner Course", "05 - Even Flow")
	if rec.outputDir != srcDir {
		t.Errorf("outputDir = %q, want the scratch folder %q", rec.outputDir, srcDir)
	}
	if want := filepath.Join(srcDir, "05 - Even Flow [Drumless].mp4"); rec.videoPath != want {
		t.Errorf("videoPath = %q, want %q", rec.videoPath, want)
	}
	assertRecordedWhole(t, rec, "05 - Even Flow [Drumless].mp4", "05 - Even Flow [Original].mp4", "resources/song.pdf")
	if names := readDirNames(t, filepath.Join(tmp, "lib", "Beginner Course", "Season 01")); len(names) != 0 {
		t.Errorf("season folder holds %v, want nothing of an undone move", names)
	}
}

// TestWorkerPlexTvMoveSourceNotRemovableRecordsLibrary covers D52's third case
// in plex-tv at the worker: every entry is in the season folder, which is what
// gets recorded, and the log names the downloads leftover.
func TestWorkerPlexTvMoveSourceNotRemovableRecordsLibrary(t *testing.T) {
	skipWithoutPermissionChecks(t)
	w, store, dl, logBuf, tmp := songJobWorker(t, LayoutPlexTV)
	forceCopyFallback(t)
	dl.afterWrite = func(dir string) { makeUndeletable(t, filepath.Join(dir, "resources")) }

	rec := runSongJob(t, w, store)
	seasonDir := filepath.Join(tmp, "lib", "Beginner Course", "Season 01")
	if rec.outputDir != seasonDir {
		t.Errorf("outputDir = %q, want the season folder %q", rec.outputDir, seasonDir)
	}
	base := "Beginner Course - s01e05 - Even Flow"
	assertRecordedWhole(t, rec, base+" [Drumless].mp4", base+" [Original].mp4", base+" resources/song.pdf")
	srcDir := filepath.Join(tmp, "dl", "Beginner Course", "05 - Even Flow")
	if !strings.Contains(logBuf.String(), fmt.Sprintf("leftover at %q", srcDir)) {
		t.Errorf("log %q does not name the downloads leftover %q", logBuf.String(), srcDir)
	}
}
