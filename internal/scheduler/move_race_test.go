package scheduler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// swapForSymlink moves folder dir aside (to dir+".aside") and puts a symlink
// to target in its place, as someone with write access to the library could
// between the move's checks and its rename.
func swapForSymlink(t *testing.T, dir, target string) {
	t.Helper()
	if err := os.Rename(dir, dir+".aside"); err != nil {
		t.Fatalf("move %q aside: %v", dir, err)
	}
	if err := os.Symlink(target, dir); err != nil {
		t.Fatalf("plant a symlink at %q: %v", dir, err)
	}
}

// swapBeforeFirstRename swaps dir for a symlink to target right before the
// first rename of the move, then renames as the move would.
func swapBeforeFirstRename(t *testing.T, dir, target string) {
	t.Helper()
	orig := renameAt
	swapped := false
	renameAt = func(from *os.Root, src string, to *os.Root, dst string) error {
		if !swapped {
			swapped = true
			swapForSymlink(t, dir, target)
		}
		return orig(from, src, to, dst)
	}
	t.Cleanup(func() { renameAt = orig })
}

// TestMovesStayInTheLibraryUnderARace (security L1) proves that a folder the
// move checked and opened, swapped for a symlink leading out of the library
// before the rename, can not redirect the placement: the rename acts on the
// folder the move holds open (renameat on its descriptor), so nothing lands
// outside and nothing outside is replaced. The platforms without renameat (see
// renameat_other.go) rename by path and are not covered.
func TestMovesStayInTheLibraryUnderARace(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the rename is by path on " + runtime.GOOS)
	}
	t.Run("default layout", func(t *testing.T) {
		tmp := t.TempDir()
		dl, lib, outside := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib"), filepath.Join(tmp, "outside")
		scratch := filepath.Join(dl, "Course", "05 - Five")
		seedSeason(t, scratch, "05 - Five.mp4", "05 - Five.nfo")
		seedSeason(t, outside)
		swapBeforeFirstRename(t, filepath.Join(lib, "Course"), outside)

		newDir, err := testMoveToLibrary(t, dl, lib, scratch)
		if len(readDirNames(t, outside)) != 0 {
			t.Fatalf("the move landed outside the library: %v (move = %q, %v)", readDirNames(t, outside), newDir, err)
		}
		assertExist(t, true, filepath.Join(lib, "Course.aside", "05 - Five", "05 - Five.mp4"))
	})
	t.Run("plex-tv", func(t *testing.T) {
		tmp := t.TempDir()
		dl, lib, outside := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib"), filepath.Join(tmp, "outside")
		scratch := filepath.Join(dl, "Course", "05 - Five")
		seedSeason(t, scratch, "05 - Five.mp4", "05 - Five.nfo")
		precious := filepath.Join(outside, "Show - s01e05 - Five.nfo")
		seedSeason(t, outside, "Show - s01e05 - Five.nfo")
		season := filepath.Join(lib, "Show", "Season 01")
		swapBeforeFirstRename(t, season, outside)

		res, err := testMovePlexTV(t, lib, "Show", 1, 5, "Five", scratch,
			plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}, downloads: dl})
		if got := readDirNames(t, outside); len(got) != 1 {
			t.Errorf("the move landed outside the library: %v (move = %+v, %v)", got, res, err)
		}
		if got, _ := os.ReadFile(precious); string(got) != "Show - s01e05 - Five.nfo" {
			t.Errorf("a file outside the library was replaced: %q", got)
		}
		assertExist(t, true, filepath.Join(season+".aside", "Show - s01e05 - Five.mp4"))
	})
}

// TestRenameNeverReplacesAnEntry (security L1) proves the rename refuses an
// entry already at its destination rather than replacing it: an entry that
// appears there after the move's checks is kept, and the move falls back to
// its copy, which refuses it too (O_EXCL).
func TestRenameNeverReplacesAnEntry(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the rename is by path on " + runtime.GOOS)
	}
	tmp := t.TempDir()
	seedSeason(t, filepath.Join(tmp, "a"), "x.mp4")
	seedSeason(t, filepath.Join(tmp, "b"), "y.mp4")
	a, err := os.OpenRoot(filepath.Join(tmp, "a"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := os.OpenRoot(filepath.Join(tmp, "b"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := renameIn(a, "x.mp4", b, "y.mp4"); err == nil {
		t.Error("renameIn replaced an existing entry, want a refusal")
	}
	if got, _ := os.ReadFile(filepath.Join(tmp, "b", "y.mp4")); string(got) != "y.mp4" {
		t.Errorf("the entry at the destination was replaced: %q", got)
	}
	if err := renameIn(a, "../x.mp4", b, "z.mp4"); err == nil {
		t.Error("renameIn took a name that is not a single part")
	}
}

// TestCopyNeverWritesThroughAPlantedSymlink (security L4, M07) proves the
// copy fallback creates every file afresh: a symlink planted at its
// destination, even one leading to another lesson's file inside the library,
// makes the copy fail rather than overwrite that file.
func TestCopyNeverWritesThroughAPlantedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	season := filepath.Join(tmp, "lib", "Show", "Season 01")
	seedSeason(t, season, "Show - s01e06 - Six.nfo")
	if err := os.Symlink("Show - s01e06 - Six.nfo", filepath.Join(season, "Show - s01e05 - Five.nfo")); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(tmp, "dl", "05 - Five")
	seedSeason(t, scratch, "05 - Five.nfo")
	dst, err := os.OpenRoot(season)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	src, err := os.OpenRoot(scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := copyFileInto(dst, "Show - s01e05 - Five.nfo", src, "05 - Five.nfo", 0o644); err == nil {
		t.Error("the copy wrote through a planted symlink, want a refusal")
	}
	if got, _ := os.ReadFile(filepath.Join(season, "Show - s01e06 - Six.nfo")); string(got) != "Show - s01e06 - Six.nfo" {
		t.Errorf("another lesson's nfo was overwritten: %q", got)
	}
}

// TestEpisodeNFONeverReplacesAPlantedSymlink (security L4, M25b) proves the
// episode nfo is written only over a regular nfo the download produced: a
// symlink at that name in scratch (planted, or anything else) is a note, not a
// write, so no episode nfo is placed and the symlink's target is untouched.
func TestEpisodeNFONeverReplacesAPlantedSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	scratch := filepath.Join(dl, "Course", "05 - Five")
	seedSeason(t, scratch, "05 - Five.mp4")
	victim := filepath.Join(tmp, "victim.nfo")
	seedSeason(t, tmp, "victim.nfo")
	if err := os.Symlink(victim, filepath.Join(scratch, "05 - Five.nfo")); err != nil {
		t.Fatal(err)
	}
	res, err := testMovePlexTV(t, lib, "Show", 1, 5, "Five", scratch,
		plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}, downloads: dl, episodeNFO: []byte("<episodedetails/>")})
	if got, _ := os.ReadFile(victim); string(got) != "victim.nfo" {
		t.Errorf("the symlink's target was written: %q", got)
	}
	season := filepath.Join(lib, "Show", "Season 01")
	assertExist(t, false, filepath.Join(season, "Show - s01e05 - Five.nfo"))
	assertExist(t, true, filepath.Join(season, "Show - s01e05 - Five.mp4"))
	if err == nil || res.seasonDir == "" {
		t.Errorf("move = %+v, %v; want the video placed with a note about the nfo", res, err)
	}
}
