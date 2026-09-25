package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// countLessons returns the number of rows in lessons. Used to assert dedup: a
// re-upsert of the same railcontent_id must not create a second row.
func countLessons(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.rawDB().QueryRow("SELECT count(*) FROM lessons").Scan(&n); err != nil {
		t.Fatalf("count lessons: %v", err)
	}
	return n
}

func TestUpsertLessonInsertsNewRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	parent := sql.NullInt64{Int64: 100, Valid: true}
	if err := s.UpsertLesson(ctx, 409875, "Lesson One", parent, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
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

// TestUpsertLessonStoresFollowID asserts a new lesson row records the follow_id
// it was discovered under, round-tripping through GetLesson as a valid NullInt64.
func TestUpsertLessonStoresFollowID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	followID := seedFollowForLesson(t, s)
	follow := sql.NullInt64{Int64: followID, Valid: true}
	if err := s.UpsertLesson(ctx, 409875, "Lesson One", sql.NullInt64{}, "drumeo", sql.NullInt64{}, follow); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if !got.FollowID.Valid || got.FollowID.Int64 != followID {
		t.Errorf("FollowID = %+v, want valid %d", got.FollowID, followID)
	}
}

// TestUpsertLessonNullFollowID asserts an upsert with an invalid NullInt64 leaves
// follow_id NULL (e.g. an instructor follow whose parent linkage is unknown, or a
// manually inserted lesson).
func TestUpsertLessonNullFollowID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 409875, "Lesson One", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if got.FollowID.Valid {
		t.Errorf("FollowID = %+v, want NULL", got.FollowID)
	}
}

// TestUpsertLessonPreservesFollowID is the first-follow-wins invariant: a lesson
// discovered under one follow keeps that follow_id even when a later sync
// re-upserts it under a different follow. (Title is still refreshed; parent is
// the attributed follow's to write: TestUpsertLessonStampsUpdatedAtOnlyOnAChange.)
func TestUpsertLessonPreservesFollowID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first := seedFollowForLesson(t, s)
	second := seedFollowForLesson(t, s)

	if err := s.UpsertLesson(ctx, 409875, "Lesson", sql.NullInt64{}, "drumeo",
		sql.NullInt64{}, sql.NullInt64{Int64: first, Valid: true}); err != nil {
		t.Fatalf("first UpsertLesson: %v", err)
	}
	// A later sync re-discovers the same lesson under a different follow.
	if err := s.UpsertLesson(ctx, 409875, "Lesson (renamed)", sql.NullInt64{}, "drumeo",
		sql.NullInt64{}, sql.NullInt64{Int64: second, Valid: true}); err != nil {
		t.Fatalf("second UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if !got.FollowID.Valid || got.FollowID.Int64 != first {
		t.Errorf("FollowID = %+v, want preserved original %d (first-follow-wins)", got.FollowID, first)
	}
	if got.Title != "Lesson (renamed)" {
		t.Errorf("Title = %q, want refreshed %q", got.Title, "Lesson (renamed)")
	}
}

// TestUpsertLessonStoresPosition asserts a new lesson row records its position
// (the "NN - " folder prefix sequence), round-tripping through GetLesson as a
// valid NullInt64.
func TestUpsertLessonStoresPosition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pos := sql.NullInt64{Int64: 7, Valid: true}
	if err := s.UpsertLesson(ctx, 409875, "Lesson One", sql.NullInt64{}, "drumeo", pos, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if !got.Position.Valid || got.Position.Int64 != 7 {
		t.Errorf("Position = %+v, want valid 7", got.Position)
	}
}

// TestUpsertLessonNullPosition asserts an upsert with an invalid NullInt64 leaves
// the position NULL (an unnumbered lesson).
func TestUpsertLessonNullPosition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 409875, "Lesson One", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if got.Position.Valid {
		t.Errorf("Position = %+v, want NULL on a position-less upsert", got.Position)
	}
}

