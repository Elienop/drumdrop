package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// scratchLesson writes a finished download's scratch folder "NN - title" under
// tmp/dl, with a file per suffix ("<base><suffix>") and a folder per name in
// dirs (each holding one file), and returns it.
func scratchLesson(t *testing.T, tmp string, index int, title string, suffixes []string, dirs ...string) string {
	t.Helper()
	base := filepath.Base(lessonDir("", index, title))
	dir := filepath.Join(tmp, "dl", base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	for _, s := range suffixes {
		if err := os.WriteFile(filepath.Join(dir, base+s), []byte("new "+s), 0o644); err != nil {
			t.Fatalf("write %s: %v", s, err)
		}
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
		if err := os.WriteFile(filepath.Join(dir, d, "new.pdf"), []byte("new"), 0o644); err != nil {
			t.Fatalf("write in %s: %v", d, err)
		}
	}
	return dir
}

// TestPlexTVMoveRecordsExactlyWhatItPlaced proves the move reports exactly the
// entries it placed (what the worker records), that it destroys none of the
// season folder's other entries (a same-episode look-alike included, recorded
// or not), and that a delete planned from that record removes exactly those.
func TestPlexTVMoveRecordsExactlyWhatItPlaced(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	season := filepath.Join(lib, "Songs", "Season 01")
	lessonDir := scratchLesson(t, tmp, 5, "Even Flow",
		[]string{" [Original].mp4", " [Drumless].mp4", ".nfo", "-poster.jpg"},
		"resources", "play-along", "sheet-music")
	siblings := []string{
		"Songs - s01e50 - Fifty [Drumless].mp4",
		"Songs - s01e05 - Even Flow Live.mp4",
		"Songs - s01e05 - Even Flow [Live].mp4", // an untracked look-alike
		"Songs - s01e05 - Even Flow-Part 2.mp4",
		"Songs - s01e05 - Even Flow [Live] resources/",
	}
	seedSeason(t, season, siblings...)
	other := recordedRow(9, season, "Songs - s01e05 - Even Flow-Part 2.mp4")

	res, err := testMovePlexTV(t, lib, "Songs", 1, 5, "Even Flow", lessonDir, plexLibrary{}, []database.Lesson{other}...)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	want := paths(season,
		"Songs - s01e05 - Even Flow [Drumless].mp4", "Songs - s01e05 - Even Flow [Original].mp4",
		"Songs - s01e05 - Even Flow play-along", "Songs - s01e05 - Even Flow resources",
		"Songs - s01e05 - Even Flow sheet-music", "Songs - s01e05 - Even Flow-poster.jpg",
		"Songs - s01e05 - Even Flow.nfo")
	if !reflect.DeepEqual(sorted(res.placed), sorted(want)) || len(res.kept) != 0 {
		t.Errorf("placed %v kept %v, want exactly %v", res.placed, res.kept, want)
	}
	assertExist(t, true, want...)
	assertExist(t, true, paths(season, siblings...)...)
	if res.seasonDir != season || res.videoPath != want[0] || res.episodeBase != "Songs - s01e05 - Even Flow" {
		t.Errorf("result = %+v", res)
	}

	// The delete, planned from that record, removes exactly what was placed.
	self := recordedRow(1, season)
	rec, err := res.record(lib)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	self.LibraryEntries = database.EncodeLibraryEntries(rec)
	c, err := library.NewClaims(lib, []database.Lesson{self, other})
	if err != nil {
		t.Fatalf("NewClaims: %v", err)
	}
	plan, err := c.Plan(self)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !reflect.DeepEqual(sorted(plan.Remove), sorted(want)) || len(plan.Kept) != 0 {
		t.Errorf("delete plan %+v, want exactly %v", plan, want)
	}
}

// TestPlexTVMoveAcceptsAnyName proves a lesson is never refused a move for
// the characters in its title, show name or version labels, or for a folder
// whose name has a space: with names on record, nothing parses them back.
func TestPlexTVMoveAcceptsAnyName(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	lessonDir := scratchLesson(t, tmp, 5, "Fill[1]",
		[]string{" [Mix [Live].mp4", " [Original (Live) [HD]].mp4", ".nfo"}, "sheet music")
	res, err := testMovePlexTV(t, lib, "Drum Fills [Beginner]", 1, 5, "Fill[1]", lessonDir, plexLibrary{})
	if err != nil || res.seasonDir == "" {
		t.Fatalf("move = (%+v, %v), want a move", res, err)
	}
	base := "Drum Fills [Beginner] - s01e05 - Fill[1]"
	want := paths(res.seasonDir, base+" [Mix [Live].mp4", base+" [Original (Live) [HD]].mp4", base+".nfo", base+" sheet music")
	if !reflect.DeepEqual(sorted(res.placed), sorted(want)) {
		t.Errorf("placed %v, want %v", res.placed, want)
	}
	assertExist(t, true, want...)
}

// TestPlexTVMoveAtOneEpisodeNumberLeavesTheOthersAlone covers the collision
// the owner ruled on: four lessons share episode 5, each title the first plus
// a tag or a suffix. Re-downloading any one of them replaces its own entries
// and never touches the others', all recorded, or all moved before the record
// existed (then the mover's own previous entries are found by name).
func TestPlexTVMoveAtOneEpisodeNumberLeavesTheOthersAlone(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for i, mover := range fiveTitles {
			t.Run(fmt.Sprintf("%s/legacy=%v", mover, legacy), func(t *testing.T) {
				tmp := t.TempDir()
				lib := filepath.Join(tmp, "lib")
				season := filepath.Join(lib, "Show", "Season 01")
				var others []database.Lesson
				for j, title := range fiveTitles {
					seedSeason(t, season, fiveLookAlikes[title]...)
					if j == i {
						continue
					}
					if legacy {
						others = append(others, legacyRow(j+1, title, 5, season, "Show - s01e05 - "+title+".mp4"))
					} else {
						others = append(others, recordedRow(j+1, season, fiveLookAlikes[title]...))
					}
				}
				self := recordedRow(i+1, season, fiveLookAlikes[mover]...)
				if legacy {
					self = legacyRow(i+1, mover, 5, season, "Show - s01e05 - "+mover+".mp4")
				}
				lessonDir := scratchLesson(t, tmp, 5, mover, []string{".mp4", ".nfo"})

				res, err := testMovePlexTV(t, lib, "Show", 1, 5, mover, lessonDir, plexLibrary{self: self}, others...)
				if err != nil || res.seasonDir != season {
					t.Fatalf("move = (%+v, %v)", res, err)
				}
				for _, title := range fiveTitles {
					if title != mover {
						assertExist(t, true, paths(season, fiveLookAlikes[title]...)...)
					}
				}
				base := "Show - s01e05 - " + mover
				for _, p := range paths(season, fiveLookAlikes[mover]...) {
					if strings.HasSuffix(p, ".mp4") || strings.HasSuffix(p, ".nfo") {
						if got, _ := os.ReadFile(p); !strings.HasPrefix(string(got), "new") {
							t.Errorf("%s = %q, want the new download", p, got)
						}
						continue
					}
					assertExist(t, false, p) // the previous download's other entries are gone
				}
				if want := paths(season, base+".mp4", base+".nfo"); !reflect.DeepEqual(sorted(owned(res)), sorted(want)) {
					t.Errorf("owned %v, want %v", owned(res), want)
				}
			})
		}
	}
}

