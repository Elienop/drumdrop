package library

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// TestClaimsCompareFoldersByIdentity (security L2) covers a filesystem that
// reads two folder names as one folder (case-insensitive: "Show" and "SHOW"):
// a path under the other spelling, at any folder level, is claimed by the
// lesson that records it, whether or not the file exists yet, and a folder
// under the other spelling holds what is recorded in it. A symlink stands in
// for the case fold: two names, one folder.
func TestClaimsCompareFoldersByIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	lib := filepath.Join(t.TempDir(), "lib")
	season := filepath.Join(lib, "Show", "Season 01")
	seedSeason(t, season, "Show - s01e05 - Groove.mp4")
	if err := os.Symlink("Show", filepath.Join(lib, "SHOW")); err != nil {
		t.Fatal(err)
	}
	a := recordedRow(1, season, "Show - s01e05 - Groove.mp4", "Show - s01e05 - Groove.nfo")
	c, err := NewClaims(lib, []database.Lesson{a})
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(lib, "SHOW", "Season 01")
	for _, name := range []string{"Show - s01e05 - Groove.mp4", "Show - s01e05 - Groove.nfo"} {
		if ids, err := c.Claimants(filepath.Join(alias, name), 2, true); err != nil || !reflect.DeepEqual(ids, []int{1}) {
			t.Errorf("Claimants(SHOW/.../%s) = (%v, %v), want [1]", name, ids, err)
		}
	}
	if ids, err := c.Claimants(filepath.Join(alias, "Show - s01e06 - Other.mp4"), 2, true); err != nil || len(ids) != 0 {
		t.Errorf("Claimants of an unclaimed name = (%v, %v), want none", ids, err)
	}
	if got := c.Holds(filepath.Join(lib, "SHOW"), 0); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("Holds(SHOW) = %v, want [1]", got)
	}
}

// TestRemoveLessonFolderGuards pins RemoveLessonFolder's two refusals, each
// with an input only it catches, and that it removes an unclaimed lesson
// folder whole.
func TestRemoveLessonFolderGuards(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show")
	seedSeason(t, show, "x.mp4")
	mine := filepath.Join(root, "Course", "05 - Five")
	seedSeason(t, mine, "v.mp4", "other.mp4")
	other := database.Lesson{RailcontentID: 2, VideoPath: sql.NullString{String: filepath.Join(mine, "other.mp4"), Valid: true}}
	c, err := NewClaims("", []database.Lesson{other})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveLessonFolder([]string{root}, show, 1); err == nil {
		t.Error("removed a folder not named like a lesson folder")
	}
	if err := c.RemoveLessonFolder([]string{root}, mine, 1); err == nil {
		t.Error("removed a folder holding another lesson's file")
	}
	assertExist(t, true, filepath.Join(show, "x.mp4"), filepath.Join(mine, "other.mp4"))
	if err := c.RemoveLessonFolder([]string{root}, mine, 2); err != nil {
		t.Errorf("RemoveLessonFolder of the holder's own folder = %v, want removed", err)
	}
	assertExist(t, false, mine)
}
