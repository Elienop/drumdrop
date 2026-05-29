package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// ---- flag handling -------------------------------------------------------

// TestFollowFlagSplitting proves --brand is honored alongside the existing
// value flags whether it appears before or after the positional target.
func TestFollowFlagSplitting(t *testing.T) {
	if !valueFlags["--brand"] {
		t.Fatal("--brand must be registered in valueFlags so its value is not orphaned")
	}

	parse := func(argv []string) (target, brand, quality, instructor string, err error) {
		fs := flag.NewFlagSet("follow", flag.ContinueOnError)
		b := fs.String("brand", "drumeo", "")
		q := fs.String("quality", "best", "")
		ins := fs.String("instructor", "", "")
		positionals, flags := splitArgs(argv)
		if err = fs.Parse(flags); err != nil {
			return
		}
		if len(positionals) > 0 {
			target = positionals[0]
		}
		return target, *b, *q, *ins, nil
	}

	t.Run("brand value after target is kept", func(t *testing.T) {
		target, brand, quality, _, err := parse([]string{"409875", "--brand", "pianote", "--quality", "1080"})
		if err != nil {
			t.Fatal(err)
		}
		if target != "409875" {
			t.Errorf("target = %q, want 409875", target)
		}
		if brand != "pianote" {
			t.Errorf("brand = %q, want pianote", brand)
		}
		if quality != "1080" {
			t.Errorf("quality = %q, want 1080", quality)
		}
	})

	t.Run("instructor flag", func(t *testing.T) {
		_, _, _, instructor, err := parse([]string{"--instructor", "aaron-edgar"})
		if err != nil {
			t.Fatal(err)
		}
		if instructor != "aaron-edgar" {
			t.Errorf("instructor = %q, want aaron-edgar", instructor)
		}
	})
}

// ---- pure decision helper ------------------------------------------------

func TestShouldDownload(t *testing.T) {
	cases := []struct {
		isDownloaded bool
		dryRun       bool
		want         bool
	}{
		{isDownloaded: false, dryRun: false, want: true}, // fresh, real run -> download
		{isDownloaded: true, dryRun: false, want: false}, // already downloaded -> skip (dedup)
		{isDownloaded: false, dryRun: true, want: false}, // dry-run -> never download
		{isDownloaded: true, dryRun: true, want: false},  // both -> skip
	}
	for _, c := range cases {
		if got := shouldDownload(c.isDownloaded, c.dryRun); got != c.want {
			t.Errorf("shouldDownload(downloaded=%v, dryRun=%v) = %v, want %v",
				c.isDownloaded, c.dryRun, got, c.want)
		}
	}
}

// ---- fakes ---------------------------------------------------------------

// fakeStore records the sync-relevant mutations and answers IsDownloaded from a
// preset set, so runSync can be exercised with no real database.
type fakeStore struct {
	follows    []database.Follow
	downloaded map[int]bool // ids already downloaded

	upserts       []int
	enqueued      []int
	markedDLing   []int
	markedDLed    []int
	markedFailed  []int
	markedSkipped []int
	touched       []int64
	jobsRun       int
	jobsDone      int
	jobsFailed    int
	nextJobID     int64
}

func (f *fakeStore) ListFollows(ctx context.Context) ([]database.Follow, error) {
	return f.follows, nil
}
func (f *fakeStore) UpsertLesson(ctx context.Context, id int, title string, parent sql.NullInt64, brand string) error {
	f.upserts = append(f.upserts, id)
	return nil
}
func (f *fakeStore) IsDownloaded(ctx context.Context, id int) (bool, error) {
	return f.downloaded[id], nil
}
func (f *fakeStore) EnqueueJob(ctx context.Context, followID sql.NullInt64, id int) (int64, error) {
	f.enqueued = append(f.enqueued, id)
	f.nextJobID++
	return f.nextJobID, nil
}
func (f *fakeStore) MarkJobRunning(ctx context.Context, id int64) error { f.jobsRun++; return nil }
func (f *fakeStore) MarkJobDone(ctx context.Context, id int64) error    { f.jobsDone++; return nil }
func (f *fakeStore) MarkJobFailed(ctx context.Context, id int64, msg string) error {
	f.jobsFailed++
	return nil
}
func (f *fakeStore) MarkDownloading(ctx context.Context, id int) error {
	f.markedDLing = append(f.markedDLing, id)
	return nil
}
func (f *fakeStore) MarkDownloaded(ctx context.Context, id int, q, dir, vp string, b int64) error {
	f.markedDLed = append(f.markedDLed, id)
	f.downloaded[id] = true
	return nil
}
func (f *fakeStore) MarkFailed(ctx context.Context, id int, msg string) error {
	f.markedFailed = append(f.markedFailed, id)
	return nil
}
func (f *fakeStore) MarkSkipped(ctx context.Context, id int, reason string) error {
	f.markedSkipped = append(f.markedSkipped, id)
	return nil
}
func (f *fakeStore) TouchLastSynced(ctx context.Context, id int64) error {
	f.touched = append(f.touched, id)
	return nil
}

