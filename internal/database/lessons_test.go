package database

import (
	"context"
	"database/sql"
	"testing"
)

// countLessons returns the number of rows in lessons. Used to assert dedup: a
// re-upsert of the same railcontent_id must not create a second row.
func countLessons(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM lessons").Scan(&n); err != nil {
		t.Fatalf("count lessons: %v", err)
	}
	return n
}

func TestUpsertLessonInsertsNewRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	parent := sql.NullInt64{Int64: 100, Valid: true}
	if err := s.UpsertLesson(ctx, 409875, "Lesson One", parent, "drumeo"); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if got.RailcontentID != 409875 {
		t.Errorf("RailcontentID = %d, want 409875", got.RailcontentID)
	}
	if got.Title != "Lesson One" {
		t.Errorf("Title = %q, want %q", got.Title, "Lesson One")
	}
	if !got.ParentRailcontentID.Valid || got.ParentRailcontentID.Int64 != 100 {
		t.Errorf("ParentRailcontentID = %+v, want valid 100", got.ParentRailcontentID)
	}
	if got.Brand != "drumeo" {
		t.Errorf("Brand = %q, want %q", got.Brand, "drumeo")
	}
	// A freshly upserted lesson defaults to pending.
	if got.Status != StatusPending {
		t.Errorf("Status = %q, want %q on a new row", got.Status, StatusPending)
	}
	if !got.FirstSeenAt.Valid || got.FirstSeenAt.Time.IsZero() {
		t.Errorf("FirstSeenAt = %+v, want a populated CURRENT_TIMESTAMP", got.FirstSeenAt)
	}
	if got.DownloadedAt.Valid {
		t.Errorf("DownloadedAt = %+v, want NULL on a pending lesson", got.DownloadedAt)
	}
}

func TestUpsertLessonDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 409875, "Original Title", sql.NullInt64{}, "drumeo"); err != nil {
		t.Fatalf("first UpsertLesson: %v", err)
	}
	// Re-upserting the same railcontent_id must update in place, not duplicate.
	if err := s.UpsertLesson(ctx, 409875, "Updated Title",
		sql.NullInt64{Int64: 200, Valid: true}, "drumeo"); err != nil {
		t.Fatalf("second UpsertLesson: %v", err)
	}

	if got := countLessons(t, s); got != 1 {
		t.Fatalf("after re-upserting the same id, lessons has %d rows, want 1", got)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	// title + parent are refreshed by the upsert.
	if got.Title != "Updated Title" {
		t.Errorf("Title = %q, want refreshed %q", got.Title, "Updated Title")
	}
	if !got.ParentRailcontentID.Valid || got.ParentRailcontentID.Int64 != 200 {
		t.Errorf("ParentRailcontentID = %+v, want refreshed 200", got.ParentRailcontentID)
	}
}

// TestUpsertLessonDoesNotResetStatus is the key dedup invariant: once a lesson
// is downloaded, a later UpsertLesson (e.g. a re-sync that re-discovers it) must
// NOT downgrade its status back to pending, or sync would redundantly re-download.
func TestUpsertLessonDoesNotResetStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 409875, "Lesson", sql.NullInt64{}, "drumeo"); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	if err := s.MarkDownloaded(ctx, 409875, "best", "/out/dir", "/out/dir/video.mp4", 12345); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}

	// A re-sync upserts the same lesson again.
	if err := s.UpsertLesson(ctx, 409875, "Lesson (renamed)", sql.NullInt64{}, "drumeo"); err != nil {
		t.Fatalf("re-UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if got.Status != StatusDownloaded {
		t.Errorf("Status = %q after re-upsert, want it preserved as %q", got.Status, StatusDownloaded)
	}
	// The download metadata must survive the upsert too.
	if !got.VideoPath.Valid || got.VideoPath.String != "/out/dir/video.mp4" {
		t.Errorf("VideoPath = %+v, want preserved", got.VideoPath)
	}
	if !got.DownloadedAt.Valid {
		t.Error("DownloadedAt cleared by re-upsert, want preserved")
	}
	// But the descriptive fields still refresh.
	if got.Title != "Lesson (renamed)" {
		t.Errorf("Title = %q, want refreshed %q", got.Title, "Lesson (renamed)")
	}
}

func TestGetLessonMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.GetLesson(ctx, 12345); err == nil {
		t.Error("GetLesson for a missing id returned nil error, want error")
	}
}

func TestIsDownloaded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo"); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	// Pending lesson is not downloaded.
	if ok, err := s.IsDownloaded(ctx, 1); err != nil {
		t.Fatalf("IsDownloaded: %v", err)
	} else if ok {
		t.Error("IsDownloaded = true for a pending lesson, want false")
	}

	if err := s.MarkDownloaded(ctx, 1, "best", "/d", "/d/v.mp4", 0); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
	if ok, err := s.IsDownloaded(ctx, 1); err != nil {
		t.Fatalf("IsDownloaded: %v", err)
	} else if !ok {
		t.Error("IsDownloaded = false after MarkDownloaded, want true")
	}

	// An id that was never seen is, by definition, not downloaded (no error).
	if ok, err := s.IsDownloaded(ctx, 99999); err != nil {
		t.Fatalf("IsDownloaded for unknown id: %v", err)
	} else if ok {
		t.Error("IsDownloaded = true for an unknown id, want false")
	}
}

func TestStatusTransitions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo"); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	// pending -> downloading
	if err := s.MarkDownloading(ctx, 1); err != nil {
		t.Fatalf("MarkDownloading: %v", err)
	}
	if got := statusOf(t, s, 1); got != StatusDownloading {
		t.Errorf("after MarkDownloading status = %q, want %q", got, StatusDownloading)
	}

	// downloading -> failed (sets error)
	if err := s.MarkFailed(ctx, 1, "boom"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	failed, err := s.GetLesson(ctx, 1)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if failed.Status != StatusFailed {
		t.Errorf("status = %q, want %q", failed.Status, StatusFailed)
	}
	if !failed.Error.Valid || failed.Error.String != "boom" {
		t.Errorf("Error = %+v, want %q", failed.Error, "boom")
	}

	// failed -> downloaded clears the error and records paths + downloaded_at.
	if err := s.MarkDownloaded(ctx, 1, "best", "/out", "/out/v.mp4", 999); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
	done, err := s.GetLesson(ctx, 1)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if done.Status != StatusDownloaded {
		t.Errorf("status = %q, want %q", done.Status, StatusDownloaded)
	}
	if done.Error.Valid {
		t.Errorf("Error = %+v, want cleared after MarkDownloaded", done.Error)
	}
	if !done.Quality.Valid || done.Quality.String != "best" {
		t.Errorf("Quality = %+v, want %q", done.Quality, "best")
	}
	if !done.OutputDir.Valid || done.OutputDir.String != "/out" {
		t.Errorf("OutputDir = %+v, want %q", done.OutputDir, "/out")
	}
	if !done.VideoPath.Valid || done.VideoPath.String != "/out/v.mp4" {
		t.Errorf("VideoPath = %+v, want %q", done.VideoPath, "/out/v.mp4")
	}
	if !done.Bytes.Valid || done.Bytes.Int64 != 999 {
		t.Errorf("Bytes = %+v, want 999", done.Bytes)
	}
	if !done.DownloadedAt.Valid {
		t.Error("DownloadedAt is NULL after MarkDownloaded, want set")
	}
}

func TestMarkSkipped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo"); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	if err := s.MarkSkipped(ctx, 1, "locked content"); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	got, err := s.GetLesson(ctx, 1)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if got.Status != StatusSkipped {
		t.Errorf("status = %q, want %q", got.Status, StatusSkipped)
	}
	if !got.Error.Valid || got.Error.String != "locked content" {
		t.Errorf("Error = %+v, want %q", got.Error, "locked content")
	}
}

// TestMarkTransitionMissing asserts the mark helpers report an error when no
// lesson row matches, rather than silently succeeding on zero rows.
func TestMarkTransitionMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.MarkDownloading(ctx, 404); err == nil {
		t.Error("MarkDownloading on a missing id returned nil error, want error")
	}
	if err := s.MarkDownloaded(ctx, 404, "best", "/d", "/d/v.mp4", 0); err == nil {
		t.Error("MarkDownloaded on a missing id returned nil error, want error")
	}
	if err := s.MarkFailed(ctx, 404, "x"); err == nil {
		t.Error("MarkFailed on a missing id returned nil error, want error")
	}
	if err := s.MarkSkipped(ctx, 404, "x"); err == nil {
		t.Error("MarkSkipped on a missing id returned nil error, want error")
	}
}

