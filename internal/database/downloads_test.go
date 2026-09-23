package database

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// TestMigration004UpgradesAnExistingDatabase applies only the migrations that
// shipped before 004, writes a downloaded lesson the way the old code did, then
// runs the full migration set: 004 applies once, the old row survives unchanged
// with no library record (NULL), and the new column takes a record.
func TestMigration004UpgradesAnExistingDatabase(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := ensureMigrationsTable(db); err != nil {
		t.Fatalf("ensure migrations table: %v", err)
	}
	for _, name := range []string{"001_initial_schema.sql", "002_lessons_follow_id.sql", "003_lessons_position.sql"} {
		body, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, name); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO lessons(railcontent_id, title, status, output_dir, video_path, position)
		VALUES(7, 'Old', 'downloaded', '/lib/Show/Season 01', '/lib/Show/Season 01/Show - s01e03 - Old.mp4', 3)`); err != nil {
		t.Fatalf("insert old-style row: %v", err)
	}

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations on the old database: %v", err)
	}
	var applied int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = '004_lessons_library_entries.sql'`).Scan(&applied); err != nil {
		t.Fatalf("count 004: %v", err)
	}
	if applied != 1 {
		t.Fatalf("004 recorded %d times, want 1", applied)
	}

	s := NewStore(db)
	l, err := s.GetLesson(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetLesson after upgrade: %v", err)
	}
	if l.Title != "Old" || l.Status != StatusDownloaded || l.OutputDir.String != "/lib/Show/Season 01" ||
		l.VideoPath.String != "/lib/Show/Season 01/Show - s01e03 - Old.mp4" || l.Position.Int64 != 3 {
		t.Errorf("old row changed by the upgrade: %+v", l)
	}
	if _, recorded, err := l.PlacedEntries(); recorded || err != nil {
		t.Errorf("old row PlacedEntries recorded=%v err=%v, want no record (NULL)", recorded, err)
	}
	if l.Deleting {
		t.Error("old row reads as being deleted after the upgrade")
	}
	if _, err := db.Exec(`UPDATE lessons SET library_entries = '["Show/Season 01/x"]', deleting = 1 WHERE railcontent_id = 7`); err != nil {
		t.Fatalf("write the new columns: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO abandoned_jobs(job_id, discard) VALUES(1, 1)`); err != nil {
		t.Fatalf("write the new table: %v", err)
	}
}

// TestPlacedEntriesRoundTrip pins the record encoding: nil is NULL (no
// record), an empty slice is a record that holds nothing, and a damaged value
// is an error rather than an empty record.
func TestPlacedEntriesRoundTrip(t *testing.T) {
	if got := EncodeLibraryEntries(nil); got.Valid {
		t.Errorf("EncodeLibraryEntries(nil) = %+v, want NULL", got)
	}
	empty := Lesson{LibraryEntries: EncodeLibraryEntries([]string{})}
	if paths, recorded, err := empty.PlacedEntries(); !recorded || err != nil || len(paths) != 0 {
		t.Errorf("empty record = (%v, %v, %v), want ([], true, nil)", paths, recorded, err)
	}
	want := []string{"/lib/S/Season 01/S - s01e01 - A [x].mp4", `/lib/S/Season 01/S - s01e01 - "q".nfo`}
	full := Lesson{LibraryEntries: EncodeLibraryEntries(want)}
	if paths, recorded, err := full.PlacedEntries(); !recorded || err != nil || !reflect.DeepEqual(paths, want) {
		t.Errorf("record = (%v, %v, %v), want (%v, true, nil)", paths, recorded, err, want)
	}
	for _, bad := range []string{"not json", "null", `[""]`, `{"a":1}`, `[1]`} {
		l := Lesson{LibraryEntries: sql.NullString{String: bad, Valid: true}}
		if _, _, err := l.PlacedEntries(); err == nil {
			t.Errorf("PlacedEntries(%q) = nil error, want a damaged-record error", bad)
		}
	}
}

// seedJob upserts lesson id and enqueues + claims a job for it, returning the
// running job's id.
func seedJob(t *testing.T, s *Store, id int, follow sql.NullInt64) int64 {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertLesson(ctx, id, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, follow); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	jobID, _, err := s.EnqueueJob(ctx, follow, id)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	return jobID
}

