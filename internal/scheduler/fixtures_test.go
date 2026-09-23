package scheduler

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// testMovePlexTV runs the plex-tv move as the worker does, with the claims of
// others (none when empty) unless pl already carries claims.
func testMovePlexTV(t *testing.T, libraryDir, show string, season, episode int, title, lessonDir string, pl plexLibrary, others ...database.Lesson) (plexMoveResult, error) {
	t.Helper()
	if pl.claims == nil {
		c, err := library.NewClaims(libraryDir, others)
		if err != nil {
			t.Fatalf("NewClaims: %v", err)
		}
		pl.claims = c
	}
	if pl.downloads == "" {
		pl.downloads = filepath.Dir(lessonDir)
	}
	return moveToLibraryPlexTV(libraryDir, show, season, episode, title, lessonDir, pl)
}

// testMoveToLibrary runs the default-layout move as the worker does, as
// lesson 1, with the claims of others (none when empty).
func testMoveToLibrary(t *testing.T, downloadsDir, libraryDir, lessonDir string, others ...database.Lesson) (string, error) {
	t.Helper()
	c, err := library.NewClaims(libraryDir, others)
	if err != nil {
		t.Fatalf("NewClaims: %v", err)
	}
	return moveToLibrary(downloadsDir, libraryDir, lessonDir, c, 1)
}

// stubRename makes every rename of the moves call f with the two full paths
// instead, for the rest of the test (f may call os.Rename to let one through).
func stubRename(t *testing.T, f func(oldpath, newpath string) error) {
	t.Helper()
	orig := renameAt
	renameAt = func(from *os.Root, src string, to *os.Root, dst string) error {
		return f(filepath.Join(from.Name(), src), filepath.Join(to.Name(), dst))
	}
	t.Cleanup(func() { renameAt = orig })
}
