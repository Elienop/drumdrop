package scheduler

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// The season-folder fixtures below mirror internal/library's, for the move and
// worker tests here.

// seedSeason creates dir and, inside it, every name: a folder (holding one
// file) when the name ends in "/", else a file whose content is its name.
func seedSeason(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, name := range names {
		if folder, ok := strings.CutSuffix(name, "/"); ok {
			if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", folder, err)
			}
			if err := os.WriteFile(filepath.Join(dir, folder, "f.pdf"), []byte(folder), 0o644); err != nil {
				t.Fatalf("write in %s: %v", folder, err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// paths joins every name (a trailing "/" dropped) onto dir.
func paths(dir string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(dir, strings.TrimSuffix(n, "/")))
	}
	return out
}

// recordedRow is a lesson filed in seasonDir (<lib>/<show>/<Season NN>) whose
// record names exactly names, relative to the library as the move writes them.
func recordedRow(id int, seasonDir string, names ...string) database.Lesson {
	return database.Lesson{
		RailcontentID:  id,
		Status:         database.StatusDownloaded,
		OutputDir:      sql.NullString{String: seasonDir, Valid: true},
		LibraryEntries: database.EncodeLibraryEntries(recordOf(seasonDir, names...)),
	}
}

// recordOf is the record entries for names in seasonDir (<lib>/<show>/<Season
// NN>): "<show>/<Season NN>/<name>", as the move writes them. Never nil.
func recordOf(seasonDir string, names ...string) []string {
	entries := make([]string, 0, len(names))
	prefix := filepath.Base(filepath.Dir(seasonDir)) + "/" + filepath.Base(seasonDir) + "/"
	for _, n := range names {
		entries = append(entries, prefix+strings.TrimSuffix(n, "/"))
	}
	return entries
}

// owned is every library entry a move result says the lesson owns (absolute).
func owned(r plexMoveResult) []string {
	return append(append([]string(nil), r.placed...), r.kept...)
}

// cleaned is every path cleaned (a root's own folder is flushed as "<dir>/.").
func cleaned(ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, filepath.Clean(p))
	}
	return out
}

// legacyRow is a lesson moved into seasonDir before the record existed: no
// library_entries, only its title, position and video.
func legacyRow(id int, title string, position int, seasonDir, video string) database.Lesson {
	l := database.Lesson{
		RailcontentID: id,
		Title:         title,
		Status:        database.StatusDownloaded,
		Position:      sql.NullInt64{Int64: int64(position), Valid: true},
		OutputDir:     sql.NullString{String: seasonDir, Valid: true},
	}
	if video != "" {
		l.VideoPath = sql.NullString{String: filepath.Join(seasonDir, video), Valid: true}
	}
	return l
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func assertExist(t *testing.T, want bool, ps ...string) {
	t.Helper()
	for _, p := range ps {
		_, err := os.Lstat(p)
		if got := err == nil; got != want {
			t.Errorf("%s exists = %v, want %v (err=%v)", p, got, want, err)
		}
	}
}

// assertContent fails unless every name in dir still holds its own name as
// its content (seedSeason's files).
func assertContent(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if got, err := os.ReadFile(filepath.Join(dir, n)); err != nil || string(got) != n {
			t.Errorf("%s = %q, %v; want the file kept as it was", filepath.Join(dir, n), got, err)
		}
	}
}

// fiveLookAlikes are four lessons that share episode 5 of one show, each title
// the first plus a tag or a suffix: the collision the name matcher could not
// tell apart.
var fiveLookAlikes = map[string][]string{
	"Five":           {"Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo", "Show - s01e05 - Five-poster.jpg", "Show - s01e05 - Five resources/"},
	"Five [Live]":    {"Show - s01e05 - Five [Live].mp4", "Show - s01e05 - Five [Live].nfo", "Show - s01e05 - Five [Live]-poster.jpg", "Show - s01e05 - Five [Live] resources/"},
	"Five-Part Fill": {"Show - s01e05 - Five-Part Fill.mp4", "Show - s01e05 - Five-Part Fill.nfo", "Show - s01e05 - Five-Part Fill resources/"},
	"Five.5":         {"Show - s01e05 - Five.5.mp4", "Show - s01e05 - Five.5.nfo", "Show - s01e05 - Five.5.en.vtt"},
}

var fiveTitles = []string{"Five", "Five [Live]", "Five-Part Fill", "Five.5"}

// plexEpisode is where a plex-tv move files a lesson: its show, season and
// episode number, and its title (moveToLibraryPlexTV's arguments).
type plexEpisode struct {
	show            string
	season, episode int
	title           string
}

// testMovePlexTV runs the plex-tv move of the lesson as episode ep, as the
// worker does, with the claims of others (none when empty) unless pl already
// carries claims, reading the downloaded lesson lessonDir through its parent
// folder. A placement is committed and the downloaded folder removed, as the
// worker does once the download is recorded; a refused move leaves lessonDir
// as it is.
func testMovePlexTV(t *testing.T, libraryDir string, ep plexEpisode, lessonDir string, pl plexLibrary, others ...database.Lesson) (plexMoveResult, error) {
	t.Helper()
	return testMovePlexTVFrom(t, filepath.Dir(lessonDir), libraryDir, ep, lessonDir, pl, others...)
}

