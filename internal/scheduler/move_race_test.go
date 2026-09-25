package scheduler

import (
	"fmt"
	"io/fs"
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

		newDir, err := testPlace(t, dl, lib, scratch, database.Lesson{})
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

		res, err := testMovePlexTVFrom(t, dl, lib, plexEpisode{"Show", 1, 5, "Five"}, scratch,
			plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}})
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
// entry already at its destination rather than replacing it (the whole move
// then refuses too, see TestMovesKeepAnEntryPlantedAtTheDestination).
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
	seedSeason(t, tmp, "x.mp4") // what "../x.mp4" would reach from a
	if err := renameIn(a, "../x.mp4", b, "z.mp4"); err == nil {
		t.Error("renameIn took a name that is not a single part")
	}
	assertExist(t, true, filepath.Join(tmp, "x.mp4"))
}

// plantBeforeFirstRename puts a folder holding "racer.txt" at the destination
// of the move's first rename, right before it (as another writer with access
// to the library could, after the move's checks), then answers as the rename
// would, or with a cross-filesystem error when viaCopy is set, so the move takes
// its copy path. It returns where the entry was planted.
func plantBeforeFirstRename(t *testing.T, viaCopy bool) *string {
	t.Helper()
	orig := renameAt
	planted := new(string)
	renameAt = func(from *os.Root, src string, to *os.Root, dst string) error {
		if *planted == "" {
			*planted = filepath.Join(to.Name(), dst)
			seedSeason(t, *planted, "racer.txt")
		}
		if viaCopy {
			return errInjectedRename
		}
		return orig(from, src, to, dst)
	}
	t.Cleanup(func() { renameAt = orig })
	return planted
}

// TestMovesKeepAnEntryPlantedAtTheDestination (code #2, security LOW-2)
// proves a whole move, in both layouts, never removes or writes into an entry
// that appeared at its destination after its checks: the no-replace rename
// refuses it, and so does the copy across filesystems, whose cleanup removes
// only what the copy created. The move reports failure, and the lesson stays
// whole in downloads.
func TestMovesKeepAnEntryPlantedAtTheDestination(t *testing.T) {
	for _, viaCopy := range []bool{false, true} {
		if !viaCopy && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			continue // the rename replaces by path there (renameat_other.go)
		}
		for _, layout := range []string{"", LayoutPlexTV} {
			t.Run(fmt.Sprintf("copy=%v/layout=%s", viaCopy, layout), func(t *testing.T) {
				checkMoveKeepsAnEntryPlantedAtTheDestination(t, viaCopy, layout)
			})
		}
	}
}

// checkMoveKeepsAnEntryPlantedAtTheDestination is
// TestMovesKeepAnEntryPlantedAtTheDestination for a move in layout, by
// rename or by copy (viaCopy).
func checkMoveKeepsAnEntryPlantedAtTheDestination(t *testing.T, viaCopy bool, layout string) {
	t.Helper()
	tmp := t.TempDir()
	dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	scratch := filepath.Join(dl, "Course", "05 - Five")
	seedSeason(t, scratch, "05 - Five.mp4", "05 - Five.nfo")
	planted := plantBeforeFirstRename(t, viaCopy)
	moved, err := placeFiveIn(t, layout, dl, lib, scratch,
		plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}}, database.Lesson{})
	if *planted == "" {
		t.Fatal("the move never renamed")
	}
	if err == nil || moved != "" {
		t.Errorf("move = %q, %v; want a refusal with the lesson left in downloads", moved, err)
	}
	assertContent(t, *planted, "racer.txt")
	if got := readDirNames(t, *planted); len(got) != 1 {
		t.Errorf("the planted entry holds %v, want only racer.txt", got)
	}
	assertContent(t, scratch, "05 - Five.mp4", "05 - Five.nfo")
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
	res, err := testMovePlexTVFrom(t, dl, lib, plexEpisode{"Show", 1, 5, "Five"}, scratch,
		plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}, episodeNFO: []byte("<episodedetails/>")})
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

// TestEpisodeNFOSurvivesALeftoverTemporaryFile (security Info 3) proves a
// temporary episode nfo left by a run that died does not stop the next write:
// it is removed first, and the episode nfo takes the nfo's place.
func TestEpisodeNFOSurvivesALeftoverTemporaryFile(t *testing.T) {
	scratch := filepath.Join(t.TempDir(), "05 - Five")
	seedSeason(t, scratch, "05 - Five.nfo", "05 - Five.nfo"+episodeTempSuffix)
	dir, err := os.OpenRoot(scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := writeScratchNFO(dir, "05 - Five.nfo", []byte("<episodedetails/>")); err != nil {
		t.Fatalf("writeScratchNFO: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(scratch, "05 - Five.nfo")); string(got) != "<episodedetails/>" {
		t.Errorf("nfo = %q, want the episode nfo", got)
	}
	assertExist(t, false, filepath.Join(scratch, "05 - Five.nfo"+episodeTempSuffix))
}

// TestMovesCopyOnlyAcrossFilesystems (code #2) proves the copy fallback is
// only for a rename between two filesystems: any other rename failure (here a
// permission error) refuses the move, in both layouts, and nothing reaches the
// library.
func TestMovesCopyOnlyAcrossFilesystems(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		t.Run("layout="+layout, func(t *testing.T) {
			tmp := t.TempDir()
			dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
			scratch := filepath.Join(dl, "Course", "05 - Five")
			seedSeason(t, scratch, "05 - Five.mp4", "05 - Five.nfo")
			stubRename(t, func(oldpath, newpath string) error {
				return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrPermission}
			})
			var moved string
			var err error
			if layout == LayoutPlexTV {
				var res plexMoveResult
				res, err = testMovePlexTVFrom(t, dl, lib, plexEpisode{"Show", 1, 5, "Five"}, scratch,
					plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}})
				moved = res.seasonDir
			} else {
				moved, err = testPlace(t, dl, lib, scratch, database.Lesson{})
			}
			if err == nil || moved != "" {
				t.Errorf("move = %q, %v; want a refusal", moved, err)
			}
			if p := findContent(t, lib, "05 - Five.mp4"); p != "" {
				t.Errorf("the lesson was copied into the library at %q", p)
			}
			assertContent(t, scratch, "05 - Five.mp4", "05 - Five.nfo")
		})
	}
}
