package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// enqueueCall records one EnqueueJob invocation so tests can assert the exact
// set of lessons that were enqueued, and with which follow id.
type enqueueCall struct {
	followID      sql.NullInt64
	railcontentID int
}

// upsertCall records one UpsertLesson invocation so tests can assert the parent
// linkage (node → follow railcontent id, instructor → NULL) and the follow_id the
// lesson was discovered under.
type upsertCall struct {
	id       int
	title    string
	parent   sql.NullInt64
	brand    string
	position sql.NullInt64
	followID sql.NullInt64
}

// fakePlannerStore is an in-memory Store implementing only the methods the
// Planner exercises. The worker-side methods panic so a stray call is caught.
type fakePlannerStore struct {
	follows []database.Follow

	downloaded map[int]bool // railcontent_id → already downloaded
	skipped    map[int]bool // railcontent_id → recorded as skipped
	active     map[int]bool // railcontent_id → has a queued/running job
	// deleting marks lessons whose files a delete began removing after the
	// planner's ShouldSkipEnqueue read: EnqueueJob refuses them.
	deleting map[int]bool

	enqueued []enqueueCall
	upserts  []upsertCall
	touched  []int64

	nextJobID int64
}

func (s *fakePlannerStore) ListFollows(ctx context.Context) ([]database.Follow, error) {
	return s.follows, nil
}

func (s *fakePlannerStore) UpsertLesson(ctx context.Context, railcontentID int, title string, parent sql.NullInt64, brand string, position sql.NullInt64, followID sql.NullInt64) error {
	s.upserts = append(s.upserts, upsertCall{id: railcontentID, title: title, parent: parent, brand: brand, position: position, followID: followID})
	return nil
}

func (s *fakePlannerStore) IsDownloaded(ctx context.Context, id int) (bool, error) {
	return s.downloaded[id], nil
}

func (s *fakePlannerStore) ShouldSkipEnqueue(ctx context.Context, id int) (bool, error) {
	return s.downloaded[id] || s.skipped[id], nil
}

func (s *fakePlannerStore) ActiveJobExists(ctx context.Context, railcontentID int) (bool, error) {
	return s.active[railcontentID], nil
}

func (s *fakePlannerStore) EnqueueJob(ctx context.Context, followID sql.NullInt64, railcontentID int) (int64, bool, error) {
	if s.active == nil {
		s.active = map[int]bool{}
	}
	if s.deleting[railcontentID] {
		return 0, false, fmt.Errorf("lesson %d: %w", railcontentID, database.ErrLessonDeleting)
	}
	// Mirror the real store's atomic dedup: an already-active lesson returns its
	// existing job with created=false and is not recorded as a fresh enqueue.
	if s.active[railcontentID] {
		return s.nextJobID, false, nil
	}
	s.enqueued = append(s.enqueued, enqueueCall{followID: followID, railcontentID: railcontentID})
	// Mark active so a second pass within the same run would dedupe too.
	s.active[railcontentID] = true
	s.nextJobID++
	return s.nextJobID, true, nil
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
func (s *fakePlannerStore) GetLesson(ctx context.Context, id int) (database.Lesson, error) {
	panic("GetLesson: not expected from Planner")
}
func (s *fakePlannerStore) MarkJobRunning(ctx context.Context, id int64) error {
	panic("MarkJobRunning: not expected from Planner")
}
func (s *fakePlannerStore) ListLessonsWithFiles(ctx context.Context) ([]database.Lesson, error) {
	panic("ListLessonsWithFiles: not expected from Planner")
}
func (s *fakePlannerStore) StartDownload(ctx context.Context, jobID int64, id int) error {
	panic("StartDownload: not expected from Planner")
}
func (s *fakePlannerStore) FinishDownload(ctx context.Context, jobID int64, id int, rec database.DownloadRecord) error {
	panic("FinishDownload: not expected from Planner")
}
func (s *fakePlannerStore) FailDownload(ctx context.Context, jobID int64, id int, msg string) error {
	panic("FailDownload: not expected from Planner")
}
func (s *fakePlannerStore) SkipDownload(ctx context.Context, jobID int64, id int, reason string) error {
	panic("SkipDownload: not expected from Planner")
}
func (s *fakePlannerStore) CancelDownload(ctx context.Context, jobID int64, id int) error {
	panic("CancelDownload: not expected from Planner")
}
func (s *fakePlannerStore) RequeueStaleRunning(ctx context.Context) (int, error) {
	panic("RequeueStaleRunning: not expected from Planner")
}
func (s *fakePlannerStore) ConfirmDownload(ctx context.Context, jobID int64, id int) error {
	panic("ConfirmDownload: not expected from Planner")
}

// fakeExpander returns canned ids (or an error) per follow id. The ids map keeps
// the per-follow lesson ids; titles are synthesized so tests that only assert on
// ids stay unchanged while the planner now receives []LessonItem.
type fakeExpander struct {
	ids  map[int64][]int
	errs map[int64]error
	// titles optionally maps railcontent_id → title; ids without an entry get an
	// empty title, preserving the prior id-only behavior for existing tests.
	titles map[int]string
}

func (e fakeExpander) Expand(f database.Follow, permIDs string) ([]musora.LessonItem, error) {
	if err := e.errs[f.ID]; err != nil {
		return nil, err
	}
	ids := e.ids[f.ID]
	items := make([]musora.LessonItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, musora.LessonItem{ID: id, Title: e.titles[id]})
	}
	return items, nil
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

func TestPlanDoesNotReEnqueueSkipped(t *testing.T) {
	// A lesson recorded as skipped must NOT be re-enqueued, while a failed lesson
	// is still retried. Both are still upserted (record-keeping).
	store := &fakePlannerStore{
		follows: []database.Follow{nodeFollow()},
		skipped: map[int]bool{
			21: true, // skipped → recorded but never re-enqueued
		},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {20, 21, 22}}}

	p := &Planner{Store: store, Expander: exp, PermIDs: "perm"}
	enqueued, err := p.Plan(context.Background(), 0)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	// 20 (new) and 22 (failed, still retried) enqueue; 21 (skipped) does not.
	if enqueued != 2 {
		t.Errorf("enqueued = %d, want 2 (skipped 21 excluded)", enqueued)
	}
	if got, want := enqueuedIDs(store.enqueued), []int{20, 22}; !reflect.DeepEqual(got, want) {
		t.Errorf("enqueued ids = %v, want %v (skipped must not re-enqueue)", got, want)
	}
	// Every lesson is still upserted, including the skipped one.
	if len(store.upserts) != 3 {
		t.Errorf("upserts = %d, want 3 (all seen lessons recorded)", len(store.upserts))
	}
}

