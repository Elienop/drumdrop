package database

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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
	if _, err := db.Exec(`UPDATE lessons SET library_entries = '["/lib/x"]' WHERE railcontent_id = 7`); err != nil {
		t.Fatalf("write the new column: %v", err)
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

func jobStatus(t *testing.T, s *Store, id int64) string {
	t.Helper()
	j, err := s.GetJob(context.Background(), id)
	if err != nil {
		t.Fatalf("GetJob %d: %v", id, err)
	}
	return j.Status
}

// TestFinishDownloadRecordsAndClosesTheJob proves the worker's final write sets
// the lesson downloaded with its library record and the job done, together.
func TestFinishDownloadRecordsAndClosesTheJob(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	jobID := seedJob(t, s, 1, sql.NullInt64{})
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
	if l.Status != StatusDownloaded || l.OutputDir.String != rec.OutputDir || l.VideoPath.String != rec.VideoPath || l.Bytes.Int64 != 9 {
		t.Errorf("lesson = %+v, want downloaded with %+v", l, rec)
	}
	if got, recorded, err := l.PlacedEntries(); !recorded || err != nil || !reflect.DeepEqual(got, entries) {
		t.Errorf("PlacedEntries = (%v, %v, %v), want %v", got, recorded, err, entries)
	}
	if st := jobStatus(t, s, jobID); st != JobDone {
		t.Errorf("job status = %q, want done", st)
	}

	// A default-layout record (nil entries) stores NULL, not "[]".
	job2 := seedJob(t, s, 2, sql.NullInt64{})
	if err := s.FinishDownload(ctx, job2, 2, DownloadRecord{OutputDir: "/dl/F/01 - B"}); err != nil {
		t.Fatalf("FinishDownload default: %v", err)
	}
	if l, _ := s.GetLesson(ctx, 2); l.LibraryEntries.Valid {
		t.Errorf("default-layout record = %+v, want NULL", l.LibraryEntries)
	}
}

// TestWorkerWritesAreAbandonedOnceTheJobIsGone proves every guarded worker
// write lands nothing, and says so with ErrDownloadAbandoned, once a delete has
// removed the job (or the lesson row), so a download that started before a
// delete can never bring the row back.
func TestWorkerWritesAreAbandonedOnceTheJobIsGone(t *testing.T) {
	ctx := context.Background()
	writes := map[string]func(s *Store, jobID int64, id int) error{
		"StartDownload": func(s *Store, j int64, id int) error { return s.StartDownload(ctx, j, id) },
		"FinishDownload": func(s *Store, j int64, id int) error {
			return s.FinishDownload(ctx, j, id, DownloadRecord{OutputDir: "/x", LibraryEntries: []string{"/x/y"}})
		},
		"FailDownload":   func(s *Store, j int64, id int) error { return s.FailDownload(ctx, j, id, "boom") },
		"SkipDownload":   func(s *Store, j int64, id int) error { return s.SkipDownload(ctx, j, id, "gated") },
		"CancelDownload": func(s *Store, j int64, id int) error { return s.CancelDownload(ctx, j, id) },
	}
	for name, write := range writes {
		t.Run(name+"/lesson deleted", func(t *testing.T) {
			s := newTestStore(t)
			jobID := seedJob(t, s, 5, sql.NullInt64{})
			if _, _, err := s.ClaimNextJob(ctx); err != nil {
				t.Fatalf("claim: %v", err)
			}
			if _, _, err := s.BeginLessonDelete(ctx, 5); err != nil {
				t.Fatalf("BeginLessonDelete: %v", err)
			}
			if err := s.TombstoneLesson(ctx, mustLesson(t, s, 5)); err != nil {
				t.Fatalf("TombstoneLesson: %v", err)
			}
			if err := write(s, jobID, 5); !errors.Is(err, ErrDownloadAbandoned) {
				t.Fatalf("%s after the delete = %v, want ErrDownloadAbandoned", name, err)
			}
			l := mustLesson(t, s, 5)
			if l.Status != StatusSkipped || l.Error.String != "deleted" || l.OutputDir.Valid || l.LibraryEntries.Valid {
				t.Errorf("tombstone overwritten by %s: %+v", name, l)
			}
		})
		t.Run(name+"/row deleted", func(t *testing.T) {
			s := newTestStore(t)
			jobID := seedJob(t, s, 6, sql.NullInt64{})
			if _, err := s.rawDB().Exec(`DELETE FROM lessons WHERE railcontent_id = 6`); err != nil {
				t.Fatalf("delete row: %v", err)
			}
			if err := write(s, jobID, 6); !errors.Is(err, ErrDownloadAbandoned) {
				t.Fatalf("%s with no lesson row = %v, want ErrDownloadAbandoned", name, err)
			}
		})
	}
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
// the lesson and the job while the job is live.
func TestGuardedFailSkipCancelWrites(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		write      func(s *Store, j int64) error
		lesson     string
		lessonErr  string
		jobStatus  string
		jobErrText string
	}{
		{"fail", func(s *Store, j int64) error { return s.FailDownload(ctx, j, 1, "boom") }, StatusFailed, "boom", JobFailed, "boom"},
		{"skip", func(s *Store, j int64) error { return s.SkipDownload(ctx, j, 1, "gated") }, StatusSkipped, "gated", JobFailed, "gated"},
		{"cancel", func(s *Store, j int64) error { return s.CancelDownload(ctx, j, 1) }, StatusSkipped, "canceled", JobCanceled, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestStore(t)
			jobID := seedJob(t, s, 1, sql.NullInt64{})
			if _, _, err := s.ClaimNextJob(ctx); err != nil {
				t.Fatalf("claim: %v", err)
			}
			if err := c.write(s, jobID); err != nil {
				t.Fatalf("write: %v", err)
			}
			l := mustLesson(t, s, 1)
			if l.Status != c.lesson || l.Error.String != c.lessonErr {
				t.Errorf("lesson = %s/%q, want %s/%q", l.Status, l.Error.String, c.lesson, c.lessonErr)
			}
			j, err := s.GetJob(ctx, jobID)
			if err != nil {
				t.Fatalf("GetJob: %v", err)
			}
			if j.Status != c.jobStatus || j.Error.String != c.jobErrText || !j.FinishedAt.Valid {
				t.Errorf("job = %s/%q finished=%v, want %s/%q finished", j.Status, j.Error.String, j.FinishedAt.Valid, c.jobStatus, c.jobErrText)
			}
		})
	}
}

