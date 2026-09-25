package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// The one-time rename of the plex-tv episode files placed before owner
// ruling #78 (episodefiles.go): invariant 6 and ruling 5b.

// renameBase is the episode base of the lessons the rename tests seed.
const renameBase = "Show - s01e05 - Five"

// renameWorker is a plex-tv worker over lib (with its season folder "Show/
// Season 01"), whose store lists rows, logging into log.
func renameWorker(t *testing.T, rows ...database.Lesson) (w *Worker, store *fakeWorkerStore, log *bytes.Buffer, lib, season string) {
	t.Helper()
	store = newFakeWorkerStore()
	store.withFiles = rows
	lib = filepath.Join(t.TempDir(), "lib")
	season = filepath.Join(lib, "Show", "Season 01")
	w = newTestWorker(t, store, fakeResolver{}, newFakeDownloader(), func(time.Duration) {})
	w.Cfg.DownloadsDir = filepath.Join(filepath.Dir(lib), "dl")
	w.Cfg.LibraryDir = lib
	w.Cfg.Layout = LayoutPlexTV
	log = &bytes.Buffer{}
	w.Log = log
	return w, store, log, lib, season
}

// row is the store's current row id (the fake store's withFiles).
func (s *fakeWorkerStore) row(t *testing.T, id int) database.Lesson {
	t.Helper()
	for _, r := range s.withFiles {
		if r.RailcontentID == id {
			return r
		}
	}
	t.Fatalf("no row %d", id)
	return database.Lesson{}
}

// assertRowRecords fails unless row id records exactly names in season, in
// that order.
func assertRowRecords(t *testing.T, store *fakeWorkerStore, id int, season string, names ...string) {
	t.Helper()
	got, recorded, err := store.row(t, id).PlacedEntries()
	if err != nil || !recorded || !reflect.DeepEqual(got, recordOf(season, names...)) {
		t.Errorf("lesson %d records %v (recorded %v, %v), want %v", id, got, recorded, err, recordOf(season, names...))
	}
}