// TestFinishDownloadRecordsAndClosesTheJob proves the worker's final write sets
// the lesson downloaded with its library record (clearing an earlier attempt's
// error and stamping downloaded_at) and the job done, together.
func TestFinishDownloadRecordsAndClosesTheJob(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	jobID := seedJob(t, s, 1, sql.NullInt64{})
	mustExec(t, s, `UPDATE lessons SET status = ?, error = 'boom' WHERE railcontent_id = 1`, StatusFailed)
	if _, _, err := s.ClaimNextJob(ctx); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.StartDownload(ctx, jobID, 1); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	if l, _ := s.GetLesson(ctx, 1); l.Status != StatusDownloading {
		t.Errorf("status after StartDownload = %q, want downloading", l.Status)
	}
	entries := []string{"/lib/S/Season 01/S - s01e01 - A.mp4", "/lib/S/Season 01/S - s01e01 - A.nfo"}
	rec := DownloadRecord{Quality: "1080", OutputDir: "/lib/S/Season 01", VideoPath: entries[0], Bytes: 9, LibraryEntries: entries}
	if err := s.FinishDownload(ctx, jobID, 1, rec); err != nil {
		t.Fatalf("FinishDownload: %v", err)
	}
	l, err := s.GetLesson(ctx, 1)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if l.Status != StatusDownloaded || l.Quality.String != "1080" || l.OutputDir.String != rec.OutputDir ||
		l.VideoPath.String != rec.VideoPath || l.Bytes.Int64 != 9 || l.Error.Valid || !l.DownloadedAt.Valid {
		t.Errorf("lesson = %+v, want downloaded with %+v, no error, downloaded_at set", l, rec)
	}
	if got, recorded, err := l.PlacedEntries(); !recorded || err != nil || !reflect.DeepEqual(got, entries) {
		t.Errorf("PlacedEntries = (%v, %v, %v), want %v", got, recorded, err, entries)
	}
	if j := mustJob(t, s, jobID); j.Status != JobDone || !j.FinishedAt.Valid || j.Error.Valid {
		t.Errorf("job = %s finished=%v error=%+v, want done, finished, no error", j.Status, j.FinishedAt.Valid, j.Error)
	}

	// A default-layout record (nil entries) stores NULL, not "[]".
	job2 := claimed(t, s, 2, sql.NullInt64{})
	if err := s.FinishDownload(ctx, job2, 2, DownloadRecord{OutputDir: "/dl/F/01 - B"}); err != nil {
		t.Fatalf("FinishDownload default: %v", err)
	}
	if l, _ := s.GetLesson(ctx, 2); l.LibraryEntries.Valid {
		t.Errorf("default-layout record = %+v, want NULL", l.LibraryEntries)
	}
}

// seedFiles makes lesson id record files directly: an output_dir (or NULL for
// ""), a video (or NULL), and a raw library_entries value (NULL when invalid).
// A fixture only: the code under test records through FinishDownload.
func seedFiles(t *testing.T, s *Store, id int, outputDir, video string, entries sql.NullString) {
	t.Helper()
	orNull := func(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }
	if _, err := s.rawDB().Exec(
		`UPDATE lessons SET status = ?, output_dir = ?, video_path = ?, bytes = 5, library_entries = ? WHERE railcontent_id = ?`,
		StatusDownloaded, orNull(outputDir), orNull(video), entries, id,
	); err != nil {
		t.Fatalf("seed files of lesson %d: %v", id, err)
	}
}

// claimed seeds lesson id with a job and claims it (running), returning its id.
func claimed(t *testing.T, s *Store, id int, follow sql.NullInt64) int64 {
	t.Helper()
	jobID := seedJob(t, s, id, follow)
	forceRunning(t, s, jobID)
	return jobID
}

// recordDownloaded records lesson id as downloaded the way the worker does:
// a claimed job, then FinishDownload.
func recordDownloaded(t *testing.T, s *Store, id int, rec DownloadRecord) {
	t.Helper()
	if err := s.FinishDownload(context.Background(), claimed(t, s, id, sql.NullInt64{}), id, rec); err != nil {
		t.Fatalf("FinishDownload %d: %v", id, err)
	}
}

// recordFailed records lesson id as failed with msg the way the worker does:
// a claimed job, then FailDownload.
func recordFailed(t *testing.T, s *Store, id int, msg string) {
	t.Helper()
	if err := s.FailDownload(context.Background(), claimed(t, s, id, sql.NullInt64{}), id, msg); err != nil {
		t.Fatalf("FailDownload %d: %v", id, err)
	}
}

