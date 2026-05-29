package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// enqueueCall records one EnqueueJob invocation so tests can assert the exact
// set of lessons that were enqueued, and with which follow id.
type enqueueCall struct {
	followID      sql.NullInt64
	railcontentID int
}

// upsertCall records one UpsertLesson invocation so tests can assert the parent
// linkage (node → follow railcontent id, instructor → NULL).
type upsertCall struct {
	id     int
	parent sql.NullInt64
	brand  string
}

// fakePlannerStore is an in-memory Store implementing only the methods the
// Planner exercises. The worker-side methods panic so a stray call is caught.
type fakePlannerStore struct {
	follows []database.Follow

	downloaded map[int]bool // railcontent_id → already downloaded
	active     map[int]bool // railcontent_id → has a queued/running job

	enqueued []enqueueCall
	upserts  []upsertCall
	touched  []int64

	nextJobID int64
}

func (s *fakePlannerStore) ListFollows(ctx context.Context) ([]database.Follow, error) {
	return s.follows, nil
}

func (s *fakePlannerStore) UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string) error {
	s.upserts = append(s.upserts, upsertCall{id: railcontentID, parent: parent, brand: brand})
	return nil
}

func (s *fakePlannerStore) IsDownloaded(ctx context.Context, id int) (bool, error) {
	return s.downloaded[id], nil
}

func (s *fakePlannerStore) ActiveJobExists(ctx context.Context, railcontentID int) (bool, error) {
	return s.active[railcontentID], nil
}

func (s *fakePlannerStore) EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (int64, error) {
	s.enqueued = append(s.enqueued, enqueueCall{followID: followID, railcontentID: railcontentID})
	// Mark active so a second pass within the same run would dedupe too.
	if s.active == nil {
		s.active = map[int]bool{}
	}
	s.active[railcontentID] = true
	s.nextJobID++
	return s.nextJobID, nil
}

func (s *fakePlannerStore) TouchLastSynced(ctx context.Context, id int64) error {
	s.touched = append(s.touched, id)
	return nil
}

// Worker-side Store methods: unused by the Planner, so any call is a bug.
func (s *fakePlannerStore) ClaimNextJob(ctx context.Context) (database.Job, bool, error) {
	panic("ClaimNextJob: not expected from Planner")
}
func (s *fakePlannerStore) GetFollow(ctx context.Context, id int64) (database.Follow, error) {
	panic("GetFollow: not expected from Planner")
}
func (s *fakePlannerStore) MarkJobRunning(ctx context.Context, id int64) error {
	panic("MarkJobRunning: not expected from Planner")
}
func (s *fakePlannerStore) MarkJobDone(ctx context.Context, id int64) error {
	panic("MarkJobDone: not expected from Planner")
}
func (s *fakePlannerStore) MarkJobFailed(ctx context.Context, id int64, errMsg string) error {
	panic("MarkJobFailed: not expected from Planner")
}
func (s *fakePlannerStore) MarkDownloading(ctx context.Context, id int) error {
	panic("MarkDownloading: not expected from Planner")
}
func (s *fakePlannerStore) MarkDownloaded(ctx context.Context, id int, quality, outputDir, videoPath string, bytes int64) error {
	panic("MarkDownloaded: not expected from Planner")
}
func (s *fakePlannerStore) MarkFailed(ctx context.Context, id int, errMsg string) error {
	panic("MarkFailed: not expected from Planner")
}
func (s *fakePlannerStore) MarkSkipped(ctx context.Context, id int, reason string) error {
	panic("MarkSkipped: not expected from Planner")
}
func (s *fakePlannerStore) RequeueStaleRunning(ctx context.Context) (int, error) {
	panic("RequeueStaleRunning: not expected from Planner")
}

// fakeExpander returns canned ids (or an error) per follow id.
type fakeExpander struct {
	ids  map[int64][]int
	errs map[int64]error
}

func (e fakeExpander) Expand(f database.Follow, permIDs string) ([]int, error) {
	if err := e.errs[f.ID]; err != nil {
		return nil, err
	}
	return e.ids[f.ID], nil
}

// enqueuedIDs returns the sorted set of railcontent ids that were enqueued.
func enqueuedIDs(calls []enqueueCall) []int {
	out := make([]int, len(calls))
	for i, c := range calls {
		out[i] = c.railcontentID
	}
	sort.Ints(out)
	return out
}

func TestPlanEnqueuesOnlyNewLessons(t *testing.T) {
	store := &fakePlannerStore{
		follows: []database.Follow{nodeFollow()},
		downloaded: map[int]bool{
			11: true, // already downloaded → skip
		},
		active: map[int]bool{
			12: true, // already queued/running → dedupe skip
		},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {10, 11, 12, 13}}}

	p := &Planner{Store: store, Expander: exp, PermIDs: "perm"}
	enqueued, err := p.Plan(context.Background(), 0)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	if enqueued != 2 {
		t.Errorf("enqueued = %d, want 2", enqueued)
	}
	if got, want := enqueuedIDs(store.enqueued), []int{10, 13}; !reflect.DeepEqual(got, want) {
		t.Errorf("enqueued ids = %v, want %v", got, want)
	}

	// Every lesson is upserted, even the skipped ones (record-keeping).
	if len(store.upserts) != 4 {
		t.Errorf("upserts = %d, want 4 (all seen lessons)", len(store.upserts))
	}

	// The processed follow was touched exactly once.
	if got, want := store.touched, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("touched = %v, want %v", got, want)
	}
}

