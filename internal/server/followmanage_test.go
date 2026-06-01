package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// TestUpdateFollowQualityHandler asserts PATCH with a valid quality returns 200
// with the updated row and leaves identity untouched.
func TestUpdateFollowQualityHandler(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	added, err := store.AddNodeFollow(ctx, 12345, "Title", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	body := `{"quality":"1080"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/follows/"+strconv.FormatInt(added.ID, 10), strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got FollowDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Quality != "1080" {
		t.Errorf("Quality = %q, want 1080", got.Quality)
	}
	if got.Kind != "node" || got.RailcontentID == nil || *got.RailcontentID != 12345 {
		t.Errorf("identity changed: kind=%q railcontent_id=%v", got.Kind, got.RailcontentID)
	}
}

// TestUpdateFollowInvalidQuality asserts an out-of-set quality is a 400 and the
// stored quality is left unchanged.
func TestUpdateFollowInvalidQuality(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	added, err := store.AddNodeFollow(ctx, 222, "Title", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPatch, "/api/follows/"+strconv.FormatInt(added.ID, 10), strings.NewReader(`{"quality":"1080p"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	f, err := store.GetFollow(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetFollow: %v", err)
	}
	if f.Quality != "best" {
		t.Errorf("Quality = %q, want best (invalid PATCH must not persist)", f.Quality)
	}
}

// TestUpdateFollowNotFound asserts PATCH on an unknown id is a 404.
func TestUpdateFollowNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPatch, "/api/follows/999999", strings.NewReader(`{"quality":"720"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// seedDownloadedLesson upserts a lesson attributed to followID and marks it
// downloaded with real on-disk files at its single stored location: the library
// path (rel under library) when library is non-empty — mirroring the worker's
// move — otherwise the downloads path. It returns that stored output dir.
func seedDownloadedLesson(t *testing.T, store *database.Store, downloads, library string, rcID int, followID int64, rel string) string {
	t.Helper()
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, rcID, "L", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: followID, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	// The single stored location: library when configured (post-move), else downloads.
	root := downloads
	if library != "" {
		root = library
	}
	outDir := filepath.Join(root, rel)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("MkdirAll %q: %v", outDir, err)
	}
	video := filepath.Join(outDir, "v.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile %q: %v", video, err)
	}
	if err := store.MarkDownloaded(ctx, rcID, "1080", outDir, video, 5); err != nil {
		t.Fatalf("MarkDownloaded: %v", err)
	}
	return outDir
}

// TestDeleteFollowFilesTrue asserts ?files=true removes both file copies, the
// follow/lessons/jobs records are gone, and a running job is canceled via the
// worker.
func TestDeleteFollowFilesTrue(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads := t.TempDir()
	library := t.TempDir()

	follow, err := store.AddNodeFollow(ctx, 100, "F", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	outDir := seedDownloadedLesson(t, store, downloads, library, 1001, follow.ID, "01 - Lesson")

	// A running job for this follow, so the cancel path is exercised.
	jobID, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: follow.ID, Valid: true}, 1001)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if err := store.MarkJobRunning(ctx, jobID); err != nil {
		t.Fatalf("MarkJobRunning: %v", err)
	}

	// A SECOND follow with its own running job. Deleting `follow` must NOT cancel
	// this one — it pins the cancel loop's follow-id scoping (weakening the
	// `j.FollowID.Int64 == id` guard to cancel every running job fails here).
	other, err := store.AddNodeFollow(ctx, 200, "Other", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow other: %v", err)
	}
	if err := store.UpsertLesson(ctx, 2002, "L2", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{Int64: other.ID, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson other: %v", err)
	}
	otherJobID, _, err := store.EnqueueJob(ctx, sql.NullInt64{Int64: other.ID, Valid: true}, 2002)
	if err != nil {
		t.Fatalf("EnqueueJob other: %v", err)
	}
	if err := store.MarkJobRunning(ctx, otherJobID); err != nil {
		t.Fatalf("MarkJobRunning other: %v", err)
	}

	var canceled []int64
	deps := Deps{CancelRunning: func(id int64) bool { canceled = append(canceled, id); return true }}
	srv := NewServer(store, deps, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	req := httptest.NewRequest(http.MethodDelete, "/api/follows/"+strconv.FormatInt(follow.ID, 10)+"?files=true", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	// Only THIS follow's running job is canceled; the other follow's is untouched.
	if len(canceled) != 1 || canceled[0] != jobID {
		t.Errorf("CancelRunning calls = %v, want exactly [%d] (other follow's job %d must not be canceled)", canceled, jobID, otherJobID)
	}
	// The other follow's running job survives the cascade scoped to `follow`.
	if _, err := store.GetJob(ctx, otherJobID); err != nil {
		t.Errorf("other follow's job removed by scoped cascade: %v", err)
	}
	// Records gone.
	if _, err := store.GetFollow(ctx, follow.ID); err == nil {
		t.Error("follow still present after delete")
	}
	if _, err := store.GetLesson(ctx, 1001); err == nil {
		t.Error("lesson still present after cascade")
	}
	// The single stored location (the library path) is gone.
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("lesson files still present (err=%v)", err)
	}
}

// TestDeleteFollowFilesFalseKeepsFiles asserts the default (?files absent)
// removes the records but keeps the files on disk.
func TestDeleteFollowFilesFalseKeepsFiles(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads := t.TempDir()
	library := t.TempDir()

	follow, err := store.AddNodeFollow(ctx, 300, "F", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	outDir := seedDownloadedLesson(t, store, downloads, library, 3001, follow.ID, "03 - Lesson")

	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	req := httptest.NewRequest(http.MethodDelete, "/api/follows/"+strconv.FormatInt(follow.ID, 10), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	// Records gone.
	if _, err := store.GetFollow(ctx, follow.ID); err == nil {
		t.Error("follow still present after delete")
	}
	// Files KEPT (the single stored location survives a files=false delete).
	if _, err := os.Stat(outDir); err != nil {
		t.Errorf("lesson files removed (err=%v), want kept when files=false", err)
	}
}

// TestDeleteLessonHandler asserts the lesson's files are removed (both copies)
// and the row is tombstone-skipped (status=skipped, error=deleted, paths
// cleared), returning the updated lesson with 200.
func TestDeleteLessonHandler(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	downloads := t.TempDir()
	library := t.TempDir()

	follow, err := store.AddNodeFollow(ctx, 400, "F", "drumeo", "best")
	if err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	outDir := seedDownloadedLesson(t, store, downloads, library, 4001, follow.ID, "04 - Lesson")

	srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: library}, "test")

	req := httptest.NewRequest(http.MethodDelete, "/api/lessons/4001", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got LessonDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != database.StatusSkipped {
		t.Errorf("Status = %q, want skipped", got.Status)
	}
	if got.Error == nil || *got.Error != "deleted" {
		t.Errorf("Error = %v, want 'deleted'", got.Error)
	}
	if got.OutputDir != nil || got.VideoPath != nil || got.Bytes != nil {
		t.Errorf("paths/bytes not cleared: output_dir=%v video_path=%v bytes=%v", got.OutputDir, got.VideoPath, got.Bytes)
	}
	// The single stored location (the library path) is gone.
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("lesson files still present (err=%v)", err)
	}
}

// TestDeleteLessonNotFound asserts an unknown id is a 404.
func TestDeleteLessonNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodDelete, "/api/lessons/999999", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
