package server

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// mkLessonDir creates root/<rel> with a sample video file and returns the lesson
// dir, failing the test on error.
func mkLessonDir(t *testing.T, root, rel string) string {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "v.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}

// dirLesson is a lesson recorded in its own folder (the default layout, or a
// plex-tv lesson whose move failed).
func dirLesson(id int, outputDir string) database.Lesson {
	return database.Lesson{
		RailcontentID: id,
		Status:        database.StatusDownloaded,
		OutputDir:     sql.NullString{String: outputDir, Valid: true},
	}
}

// recordedLesson is a plex-tv lesson whose record names exactly names in season
// (<library>/<show>/<Season NN>), relative to the library as the move writes
// them.
func recordedLesson(id int, season string, names ...string) database.Lesson {
	l := dirLesson(id, season)
	l.LibraryEntries = database.EncodeLibraryEntries(recordOf(season, names...))
	return l
}

// recordOf is the record entries for names in season: "<show>/<Season NN>/<name>".
func recordOf(season string, names ...string) []string {
	entries := make([]string, 0, len(names))
	prefix := filepath.Base(filepath.Dir(season)) + "/" + filepath.Base(season) + "/"
	for _, n := range names {
		entries = append(entries, prefix+strings.TrimSuffix(n, "/"))
	}
	return entries
}

// removeFiles runs a delete of lesson l's files the way a handler does: the
// claims of every row with files (others, plus l itself as the store would
// list it) under a server configured with downloads and library.
func removeFiles(downloads, lib string, l database.Lesson, others []database.Lesson) (database.KeptFiles, error) {
	rows := append([]database.Lesson(nil), others...)
	listed := false
	for _, o := range others {
		listed = listed || o.RailcontentID == l.RailcontentID
	}
	if !listed {
		rows = append(rows, l)
	}
	c, err := library.NewClaims(lib, rows)
	if err != nil {
		return database.KeptFiles{}, err
	}
	srv := &Server{cfg: Config{DownloadsDir: downloads, LibraryDir: lib}}
	return srv.removeLessonFiles(c, l)
}

// legacyLesson is a plex-tv lesson moved before the record existed: only its
// title, position and video (video "" for none).
func legacyLesson(id int, title string, position int, season, video string) database.Lesson {
	l := dirLesson(id, season)
	l.Title = title
	l.Position = sql.NullInt64{Int64: int64(position), Valid: true}
	if video != "" {
		l.VideoPath = sql.NullString{String: filepath.Join(season, video), Valid: true}
	}
	return l
}

// captureLog points logOut at a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := logOut
	logOut = &buf
	t.Cleanup(func() { logOut = old })
	return &buf
}

// TestRemoveLessonFilesOwnFolder covers a lesson recorded in its own folder:
// the folder goes whole, whether it sits in the library or in downloads, and
// with no library configured.
func TestRemoveLessonFilesOwnFolder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		inLib   bool
		library bool
	}{{"library", true, true}, {"downloads", false, true}, {"no library", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			downloads, library := t.TempDir(), t.TempDir()
			root := downloads
			if tc.inLib {
				root = library
			}
			if !tc.library {
				library = ""
			}
			outputDir := mkLessonDir(t, root, "Inst/Course/01 - Lesson")
			if _, err := removeFiles(downloads, library, dirLesson(1, outputDir), nil); err != nil {
				t.Fatalf("removeLessonFiles: %v", err)
			}
			if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
				t.Errorf("output_dir still present (err=%v)", err)
			}
			assertPresent(t, root, "Inst/Course/")
		})
	}
}

// TestRemoveLessonFilesRejectsOutsideBothRoots asserts an output_dir under
// NEITHER downloadsDir nor libraryDir is rejected with an error and NOT removed.
func TestRemoveLessonFilesRejectsOutsideBothRoots(t *testing.T) {
	downloads, library, outside := t.TempDir(), t.TempDir(), t.TempDir()
	victim := mkLessonDir(t, outside, "keep")
	if _, err := removeFiles(downloads, library, dirLesson(1, victim), nil); err == nil {
		t.Error("removeLessonFiles on a path under neither root returned nil, want error")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("victim outside both roots was removed (err=%v), want left intact", err)
	}
}

