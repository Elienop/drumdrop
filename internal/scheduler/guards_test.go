package scheduler

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// The tests below pin data-safety guards that no test failed without (round-5
// code seat Low 2, security L1).

// refuseRenames makes every rename refused (a permission error) when refuse
// says so for its two full paths, and lets every other one through.
func refuseRenames(t *testing.T, refuse func(oldpath, newpath string) bool) {
	t.Helper()
	stubRename(t, func(oldpath, newpath string) error {
		if refuse(oldpath, newpath) {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrPermission}
		}
		return renameNoReplace(oldpath, newpath)
	})
}

// TestAnEntryThatCannotGoBackKeepsTheSetAsideArea proves the set-aside area
// is kept, holding the only copy of the entry it could not put back, when a
// placement is undone (its record was refused), and when a placement fails,
// in both layouts: removing the area would delete the lesson's earlier file.
func TestAnEntryThatCannotGoBackKeepsTheSetAsideArea(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layout string
		undo   bool // the placement succeeds and is then undone
	}{
		{"default/undo", "", true},
		{"default/placement fails", "", false},
		{"plex-tv/undo", LayoutPlexTV, true},
		{"plex-tv/placement fails", LayoutPlexTV, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkAsideAreaKeptForAnEntryThatCannotGoBack(t, tc.layout, tc.undo)
		})
	}
}

// checkAsideAreaKeptForAnEntryThatCannotGoBack is
// TestAnEntryThatCannotGoBackKeepsTheSetAsideArea in layout, for a placement
// that succeeds and is then undone (undo), or one that fails.
func checkAsideAreaKeptForAnEntryThatCannotGoBack(t *testing.T, layout string, undo bool) {
	t.Helper()
	tmp := t.TempDir()
	dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	scratch := filepath.Join(dl, "Course", "05 - Five")
	writeTree(t, scratch, map[string]string{"05 - Five.mp4": "new mp4", "05 - Five.nfo": "new nfo"})
	old := filepath.Join(lib, "Course", "05 - Five", "05 - Five.mp4")
	if layout == LayoutPlexTV {
		old = filepath.Join(lib, "Show", "Season 01", "Show - s01e05 - Five.mp4")
	}
	writeTree(t, filepath.Dir(old), map[string]string{filepath.Base(old): "old mp4"})
	private := filepath.Join(lib, privateRootName)
	refuseRenames(t, func(oldpath, newpath string) bool {
		return library.Inside(private, oldpath) || (!undo && filepath.Ext(newpath) == ".nfo")
	})

	pl, err := placePendingFive(t, layout, dl, lib, scratch)
	if undo {
		err = undoLeavingOnly(t, pl, err, old)
	} else if pl != nil {
		t.Fatal("the placement succeeded, want the refused nfo's failure")
	}
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("could not put %q back", old)) {
		t.Errorf("err = %v, want the entry that could not go back named", err)
	}
	if p := findContent(t, private, "old mp4"); p == "" {
		t.Errorf("the earlier file's only copy is gone: the set-aside area was removed")
	}
}

