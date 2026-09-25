package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// TestWorkerRefusedPlacementKeepsFilesLeftBehindByALibraryMove pins owner
// ruling 2026-09-24 (y) for the downloads fallback (security round 5h F2, the
// seat's M, N and D cases): once the library folder setting points at another
// folder with the files not moved, a season-folder row is read under the
// library configured now, so a refused library placement that fell back to
// downloads recorded the downloads folder and, for the season folder, only
// what the move learned there (nothing, or a record naming the other folder):
// the files stayed where the row says, recorded by nothing. It is refused now
// (failLeftBehind): the files and the row stay as they were, whatever the
// layout, however the episode is named, with or without a record, with the
// library moved up (the downloads folder inside it, or beside it) or down (the
// old folder now in the downloads folder).
//
// The controls fall back and record the season files as before: nothing
// moved, a library remounted with its files (the recorded path is gone), the
// files moved to the same place with the old season folder left empty, and
// a library setting spelled through a symlink to the same folder.
func TestWorkerRefusedPlacementKeepsFilesLeftBehindByALibraryMove(t *testing.T) {
	plain := []string{"Beginner Course - s01e05 - Lesson A.mp4", "Beginner Course - s01e05 - Lesson A.nfo", "Beginner Course - s01e05 - Lesson A.en.vtt"}
	song := []string{"Beginner Course - s01e05 - Old Title [Live] [Drumless].mp4", "Beginner Course - s01e05 - Old Title [Live] [Drumless].en.vtt"}
	for _, c := range []struct {
		name   string
		layout string
		// lib and dl are the settings now, row the season folder the row
		// records, files where its files are ("" = row), all relative to a
		// temporary folder. link, when set, is a symlink there to
		// "media/lib".
		lib, dl, row, files, link string
		names                     []string
		recorded                  bool
		leftBehind                bool
		// emptyRow: the row's season folder is still there, emptied (the
		// files moved to the same place in the new library: round 5j J1).
		emptyRow bool
	}{
		{name: "M1 plex-tv, moved up, downloads inside, song version", layout: LayoutPlexTV, lib: "media", dl: "media/drumeo", row: "media/drumeo/Beginner Course/Season 01", names: song, leftBehind: true},
		{name: "M2 plex-tv, moved up, downloads inside, plain name", layout: LayoutPlexTV, lib: "media", dl: "media/drumeo", row: "media/drumeo/Beginner Course/Season 01", names: plain, leftBehind: true},
		{name: "M3 plex-tv, moved up, downloads inside, recorded", layout: LayoutPlexTV, lib: "media", dl: "media/drumeo", row: "media/drumeo/Beginner Course/Season 01", names: plain, recorded: true, leftBehind: true},
		{name: "M4 default, moved up, downloads inside, plain name", lib: "media", dl: "media/drumeo", row: "media/drumeo/Beginner Course/Season 01", names: plain, leftBehind: true},
		{name: "M5 default, moved up, downloads inside, recorded", lib: "media", dl: "media/drumeo", row: "media/drumeo/Beginner Course/Season 01", names: plain, recorded: true, leftBehind: true},
		{name: "N1 plex-tv, moved up, downloads beside, plain name", layout: LayoutPlexTV, lib: "media", dl: "downloads", row: "media/library/Beginner Course/Season 01", names: plain, leftBehind: true},
		{name: "N2 plex-tv, moved up, downloads beside, song version", layout: LayoutPlexTV, lib: "media", dl: "downloads", row: "media/library/Beginner Course/Season 01", names: song, leftBehind: true},
		{name: "N3 plex-tv, moved up, downloads beside, recorded", layout: LayoutPlexTV, lib: "media", dl: "downloads", row: "media/library/Beginner Course/Season 01", names: plain, recorded: true, leftBehind: true},
		{name: "D1 plex-tv, moved down into downloads, plain name", layout: LayoutPlexTV, lib: "media/lib", dl: "media", row: "media/Beginner Course/Season 01", names: plain, leftBehind: true},
		{name: "D2 plex-tv, moved down into downloads, recorded", layout: LayoutPlexTV, lib: "media/lib", dl: "media", row: "media/Beginner Course/Season 01", names: plain, recorded: true, leftBehind: true},
		{name: "C1 plex-tv, nothing moved, plain name", layout: LayoutPlexTV, lib: "media", dl: "dl", row: "media/Beginner Course/Season 01", names: plain},
		{name: "C2 plex-tv, nothing moved, recorded", layout: LayoutPlexTV, lib: "media", dl: "dl", row: "media/Beginner Course/Season 01", names: plain, recorded: true},
		{name: "R1 plex-tv, remounted with its files, plain name", layout: LayoutPlexTV, lib: "newlib", dl: "dl", row: "oldlib/Beginner Course/Season 01", files: "newlib/Beginner Course/Season 01", names: plain},
		{name: "R2 plex-tv, remounted with its files, recorded", layout: LayoutPlexTV, lib: "newlib", dl: "dl", row: "oldlib/Beginner Course/Season 01", files: "newlib/Beginner Course/Season 01", names: plain, recorded: true},
		{name: "L1 plex-tv, the setting through a symlink to the same folder, plain name", layout: LayoutPlexTV, lib: "linked", link: "linked", dl: "dl", row: "media/lib/Beginner Course/Season 01", names: plain},
		{name: "E1 plex-tv, files moved to the same place, the old folder left empty, plain name", layout: LayoutPlexTV, lib: "newlib", dl: "dl", row: "oldlib/Beginner Course/Season 01", files: "newlib/Beginner Course/Season 01", names: plain, emptyRow: true},
		{name: "E1 plex-tv, files moved to the same place, the old folder left empty, recorded", layout: LayoutPlexTV, lib: "newlib", dl: "dl", row: "oldlib/Beginner Course/Season 01", files: "newlib/Beginner Course/Season 01", names: plain, recorded: true, emptyRow: true},
		{name: "L2 plex-tv, the setting through a symlink to the same folder, recorded", layout: LayoutPlexTV, lib: "linked", link: "linked", dl: "dl", row: "media/lib/Beginner Course/Season 01", names: plain, recorded: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, store, _, _, _ := plexWorker(t)
			tmp := t.TempDir()
			abs := func(rel string) string { return filepath.Join(tmp, filepath.FromSlash(rel)) }
			if c.link != "" {
				if err := os.MkdirAll(abs("media/lib"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(abs("media/lib"), abs(c.link)); err != nil {
					t.Fatal(err)
				}
			}
			w.Cfg.LibraryDir, w.Cfg.DownloadsDir, w.Cfg.Layout = abs(c.lib), abs(c.dl), c.layout
			for _, d := range []string{w.Cfg.LibraryDir, w.Cfg.DownloadsDir} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			rowSeason, filesDir := abs(c.row), abs(c.row)
			if c.files != "" {
				filesDir = abs(c.files)
			}
			seedSeason(t, filesDir, c.names...)
			if c.emptyRow {
				seedSeason(t, rowSeason)
			}
			prev := legacyRow(100, "Lesson A", 5, rowSeason, c.names[0])
			if c.recorded {
				prev.LibraryEntries = database.EncodeLibraryEntries(recordOf(rowSeason, c.names...))
			}
			store.lessons[100] = prev
			store.withFiles = []database.Lesson{prev}
			season := filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "Season 01")
			target := season
			if c.layout == "" {
				target = filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Lesson A")
			}
			refuseFromJobInto(t, w, target)

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			assertContent(t, filesDir, c.names...)
			if c.leftBehind {
				assertRefusedAs(t, w, store, failLeftBehind)
				if !store.onDisk[100] {
					t.Errorf("the lesson's files read as missing; want it left 'downloaded' with its note")
				}
				return
			}
			rec := onlyRecord(t, store)
			if want := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A"); rec.outputDir != want {
				t.Errorf("recorded %q, want the downloads folder %q", rec.outputDir, want)
			}
			if want := recordOf(season, c.names...); !reflect.DeepEqual(sorted(rec.entries), sorted(want)) {
				t.Errorf("entries = %v, want the season files recorded %v", rec.entries, want)
			}
		})
	}
}