// TestRemoveLessonFilesRejectsRootEqual asserts an output_dir equal to the
// downloads root OR the library root is refused, guarding the RemoveAll that
// would otherwise wipe an entire root, and removes nothing.
func TestRemoveLessonFilesRejectsRootEqual(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	seedEntries(t, downloads, "keep.txt")
	seedEntries(t, library, "keep.txt")
	for _, root := range []string{downloads, library} {
		if _, err := removeFiles(downloads, library, dirLesson(1, root), nil); err == nil {
			t.Errorf("removeLessonFiles(output_dir == %q) returned nil, want error (would wipe the root)", root)
		}
	}
	assertPresent(t, downloads, "keep.txt")
	assertPresent(t, library, "keep.txt")
}

// TestRemoveLessonFilesMissingTolerated asserts an already-removed path under
// either root is a benign no-op.
func TestRemoveLessonFilesMissingTolerated(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	for _, l := range []database.Lesson{
		dirLesson(1, filepath.Join(downloads, "04 - Gone")),
		dirLesson(2, filepath.Join(library, "Inst/05 - Gone")),
		recordedLesson(3, season, "Show - s01e05 - Gone.mp4"),
	} {
		if _, err := removeFiles(downloads, library, l, nil); err != nil {
			t.Errorf("lesson %d: removeLessonFiles on a missing path = %v, want nil", l.RailcontentID, err)
		}
	}
}

// fiveLookAlikes are four lessons at episode 5 of one show, each title the
// first plus a tag or a suffix: the collision a name matcher can not tell apart.
var fiveLookAlikes = map[string][]string{
	"Five":           {"Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo", "Show - s01e05 - Five-poster.jpg", "Show - s01e05 - Five resources/"},
	"Five [Live]":    {"Show - s01e05 - Five [Live].mp4", "Show - s01e05 - Five [Live].nfo", "Show - s01e05 - Five [Live]-poster.jpg", "Show - s01e05 - Five [Live] resources/"},
	"Five-Part Fill": {"Show - s01e05 - Five-Part Fill.mp4", "Show - s01e05 - Five-Part Fill.nfo", "Show - s01e05 - Five-Part Fill resources/"},
	"Five.5":         {"Show - s01e05 - Five.5.mp4", "Show - s01e05 - Five.5.nfo", "Show - s01e05 - Five.5.en.vtt"},
}

var fiveTitles = []string{"Five", "Five [Live]", "Five-Part Fill", "Five.5"}

// TestRemoveLessonFilesCollisionsInEveryDirection proves the owner's ruling on
// look-alike titles at one episode number: deleting any one of the four
// removes exactly its own entries and leaves the other three's, whether the
// lessons carry a record or are legacy rows matched by name. Each title
// extends the first, so both directions of the collision are covered.
func TestRemoveLessonFilesCollisionsInEveryDirection(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for i, title := range fiveTitles {
			t.Run(map[bool]string{false: "record", true: "legacy"}[legacy]+"/"+title, func(t *testing.T) {
				checkLookAlikeDelete(t, i, title, legacy)
			})
		}
	}
}

// checkLookAlikeDelete files the four look-alikes in one season folder (with
// records, or as legacy rows) and deletes fiveTitles[i], title: only its own
// entries go.
func checkLookAlikeDelete(t *testing.T, i int, title string, legacy bool) {
	t.Helper()
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	rows := seedLookAlikes(t, season, legacy)
	if _, err := removeFiles(downloads, library, rows[i], rows); err != nil {
		t.Fatalf("removeLessonFiles(%s): %v", title, err)
	}
	assertGone(t, season, fiveLookAlikes[title]...)
	for _, other := range fiveTitles {
		if other != title {
			assertPresent(t, season, fiveLookAlikes[other]...)
		}
	}
	assertPresent(t, library, "Show/Season 01/")
}

// seedLookAlikes makes every look-alike's entries in season and returns their
// rows, lesson j+1 for fiveTitles[j]: recorded, or legacy rows.
func seedLookAlikes(t *testing.T, season string, legacy bool) []database.Lesson {
	t.Helper()
	var rows []database.Lesson
	for j, tt := range fiveTitles {
		seedEntries(t, season, fiveLookAlikes[tt]...)
		if legacy {
			rows = append(rows, legacyLesson(j+1, tt, 5, season, fiveLookAlikes[tt][0]))
		} else {
			rows = append(rows, recordedLesson(j+1, season, fiveLookAlikes[tt]...))
		}
	}
	return rows
}

