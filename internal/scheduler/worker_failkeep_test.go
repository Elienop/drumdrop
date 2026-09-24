package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// The tests below pin the owner's ruling 2026-09-24 (h) (round-5b code L2,
// security I2), on a real store and a real planner: a download that fails
// for a lesson that still records files from an earlier download leaves the
// lesson 'downloaded' with a note, and only its job fails. So no sync
// enqueues it again (the planner re-enqueues a 'failed' lesson every cycle,
// and each attempt re-downloads the whole video); the owner's Download
// retries it. A lesson with no files is still failed, and still retried.

// downloadedInLibrary is realWorker in the default layout with lesson 100
// downloaded once into the library: it returns the worker, the store, the
// downloader, the follow id and the lesson's library folder.
func downloadedInLibrary(t *testing.T) (*Worker, *database.Store, *fakeDownloader, int64, string) {
	t.Helper()
	ctx := context.Background()
	w, s, dl, f, _ := realWorker(t, "")
	w.Cfg.MaxAttempts = 2
	enqueue(t, s, f)
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	dir := filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Lesson A")
	if l := getLesson(t, s); l.Status != database.StatusDownloaded || l.OutputDir.String != dir {
		t.Fatalf("first download = %s in %q, want downloaded in %q", l.Status, l.OutputDir.String, dir)
	}
	return w, s, dl, f, dir
}

// enqueue queues a download of lesson 100, as the owner's Download does.
func enqueue(t *testing.T, s *database.Store, follow int64) int64 {
	t.Helper()
	id, created, err := s.EnqueueJob(context.Background(), sql.NullInt64{Int64: follow, Valid: true}, 100)
	if err != nil || !created {
		t.Fatalf("EnqueueJob = %d, %v, %v; want a new job", id, created, err)
	}
	return id
}

// getLesson reads lesson 100.
func getLesson(t *testing.T, s *database.Store) database.Lesson {
	t.Helper()
	l, err := s.GetLesson(context.Background(), 100)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	return l
}

// planCount runs one sync's planning over the follow, which lists lesson 100,
// and returns how many downloads it queued.
func planCount(t *testing.T, s *database.Store, follow int64) int {
	t.Helper()
	p := &Planner{Store: s, Expander: fakeExpander{ids: map[int64][]int{follow: {100}}}}
	n, err := p.Plan(context.Background(), 0)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return n
}

// assertEnded fails unless lesson 100 is status with note, still records
// outputDir ("" for none), and job ended failed with jobMsg.
func assertEnded(t *testing.T, s *database.Store, job int64, status, note, outputDir, jobMsg string) {
	t.Helper()
	l := getLesson(t, s)
	if l.Status != status || l.Error.String != note || l.OutputDir.String != outputDir {
		t.Errorf("lesson = %s in %q, note %q; want %s in %q, note %q", l.Status, l.OutputDir.String, l.Error.String, status, outputDir, note)
	}
	j, err := s.GetJob(context.Background(), job)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if j.Status != database.JobFailed || j.Error.String != jobMsg {
		t.Errorf("job = %s %q, want failed %q", j.Status, j.Error.String, jobMsg)
	}
}

// TestWorkerRefusedLibraryPlacementLeavesTheLessonDownloaded is the code
// seat's L2 case: the library placement of a lesson already in the library
// is refused every attempt (ruling (f)). The lesson stays downloaded, its
// library copy recorded and whole, with failKeptInLibrary's sentence as its
// note; the job fails with its own sentence; and the next sync queues
// nothing.
func TestWorkerRefusedLibraryPlacementLeavesTheLessonDownloaded(t *testing.T) {
	ctx := context.Background()
	w, s, dl, f, dir := downloadedInLibrary(t)
	files := map[string]string{"05 - Lesson A.mp4": "new mp4", "05 - Lesson A.nfo": "<movie/>"}
	assertTree(t, dir, files)
	job := enqueue(t, s, f)
	refuseIntoLessonFolder(t, w.Cfg.LibraryDir, dir)
	calls := len(dl.calls)

	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := len(dl.calls) - calls; got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
	assertEnded(t, s, job, database.StatusDownloaded, failKeptInLibrary.lesson, dir, failKeptInLibrary.job)
	assertTree(t, dir, files)
	if n := planCount(t, s, f); n != 0 {
		t.Errorf("the next sync queued %d downloads, want none", n)
	}
}

// TestWorkerFailedReDownloadLeavesTheLessonDownloaded proves a plain failure
// (yt-dlp failing every attempt) of a lesson that was downloaded before
// leaves it downloaded with msgEarlierKept, no sync queues it, and the next
// successful download (the owner's Download) clears the note.
func TestWorkerFailedReDownloadLeavesTheLessonDownloaded(t *testing.T) {
	ctx := context.Background()
	w, s, dl, f, dir := downloadedInLibrary(t)
	job := enqueue(t, s, f)
	dl.alwaysFail = true

	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertEnded(t, s, job, database.StatusDownloaded, msgEarlierKept, dir, failDownload.job)
	if n := planCount(t, s, f); n != 0 {
		t.Errorf("the next sync queued %d downloads, want none", n)
	}

	dl.alwaysFail = false
	enqueue(t, s, f)
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if l := getLesson(t, s); l.Status != database.StatusDownloaded || l.Error.Valid {
		t.Errorf("after a successful download: %s, note %v; want downloaded with no note", l.Status, l.Error)
	}
}

// TestWorkerFailedFirstDownloadFailsTheLesson proves a lesson never
// downloaded is still failed with its sentence when every attempt fails, and
// that the next sync queues it again.
func TestWorkerFailedFirstDownloadFailsTheLesson(t *testing.T) {
	ctx := context.Background()
	w, s, dl, f, _ := realWorker(t, "")
	w.Cfg.MaxAttempts = 2
	dl.alwaysFail = true
	job := enqueue(t, s, f)

	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertEnded(t, s, job, database.StatusFailed, failDownload.lesson, "", failDownload.job)
	if n := planCount(t, s, f); n != 1 {
		t.Errorf("the next sync queued %d downloads, want 1 (a failed lesson is retried)", n)
	}
}
