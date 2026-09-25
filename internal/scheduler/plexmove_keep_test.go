package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// The tests below pin what a plex-tv move that stops part-way leaves the
// lesson owning (plexMoveResult.kept and known): every entry of the lesson
// still in the library must stay recorded, and a lesson whose previous files
// are not known keeps its record exactly as it is.

// TestPlexTVMoveThatCannotOpenTheSeasonKeepsThePreviousDownload proves a move
// refused because the season folder resolves outside the library (a symlinked
// show folder) removes nothing and leaves the lesson owning its previous
// download, here under the show's old name.
func TestPlexTVMoveThatCannotOpenTheSeasonKeepsThePreviousDownload(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	oldSeason := filepath.Join(lib, "Old Show", "Season 01")
	prev := "Old Show - s01e05 - Even Flow.mp4"
	seedSeason(t, oldSeason, prev)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(lib, "Songs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	lessonDir, _, _ := seedSongScratch(t, tmp)

	res, err := testMovePlexTV(t, lib, plexEpisode{"Songs", 1, 5, "Even Flow"}, lessonDir,
		plexLibrary{self: recordedRow(1, oldSeason, prev)})
	if err == nil {
		t.Fatal("move = nil error, want the season folder refused")
	}
	if res.seasonDir != "" || len(res.placed) != 0 || !res.known || !reflect.DeepEqual(res.kept, paths(oldSeason, prev)) {
		t.Errorf("result %+v, want nothing placed and the previous download still owned", res)
	}
	assertExist(t, true, paths(oldSeason, prev)...)
	assertScratchWhole(t, lessonDir)
	if names := readDirNames(t, outside); len(names) != 0 {
		t.Errorf("wrote %v outside the library", names)
	}
}

// TestPlexTVMoveWithAnUnknownPreviousDownload pins what happens when the
// lesson's previous library files can not be told (a legacy row whose video is
// not an .mp4, so its episode name is a guess, D58): the move goes on and
// removes none of them, saying so; and if it then refuses, the lesson's record
// is left exactly as it is (record() is nil).
func TestPlexTVMoveWithAnUnknownPreviousDownload(t *testing.T) {
	legacyVideo := "Songs - s01e05 - Even Flow.mkv"
	t.Run("moved", func(t *testing.T) {
		tmp := t.TempDir()
		lib := filepath.Join(tmp, "lib")
		lessonDir, _, season := seedSongScratch(t, tmp)
		seedSeason(t, season, legacyVideo)
		self := legacyRow(1, "Even Flow", 5, season, legacyVideo)

		res, err := testMovePlexTV(t, lib, plexEpisode{"Songs", 1, 5, "Even Flow"}, lessonDir, plexLibrary{self: self}, self)
		if err == nil || !strings.Contains(err.Error(), "the previous download's library files are not known, so none were removed") {
			t.Errorf("err = %v, want the note that the previous download is not known", err)
		}
		if res.seasonDir != season || len(res.placed) != len(songScratchEntries) || len(res.kept) != 0 || !res.known {
			t.Errorf("result %+v, want the lesson moved into %s", res, season)
		}
		assertExist(t, true, filepath.Join(season, legacyVideo))
	})
	t.Run("refused", func(t *testing.T) {
		tmp := t.TempDir()
		lib := filepath.Join(tmp, "lib")
		lessonDir, episodeBase, season := seedSongScratch(t, tmp)
		taken := episodeBase + " [Original].mp4"
		seedSeason(t, season, legacyVideo, taken)
		self := legacyRow(1, "Even Flow", 5, season, legacyVideo)

		res, err := testMovePlexTV(t, lib, plexEpisode{"Songs", 1, 5, "Even Flow"}, lessonDir, plexLibrary{self: self},
			self, recordedRow(2, season, taken))
		if err == nil || res.seasonDir != "" {
			t.Fatalf("move = (%+v, %v), want the conflict refusal", res, err)
		}
		if rec, rerr := res.record(lib); rec != nil || rerr != nil {
			t.Errorf("record = (%v, %v), want nil: the lesson's record stays as it is", rec, rerr)
		}
		assertExist(t, true, filepath.Join(season, legacyVideo), filepath.Join(season, taken))
		assertScratchWhole(t, lessonDir)
	})
}

// TestPlexTVMoveSetsNothingAsideUnlessItCanSetAll (was
// TestPlexTVMoveRemovesEveryPreviousEntryItCan) proves one previous entry that
// can not be set aside stops the move, and puts back the ones already set
// aside: nothing of the earlier download is lost, and every entry is still
// owned. (The move used to remove every previous entry it could, which a
// Skip landing during the move could not undo: D79.)
func TestPlexTVMoveSetsNothingAsideUnlessItCanSetAll(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	lessonDir, episodeBase, season := seedSongScratch(t, tmp)
	stale := episodeBase + " resources"
	old := "Songs - s01e05 - Old Title.nfo"
	seedSeason(t, season, stale+"/", old)
	makeUndeletable(t, filepath.Join(season, stale))

	// The record lists old first, so it is set aside before the stale folder
	// fails.
	res, err := testMovePlexTV(t, lib, plexEpisode{"Songs", 1, 5, "Even Flow"}, lessonDir,
		plexLibrary{self: recordedRow(1, season, old, stale+"/")})
	if err == nil {
		t.Fatal("move = nil error, want the stale entry reported")
	}
	if res.seasonDir != "" || !reflect.DeepEqual(res.kept, paths(season, old, stale)) {
		t.Errorf("result %+v, want both previous entries still owned", res)
	}
	assertContent(t, season, old)
	assertScratchWhole(t, lessonDir)
	assertExist(t, false, filepath.Join(lib, privateRootName, replacedFolderName(0)))
}

// TestPlexTVUndoThatCannotRenameBackKeepsTheEntry proves an entry the move
// renamed into the season folder and could not rename back (its only copy) is
// still owned by the lesson after the undo, so the library never holds an
// untracked only copy.
func TestPlexTVUndoThatCannotRenameBackKeepsTheEntry(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	lessonDir, episodeBase, season := seedSongScratch(t, tmp)
	stubRename(t, func(oldpath, newpath string) error {
		if strings.HasPrefix(newpath, lessonDir) || strings.Contains(oldpath, "[Original]") {
			return errInjectedRename // the undo's rename back, and [Original] (then copied)
		}
		return renameNoReplace(oldpath, newpath)
	})
	makeUnreadable(t, filepath.Join(lessonDir, "05 - Even Flow [Original].mp4")) // so its copy fails

	res, err := testMovePlexTV(t, lib, plexEpisode{"Songs", 1, 5, "Even Flow"}, lessonDir, plexLibrary{self: database.Lesson{RailcontentID: 1}})
	stuck := filepath.Join(season, episodeBase+" [Drumless].mp4")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("its only copy is left at %q", stuck)) {
		t.Errorf("err = %v, want the stuck entry named", err)
	}
	if res.seasonDir != "" || !res.known || !reflect.DeepEqual(res.kept, []string{stuck}) {
		t.Errorf("result %+v, want the stuck entry still owned", res)
	}
	assertExist(t, true, stuck)
}