// TestRemoveLessonFilesByRecordAfterATitleChange proves a recorded lesson
// loses exactly its recorded entries even though its title (and so any name a
// matcher would derive) has changed since, and that another episode's entries
// in the folder are left.
func TestRemoveLessonFilesByRecordAfterATitleChange(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	mine := []string{"Show - s01e05 - Old Name.mp4", "Show - s01e05 - Old Name.nfo", "Show - s01e05 - Old Name play-along/"}
	seedEntries(t, season, mine...)
	seedEntries(t, season, "Show - s01e05 - New Name.mp4", "Show - s01e06 - Six.mp4")
	l := recordedLesson(1, season, mine...)
	l.Title = "New Name"
	if _, err := removeFiles(downloads, library, l, nil); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	assertGone(t, season, mine...)
	assertPresent(t, season, "Show - s01e05 - New Name.mp4", "Show - s01e06 - Six.mp4")
}

// TestRemoveLessonFilesNoVideoLessonByRecord (D55) proves a plex-tv lesson with
// no video (resources only) is deleted by its record: its folders go, the
// season folder and the other episodes stay.
func TestRemoveLessonFilesNoVideoLessonByRecord(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	mine := []string{"Show - s01e04 - Sheets.nfo", "Show - s01e04 - Sheets resources/", "Show - s01e04 - Sheets sheet-music/"}
	seedEntries(t, season, mine...)
	seedEntries(t, season, "Show - s01e05 - Five.mp4")
	if _, err := removeFiles(downloads, library, recordedLesson(1, season, mine...), nil); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	assertGone(t, season, mine...)
	assertPresent(t, season, "Show - s01e05 - Five.mp4")
}

// TestRemoveLessonFilesKeepsAnEntryTwoRecordsName proves an entry two records
// name is kept (and logged) rather than removed from under the other lesson.
func TestRemoveLessonFilesKeepsAnEntryTwoRecordsName(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	seedEntries(t, season, "Show - s01e05 - Same.mp4", "Show - s01e05 - Same.nfo")
	log := captureLog(t)
	a := recordedLesson(1, season, "Show - s01e05 - Same.mp4", "Show - s01e05 - Same.nfo")
	b := recordedLesson(2, season, "Show - s01e05 - Same.mp4")
	if _, err := removeFiles(downloads, library, a, []database.Lesson{a, b}); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	assertGone(t, season, "Show - s01e05 - Same.nfo")
	assertPresent(t, season, "Show - s01e05 - Same.mp4")
	if want := `kept "` + filepath.Join(season, "Show - s01e05 - Same.mp4") + `", which another lesson also claims`; !strings.Contains(log.String(), want) {
		t.Errorf("log %q does not report the kept entry", log.String())
	}
}

// TestRemoveLessonFilesRefusesADamagedRecord proves a record naming anything
// but an entry of a season folder is refused before anything is removed.
func TestRemoveLessonFilesRefusesADamagedRecord(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	seedEntries(t, season, "Show - s01e05 - Five.mp4")
	l := dirLesson(1, season)
	l.LibraryEntries = database.EncodeLibraryEntries([]string{"Show/Season 01/Show - s01e05 - Five.mp4", "../.."})
	if _, err := removeFiles(downloads, library, l, nil); err == nil {
		t.Error("removeLessonFiles(record naming the library root) = nil, want a refusal")
	}
	assertPresent(t, season, "Show - s01e05 - Five.mp4")
	assertPresent(t, library, "Show/")
}

// TestRemoveLessonFilesRefusesASymlinkedSeasonFolder proves the removal is
// confined to its root: a season folder that is a symlink to a folder outside
// the library is refused, and what it points at is untouched. (The trade-off
// the README states: an operator's deliberate symlink is refused too.)
func TestRemoveLessonFilesRefusesASymlinkedSeasonFolder(t *testing.T) {
	downloads, library, outside := t.TempDir(), t.TempDir(), t.TempDir()
	seedEntries(t, outside, "Show - s01e05 - Five.mp4")
	if err := os.MkdirAll(filepath.Join(library, "Show"), 0o755); err != nil {
		t.Fatal(err)
	}
	season := filepath.Join(library, "Show", "Season 01")
	if err := os.Symlink(outside, season); err != nil {
		t.Fatal(err)
	}
	if _, err := removeFiles(downloads, library, recordedLesson(1, season, "Show - s01e05 - Five.mp4"), nil); err == nil {
		t.Error("removeLessonFiles through a symlinked season folder = nil, want a refusal")
	}
	assertPresent(t, outside, "Show - s01e05 - Five.mp4")
}

