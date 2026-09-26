package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// Sanity-shaped image URLs (their names carry their size, as Sanity's do).
const (
	artHeader = "https://cdn.sanity.io/images/p/d/header-4500x4500.png"
	artThumb  = "https://cdn.sanity.io/images/p/d/thumb-1920x1080.png"
	artCoach  = "https://cdn.sanity.io/images/p/d/coach-800x1200.png"
	artSquare = "https://cdn.sanity.io/images/p/d/song-1500x1500.jpg"
)

// fakeImages serves every URL as "jpeg:<url>", or the error errs names for
// it, and records each fetch.
type fakeImages struct {
	mu    sync.Mutex
	errs  map[string]error
	calls []string
}

func (f *fakeImages) FetchJPEG(_ context.Context, u string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, u)
	if err := f.errs[u]; err != nil {
		return nil, err
	}
	return []byte("jpeg:" + u), nil
}

func (f *fakeImages) fetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// countingResolver answers from docs (nil for an unknown id), or errs, and
// records each id asked for.
type countingResolver struct {
	mu    sync.Mutex
	docs  map[int]*musora.Lesson
	errs  map[int]error
	calls []int
}

func (r *countingResolver) Resolve(id int, _ string) (*musora.Lesson, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, id)
	if err := r.errs[id]; err != nil {
		return nil, err
	}
	return r.docs[id], nil
}

func (r *countingResolver) asked() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.calls...)
}

// courseDoc is a guided course: a square header, a wide thumbnail, and an
// instructor with a coach card.
func courseDoc(id int, title string) *musora.Lesson {
	return &musora.Lesson{
		ID: id, Title: title, Description: "<p>A course.</p>", Brand: "drumeo", PublishedOn: "2024-01-02T00:00:00Z",
		Thumbnail: artThumb, HeaderImageURL: artHeader,
		Instructors: []musora.Instructor{{Name: "Coach", CoachCardImage: artCoach}},
	}
}

// readFile is the content of path, or "" when it can not be read.
func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// showFileNames is every entry of the show folder dir that is not a season
// folder, sorted.
func showFileNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "Season ") {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out
}

// TestSlotsOf pins which names fill a show's slots (owner ruling #78, 3):
// every name Plex reads for the poster (poster, folder, show; .jpg, .jpeg,
// .png, .tbn) and for the background (fanart, art, backdrop, background; any
// extension), and tvshow.nfo, without regard to case.
func TestSlotsOf(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  showSlots
	}{
		{nil, showSlots{}},
		{[]string{"Season 01", "poster.jpg"}, showSlots{poster: true}},
		{[]string{"Folder.PNG"}, showSlots{poster: true}},
		{[]string{"show.tbn"}, showSlots{poster: true}},
		{[]string{"poster.jpeg", "fanart.jpg", "tvshow.nfo"}, showSlots{poster: true, fanart: true, nfo: true}},
		{[]string{"BACKDROP.jpeg"}, showSlots{fanart: true}},
		{[]string{"art.webp"}, showSlots{fanart: true}},
		{[]string{"background.png"}, showSlots{fanart: true}},
		{[]string{"TVShow.NFO"}, showSlots{nfo: true}},
		// Not those slots: another extension, another stem, no extension.
		{[]string{"poster.webp", "poster", "posters.jpg", "fanart", "banner.jpg", "tvshow.nfo.bak", "movie.nfo"}, showSlots{}},
	} {
		if got := slotsOf(tc.names); got != tc.want {
			t.Errorf("slotsOf(%q) = %+v, want %+v", tc.names, got, tc.want)
		}
	}
}

// TestCreateOnlyNeverReplacesAndNeverLeavesAPart proves a show file is
// created whole or not at all: its bytes go to a hidden file first, which is
// renamed into place only if nothing is there (an entry that appears at the
// name meanwhile is kept), and a failed write leaves nothing behind.
func TestCreateOnlyNeverReplacesAndNeverLeavesAPart(t *testing.T) {
	t.Run("created through a hidden file", testCreateOnlyThroughAHiddenFile)
	t.Run("an existing entry is kept", testCreateOnlyKeepsAnExistingEntry)
	t.Run("an entry that appears meanwhile is kept", testCreateOnlyKeepsAnEntryThatAppears)
	t.Run("a failed rename leaves nothing", testCreateOnlyFailedRenameLeavesNothing)
	t.Run("a leftover part is replaced", testCreateOnlyReplacesALeftoverPart)
}

// openTempRoot is a new temporary folder, held open, and its path.
func openTempRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}

// assertOnlyNames fails unless dir holds exactly the entries want, in name
// order.
func assertOnlyNames(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s holds %q, want %q", dir, got, want)
	}
}

func testCreateOnlyThroughAHiddenFile(t *testing.T) {
	r, dir := openTempRoot(t)
	var renamed, flushed []string
	origSync := syncFile
	syncFile = func(f *os.File) error {
		flushed = append(flushed, filepath.Base(f.Name()))
		return origSync(f)
	}
	t.Cleanup(func() { syncFile = origSync })
	stubRename(t, func(oldpath, newpath string) error {
		// The file is whole, and on disk, before it takes its name, and
		// nothing is at the name yet: a crash can only ever leave the
		// hidden file.
		if readFile(oldpath) != "image" {
			t.Errorf("renamed %s holding %q, want the whole file", oldpath, readFile(oldpath))
		}
		if !slices.Contains(flushed, filepath.Base(oldpath)) {
			t.Errorf("renamed %s before it was flushed (flushed %v)", oldpath, flushed)
		}
		renamed = append(renamed, filepath.Base(oldpath)+" -> "+filepath.Base(newpath))
		return renameNoReplace(oldpath, newpath)
	})
	created, err := createOnly(r, "poster.jpg", []byte("image"))
	if !created || err != nil {
		t.Fatalf("createOnly = %v, %v; want created", created, err)
	}
	if want := []string{".poster.jpg." + tempWriter + ".drumdrop-part -> poster.jpg"}; !reflect.DeepEqual(renamed, want) {
		t.Errorf("renames %q, want %q", renamed, want)
	}
	if got := readFile(filepath.Join(dir, "poster.jpg")); got != "image" {
		t.Errorf("poster.jpg = %q", got)
	}
	assertOnlyNames(t, dir, "poster.jpg")
}

func testCreateOnlyKeepsAnExistingEntry(t *testing.T) {
	r, dir := openTempRoot(t)
	seedSeason(t, dir, "poster.jpg")
	created, err := createOnly(r, "poster.jpg", []byte("image"))
	if created || err != nil {
		t.Errorf("createOnly = %v, %v; want nothing created, no error", created, err)
	}
	assertContent(t, dir, "poster.jpg")
	assertOnlyNames(t, dir, "poster.jpg")
}