// fakeExpander returns a fixed id list per follow.
type fakeExpander struct {
	ids map[int64][]int // keyed by follow.ID
	err error
}

func (e fakeExpander) Expand(f database.Follow, permIDs string) ([]int, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.ids[f.ID], nil
}

// fakeResolver returns a stub lesson for every id (or nil for ids in the
// unresolvable set) without touching the network.
type fakeResolver struct {
	unresolvable map[int]bool
	err          error
}

func (r fakeResolver) Resolve(id int, permIDs string) (*musora.Lesson, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.unresolvable[id] {
		return nil, nil
	}
	return &musora.Lesson{ID: id, Title: fmt.Sprintf("Lesson %d", id)}, nil
}

// fakeDownloader records each download and never touches yt-dlp.
type fakeDownloader struct {
	calls   []int
	failIDs map[int]bool
}

func (d *fakeDownloader) Download(l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls = append(d.calls, l.ID)
	if d.failIDs[l.ID] {
		return errors.New("boom")
	}
	return nil
}

func nodeFollow(id int64, rc int) database.Follow {
	return database.Follow{
		ID:            id,
		Kind:          "node",
		RailcontentID: sql.NullInt64{Int64: int64(rc), Valid: true},
		Title:         "Course",
		Brand:         "drumeo",
		Quality:       "best",
	}
}

// ---- runSync behavior ----------------------------------------------------

// TestSyncSkipsAlreadyDownloaded: an id already downloaded is never enqueued or
// handed to the downloader, but it is still upserted (so its metadata refreshes)
// and reported as skipped.
func TestSyncSkipsAlreadyDownloaded(t *testing.T) {
	store := &fakeStore{
		follows:    []database.Follow{nodeFollow(1, 100)},
		downloaded: map[int]bool{11: true},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {11, 12}}}
	dl := &fakeDownloader{}
	deps := syncDeps{store: store, expander: exp, resolver: fakeResolver{}, downloader: dl}

	var buf bytes.Buffer
	if err := runSync(context.Background(), deps, syncOpts{out: "/tmp/x"}, &buf); err != nil {
		t.Fatal(err)
	}

	if len(store.upserts) != 2 {
		t.Errorf("upserts = %v, want both ids upserted", store.upserts)
	}
	if len(dl.calls) != 1 || dl.calls[0] != 12 {
		t.Errorf("downloaded = %v, want only [12] (11 was already downloaded)", dl.calls)
	}
	if !containsInt(store.markedDLed, 12) {
		t.Errorf("MarkDownloaded ids = %v, want 12", store.markedDLed)
	}
	if !strings.Contains(buf.String(), "already downloaded 11") {
		t.Errorf("output should log the skip; got:\n%s", buf.String())
	}
}

// TestSyncLimitCapsNewDownloads: --limit caps the number of NEW downloads across
// the whole run, regardless of how many follows/ids remain.
func TestSyncLimitCapsNewDownloads(t *testing.T) {
	store := &fakeStore{
		follows:    []database.Follow{nodeFollow(1, 100), nodeFollow(2, 200)},
		downloaded: map[int]bool{},
	}
	exp := fakeExpander{ids: map[int64][]int{
		1: {11, 12, 13},
		2: {21, 22},
	}}
	dl := &fakeDownloader{}
	deps := syncDeps{store: store, expander: exp, resolver: fakeResolver{}, downloader: dl}

	var buf bytes.Buffer
	if err := runSync(context.Background(), deps, syncOpts{out: "/tmp/x", limit: 2}, &buf); err != nil {
		t.Fatal(err)
	}

	if len(dl.calls) != 2 {
		t.Errorf("downloaded %d lessons, want exactly 2 (the --limit cap); calls=%v", len(dl.calls), dl.calls)
	}
}