// lockedEntry seeds a folder entry name in dir whose contents can not be
// removed (a read-only subfolder holding a file), and restores the mode at
// cleanup. It skips as root, where permissions do not bind.
func lockedEntry(t *testing.T, dir, name string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permissions do not bind as root")
	}
	locked := filepath.Join(dir, name, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
}

// TestRemoveLessonFilesReportsWhatRemains (D56) proves a removal that fails
// partway keeps going (every other entry is removed), returns the error, and
// says the row must record only what is still on disk: the recorded entries
// left, the season folder they are in, and no video once it is gone.
func TestRemoveLessonFilesReportsWhatRemains(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	stuck := "Show - s01e05 - Five resources"
	lockedEntry(t, season, stuck)
	seedEntries(t, season, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo")
	l := recordedLesson(1, season, stuck, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo")
	l.VideoPath = sql.NullString{String: filepath.Join(season, "Show - s01e05 - Five.mp4"), Valid: true}

	kept, err := removeFiles(downloads, library, l, nil)
	if err == nil {
		t.Fatal("removeLessonFiles = nil, want the removal error")
	}
	assertGone(t, season, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo")
	want := database.KeptFiles{OutputDir: true, LibraryEntries: recordOf(season, stuck)}
	if !reflect.DeepEqual(kept, want) {
		t.Errorf("kept = %+v, want %+v", kept, want)
	}
}

// TestRemoveLessonFilesKeepsOnlyWhatIsThere (D56) covers the other shapes of a
// partial failure: a default-layout folder that was removed while a library
// record it carried could not all be (output_dir is dropped, the record
// narrowed), a lesson folder that could not be removed (output_dir and video
// kept, no record invented), and a lesson moved before the record existed,
// which gains a record of exactly what is left.
func TestRemoveLessonFilesKeepsOnlyWhatIsThere(t *testing.T) {
	t.Run("folder gone, record left", func(t *testing.T) {
		downloads, library := t.TempDir(), t.TempDir()
		season := filepath.Join(library, "Show", "Season 01")
		stuck := "Show - s01e05 - Five resources"
		lockedEntry(t, season, stuck)
		folder := mkLessonDir(t, library, "Show/05 - Five")
		l := recordedLesson(1, season, stuck)
		l.OutputDir.String = folder
		kept, err := removeFiles(downloads, library, l, nil)
		if err == nil {
			t.Fatal("removeLessonFiles = nil, want the removal error")
		}
		if want := (database.KeptFiles{LibraryEntries: recordOf(season, stuck)}); !reflect.DeepEqual(kept, want) {
			t.Errorf("kept = %+v, want %+v", kept, want)
		}
	})
	t.Run("folder stuck", func(t *testing.T) {
		downloads, library := t.TempDir(), t.TempDir()
		folder := mkLessonDir(t, downloads, "Course/05 - Five")
		lockedEntry(t, folder, "resources")
		l := dirLesson(1, folder)
		l.VideoPath = sql.NullString{String: filepath.Join(folder, "v.mp4"), Valid: true}
		kept, err := removeFiles(downloads, library, l, nil)
		if err == nil {
			t.Fatal("removeLessonFiles = nil, want the removal error")
		}
		wantVideo := true
		if _, serr := os.Lstat(l.VideoPath.String); os.IsNotExist(serr) {
			wantVideo = false // RemoveAll got to the video before the stuck folder
		}
		if want := (database.KeptFiles{OutputDir: true, VideoPath: wantVideo}); !reflect.DeepEqual(kept, want) {
			t.Errorf("kept = %+v, want %+v", kept, want)
		}
	})
	t.Run("legacy lesson narrows to a record", func(t *testing.T) {
		downloads, library := t.TempDir(), t.TempDir()
		season := filepath.Join(library, "Course", "Season 01")
		stuck := "Course - s01e03 - Three resources"
		lockedEntry(t, season, stuck)
		seedEntries(t, season, "Course - s01e03 - Three.mp4", "Course - s01e03 - Three.nfo")
		l := legacyLesson(1, "Three", 3, season, "Course - s01e03 - Three.mp4")
		kept, err := removeFiles(downloads, library, l, nil)
		if err == nil {
			t.Fatal("removeLessonFiles = nil, want the removal error")
		}
		if want := (database.KeptFiles{OutputDir: true, LibraryEntries: recordOf(season, stuck)}); !reflect.DeepEqual(kept, want) {
			t.Errorf("kept = %+v, want %+v", kept, want)
		}
	})
}

// seedEntries creates each name under dir: a name ending in "/" is a folder
// (holding one file, so a non-recursive delete would fail on it), anything else
// a file whose content is its name.
func seedEntries(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, name := range names {
		p := filepath.Join(dir, name)
		if folder, ok := strings.CutSuffix(name, "/"); ok {
			if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", name, err)
			}
			p = filepath.Join(dir, folder, "inside.bin")
		}
		if err := os.WriteFile(p, []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// assertGone and assertPresent check each name (a trailing "/" is ignored)
// under dir.
func assertGone(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dir, strings.TrimSuffix(name, "/"))); !os.IsNotExist(err) {
			t.Errorf("%q survived the delete (stat err=%v), want removed", name, err)
		}
	}
}

func assertPresent(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dir, strings.TrimSuffix(name, "/"))); err != nil {
			t.Errorf("%q was removed (err=%v), want intact", name, err)
		}
	}
}

