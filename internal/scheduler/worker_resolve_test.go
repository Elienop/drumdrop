package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// musoraResolver is the engine's Resolver without the engine (which imports
// this package): the real musora.ResolveLesson, GROQ query, HTTP and decode.
type musoraResolver struct{}

func (musoraResolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	return musora.ResolveLesson(id, permIDs)
}

// sanityAnswering points musora's GROQ endpoint at a stub that answers body
// (200) to every query, or at an address nothing listens on when body is "".
func sanityAnswering(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	base := srv.URL
	if body == "" {
		srv.Close()
	} else {
		t.Cleanup(srv.Close)
	}
	t.Cleanup(musora.SetSanityBase(base))
}

// TestWorkerSkipsOnlyALessonMusoraHasNoMatchFor (pre-existing, found in round
// 5) proves, over a real store and the real resolve, that only a lesson Musora
// answers with no match (locked or removed) is skipped; one Musora couldn't be
// reached for, or whose answer couldn't be read (a GROQ shape mismatch, hard
// rule 10), is a failed attempt that later cycles retry, with a sentence as
// its error. Each job ends with exactly one terminal event, and nothing is
// downloaded.
func TestWorkerSkipsOnlyALessonMusoraHasNoMatchFor(t *testing.T) {
	cases := []resolveCase{
		{"no match", `{"result":[]}`, database.StatusSkipped, msgNotResolved, msgNotResolved, "lesson_skipped", "no lesson"},
		{"unreachable", "", database.StatusFailed, failMusora.lesson, failMusora.job, "attempt_failed", "asking Musora for the lesson"},
		{"shape mismatch", `{"result":[{"id":100,"title":["not","a","string"]}]}`, database.StatusFailed, failMusora.lesson, failMusora.job, "attempt_failed", "cannot unmarshal array"},
		{"not json", `<html>maintenance</html>`, database.StatusFailed, failMusora.lesson, failMusora.job, "attempt_failed", "invalid character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkSkipsOnlyALessonMusoraHasNoMatchFor(t, c)
		})
	}
}

// resolveCase is what Musora answers to the resolve, and how the job must
// end for it.
type resolveCase struct {
	name       string
	answer     string // "" = Musora unreachable
	status     string
	lessonErr  string
	jobErr     string
	endingKind string
	logHas     string // the detail, in the log only
}

// checkSkipsOnlyALessonMusoraHasNoMatchFor is one case of
// TestWorkerSkipsOnlyALessonMusoraHasNoMatchFor.
func checkSkipsOnlyALessonMusoraHasNoMatchFor(t *testing.T, c resolveCase) {
	t.Helper()
	ctx := context.Background()
	sanityAnswering(t, c.answer)
	w, s, dl, f, _ := realWorker(t, LayoutPlexTV)
	w.Resolver = musoraResolver{}
	sink := &recordingSink{}
	w.Progress = sink
	var log bytes.Buffer
	w.Log = &log
	jobID, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertLessonAndJobEndedAs(t, s, jobID, c)
	skip, err := s.ShouldSkipEnqueue(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if want := c.status == database.StatusSkipped; skip != want {
		t.Errorf("the planner leaves it alone = %v, want %v (only a lesson with no match stays skipped)", skip, want)
	}
	assertEndsOnce(t, sink, jobID)
	if k := sink.kinds(); k[c.endingKind] != 1 {
		t.Errorf("events %v, want its end reported as %s", k, c.endingKind)
	}
	if len(dl.calls) != 0 {
		t.Errorf("downloaded %d times, want none", len(dl.calls))
	}
	if !strings.Contains(log.String(), c.logHas) {
		t.Errorf("log %q, want the detail %q in it", log.String(), c.logHas)
	}
}

// assertLessonAndJobEndedAs fails unless lesson 100 ended with c's status
// and error, and job jobID failed with c's job error.
func assertLessonAndJobEndedAs(t *testing.T, s *database.Store, jobID int64, c resolveCase) {
	t.Helper()
	ctx := context.Background()
	l, err := s.GetLesson(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != c.status || l.Error.String != c.lessonErr {
		t.Errorf("lesson = %s/%q, want %s/%q", l.Status, l.Error.String, c.status, c.lessonErr)
	}
	j, err := s.GetJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != database.JobFailed || j.Error.String != c.jobErr {
		t.Errorf("job = %s/%q, want failed/%q", j.Status, j.Error.String, c.jobErr)
	}
}