// TestPlexTVMoveReplacesThePreviousDownloadByRecord proves a re-download
// removes exactly the lesson's previously recorded entries, even under a title
// Musora has changed since, and nothing it did not record (a same-episode
// look-alike, another lesson's entry).
func TestPlexTVMoveReplacesThePreviousDownloadByRecord(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	season := filepath.Join(lib, "Songs", "Season 01")
	previous := []string{
		"Songs - s01e05 - Old Title [Live].mp4", "Songs - s01e05 - Old Title.nfo",
		"Songs - s01e05 - Old Title resources/", "Songs - s01e05 - Even Flow [Original].mp4",
	}
	untouched := []string{"Songs - s01e05 - Even Flow Live.mp4", "Songs - s01e50 - Fifty.mp4", "Songs - s01e05 - Old Title-Part 2.mp4"}
	seedSeason(t, season, previous...)
	seedSeason(t, season, untouched...)
	other := recordedRow(2, season, "Songs - s01e05 - Old Title-Part 2.mp4")
	lessonDir, episodeBase, _ := seedSongScratch(t, tmp)

	res, err := testMovePlexTV(t, lib, "Songs", 1, 5, "Even Flow", lessonDir,
		plexLibrary{self: recordedRow(1, season, previous...)}, []database.Lesson{other}...)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	assertExist(t, false, paths(season, previous[:3]...)...)
	assertExist(t, true, paths(season, untouched...)...)
	orig := filepath.Join(season, episodeBase+" [Original].mp4")
	if got, _ := os.ReadFile(orig); !strings.Contains(string(got), "05 - Even Flow") {
		t.Errorf("%s = %q, want the new download's copy", orig, got)
	}
	if len(res.placed) != 5 || len(res.kept) != 0 {
		t.Errorf("placed %v kept %v, want the 5 new entries only", res.placed, res.kept)
	}
}