func TestPlanParentLinkageNodeVsInstructor(t *testing.T) {
	node := nodeFollow()       // RailcontentID 4242
	inst := instructorFollow() // no railcontent id
	store := &fakePlannerStore{
		follows: []database.Follow{node, inst},
	}
	exp := fakeExpander{ids: map[int64][]int{
		node.ID: {100},
		inst.ID: {200},
	}}

	p := &Planner{Store: store, Expander: exp}
	if _, err := p.Plan(context.Background(), 0); err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	parents := map[int]sql.NullInt64{}
	brands := map[int]string{}
	for _, u := range store.upserts {
		parents[u.id] = u.parent
		brands[u.id] = u.brand
	}

	// Node follow → parent is its railcontent id.
	if got := parents[100]; !got.Valid || got.Int64 != node.RailcontentID.Int64 {
		t.Errorf("node lesson parent = %+v, want valid %d", got, node.RailcontentID.Int64)
	}
	// Instructor follow → parent is NULL (invalid).
	if got := parents[200]; got.Valid {
		t.Errorf("instructor lesson parent = %+v, want invalid/NULL", got)
	}
	if brands[100] != node.Brand {
		t.Errorf("node lesson brand = %q, want %q", brands[100], node.Brand)
	}

	// Follow id flows through to EnqueueJob.
	for _, c := range store.enqueued {
		var want int64
		switch c.railcontentID {
		case 100:
			want = node.ID
		case 200:
			want = inst.ID
		}
		if !c.followID.Valid || c.followID.Int64 != want {
			t.Errorf("enqueue %d followID = %+v, want valid %d", c.railcontentID, c.followID, want)
		}
	}
}

func TestPlanLimitCapsEnqueues(t *testing.T) {
	store := &fakePlannerStore{follows: []database.Follow{nodeFollow()}}
	exp := fakeExpander{ids: map[int64][]int{1: {1, 2, 3, 4, 5}}}

	p := &Planner{Store: store, Expander: exp}
	enqueued, err := p.Plan(context.Background(), 2)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	if enqueued != 2 {
		t.Errorf("enqueued = %d, want 2 (capped by limit)", enqueued)
	}
	if len(store.enqueued) != 2 {
		t.Errorf("EnqueueJob calls = %d, want 2", len(store.enqueued))
	}
	// The follow is still touched even though the limit truncated its lessons.
	if got, want := store.touched, []int64{1}; !reflect.DeepEqual(got, want) {
		t.Errorf("touched = %v, want %v", got, want)
	}
}

func TestPlanLimitStopsAcrossFollows(t *testing.T) {
	f1 := nodeFollow()       // ID 1
	f2 := instructorFollow() // ID 2
	store := &fakePlannerStore{follows: []database.Follow{f1, f2}}
	exp := fakeExpander{ids: map[int64][]int{
		f1.ID: {1, 2},
		f2.ID: {3, 4},
	}}

	p := &Planner{Store: store, Expander: exp}
	enqueued, err := p.Plan(context.Background(), 3)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	if enqueued != 3 {
		t.Errorf("enqueued = %d, want 3 (capped across follows)", enqueued)
	}
	// First follow fully enqueues (2); the limit is reached partway through the
	// second follow (1 more). Both follows are reached, so both get touched.
	if got, want := enqueuedIDs(store.enqueued), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("enqueued ids = %v, want %v", got, want)
	}
	if got, want := store.touched, []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("touched = %v, want %v", got, want)
	}
}

func TestPlanExpandFailureIsolated(t *testing.T) {
	f1 := nodeFollow()       // ID 1 — expand fails
	f2 := instructorFollow() // ID 2 — succeeds
	store := &fakePlannerStore{follows: []database.Follow{f1, f2}}
	exp := fakeExpander{
		errs: map[int64]error{f1.ID: errors.New("boom")},
		ids:  map[int64][]int{f2.ID: {7, 8}},
	}

	p := &Planner{Store: store, Expander: exp}
	enqueued, err := p.Plan(context.Background(), 0)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	// Only the second follow's lessons are enqueued.
	if enqueued != 2 {
		t.Errorf("enqueued = %d, want 2", enqueued)
	}
	if got, want := enqueuedIDs(store.enqueued), []int{7, 8}; !reflect.DeepEqual(got, want) {
		t.Errorf("enqueued ids = %v, want %v", got, want)
	}
	// The failed follow is NOT touched; the succeeding one is.
	if got, want := store.touched, []int64{2}; !reflect.DeepEqual(got, want) {
		t.Errorf("touched = %v, want %v (failed follow must not be touched)", got, want)
	}
	// No lesson from the failed follow was upserted.
	for _, u := range store.upserts {
		if u.id == 0 {
			t.Errorf("unexpected upsert for failed follow: %+v", u)
		}
	}
}

func TestPlanLogDefaultsToDiscard(t *testing.T) {
	// A Planner with no Log must not panic and must behave identically.
	store := &fakePlannerStore{follows: []database.Follow{nodeFollow()}}
	exp := fakeExpander{ids: map[int64][]int{1: {42}}}

	p := &Planner{Store: store, Expander: exp} // Log left nil
	enqueued, err := p.Plan(context.Background(), 0)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if enqueued != 1 {
		t.Errorf("enqueued = %d, want 1", enqueued)
	}
}