// TestSyncDryRunDownloadsNothing: dry-run upserts every id but downloads none,
// enqueues no jobs, and marks nothing downloaded.
func TestSyncDryRunDownloadsNothing(t *testing.T) {
	store := &fakeStore{
		follows:    []database.Follow{nodeFollow(1, 100)},
		downloaded: map[int]bool{},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {11, 12, 13}}}
	dl := &fakeDownloader{}
	deps := syncDeps{store: store, expander: exp, resolver: fakeResolver{}, downloader: dl}

	var buf bytes.Buffer
	if err := runSync(context.Background(), deps, syncOpts{out: "/tmp/x", dryRun: true}, &buf); err != nil {
		t.Fatal(err)
	}

	if len(store.upserts) != 3 {
		t.Errorf("dry-run should still upsert all ids; upserts=%v", store.upserts)
	}
	if len(dl.calls) != 0 {
		t.Errorf("dry-run must download nothing; downloaded=%v", dl.calls)
	}
	if len(store.enqueued) != 0 {
		t.Errorf("dry-run must enqueue no jobs; enqueued=%v", store.enqueued)
	}
	if len(store.markedDLed) != 0 {
		t.Errorf("dry-run must mark nothing downloaded; markedDLed=%v", store.markedDLed)
	}
}

// TestSyncUnresolvableLessonSkippedNotAborting: a lesson that resolves to nil is
// marked skipped (job failed) and the run continues to the next lesson.
func TestSyncUnresolvableLessonSkippedNotAborting(t *testing.T) {
	store := &fakeStore{
		follows:    []database.Follow{nodeFollow(1, 100)},
		downloaded: map[int]bool{},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {11, 12}}}
	res := fakeResolver{unresolvable: map[int]bool{11: true}}
	dl := &fakeDownloader{}
	deps := syncDeps{store: store, expander: exp, resolver: res, downloader: dl}

	var buf bytes.Buffer
	if err := runSync(context.Background(), deps, syncOpts{out: "/tmp/x"}, &buf); err != nil {
		t.Fatal(err)
	}

	if !containsInt(store.markedSkipped, 11) {
		t.Errorf("unresolvable lesson 11 should be marked skipped; markedSkipped=%v", store.markedSkipped)
	}
	if len(dl.calls) != 1 || dl.calls[0] != 12 {
		t.Errorf("run should continue past the unresolvable lesson; downloaded=%v", dl.calls)
	}
	if store.jobsFailed == 0 {
		t.Error("the unresolvable lesson's job should be marked failed")
	}
}

// TestSyncDownloadFailureRecordedNotAborting: a download error marks the lesson
// + job failed and the run continues.
func TestSyncDownloadFailureRecordedNotAborting(t *testing.T) {
	store := &fakeStore{
		follows:    []database.Follow{nodeFollow(1, 100)},
		downloaded: map[int]bool{},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {11, 12}}}
	dl := &fakeDownloader{failIDs: map[int]bool{11: true}}
	deps := syncDeps{store: store, expander: exp, resolver: fakeResolver{}, downloader: dl}

	var buf bytes.Buffer
	if err := runSync(context.Background(), deps, syncOpts{out: "/tmp/x"}, &buf); err != nil {
		t.Fatal(err)
	}

	if !containsInt(store.markedFailed, 11) {
		t.Errorf("failed download 11 should be marked failed; markedFailed=%v", store.markedFailed)
	}
	if !containsInt(store.markedDLed, 12) {
		t.Errorf("run should continue and download 12; markedDLed=%v", store.markedDLed)
	}
}

// TestSyncTouchesEveryProcessedFollow: each follow that was successfully expanded
// gets its last_synced stamped.
func TestSyncTouchesEveryProcessedFollow(t *testing.T) {
	store := &fakeStore{
		follows:    []database.Follow{nodeFollow(1, 100), nodeFollow(2, 200)},
		downloaded: map[int]bool{},
	}
	exp := fakeExpander{ids: map[int64][]int{1: {11}, 2: {21}}}
	deps := syncDeps{store: store, expander: exp, resolver: fakeResolver{}, downloader: &fakeDownloader{}}

	var buf bytes.Buffer
	if err := runSync(context.Background(), deps, syncOpts{out: "/tmp/x"}, &buf); err != nil {
		t.Fatal(err)
	}
	if len(store.touched) != 2 {
		t.Errorf("both follows should be touched; touched=%v", store.touched)
	}
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