// seedFiles writes each name in season holding content.
func seedWith(t *testing.T, season, content string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(season, n), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// assertHolds fails unless each name in season holds content.
func assertHolds(t *testing.T, season, content string, names ...string) {
	t.Helper()
	for _, n := range names {
		if got, err := os.ReadFile(filepath.Join(season, n)); err != nil || string(got) != content {
			t.Errorf("%s = %q, %v; want %q", n, got, err, content)
		}
	}
}

// TestRenameEpisodeFilesRenamesARecordedLessonsImage pins ruling 4 for a
// recorded lesson: its "<base>-poster.jpg" becomes "<base>.jpg", the same
// bytes, and its record names the new file in the old one's place. Run
// again, it changes nothing; and once done it does not look at the lesson
// again in this process (a copy put back is left to the next process).
func TestRenameEpisodeFilesRenamesARecordedLessonsImage(t *testing.T) {
	w, store, _, lib, season := renameWorker(t)
	names := []string{renameBase + ".mp4", renameBase + "-poster.jpg", renameBase + ".nfo"}
	seedSeason(t, season, names...)
	store.withFiles = []database.Lesson{recordedRow(1, season, names...)}

	w.RenameEpisodeFiles(context.Background())
	assertHolds(t, season, renameBase+"-poster.jpg", renameBase+".jpg")
	assertExist(t, false, filepath.Join(season, renameBase+"-poster.jpg"))
	assertRowRecords(t, store, 1, season, renameBase+".mp4", renameBase+".jpg", renameBase+".nfo")

	for range 2 {
		w.RenameEpisodeFiles(context.Background())
	}
	if len(store.swaps) != 1 {
		t.Errorf("%d record writes, want 1: %+v", len(store.swaps), store.swaps)
	}
	// Done: a copy put back now is not even looked at in this process...
	seedWith(t, season, renameBase+"-poster.jpg", renameBase+"-poster.jpg")
	w.RenameEpisodeFiles(context.Background())
	assertExist(t, true, filepath.Join(season, renameBase+"-poster.jpg"))
	// ...and the next process removes it, an exact unclaimed copy.
	next, _, _, _, _ := renameWorker(t)
	next.Store, next.Cfg.LibraryDir = store, lib
	next.RenameEpisodeFiles(context.Background())
	assertExist(t, false, filepath.Join(season, renameBase+"-poster.jpg"))
	assertHolds(t, season, renameBase+"-poster.jpg", renameBase+".jpg")
}

// TestRenameEpisodeFilesRecordsALegacyRowFirst pins ruling 4 for a lesson
// placed before the record existed: the rename first records exactly what
// its delete would remove today (the entries the legacy grammar gives it
// that no other lesson claims), then renames them, so its files are never
// renamed unrecorded. A legacy row with nothing to rename is not recorded
// (BACKLOG D65), and one whose episode can't be told is left alone.
func TestRenameEpisodeFilesRecordsALegacyRowFirst(t *testing.T) {
	mine := []string{renameBase + ".mp4", renameBase + ".nfo", renameBase + "-poster.jpg", renameBase + ".en.vtt", renameBase + " resources/"}
	t.Run("renamed", func(t *testing.T) {
		w, store, _, _, season := renameWorker(t)
		seedSeason(t, season, mine...)
		seedSeason(t, season, renameBase+"-Part 2.mp4", "Show - s01e06 - Six-poster.jpg")
		claimed := recordedRow(2, season, renameBase+".en.vtt")
		store.withFiles = []database.Lesson{legacyRow(1, "Five", 5, season, renameBase+".mp4"), claimed}

		w.RenameEpisodeFiles(context.Background())
		if len(store.swaps) != 2 || store.swaps[0].id != 1 || store.swaps[1].id != 1 {
			t.Fatalf("record writes %+v, want lesson 1 recorded, then renamed", store.swaps)
		}
		if want := recordOf(season, renameBase+" resources", renameBase+"-poster.jpg", renameBase+".mp4", renameBase+".nfo"); !reflect.DeepEqual(store.swaps[0].entries, want) {
			t.Errorf("recorded %v, want exactly the unclaimed entries its delete removes, %v", store.swaps[0].entries, want)
		}
		assertRowRecords(t, store, 1, season, renameBase+" resources", renameBase+".jpg", renameBase+".mp4", renameBase+".nfo")
		assertHolds(t, season, renameBase+"-poster.jpg", renameBase+".jpg")
		assertContent(t, season, renameBase+".en.vtt", renameBase+"-Part 2.mp4", "Show - s01e06 - Six-poster.jpg")
	})
	t.Run("nothing to rename", func(t *testing.T) {
		w, store, _, _, season := renameWorker(t)
		seedSeason(t, season, renameBase+".mp4", renameBase+".nfo")
		store.withFiles = []database.Lesson{legacyRow(1, "Five", 5, season, renameBase+".mp4")}
		w.RenameEpisodeFiles(context.Background())
		if len(store.swaps) != 0 || store.row(t, 1).LibraryEntries.Valid {
			t.Errorf("a legacy row with nothing to rename was recorded: %+v", store.swaps)
		}
	})
	t.Run("episode can't be told", func(t *testing.T) {
		w, store, log, _, season := renameWorker(t)
		video := "Show - s01e05 - Even Flow [Drumless].mp4"
		seedSeason(t, season, video, "Show - s01e05 - Even Flow-poster.jpg")
		store.withFiles = []database.Lesson{legacyRow(1, "Even Flow (2024)", 5, season, video)}
		w.RenameEpisodeFiles(context.Background())
		if len(store.swaps) != 0 || !strings.Contains(log.String(), "refusing to guess") {
			t.Errorf("record writes %+v, log %q; want nothing, and why", store.swaps, log.String())
		}
		assertContent(t, season, video, "Show - s01e05 - Even Flow-poster.jpg")
	})
}

// TestRenameEpisodeFilesGivesASongItsFilesPerVersion pins ruling 5b: a song's
// one image and one nfo become one per version video, each the same bytes,
// in the record in the old files' place; the old ones go only once every
// version has its own. A song with no image gets none. The image a build
// named "<base>.jpg" goes the same way. A legacy song is recorded first, in
// the shape it has (its one nfo), which the legacy grammar reads.
func TestRenameEpisodeFilesGivesASongItsFilesPerVersion(t *testing.T) {
	videos := versionNames(renameBase, ".mp4", "Drumless", "Original")
	for _, tc := range []struct {
		name   string
		image  string
		legacy bool
	}{
		{"recorded", renameBase + musora.PosterSuffix, false},
		{"recorded, image named .jpg", renameBase + ".jpg", false},
		{"recorded, no image", "", false},
		{"legacy", renameBase + musora.PosterSuffix, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, store, _, _, season := renameWorker(t)
			names := append(append([]string(nil), videos...), renameBase+".nfo")
			if tc.image != "" {
				names = append(names, tc.image)
			}
			seedSeason(t, season, names...)
			row := recordedRow(1, season, names...)
			if tc.legacy {
				row = legacyRow(1, "Five", 5, season, videos[0])
			}
			store.withFiles = []database.Lesson{row}

			w.RenameEpisodeFiles(context.Background())
			nfos := versionNames(renameBase, ".nfo", "Drumless", "Original")
			images := versionNames(renameBase, ".jpg", "Drumless", "Original")
			assertHolds(t, season, renameBase+".nfo", nfos...)
			assertExist(t, false, filepath.Join(season, renameBase+".nfo"))
			want := append(append([]string(nil), videos...), nfos...)
			if tc.image != "" {
				assertHolds(t, season, tc.image, images...)
				assertExist(t, false, filepath.Join(season, tc.image))
				want = append(want, images...)
			} else {
				assertExist(t, false, paths(season, images...)...)
			}
			got, _, _ := store.row(t, 1).PlacedEntries()
			if !reflect.DeepEqual(sorted(got), sorted(recordOf(season, want...))) {
				t.Errorf("record %v, want %v", got, recordOf(season, want...))
			}
		})
	}
}