func testCreateOnlyKeepsAnEntryThatAppears(t *testing.T) {
	r, dir := openTempRoot(t)
	stubRename(t, func(oldpath, newpath string) error {
		seedSeason(t, dir, "poster.jpg") // the owner's, just before the rename
		return renameNoReplace(oldpath, newpath)
	})
	created, err := createOnly(r, "poster.jpg", []byte("image"))
	if created || err != nil {
		t.Errorf("createOnly = %v, %v; want nothing created, no error", created, err)
	}
	assertContent(t, dir, "poster.jpg")
	assertOnlyNames(t, dir, "poster.jpg")
}

func testCreateOnlyFailedRenameLeavesNothing(t *testing.T) {
	r, dir := openTempRoot(t)
	stubRename(t, func(string, string) error { return errors.New("injected") })
	created, err := createOnly(r, "poster.jpg", []byte("image"))
	if created || err == nil {
		t.Errorf("createOnly = %v, %v; want the failure", created, err)
	}
	assertOnlyNames(t, dir)
}

func testCreateOnlyReplacesALeftoverPart(t *testing.T) {
	r, dir := openTempRoot(t)
	seedSeason(t, dir, createTempName("poster.jpg")) // this writer's crash's
	if created, err := createOnly(r, "poster.jpg", []byte("image")); !created || err != nil {
		t.Fatalf("createOnly = %v, %v; want created", created, err)
	}
	assertOnlyNames(t, dir, "poster.jpg")
	if got := readFile(filepath.Join(dir, "poster.jpg")); got != "image" {
		t.Errorf("poster.jpg = %q", got)
	}
}

// TestCreateOnlyNeverPublishesAnotherWritersFile pins that two writers of one
// slot (drumdrop sync beside serve: two processes, one library, here each
// process 1 of its own container, as the image runs it) never touch each
// other's unfinished file: B starts while A's file is written but not
// yet renamed, and gets as far as writing its own; A must still publish its
// own flushed bytes, never B's unflushed ones (which, write-once, would stay
// truncated forever if B then died), and B finds the slot filled.
func TestCreateOnlyNeverPublishesAnotherWritersFile(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	origSync, origWriter := syncFile, tempWriter
	t.Cleanup(func() { syncFile, tempWriter = origSync, origWriter })

	bWrote := make(chan struct{})
	aDone := make(chan struct{})
	type result struct {
		created bool
		err     error
	}
	bResult := make(chan result, 1)
	// The temp files are flushed in turn: A's first, then B's (whatever their
	// names); a folder's flush is let through.
	var temps atomic.Int32
	syncFile = func(f *os.File) error {
		if !strings.HasSuffix(f.Name(), musora.TempSuffix) {
			return origSync(f)
		}
		switch temps.Add(1) {
		case 1: // A's file is written: B, a second process, starts
			tempWriter = writerID("container-b", 1)
			go func() {
				created, err := createOnly(r, "poster.jpg", []byte("B's, unflushed"))
				bResult <- result{created, err}
			}()
			<-bWrote
		case 2: // B has written its file and not flushed it, until A is done
			close(bWrote)
			<-aDone
		}
		return origSync(f)
	}
	tempWriter = writerID("container-a", 1)
	created, err := createOnly(r, "poster.jpg", []byte("A's"))
	close(aDone)
	b := <-bResult
	if !created || err != nil {
		t.Errorf("A: createOnly = %v, %v; want its file created", created, err)
	}
	if b.created || b.err != nil {
		t.Errorf("B: createOnly = %v, %v; want the slot found filled", b.created, b.err)
	}
	if got := readFile(filepath.Join(dir, "poster.jpg")); got != "A's" {
		t.Errorf("poster.jpg = %q, want A's own flushed bytes", got)
	}
	if got := showFileNames(t, dir); !reflect.DeepEqual(got, []string{"poster.jpg"}) {
		t.Errorf("folder holds %v, want poster.jpg only", got)
	}
}

// TestWriterID pins what tells one writer's hidden files from another's: the
// host name and the process id, so two containers each running drumdrop as
// process 1 differ; a host name is cut to letters, digits and "-" and to
// maxWriterHost bytes (it goes into a file name), and a process with no host
// name known is told apart by its id alone. This process's id is its own.
func TestWriterID(t *testing.T) {
	for _, tc := range []struct {
		host string
		pid  int
		want string
	}{
		{"3f2a9c1b7e4d", 1, "3f2a9c1b7e4d.1"},
		{"drumdrop-sync", 1, "drumdrop-sync.1"},
		{"nas.local", 42, "naslocal.42"},
		{"../a\\/b\n\x00é", 7, "ab.7"},
		{strings.Repeat("h", 40), 1, strings.Repeat("h", maxWriterHost) + ".1"},
		{"", 9, "9"},
		{"..", 9, "9"},
	} {
		if got := writerID(tc.host, tc.pid); got != tc.want {
			t.Errorf("writerID(%q, %d) = %q, want %q", tc.host, tc.pid, got, tc.want)
		}
	}
	if writerID("container-a", 1) == writerID("container-b", 1) {
		t.Error("two containers' process 1 share a writer id")
	}
	h, err := os.Hostname()
	if err != nil {
		h = ""
	}
	if want := writerID(h, os.Getpid()); tempWriter != want {
		t.Errorf("tempWriter = %q, want %q (this host, this process)", tempWriter, want)
	}
}

// TestShowFilesLeaveTheNFOOutWhenAnImageIsNotWritten proves tvshow.nfo, the
// mark of a done show, is written only once every image it was given is in
// place: an image that could not be written leaves the show to the next
// cycle.
func TestShowFilesLeaveTheNFOOutWhenAnImageIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	stubRename(t, func(oldpath, newpath string) error {
		if filepath.Base(newpath) == "poster.jpg" {
			return errors.New("injected")
		}
		return renameNoReplace(oldpath, newpath)
	})
	sf := &showFiles{poster: []byte("p"), fanart: []byte("f"), nfo: []byte("<tvshow/>")}
	created, err := sf.writeIn(r)
	if err == nil || !reflect.DeepEqual(created, []string{"fanart.jpg"}) {
		t.Errorf("writeIn = %v, %v; want the background only, and the failure", created, err)
	}
	if got := showFileNames(t, dir); !reflect.DeepEqual(got, []string{"fanart.jpg"}) {
		t.Errorf("show folder holds %v, want the background only", got)
	}
}

// artWorker is plexWorker (lesson 100 "Lesson A", episode 5 of the node
// follow "Beginner Course", 4242) with the show's course on Musora and an
// image server: lesson 100 is in a pack of its own ("Some Pack"), which is
// not the show.
func artWorker(t *testing.T) (w *Worker, store *fakeWorkerStore, dl *fakeDownloader, res *countingResolver, img *fakeImages, lib, season string) {
	t.Helper()
	w, store, dl, lib, season = plexWorker(t)
	lessonA := lesson(100, "Lesson A")
	lessonA.ParentContentData = []musora.ParentContent{{ID: 55, Title: "Some Pack"}}
	res = &countingResolver{docs: map[int]*musora.Lesson{100: lessonA, 4242: courseDoc(4242, "Beginner Course (on Musora)"), 55: courseDoc(55, "Some Pack")}}
	img = &fakeImages{}
	w.Resolver, w.Images = res, img
	return w, store, dl, res, img, lib, season
}