// guardedWrites are the worker's writes about a job, each for lesson id.
func guardedWrites(ctx context.Context) map[string]func(s *Store, jobID int64, id int) error {
	return map[string]func(s *Store, jobID int64, id int) error{
		"StartDownload":   func(s *Store, j int64, id int) error { return s.StartDownload(ctx, j, id) },
		"ConfirmDownload": func(s *Store, j int64, id int) error { return s.ConfirmDownload(ctx, j, id) },
		"FinishDownload": func(s *Store, j int64, id int) error {
			return s.FinishDownload(ctx, j, id, DownloadRecord{OutputDir: "/x", LibraryEntries: []string{"S/Season 01/y"}})
		},
		"FailDownload":   func(s *Store, j int64, id int) error { return s.FailDownload(ctx, j, id, "boom") },
		"SkipDownload":   func(s *Store, j int64, id int) error { return s.SkipDownload(ctx, j, id, "gated") },
		"CancelDownload": func(s *Store, j int64, id int) error { return s.CancelDownload(ctx, j, id) },
	}
}

// TestWorkerWritesAreAbandonedOnceTheJobIsGone proves every guarded worker
// write lands nothing, and says so with ErrDownloadAbandoned, once a delete has
// removed the job (or the lesson row), so a download that started before a
// delete can never bring the row back; and that it carries what the delete
// wanted: ErrDiscardDownload only when the delete removes the lesson's files.
func TestWorkerWritesAreAbandonedOnceTheJobIsGone(t *testing.T) {
	ctx := context.Background()
	for name, write := range guardedWrites(ctx) {
		t.Run(name+"/lesson delete", func(t *testing.T) {
			s := newTestStore(t)
			jobID := claimed(t, s, 5, sql.NullInt64{})
			if _, _, err := s.BeginLessonDelete(ctx, 5); err != nil {
				t.Fatalf("BeginLessonDelete: %v", err)
			}
			if err := s.TombstoneLesson(ctx, mustLesson(t, s, 5)); err != nil {
				t.Fatalf("TombstoneLesson: %v", err)
			}
			err := write(s, jobID, 5)
			if !errors.Is(err, ErrDownloadAbandoned) || !errors.Is(err, ErrDiscardDownload) {
				t.Fatalf("%s after the delete = %v, want ErrDownloadAbandoned with ErrDiscardDownload", name, err)
			}
			l := mustLesson(t, s, 5)
			if l.Status != StatusSkipped || l.Error.String != "deleted" || l.OutputDir.Valid || l.LibraryEntries.Valid {
				t.Errorf("tombstone overwritten by %s: %+v", name, l)
			}
		})
		t.Run(name+"/follow removed with its files", func(t *testing.T) {
			s := newTestStore(t)
			f := seedFollow(t, s, 50)
			fid := sql.NullInt64{Int64: f, Valid: true}
			jobID := claimed(t, s, 5, fid)
			if _, _, err := s.BeginFollowDelete(ctx, f); err != nil {
				t.Fatalf("BeginFollowDelete: %v", err)
			}
			if _, err := s.RemoveFilelessFollowCascade(ctx, f); err != nil {
				t.Fatalf("RemoveFilelessFollowCascade: %v", err)
			}
			if err := write(s, jobID, 5); !errors.Is(err, ErrDownloadAbandoned) || !errors.Is(err, ErrDiscardDownload) {
				t.Fatalf("%s after the follow and its files went = %v, want ErrDownloadAbandoned with ErrDiscardDownload", name, err)
			}
		})
		t.Run(name+"/follow removed keeping its files", func(t *testing.T) {
			s := newTestStore(t)
			f := seedFollow(t, s, 50)
			jobID := claimed(t, s, 5, sql.NullInt64{Int64: f, Valid: true})
			if _, err := s.RemoveFollowCascade(ctx, f); err != nil {
				t.Fatalf("RemoveFollowCascade: %v", err)
			}
			if err := write(s, jobID, 5); !errors.Is(err, ErrDownloadAbandoned) || errors.Is(err, ErrDiscardDownload) {
				t.Fatalf("%s after a keep-files removal = %v, want ErrDownloadAbandoned without ErrDiscardDownload", name, err)
			}
		})
		t.Run(name+"/row deleted, intent unknown", func(t *testing.T) {
			s := newTestStore(t)
			jobID := claimed(t, s, 6, sql.NullInt64{})
			if _, err := s.rawDB().Exec(`DELETE FROM lessons WHERE railcontent_id = 6`); err != nil {
				t.Fatalf("delete row: %v", err)
			}
			if err := write(s, jobID, 6); !errors.Is(err, ErrDownloadAbandoned) || errors.Is(err, ErrDiscardDownload) {
				t.Fatalf("%s with no lesson row = %v, want ErrDownloadAbandoned without ErrDiscardDownload", name, err)
			}
		})
	}
}