// TestBeginLessonDeleteRemovesItsActiveJobs proves the delete's first step
// reads the lesson and removes its queued and running jobs (reporting the
// running ones), leaves other lessons' jobs and finished jobs alone, and turns a
// lesson left 'downloading' into skipped/canceled.
func TestBeginLessonDeleteRemovesItsActiveJobs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	running := seedJob(t, s, 1, sql.NullInt64{})
	if _, _, err := s.ClaimNextJob(ctx); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.StartDownload(ctx, running, 1); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	other := seedJob(t, s, 2, sql.NullInt64{})

	l, runningIDs, err := s.BeginLessonDelete(ctx, 1)
	if err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	if l.RailcontentID != 1 || l.Status != StatusDownloading {
		t.Errorf("returned lesson = %+v, want lesson 1 as read (downloading)", l)
	}
	if !reflect.DeepEqual(runningIDs, []int64{running}) {
		t.Errorf("running = %v, want [%d]", runningIDs, running)
	}
	if _, err := s.GetJob(ctx, running); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("lesson 1's running job survived (err=%v), want removed", err)
	}
	if st := jobStatus(t, s, other); st != JobQueued {
		t.Errorf("another lesson's job = %q, want untouched (queued)", st)
	}
	if got := mustLesson(t, s, 1); got.Status != StatusSkipped || got.Error.String != "canceled" {
		t.Errorf("lesson after begin = %s/%q, want skipped/canceled", got.Status, got.Error.String)
	}
	if _, _, err := s.BeginLessonDelete(ctx, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown lesson = %v, want sql.ErrNoRows", err)
	}
}

// TestTombstoneAndKeepAreCompareAndSwap proves the delete's final write lands
// only while the lesson still records the files that were removed: once a later
// download records new ones, neither write touches the row.
func TestTombstoneAndKeepAreCompareAndSwap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	jobID := seedJob(t, s, 1, sql.NullInt64{})
	old := DownloadRecord{OutputDir: "/lib/S/Season 01", VideoPath: "/lib/S/Season 01/a.mp4", LibraryEntries: []string{"/lib/S/Season 01/a.mp4"}}
	if err := s.FinishDownload(ctx, jobID, 1, old); err != nil {
		t.Fatalf("FinishDownload: %v", err)
	}
	before := mustLesson(t, s, 1)

	// Keep narrows the record while it is unchanged.
	kept := EncodeLibraryEntries([]string{"/lib/S/Season 01/a.mp4"})
	if err := s.KeepLessonFiles(ctx, before, kept); err != nil {
		t.Fatalf("KeepLessonFiles: %v", err)
	}
	before = mustLesson(t, s, 1)

	// A later download records different files.
	job2 := seedJob(t, s, 1, sql.NullInt64{})
	if err := s.FinishDownload(ctx, job2, 1, DownloadRecord{OutputDir: "/lib/S/Season 01", LibraryEntries: []string{"/lib/S/Season 01/b.mp4"}}); err != nil {
		t.Fatalf("FinishDownload 2: %v", err)
	}
	if err := s.TombstoneLesson(ctx, before); !errors.Is(err, ErrLessonChanged) {
		t.Errorf("TombstoneLesson on a changed row = %v, want ErrLessonChanged", err)
	}
	if err := s.KeepLessonFiles(ctx, before, sql.NullString{}); !errors.Is(err, ErrLessonChanged) {
		t.Errorf("KeepLessonFiles on a changed row = %v, want ErrLessonChanged", err)
	}
	now := mustLesson(t, s, 1)
	if paths, _, _ := now.PlacedEntries(); len(paths) != 1 || !strings.HasSuffix(paths[0], "b.mp4") || now.Status != StatusDownloaded {
		t.Errorf("the later download's record was touched: %+v", now)
	}
	if err := s.TombstoneLesson(ctx, now); err != nil {
		t.Fatalf("TombstoneLesson on the current row: %v", err)
	}
	if got := mustLesson(t, s, 1); got.Status != StatusSkipped || got.Error.String != "deleted" || got.OutputDir.Valid || got.LibraryEntries.Valid {
		t.Errorf("tombstone = %+v", got)
	}
	if err := s.TombstoneLesson(ctx, Lesson{RailcontentID: 404}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("TombstoneLesson unknown = %v, want sql.ErrNoRows", err)
	}
}