// TestPlanSkipsALessonBeingDeleted proves a lesson whose files a delete began
// removing between the planner's skip check and its enqueue is skipped, not a
// failed cycle: the other lessons are still enqueued.
func TestPlanSkipsALessonBeingDeleted(t *testing.T) {
	store := &fakePlannerStore{
		follows:  []database.Follow{nodeFollow()},
		deleting: map[int]bool{31: true},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {30, 31, 32}}}
	p := &Planner{Store: store, Expander: exp, PermIDs: "perm"}
	enqueued, err := p.Plan(context.Background(), 0)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if got, want := enqueuedIDs(store.enqueued), []int{30, 32}; enqueued != 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("enqueued %d: %v, want 2: %v", enqueued, got, want)
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
	followIDs := map[int]sql.NullInt64{}
	for _, u := range store.upserts {
		parents[u.id] = u.parent
		brands[u.id] = u.brand
		followIDs[u.id] = u.followID
	}

	// Node follow → parent is its railcontent id.
	if got := parents[100]; !got.Valid || got.Int64 != node.RailcontentID.Int64 {
		t.Errorf("node lesson parent = %+v, want valid %d", got, node.RailcontentID.Int64)
	}
	// Instructor follow → parent is NULL (invalid).
	if got := parents[200]; got.Valid {
		t.Errorf("instructor lesson parent = %+v, want invalid/NULL", got)
	}

	// Every lesson is upserted with the id of the follow it was discovered under,
	// regardless of kind (the parent linkage differs, the follow_id does not).
	if got := followIDs[100]; !got.Valid || got.Int64 != node.ID {
		t.Errorf("node lesson follow_id = %+v, want valid %d", got, node.ID)
	}
	if got := followIDs[200]; !got.Valid || got.Int64 != inst.ID {
		t.Errorf("instructor lesson follow_id = %+v, want valid %d", got, inst.ID)
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

func TestPlanDryRunRecordsButEnqueuesNothing(t *testing.T) {
	// PlanDryRun expands + upserts every lesson (record-keeping) but enqueues
	// NOTHING and stamps NO follow, while reporting the would-be queue count with
	// the same dedupe/skip rules as a real Plan.
	store := &fakePlannerStore{
		follows: []database.Follow{nodeFollow()},
		downloaded: map[int]bool{
			11: true, // already downloaded → not counted
		},
		active: map[int]bool{
			12: true, // already queued/running → deduped, not counted
		},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {10, 11, 12, 13}}}

	p := &Planner{Store: store, Expander: exp, PermIDs: "perm"}
	would, err := p.PlanDryRun(context.Background())
	if err != nil {
		t.Fatalf("PlanDryRun returned error: %v", err)
	}

	// Only 10 and 13 would be queued (11 downloaded, 12 has an active job).
	if would != 2 {
		t.Errorf("wouldEnqueue = %d, want 2", would)
	}
	// Nothing was actually enqueued.
	if len(store.enqueued) != 0 {
		t.Errorf("PlanDryRun must enqueue nothing; enqueued=%v", store.enqueued)
	}
	// Every lesson is still upserted (record-keeping).
	if len(store.upserts) != 4 {
		t.Errorf("upserts = %d, want 4 (all seen lessons recorded)", len(store.upserts))
	}
	// No follow is stamped — nothing was actually synced.
	if len(store.touched) != 0 {
		t.Errorf("PlanDryRun must not stamp last_synced; touched=%v", store.touched)
	}
}

