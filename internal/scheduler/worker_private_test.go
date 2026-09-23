package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// The tests below pin D66: a download writes only into a folder private to its
// job, and is placed where the lesson lives only once it succeeded. A kept
// earlier download survives every way a re-download can end without being
// recorded, in every setup (no library, the library the downloads folder, a
// separate library) and both layouts.

// forceOverwriter is a downloader that behaves as yt-dlp does under
// --force-overwrites (hard rule 8): before it downloads, it deletes an
// existing "<base>.mp4" at its output path (yt-dlp's existing_file, which
// removes the final file when overwriting), then writes a partial file. during,
// when set, runs next (a stopper landing mid-download). It then fails as yt-dlp
// does on a 403 when fail is set, returns the context's error once the job was
// killed, or finishes: the video, the nfo and a resources/ PDF, all "new".
type forceOverwriter struct {
	fail   bool
	during func()
	calls  int
}

func (d *forceOverwriter) Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls++
	base := fmt.Sprintf("%02d - %s", o.Index, musora.Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, base+".mp4")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	part := filepath.Join(dir, base+".mp4.part")
	if err := os.WriteFile(part, []byte("new partial"), 0o644); err != nil {
		return err
	}
	if d.during != nil {
		d.during()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.fail {
		return errors.New("yt-dlp: HTTP Error 403: Forbidden")
	}
	if err := os.Remove(part); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, base+".mp4"), []byte("new mp4"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, base+".nfo"), []byte("new nfo"), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "resources", "new.pdf"), []byte("new pdf"), 0o644)
}

// setups are the three places a lesson can live, by the library setting.
var setups = []string{"none", "downloads", "separate"}

// setupWorker is plexWorker's worker in the given setup and layout, over a
// forceOverwriter.
func setupWorker(t *testing.T, setup, layout string) (*Worker, *fakeWorkerStore, *forceOverwriter) {
	t.Helper()
	w, store, _, _, _ := plexWorker(t)
	w.Cfg.Layout = layout
	switch setup {
	case "none":
		w.Cfg.LibraryDir = ""
	case "downloads":
		w.Cfg.LibraryDir = w.Cfg.DownloadsDir
	}
	dl := &forceOverwriter{}
	w.Downloader = dl
	return w, store, dl
}

// earlier is a lesson's earlier download, kept where it lives.
type earlier struct {
	dir   string   // the folder it is in
	names []string // its entries, seedSeason names
}

// seedEarlier writes lesson 100's earlier download where w places it, and
// records it as the lesson's (the row the job reads, and the claims). In the
// plex-tv layout it is under an older title, so its names differ from the ones
// a re-download places.
func seedEarlier(t *testing.T, w *Worker, store *fakeWorkerStore) earlier {
	t.Helper()
	root := w.Cfg.LibraryDir
	if root == "" {
		root = w.Cfg.DownloadsDir
	}
	var e earlier
	var prev database.Lesson
	if w.Cfg.LibraryDir != "" && w.Cfg.Layout == LayoutPlexTV {
		e.dir = filepath.Join(root, "Beginner Course", "Season 01")
		e.names = []string{"Beginner Course - s01e05 - Old Name.mp4", "Beginner Course - s01e05 - Old Name resources/"}
		prev = recordedRow(100, e.dir, e.names...)
	} else {
		e.dir = filepath.Join(root, "Beginner Course", "05 - Lesson A")
		e.names = []string{"05 - Lesson A.mp4", "05 - Lesson A.nfo", "resources/"}
		prev = database.Lesson{RailcontentID: 100, Status: database.StatusDownloaded, OutputDir: sql.NullString{String: e.dir, Valid: true}}
	}
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	prev.VideoPath = sql.NullString{String: filepath.Join(e.dir, strings.TrimSuffix(e.names[0], "/")), Valid: true}
	seedSeason(t, e.dir, e.names...)
	store.lessons[100] = prev
	store.withFiles = append(store.withFiles, prev)
	return e
}

// assertSeeded fails unless every seedSeason name in dir holds what
// seedSeason wrote.
func assertSeeded(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if folder, ok := strings.CutSuffix(n, "/"); ok {
			if got, err := os.ReadFile(filepath.Join(dir, folder, "f.pdf")); err != nil || string(got) != folder {
				t.Errorf("%s/f.pdf = %q, %v; want the earlier download's file", filepath.Join(dir, folder), got, err)
			}
			continue
		}
		assertContent(t, dir, n)
	}
}