// TestGuardedWritesHonourACancel (D60) proves a job
// canceled in the database while its worker holds it, before its process was
// registered, can not start or go on: StartDownload and ConfirmDownload say it
// was canceled and write nothing. Once its files are placed, a cancel that
// landed meanwhile is too late: FinishDownload records them and closes the
// job, so no file is left untracked; the other terminal writes keep the job
// canceled.
func TestGuardedWritesHonourACancel(t *testing.T) {
	ctx := context.Background()
	for name, write := range guardedWrites(ctx) {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			jobID := claimed(t, s, 1, sql.NullInt64{})
			if err := s.CancelJob(ctx, jobID); err != nil {
				t.Fatalf("CancelJob: %v", err)
			}
			err := write(s, jobID, 1)
			l, j := mustLesson(t, s, 1), mustJob(t, s, jobID)
			switch name {
			case "StartDownload", "ConfirmDownload":
				if !errors.Is(err, ErrDownloadCanceled) || l.Status != StatusPending || j.Status != JobCanceled {
					t.Errorf("%s on a canceled job = %v (lesson %s, job %s), want ErrDownloadCanceled and nothing written", name, err, l.Status, j.Status)
				}
			case "FinishDownload":
				if err != nil || l.Status != StatusDownloaded || j.Status != JobDone {
					t.Errorf("FinishDownload after a late cancel = %v (lesson %s, job %s), want recorded, job done", err, l.Status, j.Status)
				}
			default:
				if err != nil || j.Status != JobCanceled {
					t.Errorf("%s on a canceled job = %v (job %s), want it to land and the job to stay canceled", name, err, j.Status)
				}
			}
		})
	}
}

func mustJob(t *testing.T, s *Store, id int64) Job {
	t.Helper()
	j, err := s.GetJob(context.Background(), id)
	if err != nil {
		t.Fatalf("GetJob %d: %v", id, err)
	}
	return j
}

func mustLesson(t *testing.T, s *Store, id int) Lesson {
	t.Helper()
	l, err := s.GetLesson(context.Background(), id)
	if err != nil {
		t.Fatalf("GetLesson %d: %v", id, err)
	}
	return l
}

// TestGuardedFailSkipCancelWrites pins what each guarded terminal write sets on
// the lesson and the job while the job is live. A canceled download of a
// lesson that still records files from an earlier download leaves it
// 'downloaded' (D63): 'skipped' would hide files that are still there.
func TestGuardedFailSkipCancelWrites(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		files      bool
		write      func(s *Store, j int64) error
		lesson     string
		lessonErr  string
		jobStatus  string
		jobErrText string
	}{
		{"fail", false, func(s *Store, j int64) error { return s.FailDownload(ctx, j, 1, "boom") }, StatusFailed, "boom", JobFailed, "boom"},
		{"skip", false, func(s *Store, j int64) error { return s.SkipDownload(ctx, j, 1, "gated") }, StatusSkipped, "gated", JobFailed, "gated"},
		{"cancel", false, func(s *Store, j int64) error { return s.CancelDownload(ctx, j, 1) }, StatusSkipped, "canceled", JobCanceled, ""},
		{"cancel with files", true, func(s *Store, j int64) error { return s.CancelDownload(ctx, j, 1) }, StatusDownloaded, "", JobCanceled, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestStore(t)
			jobID := claimed(t, s, 1, sql.NullInt64{})
			if c.files {
				seedFiles(t, s, 1, "/dl/F/01 - L", "/dl/F/01 - L/01 - L.mp4", sql.NullString{})
			}
			if err := s.StartDownload(ctx, jobID, 1); err != nil {
				t.Fatalf("StartDownload: %v", err)
			}
			if err := c.write(s, jobID); err != nil {
				t.Fatalf("write: %v", err)
			}
			l := mustLesson(t, s, 1)
			if l.Status != c.lesson || l.Error.String != c.lessonErr {
				t.Errorf("lesson = %s/%q, want %s/%q", l.Status, l.Error.String, c.lesson, c.lessonErr)
			}
			j := mustJob(t, s, jobID)
			if j.Status != c.jobStatus || j.Error.String != c.jobErrText || !j.FinishedAt.Valid {
				t.Errorf("job = %s/%q finished=%v, want %s/%q finished", j.Status, j.Error.String, j.FinishedAt.Valid, c.jobStatus, c.jobErrText)
			}
		})
	}
}