// TestRenameEpisodeFilesTellsASongByItsVideos pins which lessons the rename
// reads as a song: version videos and no "<base>.mp4". A lesson with its own
// video that also records a version video (one a re-download kept at its
// base, ruling (j)) gets "<base>.jpg" and keeps its one nfo.
func TestRenameEpisodeFilesTellsASongByItsVideos(t *testing.T) {
	w, store, _, _, season := renameWorker(t)
	names := []string{renameBase + ".mp4", renameBase + " [Live].mp4", renameBase + ".nfo", renameBase + "-poster.jpg"}
	seedSeason(t, season, names...)
	store.withFiles = []database.Lesson{recordedRow(1, season, names...)}
	w.RenameEpisodeFiles(context.Background())
	assertRowRecords(t, store, 1, season, renameBase+".mp4", renameBase+" [Live].mp4", renameBase+".nfo", renameBase+".jpg")
	assertExist(t, false, filepath.Join(season, renameBase+" [Live].jpg"), filepath.Join(season, renameBase+" [Live].nfo"))
}

// TestRenameEpisodeFilesLeavesATakenName pins that no new name is ever taken
// over: one that holds another file (the owner's own "<base>.jpg", Plex's
// documented name), or that another lesson's record names, leaves the
// rename of that file undone, its old file where it was and recorded, and
// says so once; for a song, the versions' copies already made are taken
// back, so the old file goes only once every version has its own.
func TestRenameEpisodeFilesLeavesATakenName(t *testing.T) {
	regular := []string{renameBase + ".mp4", renameBase + "-poster.jpg"}
	song := append(versionNames(renameBase, ".mp4", "Drumless", "Original"), renameBase+"-poster.jpg")
	for _, tc := range []struct {
		name   string
		mine   []string
		taken  string
		byLess bool // another lesson's record names it, and it is not there
	}{
		{"the owner's image", regular, renameBase + ".jpg", false},
		{"another lesson's name", regular, renameBase + ".jpg", true},
		{"one version's name", song, renameBase + " [Original].jpg", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, store, log, _, season := renameWorker(t)
			seedSeason(t, season, tc.mine...)
			store.withFiles = []database.Lesson{recordedRow(1, season, tc.mine...)}
			if tc.byLess {
				store.withFiles = append(store.withFiles, recordedRow(2, season, tc.taken))
			} else {
				seedWith(t, season, "the owner's", tc.taken)
			}
			for range 2 {
				w.RenameEpisodeFiles(context.Background())
			}
			assertContent(t, season, tc.mine...)
			if tc.byLess {
				assertExist(t, false, filepath.Join(season, tc.taken))
			} else {
				assertHolds(t, season, "the owner's", tc.taken)
			}
			assertExist(t, false, filepath.Join(season, renameBase+" [Drumless].jpg"))
			assertRowRecords(t, store, 1, season, tc.mine...)
			if n := strings.Count(log.String(), "not renamed"); n != 1 {
				t.Errorf("said %d times why, want once:\n%s", n, log.String())
			}
		})
	}
}

