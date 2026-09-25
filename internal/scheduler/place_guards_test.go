package scheduler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// TestPlacementWhoseNewFolderFlushFailsIsUndone (round-5 fix code L3) proves a
// failed flush of the parent of a lesson folder the placement made (the
// folder's own name) fails the placement: the download is whole in its
// private folder, and the folder it made is gone again.
func TestPlacementWhoseNewFolderFlushFailsIsUndone(t *testing.T) {
	tmp := t.TempDir()
	dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	scratch := filepath.Join(dl, "Course", "05 - Five")
	download := map[string]string{"05 - Five.mp4": "new mp4", "resources/a.pdf": "new a"}
	writeTree(t, scratch, download)
	dest := filepath.Join(lib, "Course", "05 - Five")

	flushed, placedIn, err := placeRecordingFlushes(t, "", dl, lib, scratch, filepath.Dir(dest))
	if err == nil || placedIn != "" {
		t.Fatalf("placement = %q, %v; want the flush failure (flushed %q)", placedIn, err, flushed)
	}
	assertTree(t, scratch, download)
	assertExist(t, false, dest)
}

// noClaims is the claims of a library no lesson records anything in.
func noClaims(t *testing.T, lib string) *library.Claims {
	t.Helper()
	c, err := library.NewClaims(lib, nil)
	if err != nil {
		t.Fatalf("NewClaims: %v", err)
	}
	return c
}

// openFDsUnder counts this process's open descriptors on dir or anything in
// it (Linux: /proc/self/fd, whose links name what each is open on). Only
// those count: another test's leaked descriptors, which a finalizer may close
// at any moment, are elsewhere (round-5l security seat I2).
func openFDsUnder(t *testing.T, dir string) int {
	t.Helper()
	// The links name the resolved path.
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	n := 0
	for _, e := range entries {
		// A descriptor closed since the listing has no link: not open.
		target, lerr := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if lerr == nil && library.Inside(dir, target) {
			n++
		}
	}
	return n
}

// TestPlacementReleasesItsFolders (round-5 fix security I-2) proves a
// placement holds no folder open once it is committed, or undone: the
// folders a merged subfolder holds (both sides, at every depth) included.
func TestPlacementReleasesItsFolders(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("counts descriptors through /proc/self/fd")
	}
	for _, layout := range []string{"", LayoutPlexTV} {
		for _, commit := range []bool{true, false} {
			t.Run(map[bool]string{true: "commit", false: "undo"}[commit]+"/layout="+layout, func(t *testing.T) {
				checkPlacementReleasesItsFolders(t, layout, commit)
			})
		}
	}
}

// checkPlacementReleasesItsFolders is TestPlacementReleasesItsFolders in
// layout, for a placement committed (commit) or undone.
func checkPlacementReleasesItsFolders(t *testing.T, layout string, commit bool) {
	t.Helper()
	tmp := t.TempDir()
	dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	scratch := filepath.Join(dl, "Course", "05 - Five")
	writeTree(t, scratch, map[string]string{"05 - Five.mp4": "new mp4", "resources/a.pdf": "new a", "resources/deep/x.pdf": "new x"})
	dest, sub := fiveDest(lib, layout)
	// Earlier files at the placed names, so both a set-aside and a
	// merge at two depths happen.
	writeTree(t, filepath.Join(dest, sub), map[string]string{"a.pdf": "old a", "deep/x.pdf": "old x", "b.pdf": "old b"})

	src, err := openScratch(dl, scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer src.close()
	before := openFDsUnder(t, tmp)
	pl, err := pendingFiveFrom(t, layout, dl, lib, src)
	if err != nil || pl == nil {
		t.Fatalf("placement = %v, %v", pl, err)
	}
	if len(pl.merged) == 0 {
		t.Fatal("nothing was merged: the fixture does not exercise the held levels")
	}
	if held := openFDsUnder(t, tmp); held <= before {
		t.Fatalf("descriptors %d while placed, %d before: the count sees nothing held", held, before)
	}
	commitOrUndo(t, pl, commit)
	if after := openFDsUnder(t, tmp); after != before {
		t.Errorf("descriptors %d after, %d before: %d still held", after, before, after-before)
	}
}

// pendingFiveFrom places the downloaded lesson src (read through dl) into lib
// in layout, as lesson 1 with no other lesson's claims, and returns the
// placement uncommitted.
func pendingFiveFrom(t *testing.T, layout, dl, lib string, src *scratchDir) (*placement, error) {
	t.Helper()
	if layout == LayoutPlexTV {
		res, err := moveToLibraryPlexTV(lib, "Show", 1, 5, "Five", src, plexLibrary{self: database.Lesson{RailcontentID: 1}, claims: noClaims(t, lib), roots: []string{lib, dl}})
		return res.pending, err
	}
	return placeLessonFolder(lib, filepath.Join("Course", "05 - Five"), src, database.Lesson{RailcontentID: 1}, noClaims(t, lib), []string{lib, dl}, 7)
}

// commitOrUndo commits the placement pl (commit), or undoes it, and fails the
// test if that fails.
func commitOrUndo(t *testing.T, pl *placement, commit bool) {
	t.Helper()
	if commit {
		if _, err := pl.commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return
	}
	if _, err := pl.undo(false); err != nil {
		t.Fatalf("undo: %v", err)
	}
}
