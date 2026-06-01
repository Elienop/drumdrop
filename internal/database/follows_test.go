package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestAddNodeFollow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	f, err := s.AddNodeFollow(ctx, 409875, "Beginner Course", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}

	if f.ID == 0 {
		t.Error("AddNodeFollow returned a zero ID")
	}
	if f.Kind != "node" {
		t.Errorf("Kind = %q, want %q", f.Kind, "node")
	}
	if !f.RailcontentID.Valid || f.RailcontentID.Int64 != 409875 {
		t.Errorf("RailcontentID = %+v, want valid 409875", f.RailcontentID)
	}
	if f.Slug.Valid {
		t.Errorf("Slug = %+v, want NULL for a node follow", f.Slug)
	}
	if f.Title != "Beginner Course" {
		t.Errorf("Title = %q, want %q", f.Title, "Beginner Course")
	}
	if f.Brand != "drumeo" {
		t.Errorf("Brand = %q, want %q", f.Brand, "drumeo")
	}
	if f.Quality != "best" {
		t.Errorf("Quality = %q, want %q", f.Quality, "best")
	}
	if f.AddedAt.IsZero() {
		t.Error("AddedAt is zero, want a populated CURRENT_TIMESTAMP")
	}
	if f.LastSyncedAt.Valid {
		t.Errorf("LastSyncedAt = %+v, want NULL on a fresh follow", f.LastSyncedAt)
	}
}

func TestAddInstructorFollow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	f, err := s.AddInstructorFollow(ctx, "aaron-edgar", "Aaron Edgar", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddInstructorFollow: %v", err)
	}

	if f.ID == 0 {
		t.Error("AddInstructorFollow returned a zero ID")
	}
	if f.Kind != "instructor" {
		t.Errorf("Kind = %q, want %q", f.Kind, "instructor")
	}
	if !f.Slug.Valid || f.Slug.String != "aaron-edgar" {
		t.Errorf("Slug = %+v, want valid %q", f.Slug, "aaron-edgar")
	}
	if f.RailcontentID.Valid {
		t.Errorf("RailcontentID = %+v, want NULL for an instructor follow", f.RailcontentID)
	}
	if f.Title != "Aaron Edgar" {
		t.Errorf("Title = %q, want %q", f.Title, "Aaron Edgar")
	}
}

func TestAddNodeFollowDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.AddNodeFollow(ctx, 409875, "Beginner Course", "drumeo", "best")
	if err != nil {
		t.Fatalf("first AddNodeFollow: %v", err)
	}

	// Re-adding the same railcontent_id must be idempotent: it returns the
	// existing row alongside ErrAlreadyFollowing, and creates no new row.
	second, err := s.AddNodeFollow(ctx, 409875, "Different Title", "drumeo", "best")
	if !errors.Is(err, ErrAlreadyFollowing) {
		t.Fatalf("second AddNodeFollow err = %v, want ErrAlreadyFollowing", err)
	}
	if second.ID != first.ID {
		t.Errorf("dedup returned ID %d, want the existing %d", second.ID, first.ID)
	}
	// The original title must be preserved (the conflicting insert is ignored).
	if second.Title != "Beginner Course" {
		t.Errorf("dedup Title = %q, want the original %q", second.Title, "Beginner Course")
	}

	follows, err := s.ListFollows(ctx)
	if err != nil {
		t.Fatalf("ListFollows: %v", err)
	}
	if len(follows) != 1 {
		t.Errorf("after re-adding the same node, follows has %d rows, want 1", len(follows))
	}
}

func TestAddInstructorFollowDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.AddInstructorFollow(ctx, "aaron-edgar", "Aaron Edgar", "drumeo", "best")
	if err != nil {
		t.Fatalf("first AddInstructorFollow: %v", err)
	}

	second, err := s.AddInstructorFollow(ctx, "aaron-edgar", "Renamed", "drumeo", "best")
	if !errors.Is(err, ErrAlreadyFollowing) {
		t.Fatalf("second AddInstructorFollow err = %v, want ErrAlreadyFollowing", err)
	}
	if second.ID != first.ID {
		t.Errorf("dedup returned ID %d, want the existing %d", second.ID, first.ID)
	}
	if second.Title != "Aaron Edgar" {
		t.Errorf("dedup Title = %q, want the original %q", second.Title, "Aaron Edgar")
	}

	follows, err := s.ListFollows(ctx)
	if err != nil {
		t.Fatalf("ListFollows: %v", err)
	}
	if len(follows) != 1 {
		t.Errorf("after re-adding the same instructor, follows has %d rows, want 1", len(follows))
	}
}

func TestListFollowsOrdering(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert with explicit, increasing added_at so ordering is deterministic
	// regardless of CURRENT_TIMESTAMP's one-second resolution. Direct inserts
	// via the handle let the test control added_at precisely.
	mustExec(t, s, "INSERT INTO follows(kind, railcontent_id, title, added_at) VALUES('node', 1, 'first', '2026-01-01 00:00:00')")
	mustExec(t, s, "INSERT INTO follows(kind, railcontent_id, title, added_at) VALUES('node', 2, 'second', '2026-02-01 00:00:00')")
	mustExec(t, s, "INSERT INTO follows(kind, slug, title, added_at) VALUES('instructor', 'x', 'third', '2026-03-01 00:00:00')")

	follows, err := s.ListFollows(ctx)
	if err != nil {
		t.Fatalf("ListFollows: %v", err)
	}
	if len(follows) != 3 {
		t.Fatalf("ListFollows returned %d rows, want 3", len(follows))
	}
	wantOrder := []string{"first", "second", "third"}
	for i, want := range wantOrder {
		if follows[i].Title != want {
			t.Errorf("follows[%d].Title = %q, want %q (ordered by added_at)", i, follows[i].Title, want)
		}
	}
}

func TestGetFollow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	added, err := s.AddNodeFollow(ctx, 555, "Title", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}

	got, err := s.GetFollow(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetFollow: %v", err)
	}
	if got.ID != added.ID || got.Title != "Title" {
		t.Errorf("GetFollow = %+v, want ID %d Title %q", got, added.ID, "Title")
	}

	// A missing id must surface as an error, not a zero-value success.
	if _, err := s.GetFollow(ctx, 99999); err == nil {
		t.Error("GetFollow for a missing id returned nil error, want error")
	}
}

func TestRemoveFollow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	added, err := s.AddNodeFollow(ctx, 777, "Title", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}

	if err := s.RemoveFollow(ctx, added.ID); err != nil {
		t.Fatalf("RemoveFollow: %v", err)
	}

	follows, err := s.ListFollows(ctx)
	if err != nil {
		t.Fatalf("ListFollows: %v", err)
	}
	if len(follows) != 0 {
		t.Errorf("after RemoveFollow, follows has %d rows, want 0", len(follows))
	}

	// Removing a non-existent id must report an error so the CLI can tell the
	// user nothing matched.
	if err := s.RemoveFollow(ctx, added.ID); err == nil {
		t.Error("RemoveFollow on an already-removed id returned nil error, want error")
	}
}

func TestTouchLastSynced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	added, err := s.AddNodeFollow(ctx, 888, "Title", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	if added.LastSyncedAt.Valid {
		t.Fatalf("fresh follow already has last_synced_at set: %+v", added.LastSyncedAt)
	}

	if err := s.TouchLastSynced(ctx, added.ID); err != nil {
		t.Fatalf("TouchLastSynced: %v", err)
	}

	got, err := s.GetFollow(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetFollow: %v", err)
	}
	if !got.LastSyncedAt.Valid {
		t.Error("after TouchLastSynced, last_synced_at is still NULL")
	}
}