// TestRenameEpisodeFilesIsSafeAtEveryCrashPoint pins invariant 6's crash
// safety, the file and the record both: the new names are created and
// flushed before the record names them, and the old file is removed only
// once the record no longer does. So a crash:
//   - before the record, leaves the new names as exact copies nobody
//     records: the next pass takes them as its own (never "taken");
//   - after the record, leaves the old file beside its recorded copy: the
//     next process's pass removes it, only as an exact, unclaimed copy of a
//     recorded file that is there.
func TestRenameEpisodeFilesIsSafeAtEveryCrashPoint(t *testing.T) {
	mine := []string{renameBase + ".mp4", renameBase + "-poster.jpg"}
	t.Run("before the record", func(t *testing.T) {
		w, store, _, _, season := renameWorker(t)
		seedSeason(t, season, mine...)
		seedWith(t, season, renameBase+"-poster.jpg", renameBase+".jpg")
		store.withFiles = []database.Lesson{recordedRow(1, season, mine...)}
		w.RenameEpisodeFiles(context.Background())
		assertRowRecords(t, store, 1, season, renameBase+".mp4", renameBase+".jpg")
		assertExist(t, false, filepath.Join(season, renameBase+"-poster.jpg"))
	})
	for _, tc := range []struct {
		name   string
		old    string // what the old file holds
		other  bool   // another lesson claims the old name
		noCopy bool   // the recorded copy is not there
		gone   bool
	}{
		{"after the record", renameBase + "-poster.jpg", false, false, true},
		{"after the record, not a copy", "something else", false, false, false},
		{"after the record, claimed", renameBase + "-poster.jpg", true, false, false},
		{"after the record, copy missing", renameBase + "-poster.jpg", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, store, _, _, season := renameWorker(t)
			seedSeason(t, season, renameBase+".mp4")
			if !tc.noCopy {
				seedWith(t, season, renameBase+"-poster.jpg", renameBase+".jpg")
			}
			seedWith(t, season, tc.old, renameBase+"-poster.jpg")
			store.withFiles = []database.Lesson{recordedRow(1, season, renameBase+".mp4", renameBase+".jpg")}
			if tc.other {
				store.withFiles = append(store.withFiles, recordedRow(2, season, renameBase+"-poster.jpg"))
			}
			w.RenameEpisodeFiles(context.Background())
			assertExist(t, !tc.gone, filepath.Join(season, renameBase+"-poster.jpg"))
			if len(store.swaps) != 0 {
				t.Errorf("record writes %+v, want none", store.swaps)
			}
		})
	}
}

// TestRenameEpisodeFilesRemovesASongsLeftovers is the crash after the record
// for a song: its one nfo and one image, left beside the per-version copies
// the record names, are removed by the next process's pass as exact,
// unclaimed copies; a lesson without a video is looked at the same way.
func TestRenameEpisodeFilesRemovesASongsLeftovers(t *testing.T) {
	w, store, _, _, season := renameWorker(t)
	recorded := append(versionNames(renameBase, ".mp4", "Drumless", "Original"),
		append(versionNames(renameBase, ".jpg", "Drumless", "Original"), versionNames(renameBase, ".nfo", "Drumless", "Original")...)...)
	seedSeason(t, season, recorded[:2]...)
	seedWith(t, season, "image", recorded[2:4]...)
	seedWith(t, season, "nfo", recorded[4:]...)
	seedWith(t, season, "image", renameBase+"-poster.jpg")
	seedWith(t, season, "nfo", renameBase+".nfo")
	other := filepath.Join(filepath.Dir(season), "Season 02")
	seedWith(t, other, "img", "Show - s02e01 - Notes.jpg", "Show - s02e01 - Notes-poster.jpg")
	store.withFiles = []database.Lesson{recordedRow(1, season, recorded...), recordedRow(2, other, "Show - s02e01 - Notes.jpg")}

	w.RenameEpisodeFiles(context.Background())
	assertExist(t, false, filepath.Join(season, renameBase+"-poster.jpg"), filepath.Join(season, renameBase+".nfo"),
		filepath.Join(other, "Show - s02e01 - Notes-poster.jpg"))
	assertExist(t, true, paths(season, recorded...)...)
	assertExist(t, true, filepath.Join(other, "Show - s02e01 - Notes.jpg"))
	if len(store.swaps) != 0 {
		t.Errorf("record writes %+v, want none", store.swaps)
	}
}