// TestBeginLessonDeleteRemovesItsJobsAndBlocksNewOnes proves the delete's
// first step: it removes the lesson's queued, running and canceled jobs
// (reporting the running and canceled ones, whose workers may still hold
// them, and recording that their files go), leaves other lessons' jobs and
// finished jobs alone, and marks the lesson deleting, so until the delete ends
// no job can be enqueued or retried for it, the planner skips it, and a second
// delete is refused.
func TestBeginLessonDeleteRemovesItsJobsAndBlocksNewOnes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	canceled := claimed(t, s, 1, sql.NullInt64{})
	if err := s.CancelJob(ctx, canceled); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
	running := claimed(t, s, 1, sql.NullInt64{})
	if err := s.StartDownload(ctx, running, 1); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	done := seedJob(t, s, 3, sql.NullInt64{})
	mustExec(t, s, `UPDATE jobs SET status = ?, railcontent_id = 1 WHERE id = ?`, JobDone, done)
	other := seedJob(t, s, 2, sql.NullInt64{})

	l, kill, err := s.BeginLessonDelete(ctx, 1)
	if err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	if l.RailcontentID != 1 || !l.Deleting || l.Status != StatusSkipped || l.Error.String != "canceled" {
		t.Errorf("returned lesson = %+v, want lesson 1, deleting, its download ended (skipped/canceled: no files)", l)
	}
	sort.Slice(kill, func(i, j int) bool { return kill[i] < kill[j] })
	if !reflect.DeepEqual(kill, []int64{canceled, running}) {
		t.Errorf("jobs to kill = %v, want [%d %d]", kill, canceled, running)
	}
	for _, j := range []int64{canceled, running} {
		if _, err := s.GetJob(ctx, j); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("lesson 1's job %d survived (err=%v), want removed", j, err)
		}
		var discard int
		if err := s.rawDB().QueryRow(`SELECT discard FROM abandoned_jobs WHERE job_id = ?`, j).Scan(&discard); err != nil || discard != 1 {
			t.Errorf("job %d intent = %d (err %v), want discard recorded", j, discard, err)
		}
	}
	if st := mustJob(t, s, done).Status; st != JobDone {
		t.Errorf("a finished job = %q, want kept", st)
	}
	if st := mustJob(t, s, other).Status; st != JobQueued {
		t.Errorf("another lesson's job = %q, want untouched (queued)", st)
	}

	if _, _, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1); !errors.Is(err, ErrLessonDeleting) {
		t.Errorf("EnqueueJob while deleting = %v, want ErrLessonDeleting", err)
	}
	mustExec(t, s, `UPDATE jobs SET status = ? WHERE id = ?`, JobFailed, other)
	mustExec(t, s, `UPDATE jobs SET railcontent_id = 1 WHERE id = ?`, other)
	if err := s.RetryJob(ctx, other); !errors.Is(err, ErrLessonDeleting) {
		t.Errorf("RetryJob while deleting = %v, want ErrLessonDeleting", err)
	}
	if skip, err := s.ShouldSkipEnqueue(ctx, 1); err != nil || !skip {
		t.Errorf("ShouldSkipEnqueue while deleting = %v, %v, want true", skip, err)
	}
	if _, _, err := s.BeginLessonDelete(ctx, 1); !errors.Is(err, ErrLessonDeleting) {
		t.Errorf("a second BeginLessonDelete = %v, want ErrLessonDeleting", err)
	}

	if err := s.EndLessonDelete(ctx, 1, 404); err != nil {
		t.Fatalf("EndLessonDelete: %v", err)
	}
	if _, created, err := s.EnqueueJob(ctx, sql.NullInt64{}, 1); err != nil || !created {
		t.Errorf("EnqueueJob after the delete ended = %v, %v, want a new job", created, err)
	}
	if _, _, err := s.BeginLessonDelete(ctx, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown lesson = %v, want sql.ErrNoRows", err)
	}
}

// TestBeginLessonDeleteLeavesADownloadingLessonWithFilesDownloaded (D63)
// proves a delete that stops a re-download leaves the lesson 'downloaded'
// while it still records its earlier files, so a delete that then fails does
// not leave files behind a 'skipped' row.
func TestBeginLessonDeleteLeavesADownloadingLessonWithFilesDownloaded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	jobID := claimed(t, s, 1, sql.NullInt64{})
	seedFiles(t, s, 1, "/dl/F/01 - L", "", sql.NullString{})
	if err := s.StartDownload(ctx, jobID, 1); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	l, _, err := s.BeginLessonDelete(ctx, 1)
	if err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	if l.Status != StatusDownloaded || l.Error.Valid {
		t.Errorf("lesson = %s/%v, want downloaded with no error", l.Status, l.Error)
	}
}

// TestClearStaleDeletes proves the daemon's startup step ends a delete a dead
// process left marked, so the lesson can be downloaded again.
func TestClearStaleDeletes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedJob(t, s, 1, sql.NullInt64{})
	if _, _, err := s.BeginLessonDelete(ctx, 1); err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	if n, err := s.ClearStaleDeletes(ctx); err != nil || n != 1 {
		t.Fatalf("ClearStaleDeletes = %d, %v, want 1", n, err)
	}
	if l := mustLesson(t, s, 1); l.Deleting {
		t.Error("lesson still deleting after ClearStaleDeletes")
	}
}