// withPoster makes each download also write its image and captions.
func withPoster(dl *fakeDownloader) {
	dl.afterWrite = func(dir string) {
		base := filepath.Base(dir)
		for _, n := range []string{base + "-poster.jpg", base + ".en.vtt"} {
			_ = os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644)
		}
	}
}

// TestPlexTVPlacementWritesTheShowFilesBeforeTheVideo pins invariant 1 of the
// show-artwork change: Plex picks local artwork only when it first matches a
// show or an episode, so the show's tvshow.nfo, poster.jpg and fanart.jpg,
// and the episode's image, nfo and captions, are all in place before the
// episode's video is. The show's files come from the followed node (not the
// lesson's pack), are never recorded as the lesson's, and the episode image
// is "<episode base>.jpg". For a song, each version's own image and nfo,
// "<episode base> [Label].jpg" and ".nfo", are there before either video.
func TestPlexTVPlacementWritesTheShowFilesBeforeTheVideo(t *testing.T) {
	for _, song := range []bool{false, true} {
		t.Run(fmt.Sprintf("song=%v", song), func(t *testing.T) { checkShowFilesBeforeTheVideo(t, song) })
	}
}

// checkShowFilesBeforeTheVideo is TestPlexTVPlacementWritesTheShowFilesBeforeTheVideo
// for lesson 100 as a song or not.
func checkShowFilesBeforeTheVideo(t *testing.T, song bool) {
	w, store, dl, res, _, lib, season := artWorker(t)
	if song {
		res.docs[100].Soundslice = []musora.SoundsliceRef{{Slug: "1"}}
	}
	withPoster(dl)
	show := filepath.Join(lib, "Beginner Course")
	base := filepath.Join(season, sameTitleBase)
	before := []string{filepath.Join(show, "tvshow.nfo"), filepath.Join(show, "poster.jpg"), filepath.Join(show, "fanart.jpg"), base + ".jpg", base + ".nfo", base + ".en.vtt"}
	if song {
		before = []string{before[0], before[1], before[2],
			base + " [Drumless].jpg", base + " [Drumless].nfo", base + " [Original].jpg", base + " [Original].nfo"}
	}
	videos := countVideosPlacedAfter(t, before)
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if want := map[bool]int{false: 1, true: 2}[song]; *videos != want {
		t.Fatalf("%d videos placed, want %d", *videos, want)
	}
	assertCourseShowFiles(t, show)
	assertExist(t, false, base+"-poster.jpg")
	image := "Beginner Course/Season 01/" + sameTitleBase + ".jpg"
	if song {
		image = "Beginner Course/Season 01/" + sameTitleBase + " [Original].jpg"
	}
	assertRecordNamesTheEpisodeImage(t, store, image)
}

// countVideosPlacedAfter stubs the rename so that placing a video (".mp4")
// fails the test unless every path in before is there already; it returns
// the count of videos placed.
func countVideosPlacedAfter(t *testing.T, before []string) *int {
	t.Helper()
	videos := 0
	stubRename(t, func(oldpath, newpath string) error {
		if strings.HasSuffix(newpath, ".mp4") {
			videos++
			assertPlacedBefore(t, filepath.Base(newpath), before)
		}
		return renameNoReplace(oldpath, newpath)
	})
	return &videos
}

// assertPlacedBefore fails unless every path in before is there as video is
// placed.
func assertPlacedBefore(t *testing.T, video string, before []string) {
	t.Helper()
	for _, p := range before {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s placed before %s", video, p)
		}
	}
}

// assertCourseShowFiles fails unless the show folder show holds the course's
// (courseDoc's) poster, background and tvshow.nfo, the show named
// "Beginner Course" after node 4242.
func assertCourseShowFiles(t *testing.T, show string) {
	t.Helper()
	if got := readFile(filepath.Join(show, "poster.jpg")); got != "jpeg:"+artHeader {
		t.Errorf("poster.jpg = %q, want the course's square header", got)
	}
	if got := readFile(filepath.Join(show, "fanart.jpg")); got != "jpeg:"+artThumb {
		t.Errorf("fanart.jpg = %q, want the course's thumbnail", got)
	}
	nfo := readFile(filepath.Join(show, "tvshow.nfo"))
	for _, want := range []string{"<tvshow>", "<title>Beginner Course</title>", "<plot>A course.</plot>", `<uniqueid type="musora" default="true">4242</uniqueid>`} {
		if !strings.Contains(nfo, want) {
			t.Errorf("tvshow.nfo lacks %s:\n%s", want, nfo)
		}
	}
}

// assertRecordNamesTheEpisodeImage fails unless the only recorded download
// names the episode image image, and names nothing outside the season
// folder (never a show-level file).
func assertRecordNamesTheEpisodeImage(t *testing.T, store *fakeWorkerStore, image string) {
	t.Helper()
	rec := onlyRecord(t, store)
	if !slices.Contains(rec.entries, image) {
		t.Errorf("record %v does not name the episode image %s", rec.entries, image)
	}
	for _, e := range rec.entries {
		if !strings.HasPrefix(e, "Beginner Course/Season 01/") {
			t.Errorf("record names %q, a show-level file", e)
		}
	}
}

// TestPlexTVShowCostsNothingOnceItHasItsFiles pins invariant 2's cost: the
// show's files are fetched once, for its first episode; its next episode and
// every later cycle ask Musora and its image server nothing.
func TestPlexTVShowCostsNothingOnceItHasItsFiles(t *testing.T) {
	w, store, _, res, img, _, season := artWorker(t)
	store.queue = append(store.queue, queuedJob(2, nodeFollow().ID, 101))
	store.lessons[101] = database.Lesson{RailcontentID: 101, Position: sql.NullInt64{Int64: 6, Valid: true}}
	res.docs[101] = lesson(101, "Lesson B")
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	store.withFiles = []database.Lesson{recordedRow(100, season, sameTitleBase+".mp4")}
	w.EnsureShowFiles(context.Background())

	var course []int
	for _, id := range res.asked() {
		if id != 100 && id != 101 {
			course = append(course, id)
		}
	}
	if !reflect.DeepEqual(course, []int{4242}) {
		t.Errorf("the show's course asked for %v, want once", course)
	}
	if got, want := img.fetched(), []string{artHeader, artThumb}; !reflect.DeepEqual(got, want) {
		t.Errorf("fetched %v, want %v once", got, want)
	}
}