// testMovePlexTVFrom is testMovePlexTV reading lessonDir through downloads.
func testMovePlexTVFrom(t *testing.T, downloads, libraryDir string, ep plexEpisode, lessonDir string, pl plexLibrary, others ...database.Lesson) (plexMoveResult, error) {
	t.Helper()
	if pl.claims == nil {
		c, err := library.NewClaims(libraryDir, others)
		if err != nil {
			t.Fatalf("NewClaims: %v", err)
		}
		pl.claims = c
	}
	src, err := openScratch(downloads, lessonDir)
	if err != nil {
		return plexMoveResult{}, err
	}
	defer src.close()
	res, err := moveToLibraryPlexTV(libraryDir, ep.show, ep.season, ep.episode, ep.title, src, pl)
	if res.pending != nil {
		if _, cerr := res.pending.commit(); cerr != nil {
			t.Fatalf("commit: %v", cerr)
		}
		if rerr := os.RemoveAll(lessonDir); rerr != nil {
			t.Fatalf("remove the downloaded folder: %v", rerr)
		}
	}
	return res, err
}

// showFive is the plex-tv episode the placement fixtures below file a lesson
// "05 - Five" as.
var showFive = plexEpisode{"Show", 1, 5, "Five"}

// placeFiveIn places the downloaded lesson scratch (read through dl) into lib
// in layout, as the worker does: the plex-tv move of showFive with pl, or the
// default layout's placement as self (testPlace). It returns the folder the
// lesson was placed in, "" when it was not.
func placeFiveIn(t *testing.T, layout, dl, lib, scratch string, pl plexLibrary, self database.Lesson) (string, error) {
	t.Helper()
	if layout == LayoutPlexTV {
		res, err := testMovePlexTVFrom(t, dl, lib, showFive, scratch, pl)
		return res.seasonDir, err
	}
	return testPlace(t, dl, lib, scratch, self)
}

// fiveDest is where the lesson "05 - Five" of Course is placed in lib in
// layout, and the name its resources folder is placed under there.
func fiveDest(lib, layout string) (dest, sub string) {
	if layout == LayoutPlexTV {
		return filepath.Join(lib, "Show", "Season 01"), "Show - s01e05 - Five resources"
	}
	return filepath.Join(lib, "Course", "05 - Five"), "resources"
}

// testPlace runs the default-layout placement as the worker does, as lesson
// 1 (or self when given), into root at lessonDir's path relative to
// downloads, with the claims of others (none when empty). A placement is
// committed and lessonDir removed, as the worker does once the download is
// recorded; it returns the lesson's folder.
func testPlace(t *testing.T, downloads, root, lessonDir string, self database.Lesson, others ...database.Lesson) (string, error) {
	t.Helper()
	pl, err := testPlacePending(t, downloads, root, lessonDir, self, others...)
	if err != nil {
		return "", err
	}
	if _, cerr := pl.commit(); cerr != nil {
		t.Fatalf("commit: %v", cerr)
	}
	if rerr := os.RemoveAll(lessonDir); rerr != nil {
		t.Fatalf("remove the downloaded folder: %v", rerr)
	}
	return pl.dir, nil
}

// testPlacePending is testPlace without the commit: the caller commits or
// undoes the placement.
func testPlacePending(t *testing.T, downloads, root, lessonDir string, self database.Lesson, others ...database.Lesson) (*placement, error) {
	t.Helper()
	if self.RailcontentID == 0 {
		self.RailcontentID = 1
	}
	c, err := library.NewClaims(root, others)
	if err != nil {
		t.Fatalf("NewClaims: %v", err)
	}
	rel, err := filepath.Rel(downloads, lessonDir)
	if err != nil {
		t.Fatal(err)
	}
	src, err := openScratch(filepath.Dir(lessonDir), lessonDir)
	if err != nil {
		return nil, err
	}
	t.Cleanup(src.close)
	return placeLessonFolder(root, rel, src, self, c, library.Roots(root, downloads), 7)
}

// stubRename makes every rename of the moves call f with the two full paths
// instead, for the rest of the test (f may call renameNoReplace to let one
// through). Like renameAt, it never replaces: a rename onto an entry already
// there fails with fs.ErrExist before f is called (round-5 fix code I2), so a
// stubbed test can not pass on an overwrite production refuses.
func stubRename(t *testing.T, f func(oldpath, newpath string) error) {
	t.Helper()
	orig := renameAt
	renameAt = func(from *os.Root, src string, to *os.Root, dst string) error {
		oldpath, newpath := filepath.Join(from.Name(), src), filepath.Join(to.Name(), dst)
		if _, err := os.Lstat(newpath); err == nil {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EEXIST}
		}
		return f(oldpath, newpath)
	}
	t.Cleanup(func() { renameAt = orig })
}

// renameNoReplace is os.Rename refusing, as renameAt does, to replace an
// entry already at newpath (fs.ErrExist): the pass-through for stubRename.
func renameNoReplace(oldpath, newpath string) error {
	if _, err := os.Lstat(newpath); err == nil {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EEXIST}
	}
	return os.Rename(oldpath, newpath)
}