// TestUpsertLessonPreservesPosition is the first-write-wins invariant for
// position: a lesson shared by two follows keeps the first follow's number even
// when a later upsert supplies a different one. (Title is still refreshed.)
func TestUpsertLessonPreservesPosition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 409875, "Lesson", sql.NullInt64{}, "drumeo",
		sql.NullInt64{Int64: 3, Valid: true}, sql.NullInt64{}); err != nil {
		t.Fatalf("first UpsertLesson: %v", err)
	}
	// A later sync re-discovers the same lesson with a different position.
	if err := s.UpsertLesson(ctx, 409875, "Lesson (renamed)", sql.NullInt64{}, "drumeo",
		sql.NullInt64{Int64: 99, Valid: true}, sql.NullInt64{}); err != nil {
		t.Fatalf("second UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if !got.Position.Valid || got.Position.Int64 != 3 {
		t.Errorf("Position = %+v, want preserved original 3 (first-write-wins)", got.Position)
	}
	if got.Title != "Lesson (renamed)" {
		t.Errorf("Title = %q, want refreshed %q", got.Title, "Lesson (renamed)")
	}
}

// TestUpsertLessonFillsNullPosition asserts COALESCE first-write-wins also means a
// row first upserted without a position is later filled in by a numbered upsert
// (NULL is not a "first write" to preserve).
func TestUpsertLessonFillsNullPosition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 409875, "Lesson", sql.NullInt64{}, "drumeo",
		sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("first UpsertLesson: %v", err)
	}
	if err := s.UpsertLesson(ctx, 409875, "Lesson", sql.NullInt64{}, "drumeo",
		sql.NullInt64{Int64: 5, Valid: true}, sql.NullInt64{}); err != nil {
		t.Fatalf("second UpsertLesson: %v", err)
	}

	got, err := s.GetLesson(ctx, 409875)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if !got.Position.Valid || got.Position.Int64 != 5 {
		t.Errorf("Position = %+v, want filled-in 5 (COALESCE over prior NULL)", got.Position)
	}
}