// TestWorkerRefusedPlacementKeepsAnOldFolderItCantRead pins "when unsure,
// keep" for the fallback: the old library folder at mode 000 with the files
// still in it. The fallback is refused with failKeptInLibrary (the reason is
// logged), not taken for a folder that is gone.
func TestWorkerRefusedPlacementKeepsAnOldFolderItCantRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a folder's mode doesn't refuse this user")
	}
	names := []string{"Beginner Course - s01e05 - Lesson A.mp4", "Beginner Course - s01e05 - Lesson A.nfo"}
	for _, recorded := range []bool{true, false} {
		t.Run("recorded="+strconv.FormatBool(recorded), func(t *testing.T) {
			w, store, _, _, _ := plexWorker(t)
			tmp := t.TempDir()
			oldRoot := filepath.Join(tmp, "oldlib")
			rowSeason := filepath.Join(oldRoot, "Beginner Course", "Season 01")
			w.Cfg.LibraryDir, w.Cfg.DownloadsDir = filepath.Join(tmp, "newlib"), filepath.Join(tmp, "dl")
			for _, d := range []string{w.Cfg.LibraryDir, w.Cfg.DownloadsDir} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			seedSeason(t, rowSeason, names...)
			prev := legacyRow(100, "Lesson A", 5, rowSeason, names[0])
			if recorded {
				prev.LibraryEntries = database.EncodeLibraryEntries(recordOf(rowSeason, names...))
			}
			store.lessons[100] = prev
			store.withFiles = []database.Lesson{prev}
			refuseFromJobInto(t, w, filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "Season 01"))
			if err := os.Chmod(oldRoot, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(oldRoot, 0o755) })

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if err := os.Chmod(oldRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			assertContent(t, rowSeason, names...)
			assertRefusedAs(t, w, store, failKeptInLibrary)
		})
	}
}