// backfillWorker is a plex-tv worker over a fake store, with nothing queued,
// for the cycle's show-file step.
func backfillWorker(t *testing.T) (w *Worker, store *fakeWorkerStore, res *countingResolver, img *fakeImages, lib string) {
	t.Helper()
	store = newFakeWorkerStore()
	store.follows[nodeFollow().ID] = nodeFollow()
	store.follows[instructorFollow().ID] = instructorFollow()
	res = &countingResolver{docs: map[int]*musora.Lesson{}}
	img = &fakeImages{}
	w = newTestWorker(t, store, res, newFakeDownloader(), func(time.Duration) {})
	w.Images = img
	lib = filepath.Join(t.TempDir(), "lib")
	w.Cfg.LibraryDir = lib
	w.Cfg.Layout = LayoutPlexTV
	return w, store, res, img, lib
}

// inFollow is row filed under follow id.
func inFollow(row database.Lesson, id int64) database.Lesson {
	row.FollowID = sql.NullInt64{Int64: id, Valid: true}
	return row
}

// TestEnsureShowFilesFillsTheShowsAlreadyInTheLibrary pins invariant 2: the
// cycle's show-file step gives every show folder its lessons file episodes
// in, under today's library, the files it is missing, by plexShow's own
// branches (a node follow's show is the node; an instructor follow's is the
// lesson's course, or for a lesson in no course the instructor, whose coach
// card is the poster and who has no background), a recorded row and a
// legacy row alike. It creates no folder, and once every show has its files
// it asks nothing.
func TestEnsureShowFilesFillsTheShowsAlreadyInTheLibrary(t *testing.T) {
	w, store, res, img, lib := backfillWorker(t)
	node := filepath.Join(lib, "Beginner Course", "Season 01")
	course := filepath.Join(lib, "Groove Course", "Season 01")
	instructor := filepath.Join(lib, "Mike Johnston", "Season 01")
	for _, d := range []string{node, course, instructor} {
		seedSeason(t, d)
	}
	elsewhere := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
	store.withFiles = []database.Lesson{
		inFollow(recordedRow(100, node, "Beginner Course - s01e05 - Lesson A.mp4"), nodeFollow().ID),
		inFollow(legacyRow(200, "Groove", 1, course, "Groove Course - s01e01 - Groove.mp4"), instructorFollow().ID),
		inFollow(legacyRow(300, "Solo", 1, instructor, "Mike Johnston - s01e01 - Solo.mp4"), instructorFollow().ID),
		inFollow(database.Lesson{RailcontentID: 400, OutputDir: sql.NullString{String: elsewhere, Valid: true}}, nodeFollow().ID),
		recordedRow(500, filepath.Join(lib, "Gone Show", "Season 01"), "Gone Show - s01e01 - X.mp4"),
	}
	res.docs[4242] = courseDoc(4242, "Beginner Course")
	groove := lesson(200, "Groove")
	groove.ParentContentData = []musora.ParentContent{{ID: 77, Title: "Groove Course"}}
	res.docs[200] = groove
	res.docs[77] = &musora.Lesson{ID: 77, Title: "Groove Course", Thumbnail: artThumb, Instructors: []musora.Instructor{{Name: "Mike Johnston", CoachCardImage: artCoach}}}
	solo := lesson(300, "Solo")
	solo.Instructors = []musora.Instructor{{Name: "Someone Else", Slug: "someone-else", CoachCardImage: artSquare}, {Name: "Mike Johnston", Slug: "mike-johnston", CoachCardImage: artCoach, Biography: "<p>Founder.</p>"}}
	res.docs[300] = solo

	w.EnsureShowFiles(context.Background())

	for dir, want := range map[string]map[string]string{
		filepath.Join(lib, "Beginner Course"): {"poster.jpg": "jpeg:" + artHeader, "fanart.jpg": "jpeg:" + artThumb},
		filepath.Join(lib, "Groove Course"):   {"poster.jpg": "jpeg:" + artCoach, "fanart.jpg": "jpeg:" + artThumb},
		filepath.Join(lib, "Mike Johnston"):   {"poster.jpg": "jpeg:" + artCoach},
	} {
		assertShowImages(t, dir, want)
	}
	for dir, title := range map[string]string{"Beginner Course": "Beginner Course", "Groove Course": "Groove Course", "Mike Johnston": "Mike Johnston"} {
		if nfo := readFile(filepath.Join(lib, dir, "tvshow.nfo")); !strings.Contains(nfo, "<title>"+title+"</title>") {
			t.Errorf("%s/tvshow.nfo = %s, want titled %q", dir, nfo, title)
		}
	}
	if nfo := readFile(filepath.Join(lib, "Mike Johnston", "tvshow.nfo")); !strings.Contains(nfo, "<plot>Founder.</plot>") {
		t.Errorf("the instructor's tvshow.nfo lacks their biography:\n%s", nfo)
	}
	assertExist(t, false, filepath.Join(lib, "Gone Show"), filepath.Join(filepath.Dir(elsewhere), "poster.jpg"))
	if got, want := slices.Sorted(slices.Values(res.asked())), []int{77, 200, 300, 4242}; !reflect.DeepEqual(got, want) {
		t.Errorf("asked Musora for %v, want %v", got, want)
	}

	// Done: the next cycle costs nothing.
	asked, fetched := len(res.asked()), len(img.fetched())
	w.EnsureShowFiles(context.Background())
	if len(res.asked()) != asked || len(img.fetched()) != fetched {
		t.Errorf("a cycle over complete shows asked %v and fetched %v more", res.asked()[asked:], img.fetched()[fetched:])
	}
}

// assertShowImages fails unless the show folder dir holds exactly the images
// want names (name -> content) and a tvshow.nfo.
func assertShowImages(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	names := []string{"tvshow.nfo"}
	for n := range want {
		names = append(names, n)
	}
	slices.Sort(names)
	if got := showFileNames(t, dir); !reflect.DeepEqual(got, names) {
		t.Errorf("%s holds %v, want %v", dir, got, names)
	}
	for n, body := range want {
		if got := readFile(filepath.Join(dir, n)); got != body {
			t.Errorf("%s/%s = %q, want %q", dir, n, got, body)
		}
	}
}

