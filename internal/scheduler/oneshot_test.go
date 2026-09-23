package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// The tests below pin the one-shot download (DownloadOneShot, "drumdrop
// <url>"): it has no database, so it writes in a private folder and replaces
// the files already in the lesson's folder only once the download finished,
// and only at the names the download produced.

// oneShotLesson is lesson 5 of "Beginner Course", as the one-shot names it.
var oneShotLesson = &musora.Lesson{ID: 100, Title: "Lesson A"}

// seedOneShot writes an earlier download of oneShotLesson in root's lesson
// folder, plus a file the owner keeps there that no download produces. It
// returns the lesson folder.
func seedOneShot(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "Beginner Course", "05 - Lesson A")
	for name, body := range map[string]string{
		"05 - Lesson A.mp4":  "old mp4",
		"05 - Lesson A.nfo":  "old nfo",
		"resources/old.pdf":  "old pdf",
		"my practice notes":  "mine",
		"05 - Lesson A.srt":  "old subs",
		"play-along/old.mp3": "old mp3",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// assertFile fails unless path holds exactly body.
func assertFile(t *testing.T, path, body string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if string(got) != body {
		t.Errorf("%s = %q, want %q", path, got, body)
	}
}

// assertNoRunFolder fails if a one-shot private folder is left in root.
func assertNoRunFolder(t *testing.T, root string) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(root, privateRootName, "run-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 {
		t.Errorf("private folders left: %q", left)
	}
}

// assertUntouched fails unless the earlier download seedOneShot wrote is
// whole.
func assertUntouched(t *testing.T, dir string) {
	t.Helper()
	assertFile(t, filepath.Join(dir, "05 - Lesson A.mp4"), "old mp4")
	assertFile(t, filepath.Join(dir, "05 - Lesson A.nfo"), "old nfo")
	assertFile(t, filepath.Join(dir, "resources", "old.pdf"), "old pdf")
	assertFile(t, filepath.Join(dir, "my practice notes"), "mine")
}

// A re-download that fails (yt-dlp deleted the video first, as
// --force-overwrites does, then got a 403) leaves the earlier download whole:
// it only ever deleted its private copy.
func TestDownloadOneShotFailedKeepsEarlierFiles(t *testing.T) {
	root := t.TempDir()
	dir := seedOneShot(t, root)
	dl := &forceOverwriter{fail: true}

	replaced, err := DownloadOneShot(context.Background(), dl, oneShotLesson, root, "Beginner Course", musora.DownloadOpts{Index: 5})
	if err == nil {
		t.Fatal("a failed download returned nil")
	}
	if dl.calls != 1 {
		t.Fatalf("downloader calls = %d, want 1", dl.calls)
	}
	if len(replaced) != 0 {
		t.Errorf("replaced = %q, want nothing", replaced)
	}
	assertUntouched(t, dir)
	assertExist(t, false, filepath.Join(dir, "05 - Lesson A.mp4.part"))
	assertNoRunFolder(t, root)
}

