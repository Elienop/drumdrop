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

// TestWorkerCancelLeavesASentence (round-4 item 7) proves a canceled
// download, over a real store, leaves a sentence, not the bare word
// "canceled", both as the note under its lesson and as its event's Err.
func TestWorkerCancelLeavesASentence(t *testing.T) {
	ctx := context.Background()
	w, s, _, f, _ := realWorker(t, "")
	dl := newBlockingDownloader()
	w.Downloader = dl
	sink := &recordingSink{}
	w.Progress = sink
	jobID, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 100)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = w.RunOnce(ctx, 0)
	}()
	<-dl.started
	if !w.CancelRunning(jobID) {
		t.Fatal("CancelRunning = false, want the running download canceled")
	}
	<-done
	l, err := s.GetLesson(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if note := l.Error.String; l.Status != database.StatusSkipped || !strings.HasSuffix(note, "Download again to get this lesson.") {
		t.Errorf("lesson = %s/%q, want skipped with a sentence that says what to do", l.Status, note)
	}
	assertEndsOnce(t, sink, jobID)
	for _, e := range sink.snapshot() {
		if e.Kind == "lesson_skipped" && e.Err != msgCanceled {
			t.Errorf("the cancel's event Err = %q, want %q", e.Err, msgCanceled)
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
// error, the job's, and the events' Err hold a sentence written for the user,
// never the Go error behind it, for each way a job fails: every attempt
// failed, a precondition failed, Musora couldn't be asked, and a lesson
// Musora has no match for. The lesson's sentence and the job's are each the
// one for the place it is shown in (round-4 item 7).
func TestWorkerRecordsSentencesNotErrors(t *testing.T) {
	cases := []struct {
		name   string
		set    func(w *Worker, store *fakeWorkerStore)
		want   failure
		secret string
	}{
		{"every attempt failed", func(w *Worker, store *fakeWorkerStore) {
			w.Downloader = &scratchWriter{}
		}, failDownload, "403"},
		{"records unreadable", func(w *Worker, store *fakeWorkerStore) {
			store.getLessonErr = errors.New("database is locked at /srv/drumdrop.db")
		}, failNotStarted, "/srv/drumdrop.db"},
		{"claims unreadable", func(w *Worker, store *fakeWorkerStore) {
			store.withFilesErr = errors.New("no such column: library_entries")
		}, failNotStarted, "no such column"},
		{"Musora unreachable", func(w *Worker, store *fakeWorkerStore) {
			w.Resolver = fakeResolver{errs: map[int]error{100: errors.New("GET https://musora.example/api: 500")}}
		}, failMusora, "musora.example"},
		{"no match", func(w *Worker, store *fakeWorkerStore) {
			w.Resolver = fakeResolver{}
		}, failure{lesson: msgNotResolved, job: msgNotResolved}, "nil"},
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
			if got := store.jobs[1].Error.String; got != c.want.job {
				t.Errorf("recorded job error %q, want %q", got, c.want.job)
			}
			if got := store.lessonErr[100]; got != c.want.lesson {
				t.Errorf("recorded lesson error %q, want %q", got, c.want.lesson)
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
// against the copy rules: at most 220 characters, no raw "could not", and a
// "check the log" sentence in the one wording.
func TestWorkerMessagesFitTheDialog(t *testing.T) {
	all := []string{msgAttemptFailed, msgNotResolved, msgStopped, msgRequeued, msgShutdown, msgCanceled}
	for _, f := range failures {
		all = append(all, f.lesson, f.job)
	}
	for _, m := range all {
		if len(m) > 220 || strings.Contains(m, "could not") {
			t.Errorf("%q: %d characters; want at most 220, in the UI's voice (couldn't)", m, len(m))
		}
		if strings.Contains(m, "server log") && !strings.Contains(m, "Check the server log, fix the problem, then ") {
			t.Errorf("%q: want the one \"check the log\" wording", m)
		}
	}
}

// failures are every failure the worker records.
var failures = []failure{failDownload, failNotStarted, failMusora}

// TestWorkerSentencesNameTheButtonsWhereTheyAreShown (round-4 item 7) proves
// every sentence the worker stores is true where it is shown: a failure's
// lesson sentence (under the lesson, whose menu offers Download) names
// Download and never Retry, its job sentence (in the Queue, beside Retry)
// names Retry and never Download, and a sentence stored in both places
// (SkipDownload's, and the one a stopped download leaves) names neither.
func TestWorkerSentencesNameTheButtonsWhereTheyAreShown(t *testing.T) {
	for _, f := range failures {
		if !strings.HasSuffix(f.lesson, "then Download again.") || strings.Contains(f.lesson, "Retry") {
			t.Errorf("lesson sentence %q: want it to end by naming Download, and never Retry", f.lesson)
		}
		if !strings.HasSuffix(f.job, "then Retry.") || strings.Contains(f.job, "Download") {
			t.Errorf("job sentence %q: want it to end by naming Retry, and never Download", f.job)
		}
	}
	if strings.Contains(msgNotResolved, "Download") || strings.Contains(msgNotResolved, "Retry") {
		t.Errorf("%q is stored on the lesson and on its job alike: want it to name no button", msgNotResolved)
	}
}