// TestUpsertLessonRejectsBadStatus guards the lessons.status CHECK at the store
// boundary: a raw insert with an out-of-set status must be rejected so the
// constraint is real, not merely declared.
func TestUpsertLessonRejectsBadStatus(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.DB().Exec(
		"INSERT INTO lessons(railcontent_id, status) VALUES(1, 'bogus')",
	); err == nil {
		t.Error("inserting a lesson with status='bogus' succeeded, want CHECK violation")
	}
	// The valid status set inserts cleanly (guards against an over-broad CHECK).
	for _, st := range []string{
		StatusPending, StatusDownloading, StatusDownloaded, StatusFailed, StatusSkipped,
	} {
		if _, err := s.DB().Exec(
			"INSERT INTO lessons(railcontent_id, status) VALUES(?, ?)",
			idForStatus(st), st,
		); err != nil {
			t.Errorf("inserting a lesson with valid status %q failed: %v", st, err)
		}
	}
}

func TestListByStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Three lessons in three different terminal states plus one pending.
	for id, title := range map[int]string{1: "a", 2: "b", 3: "c", 4: "d"} {
		if err := s.UpsertLesson(ctx, id, title, sql.NullInt64{}, "drumeo"); err != nil {
			t.Fatalf("UpsertLesson %d: %v", id, err)
		}
	}
	if err := s.MarkDownloaded(ctx, 1, "best", "/d", "/d/v.mp4", 0); err != nil {
		t.Fatalf("MarkDownloaded 1: %v", err)
	}
	if err := s.MarkDownloaded(ctx, 2, "best", "/d", "/d/v.mp4", 0); err != nil {
		t.Fatalf("MarkDownloaded 2: %v", err)
	}
	if err := s.MarkFailed(ctx, 3, "boom"); err != nil {
		t.Fatalf("MarkFailed 3: %v", err)
	}
	// id 4 stays pending.

	downloaded, err := s.ListByStatus(ctx, StatusDownloaded)
	if err != nil {
		t.Fatalf("ListByStatus(downloaded): %v", err)
	}
	if len(downloaded) != 2 {
		t.Errorf("ListByStatus(downloaded) returned %d rows, want 2", len(downloaded))
	}
	for _, l := range downloaded {
		if l.Status != StatusDownloaded {
			t.Errorf("ListByStatus(downloaded) returned a %q row", l.Status)
		}
	}

	failed, err := s.ListByStatus(ctx, StatusFailed)
	if err != nil {
		t.Fatalf("ListByStatus(failed): %v", err)
	}
	if len(failed) != 1 || failed[0].RailcontentID != 3 {
		t.Errorf("ListByStatus(failed) = %+v, want exactly lesson 3", failed)
	}

	pending, err := s.ListByStatus(ctx, StatusPending)
	if err != nil {
		t.Fatalf("ListByStatus(pending): %v", err)
	}
	if len(pending) != 1 || pending[0].RailcontentID != 4 {
		t.Errorf("ListByStatus(pending) = %+v, want exactly lesson 4", pending)
	}
}

// statusOf reads the status column of a lesson directly for assertions.
func statusOf(t *testing.T, s *Store, id int) string {
	t.Helper()
	var st string
	if err := s.DB().QueryRow("SELECT status FROM lessons WHERE railcontent_id = ?", id).Scan(&st); err != nil {
		t.Fatalf("read status of lesson %d: %v", id, err)
	}
	return st
}

// idForStatus maps a status string to a distinct railcontent_id so the
// valid-status loop in TestUpsertLessonRejectsBadStatus does not collide on the
// primary key.
func idForStatus(status string) int {
	switch status {
	case StatusPending:
		return 10
	case StatusDownloading:
		return 11
	case StatusDownloaded:
		return 12
	case StatusFailed:
		return 13
	case StatusSkipped:
		return 14
	default:
		return 0
	}
}
