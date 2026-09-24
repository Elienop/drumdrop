package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// The tests below pin owner rulings 2026-09-24 (n) and (o), on a real store
// and a real planner. (o): a failed, not-returned or stopped attempt leaves a
// lesson 'downloaded' only if the files its row records are on disk at that
// moment; otherwise it ends as a lesson without files does, and a failed one
// is queued by the next sync. (n): a lesson Musora doesn't return, whose
// earlier download is on disk, stays 'downloaded' with a note, and only its
// job fails.

// videoOf is lesson 100's recorded video.
func videoOf(t *testing.T, s *database.Store) string {
	t.Helper()
	l := getLesson(t, s)
	if !l.VideoPath.Valid || l.VideoPath.String == "" {
		t.Fatalf("lesson 100 records no video: %+v", l)
	}
	return l.VideoPath.String
}

// TestWorkerFailedAttemptOfALessonWhoseVideoIsGoneFailsIt (round-5c security
// L1, probe E1) is the row an older release left: its re-download ran
// yt-dlp's --force-overwrites in the lesson's folder, which deleted the video,
// and then failed, so the row records a video that is gone. A failed attempt
// fails the lesson, keeps what it records, and the next sync queues it again,
// every sync until one succeeds.
func TestWorkerFailedAttemptOfALessonWhoseVideoIsGoneFailsIt(t *testing.T) {
	ctx := context.Background()
	w, s, dl, f, _ := realWorker(t, "")
	w.Cfg.LibraryDir = ""
	w.Cfg.MaxAttempts = 1
	enqueue(t, s, f)
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	dir := getLesson(t, s).OutputDir.String
	if err := os.Remove(videoOf(t, s)); err != nil {
		t.Fatal(err)
	}
	dl.alwaysFail = true

	job := enqueue(t, s, f)
	for round := range 2 {
		if _, err := w.RunOnce(ctx, 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		assertEnded(t, s, job, database.StatusFailed, failDownload.lesson, dir, failDownload.job)
		if n := planCount(t, s, f); n != 1 {
			t.Fatalf("round %d: the next sync queued %d downloads, want 1 (a failed lesson is retried)", round, n)
		}
		jobs, err := s.ListJobsByStatus(ctx, database.JobQueued)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("queued jobs = %v, %v; want the one the sync queued", jobs, err)
		}
		job = jobs[0].ID
	}
}

// TestWorkerFailedReDownloadWithTheLibraryUnpluggedFailsTheLesson proves the
// case the owner accepted with (o): with the library drive not mounted, the
// recorded files read as missing, so a failed re-download fails the lesson,
// and syncs retry it until the drive is back.
func TestWorkerFailedReDownloadWithTheLibraryUnpluggedFailsTheLesson(t *testing.T) {
	ctx := context.Background()
	w, s, dl, f, dir := downloadedInLibrary(t)
	if err := os.Rename(w.Cfg.LibraryDir, w.Cfg.LibraryDir+".unplugged"); err != nil {
		t.Fatal(err)
	}
	dl.alwaysFail = true
	job := enqueue(t, s, f)

	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertEnded(t, s, job, database.StatusFailed, failDownload.lesson, dir, failDownload.job)
	if n := planCount(t, s, f); n != 1 {
		t.Errorf("the next sync queued %d downloads, want 1", n)
	}
}

// TestWorkerAttemptThatFailsBeforeDownloadingKeepsALessonOnDisk (round-5c
// code L3) proves an attempt that fails before it downloads anything
// (Musora didn't answer) leaves a lesson whose earlier download is on disk
// 'downloaded' with the kept note, not the failure's own sentence, and fails
// one whose recorded video is gone.
func TestWorkerAttemptThatFailsBeforeDownloadingKeepsALessonOnDisk(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(map[bool]string{false: "on disk", true: "video gone"}[gone], func(t *testing.T) {
			ctx := context.Background()
			w, s, _, f, dir := downloadedInLibrary(t)
			if gone {
				if err := os.Remove(videoOf(t, s)); err != nil {
					t.Fatal(err)
				}
			}
			w.Resolver = fakeResolver{errs: map[int]error{100: errors.New("musora answered 502")}}
			job := enqueue(t, s, f)

			if _, err := w.RunOnce(ctx, 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if gone {
				assertEnded(t, s, job, database.StatusFailed, failMusora.lesson, dir, failMusora.job)
				return
			}
			assertEnded(t, s, job, database.StatusDownloaded, msgEarlierKept, dir, failMusora.job)
		})
	}
}

