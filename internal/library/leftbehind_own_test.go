package library

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// The lesson of the tests below: "Lesson A", episode 5 of "Show", filed in
// <tmp>/old/Show/Season 01 while the library setting now reads <tmp>/new.
var (
	ownNames     = []string{"Show - s01e05 - Lesson A.mp4", "Show - s01e05 - Lesson A.nfo", "Show - s01e05 - Lesson A.en.vtt"}
	siblingNames = []string{"Show - s01e06 - Lesson B.mp4", "Show - s01e06 - Lesson B.nfo"}
)

// leftBehindRow is lesson 100 filed in season, with a record of ownNames and
// its video, or as a legacy row (its video only).
func leftBehindRow(season string, recorded bool) database.Lesson {
	if recorded {
		row := recordedRow(100, season, ownNames...)
		row.VideoPath = sql.NullString{String: filepath.Join(season, ownNames[0]), Valid: true}
		return row
	}
	return legacyRow(100, "Lesson A", 5, season, ownNames[0])
}

// movedLibrary makes <tmp>/new (the library setting now) and returns it with
// the old season folder <tmp>/old/Show/Season 01 (not made).
func movedLibrary(t *testing.T) (tmp, lib, oldSeason string) {
	t.Helper()
	tmp = t.TempDir()
	lib = filepath.Join(tmp, "new")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	return tmp, lib, filepath.Join(tmp, "old", "Show", "Season 01")
}

// leftBehind is NewClaims(lib, [row]).LeftBehind(row).
func leftBehind(t *testing.T, lib string, row database.Lesson) (string, bool, error) {
	t.Helper()
	claims, err := NewClaims(lib, []database.Lesson{row})
	if err != nil {
		t.Fatal(err)
	}
	return claims.LeftBehind(row)
}

// TestLeftBehindAsksForTheLessonsOwnFiles pins round 5j's J1 (the seats' E1,
// rsync --remove-source-files, and the moved-only-this-lesson probe): a
// lesson is left behind only while one of its OWN files (a name its record
// gives, its video, or for a legacy row a name the episode grammar gives it)
// is still in the folder its row records. An old folder the files were moved
// out of, emptied or holding only another lesson's files, is not; a partial
// move, or a copy kept in both folders, still is.
func TestLeftBehindAsksForTheLessonsOwnFiles(t *testing.T) {
	for _, c := range []struct {
		name string
		// old and new are the names in the old and in the new season folder
		// (nil: that folder is not there; empty: it is there, empty).
		old, new []string
		want     bool
	}{
		{name: "emptied: every file moved to the same place, the old folder left", old: []string{}, new: ownNames},
		{name: "only a sibling's files left in the old folder", old: siblingNames, new: ownNames},
		{name: "a partial move: the video moved, its .nfo still in the old folder", old: ownNames[1:2], new: []string{ownNames[0], ownNames[2]}, want: true},
		{name: "a partial move: only the video still in the old folder", old: ownNames[:1], new: ownNames[1:], want: true},
		{name: "a copy kept in both folders", old: ownNames, new: ownNames, want: true},
		{name: "nothing moved, a sibling beside it", old: append(append([]string{}, ownNames...), siblingNames...), want: true},
	} {
		for _, recorded := range []bool{true, false} {
			t.Run(c.name+"/recorded="+strconv.FormatBool(recorded), func(t *testing.T) {
				_, lib, oldSeason := movedLibrary(t)
				if c.old != nil {
					seedSeason(t, oldSeason, c.old...)
				}
				if c.new != nil {
					seedSeason(t, filepath.Join(lib, "Show", "Season 01"), c.new...)
				}
				dir, got, err := leftBehind(t, lib, leftBehindRow(oldSeason, recorded))
				if err != nil || got != c.want {
					t.Fatalf("LeftBehind = %q, %v, %v; want %v, no error", dir, got, err, c.want)
				}
				if got && dir != oldSeason {
					t.Errorf("LeftBehind named %q, want the recorded folder %q", dir, oldSeason)
				}
			})
		}
	}
}

