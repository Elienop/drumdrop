package library

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// TestLeftBehind pins Claims.LeftBehind (owner ruling 2026-09-24 (y)): a
// season-folder row's files stayed behind when its recorded folder is still
// there and is not the folder it is read as under the library configured now.
// A library moved or remounted with its files (the recorded path is gone), a
// setting spelled another way (a symlink to the same folder), and nothing
// moved read as before; so does a lesson folder, which is never re-pointed.
func TestLeftBehind(t *testing.T) {
	video := "Show - s01e05 - Lesson A.mp4"
	for _, c := range []struct {
		name string
		// lib is the library setting now, row the season folder the row
		// records, files where the files are ("" for none), all relative to
		// a temporary folder.
		lib, row, files string
		// link, when set, is a symlink made at link, leading to lib's real
		// folder "media/lib": the setting then reads through it.
		link     string
		recorded bool
		want     bool
	}{
		{name: "nothing moved", lib: "media", row: "media/Show/Season 01", files: "media/Show/Season 01", want: false},
		{name: "nothing moved, recorded", lib: "media", row: "media/Show/Season 01", files: "media/Show/Season 01", recorded: true, want: false},
		{name: "moved up, files left", lib: "media", row: "media/drumeo/Show/Season 01", files: "media/drumeo/Show/Season 01", want: true},
		{name: "moved up, files left, recorded", lib: "media", row: "media/drumeo/Show/Season 01", files: "media/drumeo/Show/Season 01", recorded: true, want: true},
		{name: "moved down, files left", lib: "media/lib", row: "media/Show/Season 01", files: "media/Show/Season 01", want: true},
		{name: "moved sideways, files left, a season folder at the new place too", lib: "new", row: "old/Show/Season 01", files: "old/Show/Season 01", want: true},
		{name: "remounted with its files", lib: "new", row: "old/Show/Season 01", files: "new/Show/Season 01", want: false},
		{name: "remounted, nothing there", lib: "new", row: "old/Show/Season 01", want: false},
		{name: "the setting through a symlink to the same folder", lib: "linked", link: "linked", row: "media/lib/Show/Season 01", files: "media/lib/Show/Season 01", want: false},
		{name: "a lesson folder, not re-pointed", lib: "media", row: "media/drumeo/Show/05 - Lesson A", files: "media/drumeo/Show/05 - Lesson A", want: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			tmp := t.TempDir()
			abs := func(rel string) string { return filepath.Join(tmp, filepath.FromSlash(rel)) }
			lib := abs(c.lib)
			if c.link != "" {
				if err := os.MkdirAll(abs("media/lib"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(abs("media/lib"), abs(c.link)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(lib, 0o755); err != nil {
				t.Fatal(err)
			}
			if c.files != "" {
				seedSeason(t, abs(c.files), video)
			}
			if c.name == "moved sideways, files left, a season folder at the new place too" {
				seedSeason(t, filepath.Join(lib, "Show", "Season 01"), "Show - s01e06 - Lesson B.mp4")
			}
			row := legacyRow(100, "Lesson A", 5, abs(c.row), video)
			if c.recorded {
				row = recordedRow(100, abs(c.row), video)
			}
			claims, err := NewClaims(lib, []database.Lesson{row})
			if err != nil {
				t.Fatal(err)
			}
			dir, got := claims.LeftBehind(row)
			if got != c.want {
				t.Fatalf("LeftBehind = %q, %v; want %v", dir, got, c.want)
			}
			if got && dir != abs(c.row) {
				t.Errorf("LeftBehind named %q, want the recorded folder %q", dir, abs(c.row))
			}
		})
	}
}

// TestLeftBehindWithoutALibrary proves a season-folder row is not reported
// left behind when no library folder is configured: nothing reads it under a
// library then (Plan refuses a season folder), so there is no other folder to
// compare it with.
func TestLeftBehindWithoutALibrary(t *testing.T) {
	season := filepath.Join(t.TempDir(), "lib", "Show", "Season 01")
	seedSeason(t, season, "Show - s01e05 - Lesson A.mp4")
	row := database.Lesson{RailcontentID: 100, OutputDir: sql.NullString{String: season, Valid: true}}
	claims, err := NewClaims("", []database.Lesson{row})
	if err != nil {
		t.Fatal(err)
	}
	if dir, ok := claims.LeftBehind(row); ok {
		t.Errorf("LeftBehind = %q, true; want false without a library", dir)
	}
}