// TestPlexTVMoveRecordOutranksALegacyNameMatch proves a re-download replaces
// an entry its own record names even when a legacy row's name grammar would
// also give that entry to another lesson: a record is proof, a legacy match a
// guess, exactly as in the delete (PlanLessonEntries).
func TestPlexTVMoveRecordOutranksALegacyNameMatch(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	season := filepath.Join(lib, "Songs", "Season 01")
	mine := "Songs - s01e05 - Even Flow [Original].mp4"
	seedSeason(t, season, mine)
	// A legacy "Even Flow" at the same episode reads mine as its own
	// [Original] version (no "Even Flow [Original].nfo" says otherwise).
	legacy := legacyRow(2, "Even Flow", 5, season, "Songs - s01e05 - Even Flow [Drumless].mp4")
	lessonDir, _, _ := seedSongScratch(t, tmp)

	res, err := testMovePlexTV(t, lib, "Songs", 1, 5, "Even Flow", lessonDir,
		plexLibrary{self: recordedRow(1, season, mine)}, []database.Lesson{legacy}...)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(season, mine)); !strings.Contains(string(got), "05 - Even Flow") {
		t.Errorf("%s = %q, want the new download's copy", mine, got)
	}
	if !slices.Contains(res.placed, filepath.Join(season, mine)) {
		t.Errorf("placed %v, want it to include %q", res.placed, mine)
	}
}

// TestPlexTVMoveRefusesAnEntryAnotherLessonOwns proves the move never
// overwrites what another lesson claims: a name another record holds (identical
// titles at one episode), or an existing entry a legacy row's name match gives
// to another lesson. It writes nothing, the lesson stays whole in scratch, and
// the previous download stays the lesson's own.
func TestPlexTVMoveRefusesAnEntryAnotherLessonOwns(t *testing.T) {
	cases := map[string]func(season string) database.Lesson{
		"record": func(season string) database.Lesson {
			return recordedRow(2, season, "Songs - s01e05 - Even Flow.nfo")
		},
		"record of a missing entry": func(season string) database.Lesson {
			return recordedRow(2, season, "Songs - s01e05 - Even Flow resources")
		},
		"legacy": func(season string) database.Lesson {
			return legacyRow(2, "Even Flow", 5, season, "Songs - s01e05 - Even Flow [Original].mp4")
		},
	}
	for name, otherOf := range cases {
		t.Run(name, func(t *testing.T) {
			tmp := t.TempDir()
			lib := filepath.Join(tmp, "lib")
			lessonDir, episodeBase, season := seedSongScratch(t, tmp)
			theirs := []string{episodeBase + ".nfo", episodeBase + " [Original].mp4"}
			mine := []string{"Songs - s01e05 - Mine Before.mp4"}
			seedSeason(t, season, theirs...)
			seedSeason(t, season, mine...)
			before := readDirNames(t, season)

			res, err := testMovePlexTV(t, lib, "Songs", 1, 5, "Even Flow", lessonDir,
				plexLibrary{self: recordedRow(1, season, mine...)}, []database.Lesson{otherOf(season)}...)
			if err == nil || !strings.Contains(err.Error(), "refusing to move") || !strings.Contains(err.Error(), "claimed by lesson [2]") {
				t.Fatalf("err = %v, want a refusal naming lesson 2", err)
			}
			if res.seasonDir != "" || len(res.placed) != 0 || !reflect.DeepEqual(res.kept, paths(season, mine...)) {
				t.Errorf("result %+v, want nothing placed and the previous entry still owned", res)
			}
			if got := readDirNames(t, season); !reflect.DeepEqual(got, before) {
				t.Errorf("season folder changed by a refused move: %v, want %v", got, before)
			}
			for _, p := range paths(season, theirs...) {
				if got, _ := os.ReadFile(p); string(got) != filepath.Base(p) {
					t.Errorf("%s overwritten: %q", p, got)
				}
			}
			assertScratchWhole(t, lessonDir)
		})
	}
}

