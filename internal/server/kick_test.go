package server

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
)

// The tests below pin owner ruling 2026-09-24 (m): pressing Download, Retry
// or Un-skip, or adding a follow, starts a sync right away, through the kick
// channel Sync and Resume already use. Download starts one even when the
// lesson is already queued; a press refused, or one that changed nothing
// otherwise, starts none. That a kick while syncs are paused is dropped
// (pause wins) is the daemon's, pinned by TestDaemonPauseSkipsCyclesThenResumes.

// press is one request, sent to a server over a store the setup prepared.
type press struct {
	name   string
	setup  func(t *testing.T, s *database.Store) (method, path, body string)
	status int
}

// lessonAt upserts lesson id, with no follow.
func lessonAt(t *testing.T, s *database.Store, id int) {
	t.Helper()
	if err := s.UpsertLesson(t.Context(), id, "Lesson", sql.NullInt64{}, "drumeo", sql.NullInt64{}, sql.NullInt64{}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
}

// queuedFor enqueues a job for lesson id and returns it.
func queuedFor(t *testing.T, s *database.Store, id int) int64 {
	t.Helper()
	job, _, err := s.EnqueueJob(t.Context(), sql.NullInt64{}, id)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	return job
}

func retryPath(job int64) string { return "/api/jobs/" + strconv.FormatInt(job, 10) + "/retry" }

// serve sends p's request to a server whose kick channel is kick, and fails
// unless it answers p.status within a few seconds (a press never waits on
// the daemon).
func serve(t *testing.T, kick chan struct{}, p press) {
	t.Helper()
	store := newTestStore(t)
	method, path, body := p.setup(t, store)
	srv := NewServer(store, Deps{Kick: kick}, nil, Config{}, "test")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s %s did not answer: it waits on the kick channel", method, path)
	}
	if rec.Code != p.status {
		t.Fatalf("%s %s = %d, want %d (body %s)", method, path, rec.Code, p.status, rec.Body.String())
	}
}

var queuingPresses = []press{
	{"Download", func(t *testing.T, s *database.Store) (string, string, string) {
		lessonAt(t, s, 8001)
		return http.MethodPost, "/api/lessons/8001/download", ""
	}, http.StatusAccepted},
	{"Download of a lesson already queued", func(t *testing.T, s *database.Store) (string, string, string) {
		lessonAt(t, s, 8001)
		queuedFor(t, s, 8001)
		return http.MethodPost, "/api/lessons/8001/download", ""
	}, http.StatusOK},
	{"Retry", func(t *testing.T, s *database.Store) (string, string, string) {
		lessonAt(t, s, 8001)
		job := queuedFor(t, s, 8001)
		if err := s.CancelJob(t.Context(), job); err != nil {
			t.Fatalf("CancelJob: %v", err)
		}
		return http.MethodPost, retryPath(job), ""
	}, http.StatusAccepted},
	{"Un-skip", func(t *testing.T, s *database.Store) (string, string, string) {
		lessonAt(t, s, 8001)
		if _, err := s.SkipLesson(t.Context(), 8001, "not now"); err != nil {
			t.Fatalf("SkipLesson: %v", err)
		}
		return http.MethodPost, "/api/lessons/8001/unskip", ""
	}, http.StatusOK},
	{"adding a follow", func(t *testing.T, s *database.Store) (string, string, string) {
		stubSanity(t, `{"result":[{"id":409875,"title":"Course A"}]}`)
		return http.MethodPost, "/api/follows", `{"kind":"node","id":"409875","brand":"drumeo"}`
	}, http.StatusCreated},
}

// TestAPressThatQueuesStartsASync proves each press that queues a download,
// or lets a sync queue one, sends one kick.
func TestAPressThatQueuesStartsASync(t *testing.T) {
	for _, p := range queuingPresses {
		t.Run(p.name, func(t *testing.T) {
			kick := make(chan struct{}, 1)
			serve(t, kick, p)
			if len(kick) != 1 {
				t.Errorf("kicks pending = %d, want 1: a sync must start now", len(kick))
			}
		})
	}
}