// An interrupt landing mid-download leaves the earlier download whole.
func TestDownloadOneShotInterruptedKeepsEarlierFiles(t *testing.T) {
	root := t.TempDir()
	dir := seedOneShot(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dl := &forceOverwriter{during: cancel}

	if _, err := DownloadOneShot(ctx, dl, oneShotLesson, root, "Beginner Course", musora.DownloadOpts{Index: 5}); err == nil {
		t.Fatal("an interrupted download returned nil")
	}
	assertUntouched(t, dir)
	assertNoRunFolder(t, root)
}

// A download that finished just as the interrupt landed is not placed: an
// interrupt leaves the destination as it was.
func TestDownloadOneShotInterruptedAfterDownloadIsNotPlaced(t *testing.T) {
	root := t.TempDir()
	dir := seedOneShot(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dl := downloaderFunc(func(c context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
		err := (&forceOverwriter{}).Download(c, l, o)
		cancel()
		return err
	})

	if _, err := DownloadOneShot(ctx, dl, oneShotLesson, root, "Beginner Course", musora.DownloadOpts{Index: 5}); err == nil {
		t.Fatal("a download interrupted before its placement returned nil")
	}
	assertUntouched(t, dir)
	assertNoRunFolder(t, root)
}

// A finished download replaces the entries at the names it produced (the
// video, the nfo, the resources folder) and keeps every other entry of the
// lesson's folder: the owner's own file, a subtitle and a play-along folder
// this download did not produce.
func TestDownloadOneShotReplacesOnlyItsOwnNames(t *testing.T) {
	root := t.TempDir()
	dir := seedOneShot(t, root)
	dl := &forceOverwriter{}

	replaced, err := DownloadOneShot(context.Background(), dl, oneShotLesson, root, "Beginner Course", musora.DownloadOpts{Index: 5})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dir, "05 - Lesson A.mp4"), "new mp4")
	assertFile(t, filepath.Join(dir, "05 - Lesson A.nfo"), "new nfo")
	assertFile(t, filepath.Join(dir, "resources", "new.pdf"), "new pdf")
	assertExist(t, false, filepath.Join(dir, "resources", "old.pdf"))
	assertFile(t, filepath.Join(dir, "my practice notes"), "mine")
	assertFile(t, filepath.Join(dir, "05 - Lesson A.srt"), "old subs")
	assertFile(t, filepath.Join(dir, "play-along", "old.mp3"), "old mp3")
	want := paths(dir, "05 - Lesson A.mp4", "05 - Lesson A.nfo", "resources")
	if !slices.Equal(sorted(replaced), sorted(want)) {
		t.Errorf("replaced = %q, want %q", replaced, want)
	}
	assertNoRunFolder(t, root)
	// Nothing it set aside is left behind either.
	left, _ := filepath.Glob(filepath.Join(root, privateRootName, "replaced-*"))
	if len(left) > 0 {
		t.Errorf("set-aside folders left: %q", left)
	}
}

// A first download (nothing at the destination yet) creates the lesson's
// folder, under a relative --out too.
func TestDownloadOneShotFirstDownloadRelativeRoot(t *testing.T) {
	t.Chdir(t.TempDir())
	replaced, err := DownloadOneShot(context.Background(), &forceOverwriter{}, oneShotLesson, "downloads", "Beginner Course", musora.DownloadOpts{Index: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 0 {
		t.Errorf("replaced = %q, want nothing", replaced)
	}
	assertFile(t, filepath.Join("downloads", "Beginner Course", "05 - Lesson A", "05 - Lesson A.mp4"), "new mp4")
	assertNoRunFolder(t, "downloads")
}

// The one-shot shares the private root with a daemon whose downloads folder
// is the same: its folder is never a job- folder (the daemon's sweep and a
// job's start remove those by id), and it never touches one.
func TestDownloadOneShotLeavesDaemonJobFoldersAlone(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, privateRootName, jobFolderName(1), "01 - Other", "01 - Other.mp4.part")
	if err := os.MkdirAll(filepath.Dir(job), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(job, []byte("a daemon's download in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	var private string
	dl := downloaderFunc(func(c context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
		private = o.Dir
		return (&forceOverwriter{}).Download(c, l, o)
	})

	if _, err := DownloadOneShot(context.Background(), dl, oneShotLesson, root, "Beginner Course", musora.DownloadOpts{Index: 5}); err != nil {
		t.Fatal(err)
	}
	if stagingEntry.MatchString(filepath.Base(private)) {
		t.Errorf("the one-shot's private folder %q is one the daemon's sweep would remove", private)
	}
	if filepath.Dir(private) != filepath.Join(root, privateRootName) {
		t.Errorf("private folder %q is not in %q", private, filepath.Join(root, privateRootName))
	}
	assertFile(t, job, "a daemon's download in progress")
}

// downloaderFunc adapts a function to Downloader.
type downloaderFunc func(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error

func (f downloaderFunc) Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	return f(ctx, l, o)
}