// TestRenameEpisodeFilesLosesToADelete pins "a row a delete holds is left
// alone": one held when it is read is not touched; and when a delete takes
// it (or anything records other files) between the copies and the record,
// the record write is refused, every copy the pass made is taken back, and
// the old files and the record stay as they were, for the next cycle.
func TestRenameEpisodeFilesLosesToADelete(t *testing.T) {
	mine := append(versionNames(renameBase, ".mp4", "Drumless", "Original"), renameBase+".nfo", renameBase+"-poster.jpg")
	made := append(versionNames(renameBase, ".jpg", "Drumless", "Original"), versionNames(renameBase, ".nfo", "Drumless", "Original")...)
	for _, when := range []string{"held when read", "delete lands", "record changes", "database fails"} {
		t.Run(when, func(t *testing.T) {
			w, store, _, _, season := renameWorker(t)
			seedSeason(t, season, mine...)
			row := recordedRow(1, season, mine...)
			store.withFiles = []database.Lesson{row}
			switch when {
			case "held when read":
				store.withFiles[0].Deleting = true
			case "delete lands":
				store.onSwap = func() { store.withFiles[0].Deleting = true }
			case "record changes":
				store.onSwap = func() { store.withFiles[0].VideoPath = sql.NullString{String: "x", Valid: true} }
			case "database fails":
				store.swapErr = errors.New("database is locked")
			}
			w.RenameEpisodeFiles(context.Background())
			assertContent(t, season, mine...)
			assertExist(t, false, paths(season, made...)...)
			if got := store.row(t, 1).LibraryEntries; got != row.LibraryEntries {
				t.Errorf("record = %v, want it as it was", got)
			}
			if when == "held when read" {
				return
			}
			// The next cycle, with the store answering again, does it.
			store.onSwap, store.swapErr = nil, nil
			store.withFiles[0] = row
			w.RenameEpisodeFiles(context.Background())
			assertExist(t, true, paths(season, made...)...)
			assertExist(t, false, filepath.Join(season, renameBase+".nfo"), filepath.Join(season, renameBase+"-poster.jpg"))
		})
	}
}