// assertNothingOfTheDownload fails if anything the re-download wrote is left
// in the downloads folder or the library, or its private folder or a
// replaced-<id> folder is.
func assertNothingOfTheDownload(t *testing.T, w *Worker) {
	t.Helper()
	for _, root := range []string{w.Cfg.DownloadsDir, w.Cfg.LibraryDir} {
		if root == "" {
			continue
		}
		for _, c := range []string{"new partial", "new mp4", "new nfo", "new pdf"} {
			if p := findContent(t, root, c); p != "" {
				t.Errorf("the download's %q is left at %q", c, p)
			}
		}
		assertExist(t, false, filepath.Join(root, privateRootName, replacedFolderName(1)))
	}
	assertExist(t, false, w.privateDir(1))
}

// TestWorkerStoppedReDownloadKeepsTheEarlierDownload (D66) proves a
// re-download that fails every attempt, or that a Skip, a delete (either
// intent), a Cancel or a shutdown stops mid-download, leaves the lesson's
// earlier download exactly as it was, in every setup and layout, although
// the downloader deletes the video at its output path as yt-dlp's
// --force-overwrites does: it only ever writes into its private folder,
// which goes whole. The worker never removes a kept file for a delete either:
// removing the lesson's files is the delete's own work.
func TestWorkerStoppedReDownloadKeepsTheEarlierDownload(t *testing.T) {
	for _, setup := range setups {
		for _, layout := range []string{"", LayoutPlexTV} {
			if setup == "none" && layout == LayoutPlexTV {
				continue // plex-tv needs a library
			}
			for _, stop := range []string{"failure", "skip", "delete", "keep", "cancel", "shutdown"} {
				t.Run(fmt.Sprintf("%s/layout=%s/%s", setup, layout, stop), func(t *testing.T) {
					w, store, dl := setupWorker(t, setup, layout)
					w.Cfg.MaxAttempts = 2
					e := seedEarlier(t, w, store)
					ctx, shutdown := context.WithCancel(context.Background())
					defer shutdown()
					kill := func(set map[int64]bool) func() {
						return func() {
							if set != nil {
								set[1] = true
							}
							w.CancelRunning(1)
						}
					}
					store.skipped, store.gone, store.kept = map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
					switch stop {
					case "failure":
						dl.fail = true
					case "skip":
						dl.during = kill(store.skipped)
					case "delete":
						dl.during = kill(store.gone)
					case "keep":
						dl.during = kill(store.kept)
					case "cancel":
						dl.during = kill(nil)
					case "shutdown":
						dl.during = shutdown
					}
					if _, err := w.RunOnce(ctx, 0); err != nil {
						t.Fatalf("RunOnce: %v", err)
					}
					if len(store.markDownloaded) != 0 {
						t.Errorf("recorded %+v, want nothing", store.markDownloaded)
					}
					if stop == "failure" && dl.calls != 2 {
						t.Errorf("attempts = %d, want 2", dl.calls)
					}
					assertSeeded(t, e.dir, e.names...)
					assertNothingOfTheDownload(t, w)
				})
			}
		}
	}
}

// TestWorkerStopDuringThePlacementPutsTheEarlierFilesBack (D79) proves a
// Skip, a delete (either intent) or a record that fails, landing once the
// finished download is being placed, leaves nothing of the download and puts
// back every entry the placement set aside: the lesson's earlier download (in
// plex-tv under an older title, so its names differ from the placed ones),
// or, where no row records the lesson folder, an entry at a placed name no
// lesson records. The one exception is a delete of the lesson's files: the
// lesson's own earlier entries are what it deletes, so they are not put back
// (the rest still is).
func TestWorkerStopDuringThePlacementPutsTheEarlierFilesBack(t *testing.T) {
	for _, setup := range setups {
		for _, layout := range []string{"", LayoutPlexTV} {
			if setup == "none" && layout == LayoutPlexTV {
				continue
			}
			for _, recorded := range []bool{true, false} {
				for _, stop := range []string{"skip", "keep", "delete", "unrecorded"} {
					t.Run(fmt.Sprintf("%s/layout=%s/recorded=%v/%s", setup, layout, recorded, stop), func(t *testing.T) {
						w, store, _ := setupWorker(t, setup, layout)
						w.Cfg.MaxAttempts = 1
						var e earlier
						if recorded {
							e = seedEarlier(t, w, store)
						} else {
							e = seedUnrecorded(t, w)
						}
						store.skipped, store.gone, store.kept = map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
						store.onConfirm = func() {
							switch stop {
							case "skip":
								store.skipped[1] = true
							case "keep":
								store.kept[1] = true
							case "delete":
								store.gone[1] = true
							case "unrecorded":
								store.finishErr = errors.New("disk I/O error")
							}
						}
						if _, err := w.RunOnce(context.Background(), 0); err != nil {
							t.Fatalf("RunOnce: %v", err)
						}
						if len(store.markDownloaded) != 0 {
							t.Errorf("recorded %+v, want nothing", store.markDownloaded)
						}
						if recorded && stop == "delete" {
							assertExist(t, false, paths(e.dir, e.names...)...)
						} else {
							assertSeeded(t, e.dir, e.names...)
						}
						assertNothingOfTheDownload(t, w)
					})
				}
			}
		}
	}
}