// TestPlexTVUndoThatCannotRemoveACopyKeepsIt proves copies the undo could not
// take back out of the season folder (after the folder flush failed) stay
// owned by the lesson.
func TestPlexTVUndoThatCannotRemoveACopyKeepsIt(t *testing.T) {
	skipWithoutPermissionChecks(t)
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	lessonDir, _, season := seedSongScratch(t, tmp)
	forceCopyFallback(t)
	orig := syncFile
	syncFile = func(f *os.File) error {
		if filepath.Clean(f.Name()) == season {
			// The season folder turns read-only just as its flush fails, so the
			// undo can not remove the copies.
			if err := os.Chmod(season, 0o555); err != nil {
				t.Errorf("chmod: %v", err)
			}
			return errors.New("injected flush failure")
		}
		return orig(f)
	}
	t.Cleanup(func() { syncFile = orig; _ = os.Chmod(season, 0o755) })

	res, err := testMovePlexTV(t, lib, plexEpisode{"Songs", 1, 5, "Even Flow"}, lessonDir, plexLibrary{self: database.Lesson{RailcontentID: 1}})
	if err == nil || res.seasonDir != "" {
		t.Fatalf("move = (%+v, %v), want the flush failure", res, err)
	}
	if len(res.kept) != len(songScratchEntries) || !res.known {
		t.Errorf("kept %v, want every copy still in the season folder", res.kept)
	}
	assertExist(t, true, res.kept...)
}