// TestRemoveLessonFilesLegacySong (D51) proves deleting a song moved before
// the record existed removes everything the move placed for it: both versions
// (the recorded one is [Drumless], so matching on the recorded name alone
// missed [Original]), the nfo, the poster and every subfolder. Episode 50, a
// sibling song, a look-alike title that extends this one, the season folder
// and the show folder all survive.
func TestRemoveLessonFilesLegacySong(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Songs", "Season 01")
	target := []string{
		"Songs - s01e05 - Five [Drumless].mp4",
		"Songs - s01e05 - Five [Original].mp4",
		"Songs - s01e05 - Five.nfo",
		"Songs - s01e05 - Five-poster.jpg",
		"Songs - s01e05 - Five resources/",
		"Songs - s01e05 - Five play-along/",
		"Songs - s01e05 - Five sheet-music/",
	}
	siblings := []string{
		"Songs - s01e50 - Fifty [Drumless].mp4",
		"Songs - s01e50 - Fifty [Original].mp4",
		"Songs - s01e50 - Fifty.nfo",
		"Songs - s01e50 - Fifty resources/",
		"Songs - s01e06 - Six [Drumless].mp4",
		"Songs - s01e06 - Six [Original].mp4",
		"Songs - s01e06 - Six-poster.jpg",
		"Songs - s01e06 - Six play-along/",
		"Songs - s01e05 - Five Bonus.mp4",
		"Songs - s01e05 - Five Bonus resources/",
	}
	seedEntries(t, season, append(append([]string{}, target...), siblings...)...)

	l := legacyLesson(1, "Five", 5, season, "Songs - s01e05 - Five [Drumless].mp4")
	if _, err := removeFiles(downloads, library, l, []database.Lesson{l}); err != nil {
		t.Fatalf("removeLessonFiles(song): %v", err)
	}
	assertGone(t, season, target...)
	assertPresent(t, season, siblings...)
	assertPresent(t, library, "Songs/Season 01/", "Songs/")
}