// TestUpdateFollowQuality asserts the quality is changed in place while the
// follow's identity (kind/railcontent_id/slug/brand) is untouched.
func TestUpdateFollowQuality(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	added, err := s.AddNodeFollow(ctx, 12345, "Title", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}

	if err := s.UpdateFollowQuality(ctx, added.ID, "1080"); err != nil {
		t.Fatalf("UpdateFollowQuality: %v", err)
	}

	got, err := s.GetFollow(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetFollow: %v", err)
	}
	if got.Quality != "1080" {
		t.Errorf("Quality = %q, want %q", got.Quality, "1080")
	}
	// Identity must be untouched.
	if got.Kind != "node" || !got.RailcontentID.Valid || got.RailcontentID.Int64 != 12345 || got.Brand != "drumeo" {
		t.Errorf("identity changed: kind=%q railcontent_id=%v brand=%q", got.Kind, got.RailcontentID, got.Brand)
	}
}

// TestUpdateFollowQualityNotFound asserts an unknown id is reported as an error
// so the API handler can map it to a 404.
func TestUpdateFollowQualityNotFound(t *testing.T) {
	s := newTestStore(t)

	if err := s.UpdateFollowQuality(context.Background(), 999999, "720"); err == nil {
		t.Error("UpdateFollowQuality on an unknown id returned nil, want error")
	}
}

// TestRemoveFollowCascade asserts the follow plus its jobs and lessons are all
// gone, while another follow's records are left untouched.
func TestRemoveFollowCascade(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	target, err := s.AddNodeFollow(ctx, 100, "Target", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow target: %v", err)
	}
	other, err := s.AddNodeFollow(ctx, 200, "Other", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow other: %v", err)
	}

	// Seed a lesson + job under each follow.
	if err := s.UpsertLesson(ctx, 1001, "L1", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: target.ID, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson target: %v", err)
	}
	if err := s.UpsertLesson(ctx, 2001, "L2", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: other.ID, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson other: %v", err)
	}
	targetJob, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: target.ID, Valid: true}, 1001)
	if err != nil {
		t.Fatalf("EnqueueJob target: %v", err)
	}
	otherJob, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: other.ID, Valid: true}, 2001)
	if err != nil {
		t.Fatalf("EnqueueJob other: %v", err)
	}

	if err := s.RemoveFollowCascade(ctx, target.ID); err != nil {
		t.Fatalf("RemoveFollowCascade: %v", err)
	}

	// Target follow gone.
	if _, err := s.GetFollow(ctx, target.ID); err == nil {
		t.Error("target follow still present after cascade")
	}
	// Target lesson gone.
	if _, err := s.GetLesson(ctx, 1001); err == nil {
		t.Error("target lesson still present after cascade")
	}
	// Target job gone.
	if _, err := s.GetJob(ctx, targetJob); err == nil {
		t.Error("target job still present after cascade")
	}

	// Other follow + its records untouched.
	if _, err := s.GetFollow(ctx, other.ID); err != nil {
		t.Errorf("other follow gone after cascade: %v", err)
	}
	if _, err := s.GetLesson(ctx, 2001); err != nil {
		t.Errorf("other lesson gone after cascade: %v", err)
	}
	if _, err := s.GetJob(ctx, otherJob); err != nil {
		t.Errorf("other job gone after cascade: %v", err)
	}
}

// TestRemoveFollowCascadeNotFound asserts an unknown id is reported as an error
// so the API handler can map it to a 404.
func TestRemoveFollowCascadeNotFound(t *testing.T) {
	s := newTestStore(t)

	if err := s.RemoveFollowCascade(context.Background(), 999999); err == nil {
		t.Error("RemoveFollowCascade on an unknown id returned nil, want error")
	}
}

// mustExec runs a raw statement against the store's handle for test setup,
// failing the test on error. It is only for arranging rows the public API can
// then read back; mutations under test still go through Store methods.
func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.rawDB().Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
