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

// openFDs counts this process's open descriptors (Linux: /proc/self/fd).
func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	return len(entries)
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
				tmp := t.TempDir()
				dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
				scratch := filepath.Join(dl, "Course", "05 - Five")
				writeTree(t, scratch, map[string]string{"05 - Five.mp4": "new mp4", "resources/a.pdf": "new a", "resources/deep/x.pdf": "new x"})
				dest, sub := filepath.Join(lib, "Course", "05 - Five"), "resources"
				if layout == LayoutPlexTV {
					dest, sub = filepath.Join(lib, "Show", "Season 01"), "Show - s01e05 - Five resources"
				}
				// Earlier files at the placed names, so both a set-aside and a
				// merge at two depths happen.
				writeTree(t, filepath.Join(dest, sub), map[string]string{"a.pdf": "old a", "deep/x.pdf": "old x", "b.pdf": "old b"})

				src, err := openScratch(dl, scratch)
				if err != nil {
					t.Fatal(err)
				}
				defer src.close()
				before := openFDs(t)
				var pl *placement
				if layout == LayoutPlexTV {
					var res plexMoveResult
					res, err = moveToLibraryPlexTV(lib, "Show", 1, 5, "Five", src, plexLibrary{self: database.Lesson{RailcontentID: 1}, claims: noClaims(t, lib), roots: []string{lib, dl}})
					pl = res.pending
				} else {
					pl, err = placeLessonFolder(lib, filepath.Join("Course", "05 - Five"), src, database.Lesson{RailcontentID: 1}, noClaims(t, lib), []string{lib, dl}, 7)
				}
				if err != nil || pl == nil {
					t.Fatalf("placement = %v, %v", pl, err)
				}
				if len(pl.merged) == 0 {
					t.Fatal("nothing was merged: the fixture does not exercise the held levels")
				}
				if held := openFDs(t); held <= before {
					t.Fatalf("descriptors %d while placed, %d before: the count sees nothing held", held, before)
				}
				if commit {
					if _, err := pl.commit(); err != nil {
						t.Fatalf("commit: %v", err)
					}
				} else if _, err := pl.undo(false); err != nil {
					t.Fatalf("undo: %v", err)
				}
				if after := openFDs(t); after != before {
					t.Errorf("descriptors %d after, %d before: %d still held", after, before, after-before)
				}
			})
		}
	}
}
