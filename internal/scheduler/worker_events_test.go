package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// terminalKinds are the events the web reducer ends an active download on
// (web/src/lib/sse-reducer.ts).
var terminalKinds = map[string]bool{"download_ok": true, "attempt_failed": true, "lesson_skipped": true}

// assertEndsOnce fails unless the job's events end with exactly one terminal
// event after its last in-flight one (job_claimed, download_started,
// download_progress), so no client is left showing it as active.
func assertEndsOnce(t *testing.T, sink *recordingSink, jobID int64) {
	t.Helper()
	var kinds []string
	for _, e := range sink.snapshot() {
		if e.JobID == jobID {
			kinds = append(kinds, e.Kind)
		}
	}
	last := -1
	for i, k := range kinds {
		if !terminalKinds[k] {
			last = i
		}
	}
	after := 0
	for _, k := range kinds[last+1:] {
		if terminalKinds[k] {
			after++
		}
	}
	if last < 0 || after != 1 {
		t.Errorf("job %d events %v: want exactly one terminal event after the last in-flight one", jobID, kinds)
	}
}

// hookResolver runs before each Resolve, then answers as fakeResolver does.
type hookResolver struct {
	fakeResolver
	before func()
}

func (r hookResolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	r.before()
	return r.fakeResolver.Resolve(id, permIDs)
}

// TestWorkerStopReportsTheJobsEnd (code #1) proves a job a Skip stops outside
// yt-dlp still reports its end to the clients, once, over a real store: a
// Skip landing while the finished download is moved (in both layouts), and
// one landing between the claim and the start of the download.
func TestWorkerStopReportsTheJobsEnd(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		for _, when := range []string{"during the move", "before the start"} {
			t.Run(when+"/layout="+layout, func(t *testing.T) {
				ctx := context.Background()
				w, s, _, f, _ := realWorker(t, layout)
				jobID, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 100)
				if err != nil {
					t.Fatal(err)
				}
				skip := func() {
					if _, err := s.SkipLesson(ctx, 100, "not wanted"); err != nil {
						t.Fatalf("SkipLesson: %v", err)
					}
				}
				if when == "during the move" {
					orig := renameAt
					once := false
					renameAt = func(from *os.Root, src string, to *os.Root, dst string) error {
						if !once {
							once = true
							skip()
						}
						return orig(from, src, to, dst)
					}
					t.Cleanup(func() { renameAt = orig })
				} else {
					w.Resolver = hookResolver{fakeResolver: w.Resolver.(fakeResolver), before: skip}
				}
				sink := &recordingSink{}
				w.Progress = sink
				if _, err := w.RunOnce(ctx, 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				if l, _ := s.GetLesson(ctx, 100); l.Status != database.StatusSkipped {
					t.Errorf("lesson status = %q, want skipped", l.Status)
				}
				assertEndsOnce(t, sink, jobID)
			})
		}
	}
}

// TestWorkerRequeuedDuringTheMoveReportsTheJobsEnd (code #1) proves a job
// requeued elsewhere while its download was moved reports its end too.
func TestWorkerRequeuedDuringTheMoveReportsTheJobsEnd(t *testing.T) {
	w, store, _, _, _ := plexWorker(t)
	store.onConfirm = func() { store.finishErr = database.ErrDownloadCanceled }
	sink := &recordingSink{}
	w.Progress = sink
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertEndsOnce(t, sink, 1)
}

// TestWorkerShutdownBeforeTheFirstAttemptReportsTheJobsEnd (code #1) proves
// a job whose worker is told to stop between the claim and its first attempt
// reports its end.
func TestWorkerShutdownBeforeTheFirstAttemptReportsTheJobsEnd(t *testing.T) {
	w, _, _, _, _ := plexWorker(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.Resolver = hookResolver{fakeResolver: w.Resolver.(fakeResolver), before: cancel}
	w.Cfg.LibraryDir = "" // nothing to read before the attempt
	sink := &recordingSink{}
	w.Progress = sink
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertEndsOnce(t, sink, 1)
}

// TestWorkerRecordsSentencesNotErrors (security Info 5) proves the lesson's
// error and the events' Err hold a sentence written for the user, never the
// Go error behind it, for each way a job fails: every attempt failed, a
// precondition failed, and a lesson Musora did not return.
func TestWorkerRecordsSentencesNotErrors(t *testing.T) {
	cases := []struct {
		name   string
		set    func(w *Worker, store *fakeWorkerStore)
		want   string
		secret string
	}{
		{"every attempt failed", func(w *Worker, store *fakeWorkerStore) {
			w.Downloader = &scratchWriter{}
		}, msgDownloadFailed, "403"},
		{"records unreadable", func(w *Worker, store *fakeWorkerStore) {
			store.getLessonErr = errors.New("database is locked at /srv/drumdrop.db")
		}, msgNotStarted, "/srv/drumdrop.db"},
		{"claims unreadable", func(w *Worker, store *fakeWorkerStore) {
			store.withFilesErr = errors.New("no such column: library_entries")
		}, msgNotStarted, "no such column"},
		{"not resolved", func(w *Worker, store *fakeWorkerStore) {
			w.Resolver = fakeResolver{errs: map[int]error{100: errors.New("GET https://musora.example/api: 500")}}
		}, msgNotResolved, "musora.example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, store, _, _, _ := plexWorker(t)
			w.Cfg.MaxAttempts = 1
			c.set(w, store)
			sink := &recordingSink{}
			w.Progress = sink
			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if got := store.jobs[1].Error.String; got != c.want {
				t.Errorf("recorded error %q, want %q", got, c.want)
			}
			for _, e := range sink.snapshot() {
				if strings.Contains(e.Err, c.secret) {
					t.Errorf("event %s carries the internal detail: %q", e.Kind, e.Err)
				}
			}
		})
	}
}

// TestWorkerMessagesFitTheDialog checks every sentence the worker shows a user
// against the copy rules: at most 220 characters, and no raw "could not".
func TestWorkerMessagesFitTheDialog(t *testing.T) {
	for _, m := range []string{msgDownloadFailed, msgAttemptFailed, msgNotStarted, msgNotResolved, msgStopped, msgRequeued, msgShutdown} {
		if len(m) > 220 || strings.Contains(m, "could not") {
			t.Errorf("%q: %d characters; want at most 220, in the UI's voice (couldn't)", m, len(m))
		}
	}
}