// seedUnrecorded writes, where w places lesson 100, an entry at the name its
// video is placed under that no lesson records (lesson 100 records nothing),
// and returns it.
func seedUnrecorded(t *testing.T, w *Worker) earlier {
	t.Helper()
	root := w.Cfg.LibraryDir
	if root == "" {
		root = w.Cfg.DownloadsDir
	}
	e := earlier{dir: filepath.Join(root, "Beginner Course", "05 - Lesson A"), names: []string{"05 - Lesson A.mp4"}}
	if w.Cfg.LibraryDir != "" && w.Cfg.Layout == LayoutPlexTV {
		e = earlier{dir: filepath.Join(root, "Beginner Course", "Season 01"), names: []string{"Beginner Course - s01e05 - Lesson A.mp4"}}
	}
	seedSeason(t, e.dir, e.names...)
	return e
}

// TestWorkerReDownloadReplacesOnlyTheLessonsFiles (D66) proves a successful
// re-download replaces the lesson's earlier download (and an entry no lesson
// records at a name it places), logs each entry it replaced, keeps every
// other file beside them, and leaves no private or replaced-<id> folder, in
// every setup and layout.
func TestWorkerReDownloadReplacesOnlyTheLessonsFiles(t *testing.T) {
	for _, setup := range setups {
		for _, layout := range []string{"", LayoutPlexTV} {
			if setup == "none" && layout == LayoutPlexTV {
				continue
			}
			t.Run(fmt.Sprintf("%s/layout=%s", setup, layout), func(t *testing.T) {
				w, store, _ := setupWorker(t, setup, layout)
				var log bytes.Buffer
				w.Log = &log
				e := seedEarlier(t, w, store)
				// Beside it: a file of the owner's, and (plex-tv) another lesson's
				// episode and an nfo at the placed name that no lesson records.
				unrelated := []string{"notes.txt"}
				var unclaimed string
				video := filepath.Join(e.dir, "05 - Lesson A.mp4")
				if layout == LayoutPlexTV {
					unrelated = append(unrelated, "Beginner Course - s01e06 - Lesson B.mp4")
					unclaimed = "Beginner Course - s01e05 - Lesson A.nfo"
					seedSeason(t, e.dir, unclaimed)
					store.withFiles = append(store.withFiles, recordedRow(6, e.dir, unrelated[1]))
					video = filepath.Join(e.dir, "Beginner Course - s01e05 - Lesson A.mp4")
				}
				seedSeason(t, e.dir, unrelated...)

				if _, err := w.RunOnce(context.Background(), 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				rec := onlyRecord(t, store)
				if rec.outputDir != e.dir || rec.videoPath != video {
					t.Errorf("recorded %q / %q, want %q / %q", rec.outputDir, rec.videoPath, e.dir, video)
				}
				if got, err := os.ReadFile(video); err != nil || string(got) != "new mp4" {
					t.Errorf("placed video = %q, %v; want the new download", got, err)
				}
				assertContent(t, e.dir, unrelated...)
				replaced := paths(e.dir, e.names...)
				if layout == LayoutPlexTV {
					// The old title's entries are gone; the nfo no lesson recorded
					// was replaced by the episode nfo.
					assertExist(t, false, replaced...)
				} else if findContent(t, e.dir, "resources") != "" {
					t.Error("the earlier resources/ folder is still in the lesson folder")
				}
				for _, p := range replaced {
					if want := fmt.Sprintf("↻ 100 replaced %q (the lesson's earlier download)", p); !strings.Contains(log.String(), want) {
						t.Errorf("log %q does not say %s", log.String(), want)
					}
				}
				if unclaimed != "" {
					want := fmt.Sprintf("↻ 100 replaced %q (which no lesson recorded)", filepath.Join(e.dir, unclaimed))
					if !strings.Contains(log.String(), want) {
						t.Errorf("log %q does not say %s", log.String(), want)
					}
				}
				for _, root := range []string{w.Cfg.DownloadsDir, w.Cfg.LibraryDir} {
					if root != "" {
						assertExist(t, false, filepath.Join(root, privateRootName, replacedFolderName(1)))
					}
				}
				assertExist(t, false, w.privateDir(1))
			})
		}
	}
}

// TestWorkerClaimedAgainStartsFromAnEmptyFolder (D66) proves a job claimed
// again after a crash starts in an empty private folder: what the stopped run
// left there (a finished-looking file included) is removed before the
// download, so it is never placed.
func TestWorkerClaimedAgainStartsFromAnEmptyFolder(t *testing.T) {
	w, store, _ := setupWorker(t, "separate", "")
	left := filepath.Join(w.privateDir(1), "05 - Lesson A")
	seedSeason(t, left, "stale.pdf")
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	rec := onlyRecord(t, store)
	assertExist(t, false, filepath.Join(rec.outputDir, "stale.pdf"), w.privateDir(1))
}

// TestSweepPrivateRemovesOnlyStoppedDownloads (D66) proves the startup sweep
// removes the private folder of a download that stopped (a crash), in the
// downloads folder and the library, and never a running job's; it keeps,
// and logs, a replaced-<id> folder (it may hold an earlier download's only
// copy) and anything in the private root it did not make.
func TestSweepPrivateRemovesOnlyStoppedDownloads(t *testing.T) {
	w, _, _ := setupWorker(t, "separate", "")
	var log bytes.Buffer
	w.Log = &log
	dlStaging := filepath.Join(w.Cfg.DownloadsDir, privateRootName)
	libStaging := filepath.Join(w.Cfg.LibraryDir, privateRootName)
	seedSeason(t, dlStaging, "job-7/", "job-8/", "replaced-9/", "replaced-9.2/", "notes/", "job-x/", "job-7.1/")
	seedSeason(t, libStaging, "job-7/", "replaced-9/")
	w.register(8, func() {})
	defer w.unregister(8)

	w.SweepPrivate()

	assertExist(t, false, filepath.Join(dlStaging, "job-7"), filepath.Join(libStaging, "job-7"))
	assertExist(t, true,
		filepath.Join(dlStaging, "job-8", "f.pdf"), filepath.Join(dlStaging, "replaced-9", "f.pdf"),
		filepath.Join(dlStaging, "replaced-9.2", "f.pdf"), filepath.Join(dlStaging, "job-7.1", "f.pdf"),
		filepath.Join(dlStaging, "notes", "f.pdf"), filepath.Join(dlStaging, "job-x", "f.pdf"),
		filepath.Join(libStaging, "replaced-9", "f.pdf"))
	for _, p := range []string{filepath.Join(dlStaging, "replaced-9"), filepath.Join(dlStaging, "replaced-9.2"), filepath.Join(libStaging, "replaced-9")} {
		if want := fmt.Sprintf("startup: kept %q", p); !strings.Contains(log.String(), want) {
			t.Errorf("log %q does not say %s", log.String(), want)
		}
	}
}

// TestDaemonRecoverSweepsThePrivateFolders (D66) proves the startup recovery
// runs the sweep: a stopped download's private folder is gone once Recover
// returns.
func TestDaemonRecoverSweepsThePrivateFolders(t *testing.T) {
	d := newTestDaemon(t, newFakeDaemonStore())
	left := filepath.Join(d.Worker.Cfg.DownloadsDir, privateRootName, "job-3")
	seedSeason(t, left, "05 - Lesson A.mp4.part")
	d.Log = &bytes.Buffer{}
	d.Recover(context.Background())
	assertExist(t, false, left)
}

// TestWorkerShutdownIsRequeuedNotSkipped (D66) runs a shutdown mid-download on
// a real store: the lesson is never skipped or failed, and the job stays
// running, so the next start's requeue (RequeueStaleRunning) queues it again
// and it downloads. (A shutdown used to record CancelDownload: the lesson
// skipped for good, and the job canceled.)
func TestWorkerShutdownIsRequeuedNotSkipped(t *testing.T) {
	ctx := context.Background()
	w, s, dl, f, _ := realWorker(t, "")
	jobID, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 100)
	if err != nil {
		t.Fatal(err)
	}
	blocking := newBlockingDownloader()
	w.Downloader = blocking
	runCtx, shutdown := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		_, _ = w.RunOnce(runCtx, 0)
		close(done)
	}()
	<-blocking.started
	shutdown()
	<-done

	l, err := s.GetLesson(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status == database.StatusSkipped || l.Status == database.StatusFailed {
		t.Fatalf("lesson status = %q after a shutdown, want neither skipped nor failed", l.Status)
	}
	if j, err := s.GetJob(ctx, jobID); err != nil || j.Status != database.JobRunning {
		t.Fatalf("job = %+v, %v; want it left running for the requeue", j, err)
	}
	if skip, err := s.ShouldSkipEnqueue(ctx, 100); err != nil || skip {
		t.Errorf("ShouldSkipEnqueue = %v, %v; want the lesson still wanted", skip, err)
	}
	assertExist(t, false, w.privateDir(jobID))

	// The next start requeues it, and it downloads.
	if n, err := s.RequeueStaleRunning(ctx); err != nil || n != 1 {
		t.Fatalf("RequeueStaleRunning = %d, %v; want the job requeued", n, err)
	}
	w.Downloader = dl
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if l, err := s.GetLesson(ctx, 100); err != nil || l.Status != database.StatusDownloaded {
		t.Errorf("lesson = %+v, %v; want it downloaded once requeued", l, err)
	}
}

