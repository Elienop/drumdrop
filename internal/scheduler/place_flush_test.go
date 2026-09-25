package scheduler

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// placeRecordingFlushes places the download in scratch (under dl) into lib in
// the given layout, as the worker does before it records the download, and
// returns every folder flushed to disk meanwhile (cleaned) and the folder the
// lesson was placed in ("" when the placement failed). fail, when set, makes
// the flush of that folder fail.
func placeRecordingFlushes(t *testing.T, layout, dl, lib, scratch, fail string) (flushed []string, placedIn string, err error) {
	t.Helper()
	orig := syncFile
	syncFile = func(f *os.File) error {
		name := filepath.Clean(f.Name())
		flushed = append(flushed, name)
		if name == fail {
			return errors.New("injected flush failure")
		}
		return orig(f)
	}
	t.Cleanup(func() { syncFile = orig })
	if layout == LayoutPlexTV {
		var res plexMoveResult
		res, err = testMovePlexTVFrom(t, dl, lib, plexEpisode{"Show", 1, 5, "Five"}, scratch,
			plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}})
		return flushed, res.seasonDir, err
	}
	pl, err := testPlacePending(t, dl, lib, scratch, database.Lesson{})
	if err != nil {
		return flushed, "", err
	}
	if _, cerr := pl.commit(); cerr != nil {
		t.Fatalf("commit: %v", cerr)
	}
	return flushed, pl.dir, nil
}

// TestPlacementFlushesWhatItRenamed (security I2) proves a placement by
// rename, not only a copy, flushes to disk every folder it changed before it
// returns (so before the worker records the download): the folder the
// entries went into, the downloaded folder they left, a subfolder merged
// entry by entry (both sides), and the parent of a lesson folder it made.
// Without it a power loss after the record could leave a recorded file in
// its private folder, which the next start's sweep removes.
func TestPlacementFlushesWhatItRenamed(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		for _, merge := range []bool{false, true} {
			name := "layout=" + layout + "/new folder"
			if merge {
				name = "layout=" + layout + "/merge"
			}
			t.Run(name, func(t *testing.T) {
				checkPlacementFlushesWhatItRenamed(t, layout, merge)
			})
		}
	}
}

// checkPlacementFlushesWhatItRenamed is TestPlacementFlushesWhatItRenamed in
// layout, into a new lesson folder or merging into an earlier subfolder
// (merge).
func checkPlacementFlushesWhatItRenamed(t *testing.T, layout string, merge bool) {
	t.Helper()
	tmp := t.TempDir()
	dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	scratch := filepath.Join(dl, "Course", "05 - Five")
	writeTree(t, scratch, map[string]string{"05 - Five.mp4": "new mp4", "resources/a.pdf": "new a"})
	dest, sub := fiveDest(lib, layout)
	if merge {
		writeTree(t, filepath.Join(dest, sub), map[string]string{"b.pdf": "old b"})
	}

	flushed, placedIn, err := placeRecordingFlushes(t, layout, dl, lib, scratch, "")
	if err != nil || placedIn != dest {
		t.Fatalf("placement = %q, %v; want %q", placedIn, err, dest)
	}
	want := []string{dest, scratch}
	if merge {
		want = append(want, filepath.Join(dest, sub), filepath.Join(scratch, "resources"))
	} else if layout == "" {
		want = append(want, filepath.Dir(dest)) // the lesson folder it made
	}
	for _, w := range want {
		if !slices.Contains(flushed, w) {
			t.Errorf("%q was never flushed (flushed: %q)", w, flushed)
		}
	}
}

// TestPlacementWhoseFlushFailsIsUndone (security I2) proves a flush that fails
// on the rename path fails the placement: every entry is back in the
// downloaded folder and nothing of it is left where it was being placed.
func TestPlacementWhoseFlushFailsIsUndone(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		t.Run("layout="+layout, func(t *testing.T) {
			tmp := t.TempDir()
			dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
			scratch := filepath.Join(dl, "Course", "05 - Five")
			download := map[string]string{"05 - Five.mp4": "new mp4", "resources/a.pdf": "new a"}
			writeTree(t, scratch, download)

			_, placedIn, err := placeRecordingFlushes(t, layout, dl, lib, scratch, scratch)
			if err == nil || placedIn != "" {
				t.Fatalf("placement = %q, %v; want the flush failure", placedIn, err)
			}
			assertTree(t, scratch, download)
			for _, c := range []string{"new mp4", "new a"} {
				if p := findContent(t, lib, c); p != "" {
					t.Errorf("the download's %q is left at %q", c, p)
				}
			}
		})
	}
}
