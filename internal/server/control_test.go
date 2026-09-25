package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestCancelJobRunningUsesWorker asserts a running job is canceled through the
// worker's CancelRunning (which kills the process), NOT a bare DB flip. The
// re-fetched job may still read "running" because the worker finalizes the
// canceled state asynchronously — the handler does not block on it.
func TestCancelJobRunningUsesWorker(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 7001, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	id, _, err := store.EnqueueJob(ctx, sql.NullInt64{}, 7001)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, id)

	var canceledID int64
	deps := Deps{CancelRunning: func(jobID int64) bool {
		canceledID = jobID
		return true
	}}
	srv := NewServer(store, deps, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+strconv.FormatInt(id, 10)+"/cancel", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if canceledID != id {
		t.Errorf("CancelRunning called with %d, want %d", canceledID, id)
	}
	// The DB must NOT have been flipped by the handler (worker owns that path):
	// the job is still running until the worker finalizes it.
	j, err := store.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if j.Status != "running" {
		t.Errorf("job status = %q, want running (worker finalizes async)", j.Status)
	}
}

// TestCancelJobRunningFallsBackToStore asserts that when no worker is attached
// (CancelRunning nil) — or the worker reports the job is not in-flight — the
// handler falls back to the DB CancelJob flip so a running job is still canceled.
func TestCancelJobRunningFallsBackToStore(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 7002, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	id, _, err := store.EnqueueJob(ctx, sql.NullInt64{}, 7002)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimJob(t, store, id)

	// CancelRunning returns false: the job is not in-flight in this process.
	deps := Deps{CancelRunning: func(int64) bool { return false }}
	srv := NewServer(store, deps, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+strconv.FormatInt(id, 10)+"/cancel", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got JobDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "canceled" {
		t.Errorf("got.Status = %q, want canceled (store fallback)", got.Status)
	}
}

func TestUnskipLessonHandler(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	if err := store.UpsertLesson(ctx, 7003, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	if _, err := store.SkipLesson(ctx, 7003, "no"); err != nil {
		t.Fatalf("SkipLesson: %v", err)
	}
	srv := NewServer(store, Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/7003/unskip", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got LessonDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("got.Status = %q, want pending", got.Status)
	}
	if got.Error != nil {
		t.Errorf("got.Error = %v, want nil after un-skip", got.Error)
	}
}

func TestUnskipLessonNotFound(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/999999/unskip", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestUnskipLessonBadID(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/lessons/not-a-number/unskip", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPauseResumeAndSummaryPaused(t *testing.T) {
	store := newTestStore(t)
	paused := false
	deps := Deps{
		Pause:    func() { paused = true },
		Resume:   func() { paused = false },
		IsPaused: func() bool { return paused },
	}
	srv := NewServer(store, deps, nil, Config{}, "test")

	// Pause.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/pause", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !paused {
		t.Error("Pause() was not called")
	}

	// Summary reflects paused=true.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want %d", rec.Code, http.StatusOK)
	}
	var sum SummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if !sum.Paused {
		t.Error("summary.paused = false, want true after pause")
	}

	// Resume.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/resume", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want %d", rec.Code, http.StatusOK)
	}
	if paused {
		t.Error("Resume() was not called")
	}
}

// TestPauseNoDaemon asserts pause/resume return 503 when no daemon is attached
// (the func handles are nil), and summary reports paused=false.
func TestPauseNoDaemon(t *testing.T) {
	srv := NewServer(newTestStore(t), Deps{}, nil, Config{}, "test")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/pause", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("pause status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/resume", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("resume status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/summary", nil))
	var sum SummaryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if sum.Paused {
		t.Error("summary.paused = true, want false with no daemon")
	}
}