// TestUpsertLessonStampsUpdatedAtOnlyOnAChange pins owner ruling 2026-09-24
// (r): a sync stamps a lesson's updated_at only when one of the fields the
// upsert stores changes (title, parent, a position filled in), never when it
// finds the lesson as it was, or differs only in what a conflict leaves alone
// (brand, follow, a position already set). Parent is the attributed follow's
// to write (code round 5f-5g L1): a sync from another follow that lists the
// lesson leaves it, and stamps nothing for it, as does one after the lesson's
// follow was removed; a change from the attributed follow is written and
// stamped. The stamp is set to a fixed past time first, so CURRENT_TIMESTAMP's
// one-second grain can't hide a write.
func TestUpsertLessonStampsUpdatedAtOnlyOnAChange(t *testing.T) {
	const past = "2026-01-01 00:00:00"
	num := func(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
	type upsert struct {
		title      string
		parent     sql.NullInt64
		brand      string
		position   sql.NullInt64
		wantTitle  string
		wantPos    sql.NullInt64
		wantParent sql.NullInt64
		stamped    bool
	}
	// Who the sync comes from, against the follow the lesson was stored with.
	const (
		noFollow = ""        // no follow, before and now
		same     = "same"    // the follow the lesson is attributed to
		other    = "other"   // another follow that lists it too
		removed  = "removed" // another follow, after the lesson's own was removed
	)
	for _, c := range []struct {
		name     string
		position sql.NullInt64 // the stored position before the sync
		from     string
		sync     upsert
	}{
		{"nothing changed", num(5), noFollow, upsert{title: "Lesson", parent: num(7), brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), wantParent: num(7)}},
		{"only a brand, follow or position a conflict leaves alone", num(5), other, upsert{title: "Lesson", parent: num(7), brand: "pianote", position: num(9), wantTitle: "Lesson", wantPos: num(5), wantParent: num(7)}},
		{"the title changed", num(5), noFollow, upsert{title: "New Title", parent: num(7), brand: "drumeo", position: num(5), wantTitle: "New Title", wantPos: num(5), wantParent: num(7), stamped: true}},
		{"the parent changed", num(5), noFollow, upsert{title: "Lesson", parent: num(8), brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), wantParent: num(8), stamped: true}},
		{"the parent cleared", num(5), noFollow, upsert{title: "Lesson", brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), stamped: true}},
		{"a missing position filled in", sql.NullInt64{}, noFollow, upsert{title: "Lesson", parent: num(7), brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), wantParent: num(7), stamped: true}},
		{"the attributed follow's parent changed", num(5), same, upsert{title: "Lesson", parent: num(8), brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), wantParent: num(8), stamped: true}},
		{"the attributed follow's parent cleared", num(5), same, upsert{title: "Lesson", brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), stamped: true}},
		{"another follow's parent", num(5), other, upsert{title: "Lesson", parent: num(8), brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), wantParent: num(7)}},
		{"another follow's NULL parent (an instructor)", num(5), other, upsert{title: "Lesson", brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), wantParent: num(7)}},
		{"another follow's title change", num(5), other, upsert{title: "New Title", parent: num(8), brand: "drumeo", position: num(5), wantTitle: "New Title", wantPos: num(5), wantParent: num(7), stamped: true}},
		{"another follow's parent, the lesson's follow removed", num(5), removed, upsert{title: "Lesson", parent: num(8), brand: "drumeo", position: num(5), wantTitle: "Lesson", wantPos: num(5), wantParent: num(7)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			flw, syncFlw := sql.NullInt64{}, sql.NullInt64{}
			if c.from != noFollow {
				flw = num(seedFollowForLesson(t, s))
				syncFlw = flw
				if c.from != same {
					syncFlw = num(seedFollowForLesson(t, s))
				}
			}
			if err := s.UpsertLesson(ctx, 1, "Lesson", num(7), "drumeo", c.position, flw); err != nil {
				t.Fatalf("first UpsertLesson: %v", err)
			}
			if c.from == removed {
				if _, err := s.rawDB().Exec(`DELETE FROM follows WHERE id = ?`, flw.Int64); err != nil {
					t.Fatalf("remove the follow: %v", err)
				}
				flw = sql.NullInt64{} // ON DELETE SET NULL
			}
			if _, err := s.rawDB().Exec(`UPDATE lessons SET updated_at = ? WHERE railcontent_id = 1`, past); err != nil {
				t.Fatalf("set updated_at: %v", err)
			}
			if err := s.UpsertLesson(ctx, 1, c.sync.title, c.sync.parent, c.sync.brand, c.sync.position, syncFlw); err != nil {
				t.Fatalf("second UpsertLesson: %v", err)
			}
			got, err := s.GetLesson(ctx, 1)
			if err != nil {
				t.Fatalf("GetLesson: %v", err)
			}
			if got.Title != c.sync.wantTitle || got.Position != c.sync.wantPos || got.ParentRailcontentID != c.sync.wantParent || got.FollowID != flw {
				t.Errorf("stored title %q, position %+v, parent %+v, follow %+v; want %q, %+v, %+v, %+v",
					got.Title, got.Position, got.ParentRailcontentID, got.FollowID, c.sync.wantTitle, c.sync.wantPos, c.sync.wantParent, flw)
			}
			stamped := !got.UpdatedAt.Valid || got.UpdatedAt.Time.UTC().Format("2006-01-02 15:04:05") != past
			if stamped != c.sync.stamped {
				t.Errorf("updated_at = %+v after the sync; want it stamped: %v", got.UpdatedAt, c.sync.stamped)
			}
		})
	}
}

// seedFollowForLesson inserts a node follow and returns its id, so lesson rows can
// satisfy the follow_id foreign key.
func seedFollowForLesson(t *testing.T, s *Store) int64 {
	t.Helper()
	res, err := s.rawDB().Exec(
		`INSERT INTO follows(kind, railcontent_id, brand, quality) VALUES('node', NULL, 'drumeo', 'best')`,
	)
	if err != nil {
		t.Fatalf("seed follow: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed follow last id: %v", err)
	}
	return id
}

func TestUpsertLessonDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 409875, "Original Title", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("first UpsertLesson: %v", err)
	}
	// Re-upserting the same railcontent_id must update in place, not duplicate.
	if err := s.UpsertLesson(ctx, 409875, "Updated Title",
		sql.NullInt64{Int64: 200, Valid: true}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
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

	if err := s.UpsertLesson(ctx, 409875, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	recordDownloaded(t, s, 409875, DownloadRecord{Quality: "best", OutputDir: "/out/dir", VideoPath: "/out/dir/video.mp4", Bytes: 12345})

	// A re-sync upserts the same lesson again.
	if err := s.UpsertLesson(ctx, 409875, "Lesson (renamed)", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
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

	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}

	// Pending lesson is not downloaded.
	if ok, err := s.IsDownloaded(ctx, 1); err != nil {
		t.Fatalf("IsDownloaded: %v", err)
	} else if ok {
		t.Error("IsDownloaded = true for a pending lesson, want false")
	}

	recordDownloaded(t, s, 1, DownloadRecord{Quality: "best", OutputDir: "/d", VideoPath: "/d/v.mp4"})
	if ok, err := s.IsDownloaded(ctx, 1); err != nil {
		t.Fatalf("IsDownloaded: %v", err)
	} else if !ok {
		t.Error("IsDownloaded = false after FinishDownload, want true")
	}

	// An id that was never seen is, by definition, not downloaded (no error).
	if ok, err := s.IsDownloaded(ctx, 99999); err != nil {
		t.Fatalf("IsDownloaded for unknown id: %v", err)
	} else if ok {
		t.Error("IsDownloaded = true for an unknown id, want false")
	}
}

func TestUnskipLesson(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	if _, err := s.SkipLesson(ctx, 1, "locked content"); err != nil {
		t.Fatalf("SkipLesson: %v", err)
	}
	if err := s.UnskipLesson(ctx, 1); err != nil {
		t.Fatalf("UnskipLesson: %v", err)
	}
	got, err := s.GetLesson(ctx, 1)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("status = %q, want %q", got.Status, StatusPending)
	}
	if got.Error.Valid {
		t.Errorf("Error = %+v, want NULL after un-skip", got.Error)
	}
}

// TestUnskipLessonOnlySkipped asserts the guard: un-skipping a lesson that is
// not skipped (or an unknown id) is a benign no-op (0 rows -> nil, no mutation).
func TestUnskipLessonOnlySkipped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	// A pending lesson is not skipped: un-skip is a no-op, not an error.
	if err := s.UnskipLesson(ctx, 1); err != nil {
		t.Fatalf("UnskipLesson on a pending lesson: %v", err)
	}
	got, err := s.GetLesson(ctx, 1)
	if err != nil {
		t.Fatalf("GetLesson: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("status = %q, want %q (unchanged)", got.Status, StatusPending)
	}
	// An unknown id is also a benign no-op.
	if err := s.UnskipLesson(ctx, 404); err != nil {
		t.Errorf("UnskipLesson on a missing id returned %v, want nil (benign no-op)", err)
	}
}

// TestUnskipLessonRefusesWhileADeleteHoldsIt proves Un-skip refuses, as Skip
// does, while a delete holds the lesson: ErrLessonDeleting, and the lesson
// stays skipped (security round 5d I5). Once the delete ends it un-skips.
func TestUnskipLessonRefusesWhileADeleteHoldsIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	if _, err := s.SkipLesson(ctx, 1, "not now"); err != nil {
		t.Fatalf("SkipLesson: %v", err)
	}
	if _, _, err := s.BeginLessonDelete(ctx, 1); err != nil {
		t.Fatalf("BeginLessonDelete: %v", err)
	}
	if err := s.UnskipLesson(ctx, 1); !errors.Is(err, ErrLessonDeleting) {
		t.Errorf("UnskipLesson while deleting = %v, want ErrLessonDeleting", err)
	}
	if got := mustLesson(t, s, 1); got.Status != StatusSkipped || got.Error.String != "not now" {
		t.Errorf("lesson = %s %q, want it still skipped %q", got.Status, got.Error.String, "not now")
	}
	if err := s.EndLessonDelete(ctx, 1); err != nil {
		t.Fatalf("EndLessonDelete: %v", err)
	}
	if err := s.UnskipLesson(ctx, 1); err != nil {
		t.Fatalf("UnskipLesson after the delete: %v", err)
	}
	if got := mustLesson(t, s, 1); got.Status != StatusPending {
		t.Errorf("status = %q after the delete ended, want %q", got.Status, StatusPending)
	}
}