// TestWorkerLessonMusoraDoesNotReturn proves ruling (n) with (o): a lesson
// Musora answers with no match stays 'downloaded' with msgNotReturnedKept
// when its earlier download is on disk, its job failing with msgNotResolved
// and its end reported as a failed attempt; syncs leave it alone. One whose
// recorded video is gone, or that has no files, is skipped, as before, and
// reported so.
func TestWorkerLessonMusoraDoesNotReturn(t *testing.T) {
	for _, c := range []struct {
		name         string
		files, gone  bool
		status, note string
		kind         string
	}{
		{"earlier download on disk", true, false, database.StatusDownloaded, msgNotReturnedKept, "attempt_failed"},
		{"earlier video gone", true, true, database.StatusSkipped, msgNotResolved, "lesson_skipped"},
		{"never downloaded", false, false, database.StatusSkipped, msgNotResolved, "lesson_skipped"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			w, s, _, f, _ := realWorker(t, "")
			dir := ""
			if c.files {
				w, s, _, f, dir = downloadedInLibrary(t)
			}
			if c.gone {
				if err := os.Remove(videoOf(t, s)); err != nil {
					t.Fatal(err)
				}
			}
			w.Resolver = fakeResolver{lessons: map[int]*musora.Lesson{}}
			sink := &recordingSink{}
			w.Progress = sink
			job := enqueue(t, s, f)

			if _, err := w.RunOnce(ctx, 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			assertEnded(t, s, job, c.status, c.note, dir, msgNotResolved)
			if n := planCount(t, s, f); n != 0 {
				t.Errorf("the next sync queued %d downloads, want none", n)
			}
			var ends []string
			for _, e := range sink.snapshot() {
				if e.Kind != "job_claimed" {
					ends = append(ends, e.Kind)
					if e.Err != msgNotResolved {
						t.Errorf("%s event says %q, want %q", e.Kind, e.Err, msgNotResolved)
					}
				}
			}
			if len(ends) != 1 || ends[0] != c.kind {
				t.Errorf("the job's end was reported as %v, want one %s", ends, c.kind)
			}
		})
	}
}

// TestWorkerCanceledReDownloadChecksTheFilesAreOnDisk proves a cancel keeps
// a lesson 'downloaded' only while its recorded video is on disk; one whose
// video is gone is left as a lesson without files is, skipped with the note
// that sends the owner to Download.
func TestWorkerCanceledReDownloadChecksTheFilesAreOnDisk(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(map[bool]string{false: "on disk", true: "video gone"}[gone], func(t *testing.T) {
			ctx := context.Background()
			w, s, dl, f, dir := downloadedInLibrary(t)
			if gone {
				if err := os.Remove(videoOf(t, s)); err != nil {
					t.Fatal(err)
				}
			}
			job := enqueue(t, s, f)
			dl.afterWrite = func(string) {
				if err := s.CancelJob(ctx, job); err != nil {
					t.Errorf("CancelJob: %v", err)
				}
			}

			if _, err := w.RunOnce(ctx, 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			l := getLesson(t, s)
			want, note := database.StatusDownloaded, ""
			if gone {
				want, note = database.StatusSkipped, "The download stopped before it finished. Download again to get this lesson."
			}
			if l.Status != want || l.Error.String != note || l.OutputDir.String != dir {
				t.Errorf("lesson = %s %q in %q, want %s %q in %q", l.Status, l.Error.String, l.OutputDir.String, want, note, dir)
			}
		})
	}
}