// TestPlexTVMoveReplacesAnEntryNoLessonClaims proves an entry at one of the
// episode's own names that no lesson records (left by a follow deleted with its
// files kept) is replaced, and said so (the commit returns it, not the
// lesson's own, for the worker's log), instead of blocking the move forever. A
// folder there at the name of a folder the move places is merged, by the same
// rule (owner ruling 2026-09-24): only its file at a name placed is replaced.
func TestPlexTVMoveReplacesAnEntryNoLessonClaims(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	lessonDir, episodeBase, season := seedSongScratch(t, tmp)
	seedSeason(t, season, episodeBase+".nfo", episodeBase+" resources/")
	writeTree(t, filepath.Join(season, episodeBase+" resources"), map[string]string{"song.pdf": "old song"})

	c, err := library.NewClaims(lib, nil)
	if err != nil {
		t.Fatal(err)
	}
	src, err := openScratch(filepath.Dir(lessonDir), lessonDir)
	if err != nil {
		t.Fatal(err)
	}
	defer src.close()
	res, err := moveToLibraryPlexTV(lib, "Songs", 1, 5, "Even Flow", src, plexLibrary{claims: c})
	if err != nil || res.seasonDir != season || res.pending == nil {
		t.Fatalf("move = (%+v, %v), want a move", res, err)
	}
	replaced, err := res.pending.commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	var got []string
	for _, e := range replaced {
		if e.own {
			t.Errorf("%q was replaced as the lesson's own; no lesson records it", e.path)
		}
		got = append(got, e.path)
	}
	if want := paths(season, filepath.Join(episodeBase+" resources", "song.pdf"), episodeBase+".nfo"); !reflect.DeepEqual(sorted(got), sorted(want)) {
		t.Errorf("replaced %q, want %q", got, want)
	}
	assertTree(t, filepath.Join(season, episodeBase+" resources"), map[string]string{
		"f.pdf":    episodeBase + " resources",
		"song.pdf": filepath.Join("resources", "song.pdf"),
	})
	if got, _ := os.ReadFile(filepath.Join(season, episodeBase+".nfo")); !strings.Contains(string(got), "05 - Even Flow.nfo") {
		t.Errorf("nfo = %q, want the new download's", got)
	}
}