// TestAPressThatQueuesNothingStartsNoSync proves an answer that changed
// nothing sends no kick: an unknown lesson or job, a job that is not
// finished, a lesson that is not skipped, a follow that already exists.
func TestAPressThatQueuesNothingStartsNoSync(t *testing.T) {
	for _, p := range []press{
		{"Download of an unknown lesson", func(t *testing.T, s *database.Store) (string, string, string) {
			return http.MethodPost, "/api/lessons/8001/download", ""
		}, http.StatusNotFound},
		{"Retry of an unknown job", func(t *testing.T, s *database.Store) (string, string, string) {
			return http.MethodPost, retryPath(999999), ""
		}, http.StatusNotFound},
		{"Retry of a queued job", func(t *testing.T, s *database.Store) (string, string, string) {
			lessonAt(t, s, 8001)
			return http.MethodPost, retryPath(queuedFor(t, s, 8001)), ""
		}, http.StatusConflict},
		{"Un-skip of a lesson not skipped", func(t *testing.T, s *database.Store) (string, string, string) {
			lessonAt(t, s, 8001)
			return http.MethodPost, "/api/lessons/8001/unskip", ""
		}, http.StatusOK},
		{"Un-skip of an unknown lesson", func(t *testing.T, s *database.Store) (string, string, string) {
			return http.MethodPost, "/api/lessons/8001/unskip", ""
		}, http.StatusNotFound},
		{"adding a follow that exists", func(t *testing.T, s *database.Store) (string, string, string) {
			stubSanity(t, `{"result":[{"id":409875,"title":"Course A"}]}`)
			if _, err := s.AddNodeFollow(t.Context(), 409875, "Course A", "drumeo", "best"); err != nil {
				t.Fatalf("AddNodeFollow: %v", err)
			}
			return http.MethodPost, "/api/follows", `{"kind":"node","id":"409875","brand":"drumeo"}`
		}, http.StatusOK},
		{"adding a follow with a bad body", func(t *testing.T, s *database.Store) (string, string, string) {
			return http.MethodPost, "/api/follows", `{`
		}, http.StatusBadRequest},
	} {
		t.Run(p.name, func(t *testing.T) {
			kick := make(chan struct{}, 1)
			serve(t, kick, p)
			if len(kick) != 0 {
				t.Errorf("kicks pending = %d, want 0: nothing was queued", len(kick))
			}
		})
	}
}

// TestAPressWhileADeleteHoldsTheLessonStartsNoSync proves Download and
// Un-skip answer 409 with the being-deleted sentence while a delete holds the
// lesson, send no kick, and change nothing: Un-skip leaves the lesson skipped,
// as Skip refuses too (security round 5d I5).
func TestAPressWhileADeleteHoldsTheLessonStartsNoSync(t *testing.T) {
	for _, path := range []string{"/api/lessons/8001/download", "/api/lessons/8001/unskip"} {
		t.Run(path, func(t *testing.T) {
			store := newTestStore(t)
			lessonAt(t, store, 8001)
			if _, err := store.SkipLesson(t.Context(), 8001, "not now"); err != nil {
				t.Fatalf("SkipLesson: %v", err)
			}
			if _, _, err := store.BeginLessonDelete(t.Context(), 8001); err != nil {
				t.Fatalf("BeginLessonDelete: %v", err)
			}
			kick := make(chan struct{}, 1)
			srv := NewServer(store, Deps{Kick: kick}, nil, Config{}, "test")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))

			wantError(t, rec, http.StatusConflict, msgBeingDeleted)
			if len(kick) != 0 {
				t.Errorf("kicks pending = %d, want 0: nothing was queued", len(kick))
			}
			if l := mustLesson(t, store, 8001); l.Status != database.StatusSkipped || l.Error.String != "not now" {
				t.Errorf("lesson = %s %q, want it still skipped %q", l.Status, l.Error.String, "not now")
			}
		})
	}
}

// TestAPressNeverWaitsOnTheDaemon proves a press answers when a sync is
// already pending (the kick buffer full: it shares that sync) and when
// nothing receives the kick at all (a daemon busy in a cycle), and with no
// daemon attached (a nil Kick).
func TestAPressNeverWaitsOnTheDaemon(t *testing.T) {
	for _, p := range queuingPresses {
		t.Run(p.name, func(t *testing.T) {
			full := make(chan struct{}, 1)
			full <- struct{}{}
			serve(t, full, p)
			if len(full) != 1 {
				t.Errorf("kicks pending = %d, want the 1 already pending", len(full))
			}
			serve(t, make(chan struct{}), p)
			serve(t, nil, p)
		})
	}
}