// shutdownOnConfirm is a real store whose ConfirmDownload is followed by a
// shutdown: the download is complete and confirmed, and drumdrop starts
// stopping before it is placed.
type shutdownOnConfirm struct {
	*database.Store
	shutdown func()
}

func (s shutdownOnConfirm) ConfirmDownload(ctx context.Context, jobID int64, id int) error {
	err := s.Store.ConfirmDownload(ctx, jobID, id)
	s.shutdown()
	return err
}

// TestWorkerShutdownAfterTheDownloadFinishedStillRecordsIt (D66) runs a
// shutdown landing once the download finished and was confirmed, on a real
// store: the files are complete, so they are placed and recorded anyway (the
// other lessons' files are read, and the record written, past the dead
// context), never left for a second download after the restart.
func TestWorkerShutdownAfterTheDownloadFinishedStillRecordsIt(t *testing.T) {
	ctx := context.Background()
	w, s, _, f, _ := realWorker(t, "")
	if _, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 100); err != nil {
		t.Fatal(err)
	}
	runCtx, shutdown := context.WithCancel(ctx)
	defer shutdown()
	w.Store = shutdownOnConfirm{Store: s, shutdown: shutdown}
	if _, err := w.RunOnce(runCtx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	l, err := s.GetLesson(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Lesson A")
	if l.Status != database.StatusDownloaded || l.OutputDir.String != want {
		t.Errorf("lesson = %+v, want it downloaded into %q", l, want)
	}
}

// TestWorkerShutdownAfterResolveIsNotAFailure (D66) proves a shutdown landing
// after the lesson was resolved, while its row or the other lessons' files are
// read (the read fails with the dead context), records no failure: the job
// is left running for the requeue, and its end is reported as a shutdown.
func TestWorkerShutdownAfterResolveIsNotAFailure(t *testing.T) {
	for _, where := range []string{"row", "claims"} {
		t.Run(where, func(t *testing.T) {
			w, store, dl, _, _ := plexWorker(t)
			ctx, shutdown := context.WithCancel(context.Background())
			defer shutdown()
			w.Resolver = hookResolver{fakeResolver: w.Resolver.(fakeResolver), before: shutdown}
			if where == "row" {
				store.getLessonErr = context.Canceled
			} else {
				store.withFilesErr = context.Canceled
			}
			sink := &recordingSink{}
			w.Progress = sink
			if _, err := w.RunOnce(ctx, 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if len(store.markFailed) != 0 || len(store.markJobFailed) != 0 || len(dl.calls) != 0 {
				t.Errorf("failed %v/%v, downloads %d; want no failure and no download", store.markFailed, store.markJobFailed, len(dl.calls))
			}
			if got := store.jobs[1].Status; got != database.JobRunning {
				t.Errorf("job status = %q, want running", got)
			}
			assertEndsOnce(t, sink, 1)
			for _, e := range sink.snapshot() {
				if e.Kind == "lesson_skipped" && e.Err != msgShutdown {
					t.Errorf("end reported as %q, want the shutdown sentence", e.Err)
				}
			}
		})
	}
}