// TestPlexTVMoveStopsWhenThePreviousDownloadCannotBeCleared proves a previous
// entry that can not be removed stops the move before anything is placed: the
// lesson stays whole in scratch, the error names the entry, and the entry is
// still the lesson's own (kept), so it stays tracked.
func TestPlexTVMoveStopsWhenThePreviousDownloadCannotBeCleared(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	lessonDir, _, season := seedSongScratch(t, tmp)
	// Under an older title: at one of this episode's names, a folder of the
	// lesson's own would be merged, not set aside.
	stale := "Songs - s01e05 - Old Title resources"
	seedSeason(t, season, stale+"/", "Songs - s01e06 - Six.mp4")
	makeUndeletable(t, filepath.Join(season, stale))

	res, err := testMovePlexTV(t, lib, "Songs", 1, 5, "Even Flow", lessonDir,
		plexLibrary{self: recordedRow(1, season, stale+"/")})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("could not set %q aside", filepath.Join(season, stale))) {
		t.Fatalf("err = %v, want one naming the stale entry", err)
	}
	if res.seasonDir != "" || len(res.placed) != 0 || !reflect.DeepEqual(res.kept, paths(season, stale)) {
		t.Errorf("result %+v, want nothing placed and the stale entry still owned", res)
	}
	assertScratchWhole(t, lessonDir)
	assertNoEpisodeIn(t, season, stale, "Songs - s01e06 - Six.mp4")
}

// TestMoveToLibraryPlexTVFolderCopyFailsPartWayIsRemoved covers the folder
// half of D52's second case: a subfolder whose copy fails part-way (its second
// file cannot be read) leaves no half-copied "<episode> resources" in the
// season folder, and the whole song stays in scratch.
func TestMoveToLibraryPlexTVFolderCopyFailsPartWayIsRemoved(t *testing.T) {
	tmp := t.TempDir()
	lessonDir, _, seasonDir := seedSongScratch(t, tmp)
	late := filepath.Join(lessonDir, "resources", "zz-late.pdf")
	if err := os.WriteFile(late, []byte("late"), 0o644); err != nil {
		t.Fatal(err)
	}
	forceCopyFallback(t)
	makeUnreadable(t, late)

	res, err := testMovePlexTV(t, filepath.Join(tmp, "lib"), "Songs", 1, 5, "Even Flow", lessonDir, plexLibrary{})
	if err == nil || res.seasonDir != "" || len(owned(res)) != 0 {
		t.Fatalf("= (%+v, %v), want (nothing, the copy failure)", res, err)
	}
	assertNoEpisodeIn(t, seasonDir)
	assertScratchWhole(t, lessonDir)
}

// TestPlexTVMoveFitsLongNames proves a long show plus a long title still
// reaches the library, every name within 255 bytes (multi-byte titles too),
// and that a show name leaving no room at all is refused with the lesson whole
// in scratch.
func TestPlexTVMoveFitsLongNames(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	show := strings.Repeat("S", 120)
	title := strings.Repeat("é", 60) // 120 bytes, a rune boundary matters
	lessonDir := scratchLesson(t, tmp, 5, title, []string{".mp4", ".nfo", " [Drumless].mp4"}, "resources")
	res, err := testMovePlexTV(t, lib, show, 1, 5, title, lessonDir, plexLibrary{})
	if err != nil || res.seasonDir == "" {
		t.Fatalf("move = (%+v, %v), want a move", res, err)
	}
	for _, p := range res.placed {
		if n := len(filepath.Base(p)); n > maxNameBytes {
			t.Errorf("%q is %d bytes, want at most %d", filepath.Base(p), n, maxNameBytes)
		}
	}
	assertExist(t, true, res.placed...)
	if !strings.HasPrefix(res.episodeBase, show+" - s01e05 - é") || len(res.episodeBase)+len(" [Drumless].mp4") > maxNameBytes {
		t.Errorf("episodeBase = %q, want a shortened title that fits", res.episodeBase)
	}

	tmp2 := t.TempDir()
	lessonDir = scratchLesson(t, tmp2, 5, "T", []string{".mp4"})
	// Sanitize caps a show at 150 runes; 150 two-byte runes are 300 bytes.
	res, err = testMovePlexTV(t, filepath.Join(tmp2, "lib"), strings.Repeat("é", 150), 1, 5, "T", lessonDir, plexLibrary{})
	if err == nil || res.seasonDir != "" {
		t.Errorf("= (%+v, %v), want a refusal", res, err)
	}
	assertExist(t, true, filepath.Join(lessonDir, "05 - T.mp4"))
}