// TestUpsertLessonRejectsBadStatus guards the lessons.status CHECK at the store
// boundary: a raw insert with an out-of-set status must be rejected so the
// constraint is real, not merely declared.
func TestUpsertLessonRejectsBadStatus(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.rawDB().Exec(
		"INSERT INTO lessons(railcontent_id, status) VALUES(1, 'bogus')",
	); err == nil {
		t.Error("inserting a lesson with status='bogus' succeeded, want CHECK violation")
	}
	// The valid status set inserts cleanly (guards against an over-broad CHECK).
	for _, st := range []string{
		StatusPending, StatusDownloading, StatusDownloaded, StatusFailed, StatusSkipped,
	} {
		if _, err := s.rawDB().Exec(
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
		if err := s.UpsertLesson(ctx, id, title, sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
			t.Fatalf("UpsertLesson %d: %v", id, err)
		}
	}
	recordDownloaded(t, s, 1, DownloadRecord{Quality: "best", OutputDir: "/d", VideoPath: "/d/v.mp4"})
	recordDownloaded(t, s, 2, DownloadRecord{Quality: "best", OutputDir: "/d", VideoPath: "/d/v.mp4"})
	recordFailed(t, s, 3, "boom")
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

// TestListLessonsByFollow asserts the method returns exactly the lessons
// attributed to the given follow, ordered by railcontent_id, ignoring lessons
// belonging to other follows or to no follow at all.
func TestListLessonsByFollow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first := seedFollowForLesson(t, s)
	second := seedFollowForLesson(t, s)

	// Two lessons under the first follow (inserted out of id order to prove the
	// ORDER BY), one under the second, and one with no follow at all.
	if err := s.UpsertLesson(ctx, 30, "c", sql.NullInt64{}, "drumeo",
		sql.NullInt64{}, sql.NullInt64{Int64: first, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson 30: %v", err)
	}
	if err := s.UpsertLesson(ctx, 10, "a", sql.NullInt64{}, "drumeo",
		sql.NullInt64{}, sql.NullInt64{Int64: first, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson 10: %v", err)
	}
	if err := s.UpsertLesson(ctx, 20, "b", sql.NullInt64{}, "drumeo",
		sql.NullInt64{}, sql.NullInt64{Int64: second, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson 20: %v", err)
	}
	if err := s.UpsertLesson(ctx, 40, "d", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson 40: %v", err)
	}

	got, err := s.ListLessonsByFollow(ctx, first)
	if err != nil {
		t.Fatalf("ListLessonsByFollow: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListLessonsByFollow(first) returned %d rows, want 2", len(got))
	}
	if got[0].RailcontentID != 10 || got[1].RailcontentID != 30 {
		t.Errorf("ListLessonsByFollow order = [%d, %d], want [10, 30] by railcontent_id",
			got[0].RailcontentID, got[1].RailcontentID)
	}
	for _, l := range got {
		if !l.FollowID.Valid || l.FollowID.Int64 != first {
			t.Errorf("returned lesson %d has FollowID %+v, want %d", l.RailcontentID, l.FollowID, first)
		}
	}
}

// TestListLessonsByFollowEmpty asserts a follow with no lessons returns an empty
// slice and no error.
func TestListLessonsByFollowEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	follow := seedFollowForLesson(t, s)
	got, err := s.ListLessonsByFollow(ctx, follow)
	if err != nil {
		t.Fatalf("ListLessonsByFollow: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListLessonsByFollow on a follow with no lessons returned %d rows, want 0", len(got))
	}
}

// TestListLessons asserts the paged listing orders by updated_at DESC then
// railcontent_id, and that limit/offset page through the result.
func TestListLessons(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert three lessons, then set distinct updated_at values directly (the
	// Mark* helpers use CURRENT_TIMESTAMP, which has second granularity and would
	// tie across rapid calls). Lesson 2 is newest, then 3, then 1.
	for _, id := range []int{1, 2, 3} {
		if err := s.UpsertLesson(ctx, id, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
			t.Fatalf("UpsertLesson %d: %v", id, err)
		}
	}
	for id, ts := range map[int]string{
		1: "2026-01-01 00:00:00",
		3: "2026-01-02 00:00:00",
		2: "2026-01-03 00:00:00",
	} {
		if _, err := s.rawDB().Exec(
			"UPDATE lessons SET updated_at = ? WHERE railcontent_id = ?", ts, id,
		); err != nil {
			t.Fatalf("set updated_at for %d: %v", id, err)
		}
	}

	got, err := s.ListLessons(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListLessons: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListLessons returned %d rows, want 3", len(got))
	}
	// updated_at DESC: 2 (newest), 3, 1 (oldest).
	if got[0].RailcontentID != 2 || got[1].RailcontentID != 3 || got[2].RailcontentID != 1 {
		t.Errorf("ListLessons order = [%d, %d, %d], want [2, 3, 1] by updated_at DESC",
			got[0].RailcontentID, got[1].RailcontentID, got[2].RailcontentID)
	}

	// Paging: limit 2 returns the first page; offset 2 returns the remainder.
	page1, err := s.ListLessons(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListLessons page1: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("ListLessons(limit=2) returned %d rows, want 2", len(page1))
	}
	page2, err := s.ListLessons(ctx, 2, 2)
	if err != nil {
		t.Fatalf("ListLessons page2: %v", err)
	}
	if len(page2) != 1 {
		t.Errorf("ListLessons(limit=2, offset=2) returned %d rows, want 1", len(page2))
	}
}

// TestListLessonsDefaultLimit asserts a non-positive limit falls back to a sane
// default rather than returning zero rows.
func TestListLessonsDefaultLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertLesson(ctx, 1, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	got, err := s.ListLessons(ctx, -5, 0)
	if err != nil {
		t.Fatalf("ListLessons: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ListLessons(limit=-5) returned %d rows, want 1 (default limit applied)", len(got))
	}
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

// TestCountLessonsByStatus asserts the helper groups lessons by status and
// returns one entry per present status with the correct count, and omits
// statuses that have no rows (the handler fills zeros for the known enum set).
func TestCountLessonsByStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Two downloaded, one failed, one left pending. No downloading/skipped rows.
	for _, id := range []int{1, 2, 3, 4} {
		if err := s.UpsertLesson(ctx, id, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
			t.Fatalf("UpsertLesson %d: %v", id, err)
		}
	}
	recordDownloaded(t, s, 1, DownloadRecord{Quality: "best", OutputDir: "/d", VideoPath: "/d/v.mp4", Bytes: 1})
	recordDownloaded(t, s, 2, DownloadRecord{Quality: "best", OutputDir: "/d", VideoPath: "/d/v.mp4", Bytes: 1})
	recordFailed(t, s, 3, "boom")

	counts, err := s.CountLessonsByStatus(ctx)
	if err != nil {
		t.Fatalf("CountLessonsByStatus: %v", err)
	}

	want := map[string]int{
		StatusDownloaded: 2,
		StatusFailed:     1,
		StatusPending:    1,
	}
	if len(counts) != len(want) {
		t.Fatalf("CountLessonsByStatus returned %d statuses (%v), want %d", len(counts), counts, len(want))
	}
	for status, n := range want {
		if counts[status] != n {
			t.Errorf("count[%q] = %d, want %d", status, counts[status], n)
		}
	}
	if _, ok := counts[StatusDownloading]; ok {
		t.Errorf("count includes %q with no rows, want it omitted", StatusDownloading)
	}
}

// TestCountLessonsByStatusEmpty asserts an empty lessons table yields an empty
// (non-nil) map and no error.
func TestCountLessonsByStatusEmpty(t *testing.T) {
	s := newTestStore(t)

	counts, err := s.CountLessonsByStatus(context.Background())
	if err != nil {
		t.Fatalf("CountLessonsByStatus: %v", err)
	}
	if counts == nil {
		t.Fatal("CountLessonsByStatus returned nil map, want empty non-nil map")
	}
	if len(counts) != 0 {
		t.Errorf("CountLessonsByStatus on empty table returned %v, want empty", counts)
	}
}