// TestShouldSkipEnqueueWhileDeleting proves the planner's own check skips a
// pending lesson for as long as a delete of it runs, and only then.
func TestShouldSkipEnqueueWhileDeleting(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	skip := func() bool {
		t.Helper()
		ok, err := s.ShouldSkipEnqueue(ctx, 1)
		if err != nil {
			t.Fatalf("ShouldSkipEnqueue: %v", err)
		}
		return ok
	}
	if skip() {
		t.Fatal("a pending lesson is skipped before any delete")
	}
	if _, _, err := s.BeginLessonDelete(ctx, 1); err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	if !skip() {
		t.Error("a lesson being deleted is not skipped")
	}
	if err := s.EndLessonDelete(ctx, 1); err != nil {
		t.Fatalf("EndLessonDelete: %v", err)
	}
	if skip() {
		t.Error("a lesson is still skipped after its delete ended")
	}
}

// TestTombstoneAndKeepCompareEachColumn proves the delete's final write lands
// only while the lesson still records exactly the files that were read: a
// change to any ONE of output_dir, video_path or library_entries is caught on
// its own, and neither write touches the row.
func TestTombstoneAndKeepCompareEachColumn(t *testing.T) {
	ctx := context.Background()
	for _, col := range []string{"output_dir", "video_path", "library_entries"} {
		t.Run(col, func(t *testing.T) {
			s := newTestStore(t)
			seedJob(t, s, 1, sql.NullInt64{})
			seedFiles(t, s, 1, "/lib/S/Season 01", "/lib/S/Season 01/a.mp4", EncodeLibraryEntries([]string{"S/Season 01/a.mp4"}))
			before := mustLesson(t, s, 1)
			mustExec(t, s, `UPDATE lessons SET `+col+` = 'changed' WHERE railcontent_id = 1`)
			changed := mustLesson(t, s, 1)
			if err := s.TombstoneLesson(ctx, before); !errors.Is(err, ErrLessonChanged) {
				t.Errorf("TombstoneLesson with %s changed = %v, want ErrLessonChanged", col, err)
			}
			if err := s.KeepLessonFiles(ctx, before, KeptFiles{}); !errors.Is(err, ErrLessonChanged) {
				t.Errorf("KeepLessonFiles with %s changed = %v, want ErrLessonChanged", col, err)
			}
			if now := mustLesson(t, s, 1); !reflect.DeepEqual(now, changed) {
				t.Errorf("row touched:\n got %+v\nwant %+v", now, changed)
			}
		})
	}
	s := newTestStore(t)
	if err := s.TombstoneLesson(ctx, Lesson{RailcontentID: 404}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("TombstoneLesson unknown = %v, want sql.ErrNoRows", err)
	}
}

// TestTombstoneEndsTheDelete pins the tombstone: skipped/deleted, every path
// cleared, and the delete ended.
func TestTombstoneEndsTheDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedJob(t, s, 1, sql.NullInt64{})
	seedFiles(t, s, 1, "/dl/F/01 - L", "/dl/F/01 - L/01 - L.mp4", EncodeLibraryEntries([]string{}))
	l, _, err := s.BeginLessonDelete(ctx, 1)
	if err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	if err := s.TombstoneLesson(ctx, l); err != nil {
		t.Fatalf("TombstoneLesson: %v", err)
	}
	got := mustLesson(t, s, 1)
	if got.Status != StatusSkipped || got.Error.String != "deleted" || got.OutputDir.Valid || got.VideoPath.Valid ||
		got.Bytes.Valid || got.LibraryEntries.Valid || got.Deleting {
		t.Errorf("tombstone = %+v", got)
	}
}