// TestRecordedFilesPresent pins which recorded files count as the lesson's
// files being on disk: the video when the row records one, otherwise every
// library entry, otherwise the folder; and that only a path read as there
// counts.
func TestRecordedFilesPresent(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	season := filepath.Join(lib, "Show", "Season 01")
	seedSeason(t, season, "Show - s01e01 - A.mp4", "Show - s01e01 - A.nfo")
	folder := filepath.Join(tmp, "dl", "Show", "01 - A")
	writeTree(t, folder, map[string]string{"01 - A.nfo": "nfo"})
	// A resources folder is recorded as one entry; a dangling symlink, and
	// one to a regular file, sit at recorded names (security round 5d I2): an
	// entry is read through a symlink, as the video is.
	if err := os.Mkdir(filepath.Join(season, "Show - s01e01 - A resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "nothing"), filepath.Join(season, "Show - s01e01 - A-poster.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(season, "Show - s01e01 - A.nfo"), filepath.Join(season, "Show - s01e01 - A.en.srt")); err != nil {
		t.Fatal(err)
	}
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	entries := func(names ...string) sql.NullString { return database.EncodeLibraryEntries(recordOf(season, names...)) }

	for _, c := range []struct {
		name    string
		row     database.Lesson
		library string
		want    bool
	}{
		{"its video", database.Lesson{OutputDir: str(season), VideoPath: str(filepath.Join(season, "Show - s01e01 - A.mp4"))}, lib, true},
		{"its video gone", database.Lesson{OutputDir: str(folder), VideoPath: str(filepath.Join(folder, "01 - A.mp4"))}, lib, false},
		{"its video a folder", database.Lesson{OutputDir: str(tmp), VideoPath: str(folder)}, lib, false},
		{"the video decides, not the entries", database.Lesson{OutputDir: str(season), VideoPath: str(filepath.Join(season, "gone.mp4")), LibraryEntries: entries("Show - s01e01 - A.nfo")}, lib, false},
		{"every entry, no video", database.Lesson{OutputDir: str(season), VideoPath: str(""), LibraryEntries: entries("Show - s01e01 - A.mp4", "Show - s01e01 - A.nfo")}, lib, true},
		{"an entry gone", database.Lesson{OutputDir: str(season), LibraryEntries: entries("Show - s01e01 - A.nfo", "Show - s01e01 - A.en.vtt")}, lib, false},
		{"a recorded folder entry", database.Lesson{OutputDir: str(season), LibraryEntries: entries("Show - s01e01 - A.nfo", "Show - s01e01 - A resources")}, lib, true},
		{"an entry a dangling symlink", database.Lesson{OutputDir: str(season), LibraryEntries: entries("Show - s01e01 - A.nfo", "Show - s01e01 - A-poster.jpg")}, lib, false},
		{"an entry a symlink to a file", database.Lesson{OutputDir: str(season), LibraryEntries: entries("Show - s01e01 - A.nfo", "Show - s01e01 - A.en.srt")}, lib, true},
		{"entries with no library folder", database.Lesson{OutputDir: str(season), LibraryEntries: entries("Show - s01e01 - A.nfo")}, "", false},
		{"a damaged record", database.Lesson{OutputDir: str(season), LibraryEntries: str("not json")}, lib, false},
		{"its folder, no video", database.Lesson{OutputDir: str(folder)}, lib, true},
		{"its folder gone", database.Lesson{OutputDir: str(filepath.Join(tmp, "gone"))}, lib, false},
		{"its folder a file", database.Lesson{OutputDir: str(filepath.Join(folder, "01 - A.nfo"))}, lib, false},
		{"no files", database.Lesson{}, lib, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := &Worker{Cfg: Config{LibraryDir: c.library}}
			if got := w.recordedFilesPresent(c.row); got != c.want {
				t.Errorf("recordedFilesPresent = %v, want %v", got, c.want)
			}
		})
	}
}