// TestCopyFileRefusesANonRegularSource proves the copy never follows a
// symlinked source into whatever it points at: it refuses, and leaves no
// destination behind.
func TestCopyFileRefusesANonRegularSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	target := filepath.Join(tmp, "secret.txt")
	if err := os.WriteFile(target, []byte("not the lesson's"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "05 - Lesson.mp4")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := copyFileInto(root, "copy.mp4", root, filepath.Base(link), 0o644); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("copyFileInto(symlink) = %v, want a refusal", err)
	}
	assertExist(t, false, filepath.Join(tmp, "copy.mp4"))
}

// TestDiscardPartialCopyReportsOnlyWhatIsThere proves a leftover is reported
// only when it exists and could not be removed: never for a path that was
// never created, a name too long to exist included.
func TestDiscardPartialCopyReportsOnlyWhatIsThere(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"missing", strings.Repeat("n", 300)} {
		if err := discardPartialCopy(root, name); err != nil {
			t.Errorf("discardPartialCopy(%q) = %v, want nil (nothing there)", name[:7], err)
		}
	}
	stuck := filepath.Join(dir, "stuck")
	seedSeason(t, stuck, "f")
	makeUndeletable(t, stuck)
	if err := discardPartialCopy(root, "stuck"); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("left at %q", stuck)) {
		t.Errorf("discardPartialCopy(stuck) = %v, want the leftover named", err)
	}
}

// TestCopyIsFlushedBeforeTheSourceGoes proves the copy path flushes every
// copied file and the folders it wrote into while the scratch source still
// exists, and that a flush failure counts as a failed copy (the source stays,
// the library is clean).
func TestCopyIsFlushedBeforeTheSourceGoes(t *testing.T) {
	tmp := t.TempDir()
	lessonDir, episodeBase, season := seedSongScratch(t, tmp)
	forceCopyFallback(t)
	var synced []string
	orig := syncFile
	syncFile = func(f *os.File) error {
		if _, err := os.Stat(lessonDir); err != nil {
			t.Errorf("flushed %q after the scratch folder was removed", f.Name())
		}
		synced = append(synced, f.Name())
		return orig(f)
	}
	t.Cleanup(func() { syncFile = orig })

	res, err := testMovePlexTV(t, filepath.Join(tmp, "lib"), "Songs", 1, 5, "Even Flow", lessonDir, plexLibrary{})
	if err != nil || res.seasonDir != season {
		t.Fatalf("move = (%+v, %v)", res, err)
	}
	for _, want := range append(res.placed, filepath.Join(season, episodeBase+" resources", "song.pdf"), season) {
		found := false
		for _, s := range cleaned(synced) {
			found = found || s == want
		}
		if !found {
			t.Errorf("%q was never flushed (flushed: %q)", want, synced)
		}
	}

	// A flush that fails is a failed copy.
	tmp2 := t.TempDir()
	lessonDir2, _, season2 := seedSongScratch(t, tmp2)
	syncFile = func(f *os.File) error {
		if strings.HasSuffix(f.Name(), ".nfo") {
			return errors.New("injected flush failure")
		}
		return orig(f)
	}
	res, err = testMovePlexTV(t, filepath.Join(tmp2, "lib"), "Songs", 1, 5, "Even Flow", lessonDir2, plexLibrary{})
	if err == nil || res.seasonDir != "" {
		t.Fatalf("= (%+v, %v), want the flush failure", res, err)
	}
	assertNoEpisodeIn(t, season2)
	assertScratchWhole(t, lessonDir2)
}