// TestKeepLessonFilesRecordsOnlyWhatRemains (D56, D63) proves a delete that
// could not remove everything leaves the row recording only the files still
// on disk, each column on its own, reading 'downloaded', with the delete
// ended.
func TestKeepLessonFilesRecordsOnlyWhatRemains(t *testing.T) {
	ctx := context.Background()
	record := EncodeLibraryEntries([]string{"S/Season 01/a.mp4", "S/Season 01/a.nfo"})
	cases := []struct {
		name  string
		kept  KeptFiles
		dir   bool
		video bool
		entry sql.NullString
	}{
		{"everything kept, record narrowed", KeptFiles{OutputDir: true, VideoPath: true, LibraryEntries: []string{"S/Season 01/a.nfo"}}, true, true, EncodeLibraryEntries([]string{"S/Season 01/a.nfo"})},
		{"video gone", KeptFiles{OutputDir: true, LibraryEntries: []string{"S/Season 01/a.nfo"}}, true, false, EncodeLibraryEntries([]string{"S/Season 01/a.nfo"})},
		{"folder gone, record left as is", KeptFiles{VideoPath: true}, false, true, record},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestStore(t)
			seedJob(t, s, 1, sql.NullInt64{})
			seedFiles(t, s, 1, "/lib/S/Season 01", "/lib/S/Season 01/a.mp4", record)
			mustExec(t, s, `UPDATE lessons SET status = ?, error = 'x' WHERE railcontent_id = 1`, StatusSkipped)
			l, _, err := s.BeginLessonDelete(ctx, 1)
			if err != nil {
				t.Fatalf("BeginLessonDelete: %v", err)
			}
			if err := s.KeepLessonFiles(ctx, l, c.kept); err != nil {
				t.Fatalf("KeepLessonFiles: %v", err)
			}
			got := mustLesson(t, s, 1)
			if got.OutputDir.Valid != c.dir || got.VideoPath.Valid != c.video || got.Bytes.Valid != c.video || got.LibraryEntries != c.entry {
				t.Errorf("row = dir %v video %v bytes %v record %+v, want dir %v video %v record %+v",
					got.OutputDir.Valid, got.VideoPath.Valid, got.Bytes.Valid, got.LibraryEntries, c.dir, c.video, c.entry)
			}
			if got.Status != StatusDownloaded || got.Error.Valid || got.Deleting {
				t.Errorf("row = %s/%v deleting=%v, want downloaded, no error, delete ended", got.Status, got.Error, got.Deleting)
			}
		})
	}
}

// TestBeginFollowDeleteAndFilelessCascade covers the follow delete's store
// side: the begin step removes the jobs of the follow's own lessons (even one
// another follow queued) and none of another follow's lessons (even one this
// follow queued), marks the follow's lessons deleting, and the fileless
// cascade refuses while any lesson still records files, then deletes once none
// does.
func TestBeginFollowDeleteAndFilelessCascade(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	f := seedFollow(t, s, 10)
	g := seedFollow(t, s, 20)
	fid := sql.NullInt64{Int64: f, Valid: true}
	gid := sql.NullInt64{Int64: g, Valid: true}
	fJob := claimed(t, s, 1, fid)
	// Lesson 2 belongs to f, but its job was enqueued by g's planner.
	if err := s.UpsertLesson(ctx, 2, "L2", sql.NullInt64{}, "drumeo", sql.NullInt64{}, fid); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	crossJob, _, err := s.EnqueueJob(ctx, gid, 2)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	// Lesson 3 belongs to g, but f's planner enqueued its running job.
	if err := s.UpsertLesson(ctx, 3, "L3", sql.NullInt64{}, "drumeo", sql.NullInt64{}, gid); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	collateral, _, err := s.EnqueueJob(ctx, fid, 3)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	forceRunning(t, s, collateral)
	seedFiles(t, s, 2, "/dl/F/02 - L2", "/dl/F/02 - L2/v.mp4", sql.NullString{})

	lessons, kill, err := s.BeginFollowDelete(ctx, f)
	if err != nil {
		t.Fatalf("BeginFollowDelete: %v", err)
	}
	var ids []int
	for _, l := range lessons {
		ids = append(ids, l.RailcontentID)
		if !l.Deleting {
			t.Errorf("lesson %d not marked deleting", l.RailcontentID)
		}
	}
	if !reflect.DeepEqual(ids, []int{1, 2}) || !reflect.DeepEqual(kill, []int64{fJob}) {
		t.Errorf("begin = lessons %v kill %v, want [1 2] and [%d]", ids, kill, fJob)
	}
	for _, j := range []int64{fJob, crossJob} {
		if _, err := s.GetJob(ctx, j); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("job %d of the follow's lessons survived (err=%v)", j, err)
		}
	}
	if st := mustJob(t, s, collateral).Status; st != JobRunning {
		t.Errorf("another follow's lesson's job = %q, want untouched (running)", st)
	}
	if _, _, err := s.BeginFollowDelete(ctx, f); !errors.Is(err, ErrLessonDeleting) {
		t.Errorf("a second BeginFollowDelete = %v, want ErrLessonDeleting", err)
	}
	if _, err := s.RemoveFollowCascade(ctx, f); !errors.Is(err, ErrLessonDeleting) {
		t.Errorf("RemoveFollowCascade during the delete = %v, want ErrLessonDeleting", err)
	}

	if _, err := s.RemoveFilelessFollowCascade(ctx, f); !errors.Is(err, ErrFollowHasFiles) {
		t.Fatalf("cascade with lesson 2's files recorded = %v, want ErrFollowHasFiles", err)
	}
	if _, err := s.GetLesson(ctx, 2); err != nil {
		t.Errorf("a refused cascade deleted lesson 2: %v", err)
	}
	if err := s.TombstoneLesson(ctx, mustLesson(t, s, 2)); err != nil {
		t.Fatalf("TombstoneLesson: %v", err)
	}
	if _, err := s.RemoveFilelessFollowCascade(ctx, f); err != nil {
		t.Fatalf("cascade once no files remain: %v", err)
	}
	if _, err := s.GetFollow(ctx, f); err == nil {
		t.Error("follow survived the cascade")
	}
	if _, err := s.GetLesson(ctx, 3); err != nil {
		t.Errorf("the other follow's lesson was deleted: %v", err)
	}
	if j := mustJob(t, s, collateral); j.Status != JobRunning || j.FollowID.Valid {
		t.Errorf("the other follow's lesson's job = %+v, want still running, detached from the removed follow", j)
	}
}