// TestBeginFollowDeleteAndFilelessCascade covers the follow delete's store
// side: the begin step removes only the follow's active jobs, and the fileless
// cascade refuses while any lesson still records files, then deletes once none
// does.
func TestBeginFollowDeleteAndFilelessCascade(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	f, err := s.AddNodeFollow(ctx, 10, "F", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	g, err := s.AddNodeFollow(ctx, 20, "G", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	fid := sql.NullInt64{Int64: f.ID, Valid: true}
	gid := sql.NullInt64{Int64: g.ID, Valid: true}
	fJob := seedJob(t, s, 1, fid)
	if _, _, err := s.ClaimNextJob(ctx); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Lesson 2 belongs to f, but its job was enqueued by g's planner.
	if err := s.UpsertLesson(ctx, 2, "L2", sql.NullInt64{}, "drumeo", sql.NullInt64{}, fid); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	crossJob, _, err := s.EnqueueJob(ctx, gid, 2)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	gJob := seedJob(t, s, 3, gid)
	if err := s.MarkDownloaded(ctx, 2, "best", "/dl/F/02 - L2", "/dl/F/02 - L2/v.mp4", 1); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}

	lessons, running, err := s.BeginFollowDelete(ctx, f.ID)
	if err != nil {
		t.Fatalf("BeginFollowDelete: %v", err)
	}
	var ids []int
	for _, l := range lessons {
		ids = append(ids, l.RailcontentID)
	}
	sort.Ints(ids)
	if !reflect.DeepEqual(ids, []int{1, 2}) || !reflect.DeepEqual(running, []int64{fJob}) {
		t.Errorf("begin = lessons %v running %v, want [1 2] and [%d]", ids, running, fJob)
	}
	for _, j := range []int64{fJob, crossJob} {
		if _, err := s.GetJob(ctx, j); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("job %d of the follow's lessons survived (err=%v)", j, err)
		}
	}
	if st := jobStatus(t, s, gJob); st != JobQueued {
		t.Errorf("the other follow's job = %q, want untouched", st)
	}

	if err := s.RemoveFilelessFollowCascade(ctx, f.ID); !errors.Is(err, ErrFollowHasFiles) {
		t.Fatalf("cascade with lesson 2's files recorded = %v, want ErrFollowHasFiles", err)
	}
	if _, err := s.GetLesson(ctx, 2); err != nil {
		t.Errorf("a refused cascade deleted lesson 2: %v", err)
	}
	if err := s.TombstoneLesson(ctx, mustLesson(t, s, 2)); err != nil {
		t.Fatalf("TombstoneLesson: %v", err)
	}
	if err := s.RemoveFilelessFollowCascade(ctx, f.ID); err != nil {
		t.Fatalf("cascade once no files remain: %v", err)
	}
	if _, err := s.GetFollow(ctx, f.ID); err == nil {
		t.Error("follow survived the cascade")
	}
	if _, err := s.GetLesson(ctx, 3); err != nil {
		t.Errorf("the other follow's lesson was deleted: %v", err)
	}
}

// TestListLessonsWithFiles returns exactly the rows that record files.
func TestListLessonsWithFiles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []int{1, 2, 3} {
		if err := s.UpsertLesson(ctx, id, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
			t.Fatalf("UpsertLesson: %v", err)
		}
	}
	if err := s.MarkDownloaded(ctx, 1, "best", "/d/1", "", 0); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
	if _, err := s.rawDB().Exec(`UPDATE lessons SET library_entries = '[]' WHERE railcontent_id = 3`); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	got, err := s.ListLessonsWithFiles(ctx)
	if err != nil {
		t.Fatalf("ListLessonsWithFiles: %v", err)
	}
	var ids []int
	for _, l := range got {
		ids = append(ids, l.RailcontentID)
	}
	if !reflect.DeepEqual(ids, []int{1, 3}) {
		t.Errorf("ids = %v, want [1 3]", ids)
	}
}