// placePendingFive places the downloaded lesson scratch (read through dl)
// into lib in layout, as lesson 1, and returns the placement uncommitted (nil
// when nothing was placed). The downloaded folder stays open until the test
// ends, so the placement can be undone.
func placePendingFive(t *testing.T, layout, dl, lib, scratch string) (*placement, error) {
	t.Helper()
	if layout != LayoutPlexTV {
		return testPlacePending(t, dl, lib, scratch, database.Lesson{})
	}
	src, err := openScratch(dl, scratch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(src.close)
	c, err := library.NewClaims(lib, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := moveToLibraryPlexTV(lib, "Show", 1, 5, "Five", src, plexLibrary{self: database.Lesson{RailcontentID: 1}, claims: c, roots: []string{lib, dl}})
	return res.pending, err
}

// undoLeavingOnly undoes the placement pl, which must have been placed (its
// error placeErr nil), fails unless the undo left exactly old where it was,
// and returns the undo's error.
func undoLeavingOnly(t *testing.T, pl *placement, placeErr error, old string) error {
	t.Helper()
	if placeErr != nil || pl == nil {
		t.Fatalf("placement = %v, want it placed", placeErr)
	}
	stuck, err := pl.undo(false)
	if len(stuck) != 1 || stuck[0] != old {
		t.Errorf("undo left %q, want only %q", stuck, old)
	}
	return err
}

// TestPreviousFolderThatIsNotTheLessonsIsNeverReplaced proves a placement
// sets aside (and at the commit deletes) the lesson's previous folder only
// when its row's folder is named like a lesson folder, and neither holds nor
// is held by the destination, nor is the destination under another path: a
// damaged or odd output_dir never costs another folder's files.
func TestPreviousFolderThatIsNotTheLessonsIsNeverReplaced(t *testing.T) {
	for _, tc := range []previousNotTheLessonsCase{
		{name: "a course folder, not a lesson folder", rel: "Course/05 - Five", prev: "dl/Course"},
		{name: "a lesson-named folder holding the destination", rel: "05 - Old/05 - Five", prev: "lib/05 - Old"},
		{name: "a lesson-named folder inside the destination", rel: "Course/05 - Five", prev: "lib/Course/05 - Five/07 - Inner"},
		{name: "the destination under another path", rel: "Course/05 - Five", prev: "lib/Alias/05 - Five", symlink: "Course"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkPreviousFolderNotTheLessonsStays(t, tc)
		})
	}
}

// previousNotTheLessonsCase is a row's recorded folder that is not the
// lesson's previous folder.
type previousNotTheLessonsCase struct {
	name string
	// rel is the lesson's folder, relative to the downloads and library
	// folders; prev is its row's recorded folder, relative to tmp.
	rel, prev string
	// symlink, when set, is made at prev (relative to tmp) pointing to it.
	symlink string
}

// checkPreviousFolderNotTheLessonsStays is one case of
// TestPreviousFolderThatIsNotTheLessonsIsNeverReplaced.
func checkPreviousFolderNotTheLessonsStays(t *testing.T, tc previousNotTheLessonsCase) {
	t.Helper()
	tmp := t.TempDir()
	dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	scratch := filepath.Join(dl, filepath.FromSlash(tc.rel))
	writeTree(t, scratch, map[string]string{"05 - Five.mp4": "new mp4"})
	dest := filepath.Join(lib, filepath.FromSlash(tc.rel))
	writeTree(t, dest, map[string]string{"notes.txt": "owner notes"})
	prev := filepath.Join(tmp, filepath.FromSlash(tc.prev))
	if tc.symlink != "" {
		if err := os.Symlink(tc.symlink, filepath.Dir(prev)); err != nil {
			t.Skipf("symlink: %v", err)
		}
	} else {
		writeTree(t, prev, map[string]string{"keep.txt": "not the lesson's"})
	}
	self := database.Lesson{RailcontentID: 1, OutputDir: sql.NullString{String: prev, Valid: true}}

	if _, err := testPlace(t, dl, lib, scratch, self); err != nil {
		t.Fatalf("placeLessonFolder: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "notes.txt")); string(got) != "owner notes" {
		t.Errorf("notes.txt = %q, want the destination's file kept", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "05 - Five.mp4")); string(got) != "new mp4" {
		t.Errorf("video = %q, want the new download placed", got)
	}
	if tc.symlink == "" {
		if got, _ := os.ReadFile(filepath.Join(prev, "keep.txt")); string(got) != "not the lesson's" {
			t.Errorf("%s/keep.txt = %q, want the recorded folder left alone", prev, got)
		}
	}
}

// TestCopyKeepsAFolderPlantedAtTheDestination (security L1, round-4 LOW-2)
// proves the copy across filesystems of a FOLDER removes, when it fails, only
// what it created: a folder that appeared at its destination after the
// checks (the copy's first Mkdir refuses it) is left exactly as it is, in
// both layouts, and the download stays whole in its folder.
func TestCopyKeepsAFolderPlantedAtTheDestination(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		t.Run("layout="+layout, func(t *testing.T) {
			checkCopyKeepsAFolderPlantedAtTheDestination(t, layout)
		})
	}
}

// checkCopyKeepsAFolderPlantedAtTheDestination is
// TestCopyKeepsAFolderPlantedAtTheDestination in layout.
func checkCopyKeepsAFolderPlantedAtTheDestination(t *testing.T, layout string) {
	t.Helper()
	tmp := t.TempDir()
	dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	scratch := filepath.Join(dl, "Course", "05 - Five")
	seedSeason(t, scratch, "resources/")
	planted := plantBeforeFirstRename(t, true)

	moved, err := placeFiveIn(t, layout, dl, lib, scratch,
		plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}}, database.Lesson{})
	if *planted == "" {
		t.Fatal("the placement never renamed")
	}
	if filepath.Base(*planted) != "resources" && !strings.HasSuffix(*planted, " resources") {
		t.Fatalf("planted at %q, want the folder's destination", *planted)
	}
	if err == nil || moved != "" {
		t.Errorf("placement = %q, %v; want a refusal", moved, err)
	}
	assertContent(t, *planted, "racer.txt")
	if got := readDirNames(t, *planted); len(got) != 1 {
		t.Errorf("the planted folder holds %v, want only racer.txt", got)
	}
	assertSeeded(t, scratch, "resources/")
}

// TestStubRenameNeverReplaces pins the test helper to renameAt's contract
// (round-5 fix code I2): a stubbed rename onto an existing entry fails with
// fs.ErrExist, even when the stub would let it through, and the entry there
// is untouched.
func TestStubRenameNeverReplaces(t *testing.T) {
	tmp := t.TempDir()
	writeTree(t, tmp, map[string]string{"a/x": "moving", "b/x": "there"})
	stubRename(t, os.Rename)
	from, err := os.OpenRoot(filepath.Join(tmp, "a"))
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	to, err := os.OpenRoot(filepath.Join(tmp, "b"))
	if err != nil {
		t.Fatal(err)
	}
	defer to.Close()
	if err := renameAt(from, "x", to, "x"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("rename onto an existing entry = %v, want fs.ErrExist", err)
	}
	assertTree(t, tmp, map[string]string{"a/x": "moving", "b/x": "there"})
	if err := renameNoReplace(filepath.Join(tmp, "a", "x"), filepath.Join(tmp, "b", "x")); !errors.Is(err, fs.ErrExist) {
		t.Errorf("renameNoReplace onto an existing entry = %v, want fs.ErrExist", err)
	}
}