// TestEnsureShowFilesWritesOnlyIntoARealShowFolder pins invariant 3's
// placement: the step never creates a folder (a library that is not mounted
// gets nothing), and never writes through a show folder that is a symlink,
// even one that leads to another folder of the library.
func TestEnsureShowFilesWritesOnlyIntoARealShowFolder(t *testing.T) {
	t.Run("no library", func(t *testing.T) {
		w, store, res, _, lib := backfillWorker(t)
		store.withFiles = []database.Lesson{inFollow(recordedRow(100, filepath.Join(lib, "Beginner Course", "Season 01"), "x.mp4"), nodeFollow().ID)}
		res.docs[4242] = courseDoc(4242, "Beginner Course")
		w.EnsureShowFiles(context.Background())
		assertExist(t, false, lib)
		if len(res.asked()) != 0 {
			t.Errorf("asked Musora for %v with no library", res.asked())
		}
	})
	t.Run("symlinked show folder", func(t *testing.T) {
		w, store, res, _, lib := backfillWorker(t)
		other := filepath.Join(lib, "Other")
		seedSeason(t, filepath.Join(other, "Season 01"))
		// A relative link that stays inside the library: an os.Root refuses
		// one that leads out of it anyway, so only this one needs the guard.
		if err := os.Symlink("Other", filepath.Join(lib, "Beginner Course")); err != nil {
			t.Skip("no symlinks here:", err)
		}
		store.withFiles = []database.Lesson{inFollow(recordedRow(100, filepath.Join(lib, "Beginner Course", "Season 01"), "x.mp4"), nodeFollow().ID)}
		res.docs[4242] = courseDoc(4242, "Beginner Course")
		w.EnsureShowFiles(context.Background())
		if got := showFileNames(t, other); len(got) != 0 {
			t.Errorf("wrote %v through the symlink", got)
		}
	})
}

