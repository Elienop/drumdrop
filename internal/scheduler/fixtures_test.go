package scheduler

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
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

// recordedRow is a lesson filed in seasonDir that records exactly names.
func recordedRow(id int, seasonDir string, names ...string) database.Lesson {
	return database.Lesson{
		RailcontentID:  id,
		Status:         database.StatusDownloaded,
		OutputDir:      sql.NullString{String: seasonDir, Valid: true},
		LibraryEntries: database.EncodeLibraryEntries(paths(seasonDir, names...)),
	}
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
