package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
	"github.com/elienop/drumdrop/internal/scheduler"
)

// stubExpander hands a fixed set of lesson ids to every follow so PlanDryRun has
// something to count without touching the network.
type stubExpander struct{ ids []int }

func (e stubExpander) Expand(_ database.Follow, _ string) ([]musora.LessonItem, error) {
	items := make([]musora.LessonItem, 0, len(e.ids))
	for _, id := range e.ids {
		items = append(items, musora.LessonItem{ID: id})
	}
	return items, nil
}

// newSyncTestStore opens a fresh temp-file store with migrations applied.
func newSyncTestStore(t *testing.T) *database.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	store := database.NewStore(db)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func TestSyncDryRunReportsWouldEnqueue(t *testing.T) {
	store := newSyncTestStore(t)

	// One node follow whose expansion yields two fresh, undownloaded lessons.
	if _, err := store.AddNodeFollow(context.Background(), 42, "Lesson", "drumeo", "best"); err != nil {
		t.Fatalf("AddNodeFollow: %v", err)
	}
	planner := &scheduler.Planner{
		Store:    store,
		Expander: stubExpander{ids: []int{1001, 1002}},
		PermIDs:  "perm",
	}
	srv := NewServer(store, Deps{Planner: planner}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(`{"dry_run":true}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		WouldEnqueue int `json:"would_enqueue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.WouldEnqueue != 2 {
		t.Errorf("would_enqueue = %d, want 2", body.WouldEnqueue)
	}

	// A dry run must NOT have enqueued any job.
	jobs, err := store.ListJobs(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("dry run enqueued %d jobs, want 0", len(jobs))
	}
}

func TestSyncKicksDaemon(t *testing.T) {
	store := newSyncTestStore(t)
	kick := make(chan struct{}, 1)
	srv := NewServer(store, Deps{Kick: kick}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Triggered bool `json:"triggered"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Triggered {
		t.Errorf("triggered = false, want true")
	}

	// The non-blocking send must have landed a value on the kick channel.
	select {
	case <-kick:
	default:
		t.Error("expected a value on the kick channel")
	}
}

func TestSyncKickBufferFullStill202(t *testing.T) {
	store := newSyncTestStore(t)
	// A full buffer means a cycle is already pending; the handler must still 202.
	kick := make(chan struct{}, 1)
	kick <- struct{}{}
	srv := NewServer(store, Deps{Kick: kick}, nil, Config{}, "test")

	req := httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 even when buffer full; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSyncEmptyBodyTriggersKick(t *testing.T) {
	store := newSyncTestStore(t)
	kick := make(chan struct{}, 1)
	srv := NewServer(store, Deps{Kick: kick}, nil, Config{}, "test")

	// No body at all: treated as a non-dry-run kick.
	req := httptest.NewRequest(http.MethodPost, "/api/sync", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}