// TestRenameEpisodeFilesWithTheLibraryAway pins "an unmounted library writes
// nothing" and "finishes without a restart": with no library folder, or a
// library folder without the season folder (a mount point with nothing
// mounted), nothing is written and nothing recorded, a legacy row included;
// once the files are there, the next cycle of the same process renames them.
func TestRenameEpisodeFilesWithTheLibraryAway(t *testing.T) {
	for _, away := range []string{"no library folder", "no season folder"} {
		t.Run(away, func(t *testing.T) {
			w, store, _, lib, season := renameWorker(t)
			names := []string{renameBase + ".mp4", renameBase + ".nfo", renameBase + "-poster.jpg"}
			six := []string{"Show - s01e06 - Six.mp4", "Show - s01e06 - Six-poster.jpg"}
			store.withFiles = []database.Lesson{legacyRow(1, "Five", 5, season, renameBase+".mp4"), recordedRow(2, season, six...)}
			if away == "no season folder" {
				if err := os.MkdirAll(lib, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			w.RenameEpisodeFiles(context.Background())
			if len(store.swaps) != 0 {
				t.Errorf("record writes %+v with the library away, want none", store.swaps)
			}
			if away == "no library folder" {
				assertExist(t, false, lib)
			} else if entries, _ := os.ReadDir(lib); len(entries) != 0 {
				t.Errorf("wrote %v into the library", entries)
			}
			seedSeason(t, season, names...)
			seedSeason(t, season, six...)
			w.RenameEpisodeFiles(context.Background())
			assertHolds(t, season, renameBase+"-poster.jpg", renameBase+".jpg")
			assertRowRecords(t, store, 1, season, renameBase+".jpg", renameBase+".mp4", renameBase+".nfo")
			assertHolds(t, season, six[1], "Show - s01e06 - Six.jpg")
			assertRowRecords(t, store, 2, season, six[0], "Show - s01e06 - Six.jpg")
		})
	}
}

// TestRenameEpisodeFilesNeverThroughASymlink proves the rename writes only
// into a real show and season folder under the library: a show folder that
// is a symlink (to a folder outside) gets nothing, and nothing is recorded.
func TestRenameEpisodeFilesNeverThroughASymlink(t *testing.T) {
	w, store, _, lib, _ := renameWorker(t)
	outside := filepath.Join(t.TempDir(), "Show")
	names := []string{renameBase + ".mp4", renameBase + "-poster.jpg"}
	seedSeason(t, filepath.Join(outside, "Season 01"), names...)
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(lib, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(lib, "Show")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Show", "Season 01", names[0])); err != nil {
		t.Fatalf("the symlink does not lead to the files: %v", err)
	}
	store.withFiles = []database.Lesson{recordedRow(1, filepath.Join(lib, "Show", "Season 01"), names...)}
	w.RenameEpisodeFiles(context.Background())
	assertContent(t, filepath.Join(outside, "Season 01"), names...)
	assertExist(t, false, filepath.Join(outside, "Season 01", renameBase+".jpg"))
	if len(store.swaps) != 0 {
		t.Errorf("record writes %+v, want none", store.swaps)
	}
}

// TestRenameEpisodeFilesNeverReadsThroughASymlink proves an old name that
// is a symlink (to a file outside the library) is not renamed: nothing is
// copied from where it leads, and the lesson's record stays as it is.
func TestRenameEpisodeFilesNeverReadsThroughASymlink(t *testing.T) {
	w, store, _, _, season := renameWorker(t)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("not the library's"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedSeason(t, season, renameBase+".mp4")
	if err := os.Symlink(secret, filepath.Join(season, renameBase+"-poster.jpg")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	row := recordedRow(1, season, renameBase+".mp4", renameBase+"-poster.jpg")
	store.withFiles = []database.Lesson{row}
	w.RenameEpisodeFiles(context.Background())
	assertExist(t, false, filepath.Join(season, renameBase+".jpg"))
	if got := store.row(t, 1).LibraryEntries; got != row.LibraryEntries {
		t.Errorf("record = %v, want it as it was", got)
	}
}

// TestRenameEpisodeFilesOnlyInPlexTV proves the rename is plex-tv's alone:
// the default layout keeps "<base>-poster.jpg", and without a library folder
// there is nothing to rename.
func TestRenameEpisodeFilesOnlyInPlexTV(t *testing.T) {
	for _, cfg := range []func(*Worker){
		func(w *Worker) { w.Cfg.Layout = "" },
		func(w *Worker) { w.Cfg.LibraryDir = "" },
	} {
		w, store, _, _, season := renameWorker(t)
		names := []string{renameBase + ".mp4", renameBase + "-poster.jpg"}
		seedSeason(t, season, names...)
		store.withFiles = []database.Lesson{recordedRow(1, season, names...)}
		cfg(w)
		w.RenameEpisodeFiles(context.Background())
		assertContent(t, season, names...)
		assertExist(t, false, filepath.Join(season, renameBase+".jpg"))
	}
}

// TestCreateTempNameFitsAName proves createOnly's hidden name always fits in
// a name: ".<name>.drumdrop-part" when it does, else a short one of its own
// per name (an episode's file under a long title).
func TestCreateTempNameFitsAName(t *testing.T) {
	if got := createTempName("poster.jpg"); got != ".poster.jpg"+musora.TempSuffix {
		t.Errorf("createTempName(poster.jpg) = %q", got)
	}
	long := strings.Repeat("a", maxNameBytes-4) + ".jpg"
	other := strings.Repeat("b", maxNameBytes-4) + ".jpg"
	got := createTempName(long)
	if len(got) > maxNameBytes || !strings.HasPrefix(got, ".") || got == createTempName(other) || got != createTempName(long) {
		t.Errorf("createTempName(long) = %q (%d bytes), want a short hidden name of its own", got, len(got))
	}
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if created, err := createOnly(r, long, []byte("x")); err != nil || !created {
		t.Errorf("createOnly(a %d-byte name) = %v, %v; want it created", len(long), created, err)
	}
}