// TestPlexTVPlacementWritesNoShowFilesThroughASymlink pins invariant 3 for
// the placement: a show folder that is a symlink, even one to another show
// folder of the library, gets none of the show's files, so that other show
// is never given this one's poster, background or tvshow.nfo. The episode is
// still placed and recorded, and the log says why the show files were not.
func TestPlexTVPlacementWritesNoShowFilesThroughASymlink(t *testing.T) {
	w, store, _, _, _, lib, _ := artWorker(t)
	log := &bytes.Buffer{}
	w.Log = log
	other := filepath.Join(lib, "Other")
	seedSeason(t, filepath.Join(other, "Season 01"))
	if err := os.Symlink("Other", filepath.Join(lib, "Beginner Course")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := showFileNames(t, other); len(got) != 0 {
		t.Errorf("wrote %v through the symlink into another show's folder", got)
	}
	onlyRecord(t, store)
	if !strings.Contains(log.String(), "the show's own files were not written") {
		t.Errorf("log %q does not say why the show's files were not written", log.String())
	}
}

// TestShowFilesLeaveTheOwnersAlone pins ruling #78, 3: a slot the owner filled
// under any name Plex reads for it is left as it is and gets nothing more,
// and an owner's tvshow.nfo marks the show done: nothing is asked or written.
func TestShowFilesLeaveTheOwnersAlone(t *testing.T) {
	t.Run("images", func(t *testing.T) {
		w, store, res, img, lib := backfillWorker(t)
		show := filepath.Join(lib, "Beginner Course")
		seedSeason(t, show, "Folder.PNG", "BACKDROP.jpeg")
		store.withFiles = []database.Lesson{inFollow(recordedRow(100, filepath.Join(show, "Season 01"), "x.mp4"), nodeFollow().ID)}
		res.docs[4242] = courseDoc(4242, "Beginner Course")
		w.EnsureShowFiles(context.Background())
		if got := showFileNames(t, show); !reflect.DeepEqual(got, []string{"BACKDROP.jpeg", "Folder.PNG", "tvshow.nfo"}) {
			t.Errorf("show folder holds %v, want the owner's images and the nfo", got)
		}
		assertContent(t, show, "Folder.PNG", "BACKDROP.jpeg")
		if len(img.fetched()) != 0 {
			t.Errorf("fetched %v for filled slots", img.fetched())
		}
	})
	t.Run("nfo", func(t *testing.T) {
		w, store, res, img, lib := backfillWorker(t)
		show := filepath.Join(lib, "Beginner Course")
		seedSeason(t, show, "tvshow.nfo")
		store.withFiles = []database.Lesson{inFollow(recordedRow(100, filepath.Join(show, "Season 01"), "x.mp4"), nodeFollow().ID)}
		res.docs[4242] = courseDoc(4242, "Beginner Course")
		w.EnsureShowFiles(context.Background())
		if got := showFileNames(t, show); !reflect.DeepEqual(got, []string{"tvshow.nfo"}) {
			t.Errorf("show folder holds %v, want the owner's nfo only", got)
		}
		assertContent(t, show, "tvshow.nfo")
		if len(res.asked()) != 0 || len(img.fetched()) != 0 {
			t.Errorf("asked %v, fetched %v for a show with its nfo", res.asked(), img.fetched())
		}
	})
}

// twoShows files lesson 100 in "Beginner Course" (node 4242) and lesson 101 in
// "Second Course" (node 5555), both courses on Musora with their own images.
func twoShows(t *testing.T, w *Worker, store *fakeWorkerStore, res *countingResolver, lib string) (first, second string) {
	t.Helper()
	second5555 := database.Follow{ID: 3, Kind: "node", RailcontentID: sql.NullInt64{Int64: 5555, Valid: true}, Title: "Second Course"}
	store.follows[3] = second5555
	first, second = filepath.Join(lib, "Beginner Course"), filepath.Join(lib, "Second Course")
	store.withFiles = []database.Lesson{
		inFollow(recordedRow(100, filepath.Join(first, "Season 01"), "a.mp4"), nodeFollow().ID),
		inFollow(recordedRow(101, filepath.Join(second, "Season 01"), "b.mp4"), 3),
	}
	seedSeason(t, filepath.Join(first, "Season 01"))
	seedSeason(t, filepath.Join(second, "Season 01"))
	res.docs[4242] = courseDoc(4242, "Beginner Course")
	res.docs[5555] = &musora.Lesson{ID: 5555, Thumbnail: artSquare, Type: "song"}
	return first, second
}

// TestShowFilesWhenAFetchFails pins invariant 7: an image that is gone
// leaves its slot empty and the show is done; one that failed for now leaves
// the nfo out, so the next cycle fetches it (and only it); Musora or its image
// server out of reach stops the step until the next cycle, writing nothing
// and leaving no part of a file.
func TestShowFilesWhenAFetchFails(t *testing.T) {
	t.Run("gone", testShowFilesWhenAnImageIsGone)
	t.Run("for now", testShowFilesWhenAFetchFailsForNow)
	t.Run("out of reach", testShowFilesWhenTheImageServerIsOutOfReach)
	t.Run("no scheme", testShowFilesWhenAnImageURLHasNoScheme)
	t.Run("Musora out of reach", testShowFilesWhenMusoraIsOutOfReach)
}

func testShowFilesWhenAnImageIsGone(t *testing.T) {
	w, store, res, img, lib := backfillWorker(t)
	first, _ := twoShows(t, w, store, res, lib)
	img.errs = map[string]error{artHeader: fmt.Errorf("%w: GET 404", musora.ErrImageMissing)}
	w.EnsureShowFiles(context.Background())
	if got := showFileNames(t, first); !reflect.DeepEqual(got, []string{"fanart.jpg", "tvshow.nfo"}) {
		t.Errorf("show folder holds %v, want the background and the nfo", got)
	}
}

func testShowFilesWhenAFetchFailsForNow(t *testing.T) {
	w, store, res, img, lib := backfillWorker(t)
	first, _ := twoShows(t, w, store, res, lib)
	img.errs = map[string]error{artHeader: errors.New("GET 503")}
	w.EnsureShowFiles(context.Background())
	if got := showFileNames(t, first); !reflect.DeepEqual(got, []string{"fanart.jpg"}) {
		t.Errorf("show folder holds %v, want the background only", got)
	}
	img.errs = nil
	before := len(img.fetched())
	w.EnsureShowFiles(context.Background())
	if got := showFileNames(t, first); !reflect.DeepEqual(got, []string{"fanart.jpg", "poster.jpg", "tvshow.nfo"}) {
		t.Errorf("show folder holds %v after a retry, want all three", got)
	}
	if got := img.fetched()[before:]; !reflect.DeepEqual(got, []string{artHeader}) {
		t.Errorf("the retry fetched %v, want the poster only", got)
	}
}

func testShowFilesWhenTheImageServerIsOutOfReach(t *testing.T) {
	w, store, res, img, lib := backfillWorker(t)
	first, second := twoShows(t, w, store, res, lib)
	img.errs = map[string]error{artHeader: fmt.Errorf("%w: dial tcp: timeout", musora.ErrUnreachable)}
	w.EnsureShowFiles(context.Background())
	for _, dir := range []string{first, second} {
		if got := showFileNames(t, dir); len(got) != 0 {
			t.Errorf("%s holds %v, want nothing", dir, got)
		}
	}
	if slices.Contains(res.asked(), 5555) || slices.Contains(img.fetched(), artSquare) {
		t.Errorf("asked %v, fetched %v: the step went on", res.asked(), img.fetched())
	}
	img.errs = nil
	w.EnsureShowFiles(context.Background())
	if got := showFileNames(t, second); !reflect.DeepEqual(got, []string{"poster.jpg", "tvshow.nfo"}) {
		t.Errorf("the next cycle left %v in the song's show, want its poster and nfo", got)
	}
}

func testShowFilesWhenAnImageURLHasNoScheme(t *testing.T) {
	// The error the real client gives an image URL with no scheme (Musora
	// writing its URLs another way): not final, and not out of reach.
	_, noScheme := musora.FetchJPEG(context.Background(), "cdn.sanity.io/images/p/d/header-4500x4500.png")
	if noScheme == nil {
		t.Fatal("FetchJPEG fetched a URL with no scheme")
	}
	w, store, res, img, lib := backfillWorker(t)
	first, second := twoShows(t, w, store, res, lib)
	img.errs = map[string]error{artHeader: noScheme}
	w.EnsureShowFiles(context.Background())
	if got := showFileNames(t, first); !reflect.DeepEqual(got, []string{"fanart.jpg"}) {
		t.Errorf("show folder holds %v, want the background only (no tvshow.nfo)", got)
	}
	if got := showFileNames(t, second); !reflect.DeepEqual(got, []string{"poster.jpg", "tvshow.nfo"}) {
		t.Errorf("the next show holds %v, want its poster and nfo", got)
	}
	if w.showOffline() {
		t.Error("the show-file step was stopped for the cycle")
	}
	img.errs = nil
	w.EnsureShowFiles(context.Background())
	if got := showFileNames(t, first); !reflect.DeepEqual(got, []string{"fanart.jpg", "poster.jpg", "tvshow.nfo"}) {
		t.Errorf("show folder holds %v after a later cycle, want all three", got)
	}
}

func testShowFilesWhenMusoraIsOutOfReach(t *testing.T) {
	w, store, res, _, lib := backfillWorker(t)
	twoShows(t, w, store, res, lib)
	res.errs = map[int]error{4242: &url.Error{Op: "Post", URL: "https://sanity", Err: errors.New("no route")}}
	w.EnsureShowFiles(context.Background())
	if slices.Contains(res.asked(), 5555) {
		t.Errorf("asked %v: the step went on", res.asked())
	}
}

// TestShowFileLogQuotesTheImageURL pins that an image URL from Musora is
// quoted in the log: a newline in it (JSON decodes one, and a URL the fetch
// refuses may hold one) never starts a line of its own that reads like
// drumdrop's.
func TestShowFileLogQuotesTheImageURL(t *testing.T) {
	const forged = artCoach + "\n  + show \"Forged Show\": poster.jpg"
	for name, err := range map[string]error{
		"missing": fmt.Errorf("%w: refused", musora.ErrImageMissing),
		"for now": errors.New("GET 503"),
	} {
		t.Run(name, func(t *testing.T) {
			w, store, res, img, lib := backfillWorker(t)
			twoShows(t, w, store, res, lib)
			doc := courseDoc(4242, "Beginner Course")
			doc.HeaderImageURL = ""
			doc.Instructors[0].CoachCardImage = forged
			res.docs[4242] = doc
			img.errs = map[string]error{forged: err}
			var log bytes.Buffer
			w.Log = &log
			w.EnsureShowFiles(context.Background())
			if !strings.Contains(log.String(), fmt.Sprintf("%q", forged)) {
				t.Errorf("log %q does not quote the URL", log.String())
			}
			for _, line := range strings.Split(log.String(), "\n") {
				if strings.Contains(line, "Forged Show") && !strings.Contains(line, "⚠") {
					t.Errorf("a line of its own: %q", line)
				}
			}
		})
	}
}

// TestPlacementOutOfReachStopsTheShowFilesForTheCycle proves a placement that
// could not reach the image server stops every other show-file fetch of the
// cycle (the later placements', and the cycle's own step), so an outage
// costs one timeout, not one per show; the next cycle tries again.
func TestPlacementOutOfReachStopsTheShowFilesForTheCycle(t *testing.T) {
	w, store, _, res, img, lib, _ := artWorker(t)
	store.follows[3] = database.Follow{ID: 3, Kind: "node", RailcontentID: sql.NullInt64{Int64: 5555, Valid: true}, Title: "Second Course"}
	store.queue = append(store.queue, queuedJob(2, 3, 101))
	store.lessons[101] = database.Lesson{RailcontentID: 101, Position: sql.NullInt64{Int64: 1, Valid: true}}
	res.docs[101] = lesson(101, "Lesson B")
	res.docs[5555] = &musora.Lesson{ID: 5555, Thumbnail: artSquare, Type: "song"}
	img.errs = map[string]error{artHeader: fmt.Errorf("%w: timeout", musora.ErrUnreachable)}
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	store.withFiles = []database.Lesson{inFollow(recordedRow(101, filepath.Join(lib, "Second Course", "Season 01"), "b.mp4"), 3)}
	w.EnsureShowFiles(context.Background())
	if got := img.fetched(); !reflect.DeepEqual(got, []string{artHeader}) {
		t.Errorf("fetched %v in the cycle, want the one that failed only", got)
	}
	assertExist(t, true, filepath.Join(lib, "Second Course", "Season 01", "Second Course - s01e01 - Lesson B.mp4"))
	assertExist(t, false, filepath.Join(lib, "Beginner Course", "tvshow.nfo"), filepath.Join(lib, "Second Course", "tvshow.nfo"))

	// The next cycle (its drain, with nothing queued, then its step).
	img.errs = nil
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	w.EnsureShowFiles(context.Background())
	assertExist(t, true, filepath.Join(lib, "Second Course", "poster.jpg"), filepath.Join(lib, "Second Course", "tvshow.nfo"))
}

// TestAStoppedShowStepDoesNotReachTheNextCycle pins that the "out of reach"
// stop lasts one cycle even when that cycle's closing step never runs (the
// daemon paused, the worker failed, a sync was interrupted): the next
// cycle's first placement into a new show still writes the show's files
// before the episode, as Plex picks local art only when it first matches a
// show.
func TestAStoppedShowStepDoesNotReachTheNextCycle(t *testing.T) {
	w, _, _, _, img, lib, _ := artWorker(t)
	w.setShowOffline() // the last cycle's, whose closing step never ran
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	show := filepath.Join(lib, "Beginner Course")
	assertExist(t, true, filepath.Join(show, "poster.jpg"), filepath.Join(show, "fanart.jpg"), filepath.Join(show, "tvshow.nfo"))
	if len(img.fetched()) != 2 {
		t.Errorf("fetched %v, want the poster and the background", img.fetched())
	}
}

// TestAShowMusoraGivesNothingForGetsNoFiles pins that a show whose document
// is expected but not had (Musora answers nothing for its course, or its id
// could not be read) gets no files at all, from the placement or from the
// cycle's step: never a title-only tvshow.nfo, which would mark it done for
// good. The step does not ask about it again in this process; after a
// restart, with Musora answering, it gets every file.
func TestAShowMusoraGivesNothingForGetsNoFiles(t *testing.T) {
	t.Run("placement", func(t *testing.T) {
		w, _, _, res, img, lib, _ := artWorker(t)
		delete(res.docs, 4242)
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if got := showFileNames(t, filepath.Join(lib, "Beginner Course")); len(got) != 0 {
			t.Errorf("show folder holds %v, want nothing", got)
		}
		if len(img.fetched()) != 0 {
			t.Errorf("fetched %v, want nothing", img.fetched())
		}
	})
	for _, tc := range []struct {
		name  string
		setup func(res *countingResolver, store *fakeWorkerStore)
	}{
		{"no document", func(res *countingResolver, _ *fakeWorkerStore) { delete(res.docs, 77) }},
		{"id not read", func(res *countingResolver, _ *fakeWorkerStore) { res.docs[200].ParentContentData[0].ID = 0 }},
	} {
		t.Run("step, "+tc.name, func(t *testing.T) {
			w, store, res, _, lib := backfillWorker(t)
			season := filepath.Join(lib, "Groove Course", "Season 01")
			seedSeason(t, season)
			store.withFiles = []database.Lesson{inFollow(legacyRow(200, "Groove", 1, season, "Groove Course - s01e01 - Groove.mp4"), instructorFollow().ID)}
			l := lesson(200, "Groove")
			l.ParentContentData = []musora.ParentContent{{ID: 77, Title: "Groove Course"}}
			res.docs[200], res.docs[77] = l, courseDoc(77, "Groove Course")
			tc.setup(res, store)
			w.EnsureShowFiles(context.Background())
			w.EnsureShowFiles(context.Background())
			if got := showFileNames(t, filepath.Dir(season)); len(got) != 0 {
				t.Errorf("show folder holds %v, want nothing", got)
			}
			if n := len(res.asked()); n > 2 {
				t.Errorf("asked %v over two cycles, want the show looked at once", res.asked())
			}
			// A restart, with Musora answering again.
			res.docs[77] = courseDoc(77, "Groove Course")
			res.docs[200].ParentContentData[0].ID = 77
			next, _, _, _, _ := backfillWorker(t)
			next.Store, next.Resolver, next.Images, next.Cfg.LibraryDir = store, res, w.Images, lib
			next.EnsureShowFiles(context.Background())
			if got := showFileNames(t, filepath.Dir(season)); !reflect.DeepEqual(got, []string{"fanart.jpg", "poster.jpg", "tvshow.nfo"}) {
				t.Errorf("after a restart the show folder holds %v, want all three", got)
			}
		})
	}
}

// TestShowDocFollowsPlexShow proves the show's files come from the document
// the show is named after, by plexShow's branches: a node follow's node
// (never the lesson's own course, and the lesson itself when it is the node),
// an instructor follow's course, or for a lesson in no course the followed
// instructor's own entry (not merely the first), and for a lesson with no
// follow its course, or the lesson itself.
func TestShowDocFollowsPlexShow(t *testing.T) {
	inPack := lesson(100, "Lesson A")
	inPack.ParentContentData = []musora.ParentContent{{ID: 55, Title: "Some Pack"}}
	noCourse := lesson(300, "Solo")
	noCourse.Instructors = []musora.Instructor{{Name: "Someone Else", Slug: "someone-else", CoachCardImage: artSquare}, {Name: "Mike Johnston", Slug: "mike-johnston", CoachCardImage: artCoach}}
	docs := map[int]*musora.Lesson{4242: courseDoc(4242, "Node"), 55: courseDoc(55, "Pack")}
	for _, tc := range []struct {
		name   string
		f      database.Follow
		lesson *musora.Lesson
		asked  []int
		doc    int // the document's id, or 0 for the instructor's entry
		poster string
	}{
		{"node follow", nodeFollow(), inPack, []int{4242}, 4242, artHeader},
		{"node follow of the lesson itself", database.Follow{Kind: "node", RailcontentID: sql.NullInt64{Int64: 100, Valid: true}, Title: "Lesson A"}, inPack, nil, 100, ""},
		{"instructor follow, lesson in a course", instructorFollow(), inPack, []int{55}, 55, artHeader},
		{"instructor follow, lesson in no course", instructorFollow(), noCourse, nil, 0, artCoach},
		{"no follow, lesson in a course", database.Follow{}, inPack, []int{55}, 55, artHeader},
		{"no follow, lesson in no course", database.Follow{}, noCourse, nil, 300, artSquare},
	} {
		res := &countingResolver{docs: docs}
		w := &Worker{Resolver: res}
		doc, err := w.showDoc(tc.f, tc.lesson)
		if err != nil || doc == nil {
			t.Errorf("%s: showDoc = %v, %v", tc.name, doc, err)
			continue
		}
		if !reflect.DeepEqual(res.asked(), tc.asked) || doc.ID != tc.doc {
			t.Errorf("%s: asked %v for document %d, want %v for %d", tc.name, res.asked(), doc.ID, tc.asked, tc.doc)
		}
		if poster, _ := musora.ShowArt(doc); poster != tc.poster {
			t.Errorf("%s: poster %q, want %q", tc.name, poster, tc.poster)
		}
	}
}

// TestEnsureShowFilesAsksOnceAboutAShowItCanNotName proves a show folder none
// of whose lessons leads to a show of its name (a course renamed on Musora)
// gets nothing, since nothing proves what it is, and is not asked about again
// in the same process.
func TestEnsureShowFilesAsksOnceAboutAShowItCanNotName(t *testing.T) {
	w, store, res, _, lib := backfillWorker(t)
	old := filepath.Join(lib, "Old Name", "Season 01")
	seedSeason(t, old)
	store.withFiles = []database.Lesson{inFollow(legacyRow(200, "Groove", 1, old, "Old Name - s01e01 - Groove.mp4"), instructorFollow().ID)}
	renamed := lesson(200, "Groove")
	renamed.ParentContentData = []musora.ParentContent{{ID: 77, Title: "New Name"}}
	res.docs[200] = renamed
	var log bytes.Buffer
	w.Log = &log
	w.EnsureShowFiles(context.Background())
	w.EnsureShowFiles(context.Background())
	if got := showFileNames(t, filepath.Dir(old)); len(got) != 0 {
		t.Errorf("wrote %v into a show it could not name", got)
	}
	if !reflect.DeepEqual(res.asked(), []int{200}) {
		t.Errorf("asked %v, want lesson 200 once", res.asked())
	}
	if !strings.Contains(log.String(), "not asked again") {
		t.Errorf("log %q does not say so", log.String())
	}
}

// TestNoShowFilesInTheDefaultLayout proves the default layout is unchanged:
// no show files, from a placement or from the cycle's step (not even for a
// show folder an earlier plex-tv setting filled), and its image keeps its
// "-poster.jpg" name.
func TestNoShowFilesInTheDefaultLayout(t *testing.T) {
	w, s, dl, f, _ := realWorker(t, "")
	withPoster(dl)
	img := &fakeImages{}
	w.Images = img
	ctx := context.Background()
	if _, _, err := s.EnqueueJob(ctx, sql.NullInt64{Int64: f, Valid: true}, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := w.RunOnce(ctx, 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	w.EnsureShowFiles(ctx)
	course := filepath.Join(w.Cfg.LibraryDir, "Beginner Course")
	if got := showFileNames(t, course); !reflect.DeepEqual(got, []string{"05 - Lesson A"}) {
		t.Errorf("course folder holds %v, want the lesson folder only", got)
	}
	assertExist(t, true, filepath.Join(course, "05 - Lesson A", "05 - Lesson A-poster.jpg"))
	if len(img.fetched()) != 0 {
		t.Errorf("fetched %v in the default layout", img.fetched())
	}

	// A show folder a plex-tv setting filed episodes in before.
	bw, store, res, bimg, lib := backfillWorker(t)
	bw.Cfg.Layout = ""
	season := filepath.Join(lib, "Beginner Course", "Season 01")
	seedSeason(t, season)
	store.withFiles = []database.Lesson{inFollow(recordedRow(100, season, "x.mp4"), nodeFollow().ID)}
	res.docs[4242] = courseDoc(4242, "Beginner Course")
	bw.EnsureShowFiles(ctx)
	if got := showFileNames(t, filepath.Dir(season)); len(got) != 0 || len(res.asked()) != 0 || len(bimg.fetched()) != 0 {
		t.Errorf("the default layout wrote %v (asked %v, fetched %v)", got, res.asked(), bimg.fetched())
	}
}

// showStepStore is fakeDaemonStore recording the show-file step's read.
type showStepStore struct{ *fakeDaemonStore }

func (s showStepStore) ListLessonsWithFiles(ctx context.Context) ([]database.Lesson, error) {
	s.record("with-files")
	return nil, nil
}

// TestDaemonCycleEndsWithTheShowFileStep proves each daemon cycle runs the
// show-file step once its drain is done, so shows no download of the cycle
// placed into get their files without a restart; and a paused daemon skips
// it, as it skips the queue.
func TestDaemonCycleEndsWithTheShowFileStep(t *testing.T) {
	for _, paused := range []bool{false, true} {
		store := newFakeDaemonStore()
		d := newTestDaemon(t, store)
		wrapped := showStepStore{store}
		d.Store, d.Planner.Store, d.Worker.Store = wrapped, wrapped, wrapped
		d.Worker.Cfg.LibraryDir, d.Worker.Cfg.Layout, d.Worker.Images = t.TempDir(), LayoutPlexTV, &fakeImages{}
		if paused {
			d.Pause()
		}
		if err := d.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		// Both closing steps read the lesson rows: the one-time rename of the
		// episode files, then the show-file step, each after the drain.
		ops := store.snapshotOps()
		drained := slices.Index(ops, "drain-empty")
		stepsRan := drained >= 0 && reflect.DeepEqual(ops[drained+1:], []string{"with-files", "with-files"})
		if stepsRan == paused {
			t.Errorf("paused=%v: ops %v, want the rename and show-file steps last %v", paused, ops, map[bool]string{false: "", true: "skipped"}[paused])
		}
	}
}

// TestShowFolderOf pins which show folder a lesson row files episodes in, for
// the cycle's show-file step: its record's show (so a library that moved
// still reads right), or a legacy row's season folder's parent directly under
// today's library; never a folder elsewhere, the private folder, or a row
// whose record is damaged.
func TestShowFolderOf(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "lib")
	moved := recordedRow(1, filepath.Join("/old/lib", "Show", "Season 01"), "x.mp4")
	damaged := recordedRow(2, filepath.Join(lib, "Show", "Season 01"))
	damaged.LibraryEntries = sql.NullString{String: `["../x"]`, Valid: true}
	for _, tc := range []struct {
		name string
		row  database.Lesson
		want string
	}{
		{"recorded, library moved", moved, "Show"},
		{"legacy", legacyRow(3, "T", 1, filepath.Join(lib, "Legacy Show", "Season 02"), ""), "Legacy Show"},
		{"legacy, another library", legacyRow(4, "T", 1, filepath.Join("/elsewhere", "Show", "Season 01"), ""), ""},
		{"legacy, nested deeper", legacyRow(5, "T", 1, filepath.Join(lib, "A", "Show", "Season 01"), ""), ""},
		{"default layout folder", database.Lesson{OutputDir: sql.NullString{String: filepath.Join(lib, "Course", "05 - T"), Valid: true}}, ""},
		{"private folder", legacyRow(6, "T", 1, filepath.Join(lib, privateRootName, "Season 01"), ""), ""},
		{"private folder, recorded", recordedRow(7, filepath.Join(lib, privateRootName, "Season 01"), "x.mp4"), ""},
		{"damaged record", damaged, ""},
	} {
		if got := showFolderOf(lib, tc.row); got != tc.want {
			t.Errorf("%s: showFolderOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}