// TestMoveToLibraryCopyIsFlushedBeforeTheSourceGoes is the same proof for the
// default layout's copy-tree path.
func TestMoveToLibraryCopyIsFlushedBeforeTheSourceGoes(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
	forceCopyFallback(t)
	var synced []string
	orig := syncFile
	syncFile = func(f *os.File) error {
		if _, err := os.Stat(lessonDir); err != nil {
			t.Errorf("flushed %q after the downloads copy was removed", f.Name())
		}
		synced = append(synced, f.Name())
		return orig(f)
	}
	t.Cleanup(func() { syncFile = orig })

	newDir, err := testPlace(t, downloadsDir, libraryDir, lessonDir, database.Lesson{})
	if err != nil {
		t.Fatalf("moveToLibrary: %v", err)
	}
	want := append(paths(newDir, lessonFiles...), newDir, filepath.Dir(newDir))
	for _, w := range want {
		found := false
		for _, s := range cleaned(synced) {
			found = found || s == w
		}
		if !found {
			t.Errorf("%q was never flushed (flushed: %q)", w, synced)
		}
	}
}

// TestMoveToLibrarySymlinkedLessonFolderKeepsTheLesson proves a lesson folder
// that is a symlink is never "copied" as nothing: the copy refuses, the move
// reports no library folder, and the real files stay where they are.
func TestMoveToLibrarySymlinkedLessonFolderKeepsTheLesson(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	seedSeason(t, real, "05 - Five.mp4", "05 - Five.nfo")
	downloadsDir := filepath.Join(tmp, "dl")
	lessonDir := filepath.Join(downloadsDir, "Course", "05 - Five")
	if err := os.MkdirAll(filepath.Dir(lessonDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, lessonDir); err != nil {
		t.Fatal(err)
	}
	forceCopyFallback(t)

	newDir, err := testPlace(t, downloadsDir, filepath.Join(tmp, "lib"), lessonDir, database.Lesson{})
	if err == nil || newDir != "" {
		t.Fatalf("= (%q, %v), want (\"\", a refusal)", newDir, err)
	}
	assertExist(t, true, filepath.Join(real, "05 - Five.mp4"), filepath.Join(real, "05 - Five.nfo"), lessonDir)
	assertExist(t, false, filepath.Join(tmp, "lib", "Course", "05 - Five"))
	root, err := os.OpenRoot(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rel, err := filepath.Rel(tmp, lessonDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFileInto(root, "x", root, rel, 0o644); err == nil {
		t.Error("copyFileInto copied a symlink, want a refusal")
	}
}

// TestPlaceLessonFolderAliasedToItsSourceRefuses (was
// TestMoveToLibraryAliasedLibraryIsANoOp) proves a placement whose
// destination is its own source under another path (a symlink; a double bind
// mount behaves the same) keeps the lesson: the placement refuses instead of
// replacing the source's entries with themselves, and startup refuses a
// library that is the downloads folder under another path.
func TestPlaceLessonFolderAliasedToItsSourceRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	downloadsDir, lessonDir := seedLesson(t)
	alias := filepath.Join(filepath.Dir(downloadsDir), "lib")
	if err := os.Symlink(downloadsDir, alias); err != nil {
		t.Fatal(err)
	}
	forceCopyFallback(t)

	newDir, err := testPlace(t, downloadsDir, alias, lessonDir, database.Lesson{})
	if err == nil || newDir != "" || !strings.Contains(err.Error(), "it is the downloaded folder itself") {
		t.Fatalf("= (%q, %v), want the named refusal", newDir, err)
	}
	assertExist(t, true, paths(lessonDir, lessonFiles...)...)

	if err := CheckLibraryDir(downloadsDir, alias); err == nil || !strings.Contains(err.Error(), "under another path") {
		t.Errorf("CheckLibraryDir(alias) = %v, want a refusal", err)
	}
	for _, ok := range [][2]string{
		{downloadsDir, downloadsDir},
		{downloadsDir, downloadsDir + string(filepath.Separator)},
		{downloadsDir, filepath.Join(filepath.Dir(downloadsDir), "elsewhere")}, // missing
		{downloadsDir, t.TempDir()},
		{downloadsDir, ""},
		{"", alias},
	} {
		if err := CheckLibraryDir(ok[0], ok[1]); err != nil {
			t.Errorf("CheckLibraryDir(%q, %q) = %v, want nil", ok[0], ok[1], err)
		}
	}
}