// TestLeftBehindReadsAPathThroughAFileAsGone pins round 5i's S3b: an old
// library folder that is now a regular file makes the recorded season folder
// answer ENOTDIR, which proves it gone (Go maps only ENOENT to
// fs.ErrNotExist), so the lesson is read as before, not refused. A recorded
// season folder that is itself a regular file is gone too (round 5j code Info
// 7, security S4): read as a folder, a legacy row's listing would fail and
// refuse with "couldn't read" instead.
func TestLeftBehindReadsAPathThroughAFileAsGone(t *testing.T) {
	for _, c := range []struct {
		name string
		// file is the path made a regular file, given <tmp> and the old
		// season folder.
		file func(tmp, oldSeason string) string
	}{
		{"the old library folder", func(tmp, _ string) string { return filepath.Join(tmp, "old") }},
		{"the season folder", func(_, oldSeason string) string { return oldSeason }},
	} {
		for _, recorded := range []bool{true, false} {
			t.Run(c.name+"/recorded="+strconv.FormatBool(recorded), func(t *testing.T) {
				tmp, lib, oldSeason := movedLibrary(t)
				seedSeason(t, filepath.Join(lib, "Show", "Season 01"), ownNames...)
				writeNotAFolder(t, c.file(tmp, oldSeason))
				if dir, left, err := leftBehind(t, lib, leftBehindRow(oldSeason, recorded)); left || err != nil {
					t.Errorf("LeftBehind = %q, %v, %v; want false, no error", dir, left, err)
				}
			})
		}
	}
}

// writeNotAFolder makes path a regular file, making its parent folders.
func writeNotAFolder(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLeftBehindCountsTheVideoOnlyInTheOldFolder pins round 5j code Info 7
// (X3): the recorded video counts as one of the lesson's own files in the old
// folder only when it is in that folder. A video path naming a file anywhere
// else (here the new season folder, where it exists) says nothing about what
// was left behind, so an old folder holding only a sibling's files is not.
// The worker never records such a path; a count of it would refuse a lesson
// whose files all moved.
func TestLeftBehindCountsTheVideoOnlyInTheOldFolder(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		t.Run("recorded="+strconv.FormatBool(recorded), func(t *testing.T) {
			_, lib, oldSeason := movedLibrary(t)
			newSeason := filepath.Join(lib, "Show", "Season 01")
			seedSeason(t, oldSeason, siblingNames...)
			seedSeason(t, newSeason, ownNames...)
			row := leftBehindRow(oldSeason, recorded)
			row.VideoPath = sql.NullString{String: filepath.Join(newSeason, ownNames[0]), Valid: true}
			if dir, left, err := leftBehind(t, lib, row); left || err != nil {
				t.Errorf("LeftBehind = %q, %v, %v; want false, no error", dir, left, err)
			}
		})
	}
}