// TestRemoveLessonFilesLegacyLessonWithSubfolders proves an ordinary legacy
// plex-tv lesson's folders go with it, while episode 30's stay.
func TestRemoveLessonFilesLegacyLessonWithSubfolders(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Course", "Season 01")
	target := []string{
		"Course - s01e03 - Three.mp4",
		"Course - s01e03 - Three.nfo",
		"Course - s01e03 - Three.en.vtt",
		"Course - s01e03 - Three-poster.jpg",
		"Course - s01e03 - Three resources/",
		"Course - s01e03 - Three play-along/",
	}
	siblings := []string{
		"Course - s01e30 - Thirty.mp4",
		"Course - s01e30 - Thirty.nfo",
		"Course - s01e30 - Thirty resources/",
		"Course - s01e30 - Thirty play-along/",
	}
	seedEntries(t, season, append(append([]string{}, target...), siblings...)...)

	l := legacyLesson(1, "Three", 3, season, "Course - s01e03 - Three.mp4")
	if _, err := removeFiles(downloads, library, l, nil); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	assertGone(t, season, target...)
	assertPresent(t, season, siblings...)
	assertPresent(t, library, "Course/Season 01/")
}

// TestRemoveLessonFilesLegacyTitleEndingInTag proves a legacy lesson whose own
// title ends in a bracketed tag (so its video name looks like a song version)
// still loses every one of its files and folders.
func TestRemoveLessonFilesLegacyTitleEndingInTag(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	target := []string{
		"Show - s01e07 - Groove [Live].mp4",
		"Show - s01e07 - Groove [Live].nfo",
		"Show - s01e07 - Groove [Live]-poster.jpg",
		"Show - s01e07 - Groove [Live] resources/",
	}
	siblings := []string{"Show - s01e70 - Seventy.mp4", "Show - s01e08 - Groove [Live].mp4"}
	seedEntries(t, season, append(append([]string{}, target...), siblings...)...)

	l := legacyLesson(1, "Groove [Live]", 7, season, target[0])
	if _, err := removeFiles(downloads, library, l, nil); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	assertGone(t, season, target...)
	assertPresent(t, season, siblings...)
}

// TestRemoveLessonFilesSeasonFolderIsNeverWiped proves the delete follows the
// recorded location, not DRUMDROP_LAYOUT: a lesson recorded in a season folder
// loses only its own entries, with or without a video path, and the season
// folder itself is never removed.
func TestRemoveLessonFilesSeasonFolderIsNeverWiped(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	seedEntries(t, season, "Show - s01e01 - One.mp4", "Show - s01e01 - One.nfo", "Show - s01e02 - Two.mp4")

	if _, err := removeFiles(downloads, library, legacyLesson(1, "One", 1, season, ""), nil); err != nil {
		t.Fatalf("removeLessonFiles(season, no video): %v", err)
	}
	assertGone(t, season, "Show - s01e01 - One.mp4", "Show - s01e01 - One.nfo")
	assertPresent(t, season, "Show - s01e02 - Two.mp4")
}

// TestRemoveLessonFilesLegacyRefusesToGuess proves a legacy lesson whose
// episode name can not be told apart from a look-alike is refused, not
// guessed: nothing is removed.
func TestRemoveLessonFilesLegacyRefusesToGuess(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	// "Five [A] [B].mp4" with neither "Five.nfo" nor "Five [A].nfo": the video
	// could be Five's [A] [B] version, or Five [A]'s [B] version.
	names := []string{"Show - s01e05 - Five [A] [B].mp4", "Show - s01e05 - Five [A] [B]-poster.jpg"}
	seedEntries(t, season, names...)
	l := legacyLesson(1, "Retitled", 5, season, names[0])
	if _, err := removeFiles(downloads, library, l, nil); err == nil {
		t.Error("removeLessonFiles(ambiguous legacy lesson) = nil, want a refusal")
	}
	assertPresent(t, season, names...)
}

// TestRemoveLessonFilesLegacyLeftInDownloads proves a lesson whose library
// move failed (recorded in its own downloads folder, not a season folder) is
// removed whole, subfolders included.
func TestRemoveLessonFilesLegacyLeftInDownloads(t *testing.T) {
	downloads, library := t.TempDir(), t.TempDir()
	lessonDir := filepath.Join(downloads, "Course", "03 - Three")
	seedEntries(t, lessonDir, "03 - Three.mp4", "03 - Three.nfo", "resources/", "play-along/")
	l := dirLesson(1, lessonDir)
	l.VideoPath = sql.NullString{String: filepath.Join(lessonDir, "03 - Three.mp4"), Valid: true}
	if _, err := removeFiles(downloads, library, l, nil); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	assertGone(t, downloads, "Course/03 - Three/")
	assertPresent(t, downloads, "Course/")
}