func TestPlanAndDryRunAgreeOnDuplicateAcrossFollows(t *testing.T) {
	// The same lesson id appears under two follows. A real Plan enqueues it once
	// (the second occurrence is deduped by ActiveJobExists after the first
	// EnqueueJob). PlanDryRun enqueues nothing, so without an in-run seen-set it
	// would double-count the duplicate; the seen-set must make its count match
	// Plan's.
	f1 := nodeFollow()       // ID 1
	f2 := instructorFollow() // ID 2

	// Same id (500) under both follows, plus a unique id per follow.
	expIDs := map[int64][]int{
		f1.ID: {500, 501},
		f2.ID: {500, 502},
	}

	planStore := &fakePlannerStore{follows: []database.Follow{f1, f2}}
	planExp := fakeExpander{ids: expIDs}
	planP := &Planner{Store: planStore, Expander: planExp, PermIDs: "perm"}
	enqueued, err := planP.Plan(context.Background(), 0)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	dryStore := &fakePlannerStore{follows: []database.Follow{f1, f2}}
	dryExp := fakeExpander{ids: expIDs}
	dryP := &Planner{Store: dryStore, Expander: dryExp, PermIDs: "perm"}
	would, err := dryP.PlanDryRun(context.Background())
	if err != nil {
		t.Fatalf("PlanDryRun returned error: %v", err)
	}

	// Three distinct lessons (500, 501, 502): the duplicate 500 counts once.
	if enqueued != 3 {
		t.Errorf("Plan enqueued = %d, want 3 (duplicate 500 counted once)", enqueued)
	}
	if would != enqueued {
		t.Errorf("PlanDryRun = %d, Plan = %d; they must agree", would, enqueued)
	}
	// Plan actually enqueued 500 exactly once.
	if got, want := enqueuedIDs(planStore.enqueued), []int{500, 501, 502}; !reflect.DeepEqual(got, want) {
		t.Errorf("enqueued ids = %v, want %v", got, want)
	}
}

func TestPlanUpsertsTitleAndPosition(t *testing.T) {
	// Every lesson is recorded with its resolved title and a 1-based position
	// matching its place in expansion order (so the worker's "NN -" prefix lines
	// up). The downloaded/skipped/active lessons are still upserted with their
	// position, because record-keeping happens before any dedup.
	store := &fakePlannerStore{
		follows: []database.Follow{nodeFollow()},
		downloaded: map[int]bool{
			11: true, // still upserted with position, just not enqueued
		},
	}
	exp := fakeExpander{
		ids: map[int64][]int{1: {10, 11, 12}},
		titles: map[int]string{
			10: "Intro",
			11: "Warmup",
			12: "Finale",
		},
	}

	p := &Planner{Store: store, Expander: exp, PermIDs: "perm"}
	if _, err := p.Plan(context.Background(), 0); err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	wantTitle := map[int]string{10: "Intro", 11: "Warmup", 12: "Finale"}
	wantPos := map[int]int64{10: 1, 11: 2, 12: 3}

	if len(store.upserts) != 3 {
		t.Fatalf("upserts = %d, want 3", len(store.upserts))
	}
	for _, u := range store.upserts {
		if got, want := u.title, wantTitle[u.id]; got != want {
			t.Errorf("lesson %d title = %q, want %q", u.id, got, want)
		}
		if !u.position.Valid {
			t.Errorf("lesson %d position invalid, want valid %d", u.id, wantPos[u.id])
			continue
		}
		if got, want := u.position.Int64, wantPos[u.id]; got != want {
			t.Errorf("lesson %d position = %d, want %d", u.id, got, want)
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