// chmodOrSkip sets dir's mode until the test ends; it skips where a mode
// doesn't refuse a read (root, Windows).
func chmodOrSkip(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a folder's mode doesn't refuse this user")
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// TestLeftBehindFailsClosed pins "when unsure, keep" (round 5i security S2,
// code Low 3): with the old folder unreadable while the lesson's files are
// still in it, LeftBehind reports it left behind, with the error for the log.
// At both levels: the old library folder at mode 000 (the season folder can't
// be stat'ed), and the season folder itself (it can, but its files can't be
// looked up nor listed).
func TestLeftBehindFailsClosed(t *testing.T) {
	for _, level := range []string{"old library folder", "season folder"} {
		for _, recorded := range []bool{true, false} {
			t.Run(level+"/recorded="+strconv.FormatBool(recorded), func(t *testing.T) {
				tmp, lib, oldSeason := movedLibrary(t)
				seedSeason(t, oldSeason, ownNames...)
				row := leftBehindRow(oldSeason, recorded)
				claims, err := NewClaims(lib, []database.Lesson{row})
				if err != nil {
					t.Fatal(err)
				}
				locked := filepath.Join(tmp, "old")
				if level == "season folder" {
					locked = oldSeason
				}
				chmodOrSkip(t, locked, 0)
				dir, left, err := claims.LeftBehind(row)
				if !left || !errors.Is(err, fs.ErrPermission) || dir != oldSeason {
					t.Errorf("LeftBehind = %q, %v, %v; want %q, true and the permission error", dir, left, err, oldSeason)
				}
			})
		}
	}
}

// TestLeftBehindCountsEachOwnFileAlone proves each of the lesson's names is
// asked on its own: a legacy row that records no video still fails closed
// when its season folder can't be listed, and a record that doesn't name the
// video still counts the video, when it is all that is left.
func TestLeftBehindCountsEachOwnFileAlone(t *testing.T) {
	t.Run("a legacy row without a video, its folder unreadable", func(t *testing.T) {
		_, lib, oldSeason := movedLibrary(t)
		seedSeason(t, oldSeason, ownNames[1:]...)
		row := legacyRow(100, "Lesson A", 5, oldSeason, "")
		claims, err := NewClaims(lib, []database.Lesson{row})
		if err != nil {
			t.Fatal(err)
		}
		chmodOrSkip(t, oldSeason, 0)
		if dir, left, err := claims.LeftBehind(row); !left || !errors.Is(err, fs.ErrPermission) {
			t.Errorf("LeftBehind = %q, %v, %v; want true and the permission error", dir, left, err)
		}
	})
	t.Run("a record that doesn't name the video, only the video left", func(t *testing.T) {
		_, lib, oldSeason := movedLibrary(t)
		seedSeason(t, oldSeason, ownNames[0])
		row := recordedRow(100, oldSeason, ownNames[1:]...)
		row.VideoPath = sql.NullString{String: filepath.Join(oldSeason, ownNames[0]), Valid: true}
		if dir, left, err := leftBehind(t, lib, row); !left || err != nil {
			t.Errorf("LeftBehind = %q, %v, %v; want true, no error", dir, left, err)
		}
	})
}

// TestLeftBehindReadsNothingWithTheSettingUnchanged pins round 5i's S4: the
// paths are compared before any stat, so a row filed where the setting points
// now is never looked up on disk (a hung mount would park the delete). The
// library folder at mode 000 stands in for a folder a stat can't answer: a
// stat first would fail closed and refuse.
func TestLeftBehindReadsNothingWithTheSettingUnchanged(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		t.Run("recorded="+strconv.FormatBool(recorded), func(t *testing.T) {
			lib := filepath.Join(t.TempDir(), "lib")
			season := filepath.Join(lib, "Show", "Season 01")
			seedSeason(t, season, ownNames...)
			row := leftBehindRow(season, recorded)
			claims, err := NewClaims(lib, []database.Lesson{row})
			if err != nil {
				t.Fatal(err)
			}
			chmodOrSkip(t, lib, 0)
			if dir, left, err := claims.LeftBehind(row); left || err != nil {
				t.Errorf("LeftBehind = %q, %v, %v; want false, no error, nothing read", dir, left, err)
			}
		})
	}
}

// TestLeftBehindTriesEveryNameOfAnUnsettledLegacyEpisode pins that an unsettled
// legacy name counts a file under any of its candidate names. Titled "Six",
// episode 5, video "…Five [Live].mp4" and no .nfo, the row's names are the
// stem, "…Five" and the title's "…Six" (legacyEpisodeBases); the subtitle left
// behind matches only the middle one, so trying only the first or only the
// last name would read it as moved.
func TestLeftBehindTriesEveryNameOfAnUnsettledLegacyEpisode(t *testing.T) {
	_, lib, oldSeason := movedLibrary(t)
	seedSeason(t, oldSeason, "Show - s01e05 - Five.en.vtt")
	row := legacyRow(100, "Six", 5, oldSeason, "Show - s01e05 - Five [Live].mp4")
	if dir, left, err := leftBehind(t, lib, row); !left || err != nil {
		t.Errorf("LeftBehind = %q, %v, %v; want true (the middle name's subtitle is still there)", dir, left, err)
	}
}