// TestRemoveFollowCascadeStopsOnlyItsOwnLessons (D60) proves removing a
// follow without its files stops the downloads of its own lessons, recording
// that their files are kept, and never another follow's lesson's download,
// even one this follow queued.
func TestRemoveFollowCascadeStopsOnlyItsOwnLessons(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	f := seedFollow(t, s, 10)
	g := seedFollow(t, s, 20)
	fid := sql.NullInt64{Int64: f, Valid: true}
	own := claimed(t, s, 1, fid)
	if err := s.UpsertLesson(ctx, 3, "L3", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: g, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	collateral, _, err := s.EnqueueJob(ctx, fid, 3)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	forceRunning(t, s, collateral)

	kill, err := s.RemoveFollowCascade(ctx, f)
	if err != nil {
		t.Fatalf("RemoveFollowCascade: %v", err)
	}
	if !reflect.DeepEqual(kill, []int64{own}) {
		t.Errorf("jobs to kill = %v, want only the follow's own [%d]", kill, own)
	}
	var discard int
	if err := s.rawDB().QueryRow(`SELECT discard FROM abandoned_jobs WHERE job_id = ?`, own).Scan(&discard); err != nil || discard != 0 {
		t.Errorf("intent for job %d = %d (err %v), want keep (0)", own, discard, err)
	}
	if j := mustJob(t, s, collateral); j.Status != JobRunning || j.FollowID.Valid {
		t.Errorf("collateral job = %+v, want still running, detached", j)
	}
	if err := s.StartDownload(ctx, collateral, 3); err != nil {
		t.Errorf("the other follow's lesson's download was stopped: %v", err)
	}
}

// TestHasFilesAgreesWithTheStore proves the Go predicate behind the API's
// has_files and the SQL one behind "rows with files" (ListLessonsWithFiles,
// the fileless cascade) give the same answer for every shape of row.
func TestHasFilesAgreesWithTheStore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	f := seedFollow(t, s, 10)
	rows := []struct {
		id      int
		dir     string
		entries sql.NullString
		want    bool
	}{
		{1, "", sql.NullString{}, false},
		{2, "/dl/F/02 - B", sql.NullString{}, true},
		{3, "", EncodeLibraryEntries([]string{}), false},
		{4, "", EncodeLibraryEntries([]string{"S/Season 01/x.mp4"}), true},
		{5, "", sql.NullString{String: "not json", Valid: true}, true},
		{6, "/lib/S/Season 01", EncodeLibraryEntries([]string{}), true},
	}
	for _, r := range rows {
		if err := s.UpsertLesson(ctx, r.id, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
			t.Fatalf("UpsertLesson: %v", err)
		}
		seedFiles(t, s, r.id, r.dir, "", r.entries)
	}
	listed, err := s.ListLessonsWithFiles(ctx)
	if err != nil {
		t.Fatalf("ListLessonsWithFiles: %v", err)
	}
	inList := map[int]bool{}
	for _, l := range listed {
		inList[l.RailcontentID] = true
	}
	for _, r := range rows {
		l := mustLesson(t, s, r.id)
		if l.HasFiles() != r.want || inList[r.id] != r.want {
			t.Errorf("row %d: HasFiles=%v listed=%v, want both %v", r.id, l.HasFiles(), inList[r.id], r.want)
		}
	}
	for _, r := range rows {
		if r.want {
			mustExec(t, s, `DELETE FROM lessons WHERE railcontent_id = ?`, r.id)
		}
	}
	if _, err := s.RemoveFilelessFollowCascade(ctx, f); err != nil {
		t.Errorf("fileless cascade with only rows HasFiles calls fileless = %v, want it to go through", err)
	}
}
