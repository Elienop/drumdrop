package scheduler

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
)

// errInjectedRename is the canned failure the cross-fs fallback tests force
// from the injected rename seam: the platform's "different filesystems"
// answer, as a real rename wraps it, so the moves fall through to their copy.
var errInjectedRename error = &os.LinkError{Op: "rename", Old: "src", New: "dst", Err: errCrossDevice}

// seedLesson writes the four typical sidecar+video files of a finished lesson
// into <dl>/Inst/Course/01 - L and returns the lesson dir plus the downloads
// root. Each file gets distinct content so a copy can be checked for equality.
func seedLesson(t *testing.T) (downloadsDir, lessonDir string) {
	t.Helper()
	tmp := t.TempDir()
	downloadsDir = filepath.Join(tmp, "dl")
	lessonDir = filepath.Join(downloadsDir, "Inst", "Course", "01 - L")
	if err := os.MkdirAll(lessonDir, 0o755); err != nil {
		t.Fatalf("mkdir lesson dir: %v", err)
	}
	files := map[string]string{
		"01 - L.mp4":        "video-bytes",
		"01 - L.nfo":        "<nfo/>",
		"01 - L-poster.jpg": "poster-bytes",
		"01 - L.en.vtt":     "WEBVTT",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(lessonDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return downloadsDir, lessonDir
}

// movePlex runs the plex-tv move with no lesson on record (no previous
// download, no other lesson), returning the season folder and the video. A
// placement is committed, and the downloaded folder removed, as the worker does.
func movePlex(t *testing.T, libraryDir, show string, season, episode int, title, lessonDir string) (string, string, error) {
	t.Helper()
	res, err := testMovePlexTV(t, libraryDir, plexEpisode{show, season, episode, title}, lessonDir, plexLibrary{})
	return res.seasonDir, res.videoPath, err
}

// lessonFiles is the four file names seedLesson writes.
var lessonFiles = []string{"01 - L.mp4", "01 - L.nfo", "01 - L-poster.jpg", "01 - L.en.vtt"}

// lessonContent is what seedLesson writes in each of lessonFiles.
var lessonContent = map[string]string{
	"01 - L.mp4":        "video-bytes",
	"01 - L.nfo":        "<nfo/>",
	"01 - L-poster.jpg": "poster-bytes",
	"01 - L.en.vtt":     "WEBVTT",
}

// assertLessonIn checks every file seedLesson writes is in dir, content intact.
func assertLessonIn(t *testing.T, dir string) {
	t.Helper()
	for _, name := range lessonFiles {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("missing %s: %v", filepath.Join(dir, name), err)
			continue
		}
		if string(got) != lessonContent[name] {
			t.Errorf("%s content = %q, want %q", name, got, lessonContent[name])
		}
	}
}

// TestPlaceLessonFolderRenames proves a placement moves every entry of the
// downloaded folder into the lesson's folder at the same path relative to its
// root, by rename (the downloaded folder is left empty), content intact.
func TestPlaceLessonFolderRenames(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib") // same tmp fs

	pl, err := testPlacePending(t, downloadsDir, libraryDir, lessonDir, database.Lesson{})
	if err != nil {
		t.Fatalf("placeLessonFolder: %v", err)
	}
	wantDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	if pl.dir != wantDir {
		t.Errorf("dir = %q, want %q", pl.dir, wantDir)
	}
	if names := readDirNames(t, lessonDir); len(names) != 0 {
		t.Errorf("the downloaded folder still holds %v, want every entry renamed out", names)
	}
	if _, err := pl.commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertLessonIn(t, wantDir)
}

// TestPlaceLessonFolderCrossFsFallback proves that when a rename fails because
// the two folders are on different filesystems, the placement copies each
// entry into the lesson's folder, and leaves the source for the private
// folder's removal.
func TestPlaceLessonFolderCrossFsFallback(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
	forceCopyFallback(t)

	newDir, err := testPlace(t, downloadsDir, libraryDir, lessonDir, database.Lesson{})
	if err != nil {
		t.Fatalf("placeLessonFolder: %v", err)
	}
	assertLessonIn(t, newDir)
}

// TestPlaceLessonFolderReplacesOnlyTheNamesItPlaces (owner ruling #66, D66)
// proves a placement into a lesson folder that already exists replaces only
// the entries at the names it places, and says so (each is set aside until
// the commit, which returns it), while every other entry there stays: a
// re-download never deletes a file it does not replace.
func TestPlaceLessonFolderReplacesOnlyTheNamesItPlaces(t *testing.T) {
	for _, own := range []bool{false, true} {
		t.Run(fmt.Sprintf("the lesson records the folder=%v", own), func(t *testing.T) {
			downloadsDir, lessonDir := seedLesson(t)
			libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
			dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
			seedSeason(t, dstDir, "01 - L.mp4", "old-sheet.pdf", "my notes/")
			self := database.Lesson{RailcontentID: 1}
			if own {
				self.OutputDir = sql.NullString{String: dstDir, Valid: true}
			}

			pl, err := testPlacePending(t, downloadsDir, libraryDir, lessonDir, self)
			if err != nil {
				t.Fatalf("placeLessonFolder: %v", err)
			}
			replaced, err := pl.commit()
			if err != nil {
				t.Fatalf("commit: %v", err)
			}
			if len(replaced) != 1 || replaced[0].path != filepath.Join(dstDir, "01 - L.mp4") || replaced[0].own != own {
				t.Errorf("replaced = %+v, want only the old video, own=%v", replaced, own)
			}
			assertLessonIn(t, dstDir)
			assertContent(t, dstDir, "old-sheet.pdf")
			assertExist(t, true, filepath.Join(dstDir, "my notes", "f.pdf"))
			assertExist(t, false, filepath.Join(libraryDir, privateRootName, replacedFolderName(7)))
		})
	}
}

// TestPlaceLessonFolderUndoPutsEverythingBack (D79) proves a placement that
// is undone (its record was refused: a Skip, a delete or a follow removal
// landed while it placed) takes every placed entry back into the downloaded
// folder, and puts every entry it replaced back where it was, so the lesson
// folder is exactly as before; for a delete of the lesson's files, the
// lesson's own replaced entries are not put back.
func TestPlaceLessonFolderUndoPutsEverythingBack(t *testing.T) {
	for _, dropOwn := range []bool{false, true} {
		t.Run(fmt.Sprintf("dropOwn=%v", dropOwn), func(t *testing.T) {
			downloadsDir, lessonDir := seedLesson(t)
			libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
			dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
			seedSeason(t, dstDir, "01 - L.mp4", "old-sheet.pdf")
			self := database.Lesson{RailcontentID: 1, OutputDir: sql.NullString{String: dstDir, Valid: true}}

			pl, err := testPlacePending(t, downloadsDir, libraryDir, lessonDir, self)
			if err != nil {
				t.Fatalf("placeLessonFolder: %v", err)
			}
			if stuck, err := pl.undo(dropOwn); err != nil || len(stuck) != 0 {
				t.Fatalf("undo = %v, %v", stuck, err)
			}
			assertLessonIn(t, lessonDir)
			assertContent(t, dstDir, "old-sheet.pdf")
			if dropOwn {
				assertExist(t, false, filepath.Join(dstDir, "01 - L.mp4"))
			} else {
				assertContent(t, dstDir, "01 - L.mp4")
			}
			if got := sorted(readDirNames(t, dstDir)); dropOwn && len(got) != 1 || !dropOwn && len(got) != 2 {
				t.Errorf("lesson folder holds %v after the undo", got)
			}
			assertExist(t, false, filepath.Join(libraryDir, privateRootName, replacedFolderName(7)))
		})
	}
}

// TestPlaceLessonFolderNeverReusesTheAreaACrashLeft proves a placement for a
// job whose earlier placement a crash stopped (its replaced-<job> folder,
// which the startup sweep keeps, may hold an earlier download's only copy)
// sets its entries aside in a new area, and removes only that one once it is
// committed or undone.
func TestPlaceLessonFolderNeverReusesTheAreaACrashLeft(t *testing.T) {
	for _, end := range []string{"commit", "undo"} {
		t.Run(end, func(t *testing.T) {
			downloadsDir, lessonDir := seedLesson(t)
			libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
			dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
			seedSeason(t, dstDir, "01 - L.mp4")
			left := filepath.Join(libraryDir, privateRootName, replacedFolderName(7), "0")
			seedSeason(t, left, "01 - L.mp4")

			pl, err := testPlacePending(t, downloadsDir, libraryDir, lessonDir, database.Lesson{RailcontentID: 1})
			if err != nil {
				t.Fatalf("placeLessonFolder: %v", err)
			}
			if end == "commit" {
				_, err = pl.commit()
			} else {
				_, err = pl.undo(false)
			}
			if err != nil {
				t.Fatalf("%s: %v", end, err)
			}
			assertContent(t, left, "01 - L.mp4")
			if names := readDirNames(t, filepath.Join(libraryDir, privateRootName)); len(names) != 2 {
				t.Errorf("private root holds %v, want only the .plexignore and the crash's area", names)
			}
		})
	}
}

// TestPlaceLessonFolderUndoRemovesTheFolderItMade proves an undone placement
// into a lesson folder that did not exist leaves no empty folder behind.
func TestPlaceLessonFolderUndoRemovesTheFolderItMade(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
	pl, err := testPlacePending(t, downloadsDir, libraryDir, lessonDir, database.Lesson{})
	if err != nil {
		t.Fatalf("placeLessonFolder: %v", err)
	}
	if _, err := pl.undo(false); err != nil {
		t.Fatalf("undo: %v", err)
	}
	assertExist(t, false, filepath.Join(libraryDir, "Inst", "Course", "01 - L"))
	assertLessonIn(t, lessonDir)
}

// TestPlaceLessonFolderReplacesThePreviousFolder proves a placement replaces
// the lesson's previous folder when its row records another one (here the
// copy kept in downloads when a move into the library was refused), and keeps
// one another lesson records something in.
func TestPlaceLessonFolderReplacesThePreviousFolder(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) { checkPlaceReplacesPrevious(t, held) })
	}
}

// checkPlaceReplacesPrevious places lesson 1, whose row records a previous
// folder in downloads, into the library and commits it. When held, lesson 2
// records the previous folder's video, so the placement must keep that folder;
// otherwise it must replace it as the lesson's own.
func checkPlaceReplacesPrevious(t *testing.T, held bool) {
	tmp := t.TempDir()
	downloadsDir, libraryDir := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
	lessonDir := filepath.Join(downloadsDir, privateRootName, "job-7", "Inst", "Course", "01 - L")
	seedSeason(t, lessonDir, lessonFiles...)
	previous := filepath.Join(downloadsDir, "Inst", "Course", "01 - L")
	seedSeason(t, previous, "01 - L.mp4")
	self := database.Lesson{RailcontentID: 1, OutputDir: sql.NullString{String: previous, Valid: true}}
	var others []database.Lesson
	if held {
		others = append(others, database.Lesson{RailcontentID: 2, VideoPath: sql.NullString{String: filepath.Join(previous, "01 - L.mp4"), Valid: true}})
	}
	c, err := library.NewClaims(libraryDir, others)
	if err != nil {
		t.Fatal(err)
	}
	src, err := openScratch(downloadsDir, lessonDir)
	if err != nil {
		t.Fatal(err)
	}
	defer src.close()
	pl, err := placeLessonFolder(libraryDir, filepath.Join("Inst", "Course", "01 - L"), src, self, c, library.Roots(libraryDir, downloadsDir), 7)
	if err != nil {
		t.Fatalf("placeLessonFolder: %v", err)
	}
	replaced, err := pl.commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertExist(t, held, previous)
	if !held && (len(replaced) != 1 || replaced[0].path != previous || !replaced[0].own) {
		t.Errorf("replaced = %+v, want the previous folder, as the lesson's own", replaced)
	}
	assertContent(t, filepath.Join(libraryDir, "Inst", "Course", "01 - L"), lessonFiles...)
}

// TestPlaceLessonFolderRefusesItsOwnSource (hard rule 12, was
// TestMoveToLibraryDestEqualsSourceIsNoOp) proves a placement whose
// destination is the downloaded folder itself is refused by name, and the
// lesson is intact. The old no-op guard existed because clearing the
// destination deleted the source when the library was the downloads folder;
// a placement's source is now always the job's private folder, so this can
// only happen by a bug, and a replaced entry is only set aside anyway.
func TestPlaceLessonFolderRefusesItsOwnSource(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	_, err := testPlace(t, downloadsDir, downloadsDir, lessonDir, database.Lesson{})
	if err == nil || !strings.Contains(err.Error(), "it is the downloaded folder itself") {
		t.Fatalf("placeLessonFolder = %v, want the named refusal", err)
	}
	assertLessonIn(t, lessonDir)
	assertExist(t, false, filepath.Join(downloadsDir, privateRootName))
}

// TestPlaceLessonFolderRejectsABadFolder proves a lesson folder that is not
// strictly inside its root (".", "..", an escape, one level only), or that is
// inside the private folder (any casing), is refused before anything is
// written.
func TestPlaceLessonFolderRejectsABadFolder(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
	src, err := openScratch(filepath.Dir(lessonDir), lessonDir)
	if err != nil {
		t.Fatal(err)
	}
	defer src.close()
	c, err := library.NewClaims(libraryDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"", ".", "..", filepath.Join("..", "x", "01 - L"), "01 - L",
		filepath.Join(privateRootName, "01 - L"), filepath.Join(".DrumDrop-In-Progress", "job-1", "01 - L")} {
		if _, err := placeLessonFolder(libraryDir, rel, src, database.Lesson{RailcontentID: 1}, c, nil, 7); err == nil {
			t.Errorf("placeLessonFolder(%q) = nil, want a refusal", rel)
		}
	}
	if _, err := os.Stat(libraryDir); !os.IsNotExist(err) {
		t.Errorf("library created despite the refusals (stat err = %v)", err)
	}
	assertLessonIn(t, lessonDir)
}

// plexFiles maps each sidecar/video suffix to the content seedLesson wrote, so a
// plex-tv move can be checked for an exact rename of every file.
var plexFiles = map[string]string{
	".mp4":        "video-bytes",
	".nfo":        "<nfo/>",
	"-poster.jpg": "poster-bytes",
	".en.vtt":     "WEBVTT",
}

// TestMoveToLibraryPlexTV proves that moveToLibraryPlexTV flattens the scratch
// "NN - Title" lesson into <lib>/<Show>/Season 01/ with every file renamed to the
// episode base "<Show> - s01eNN - Title<suffix>", content intact, the scratch dir
// removed, and the returned videoPath pointing at the moved .mp4.
func TestMoveToLibraryPlexTV(t *testing.T) {
	_, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(lessonDir)))), "lib")

	seasonDir, videoPath, err := movePlex(t, libraryDir, "Beginner Course", 1, 5, "Lesson Five", lessonDir)
	if err != nil {
		t.Fatalf("moveToLibraryPlexTV: %v", err)
	}

	wantSeason := filepath.Join(libraryDir, "Beginner Course", "Season 01")
	if seasonDir != wantSeason {
		t.Errorf("seasonDir = %q, want %q", seasonDir, wantSeason)
	}
	base := "Beginner Course - s01e05 - Lesson Five"
	wantVideo := filepath.Join(wantSeason, base+".mp4")
	if videoPath != wantVideo {
		t.Errorf("videoPath = %q, want %q", videoPath, wantVideo)
	}
	// Every file is flat in the season folder under the episode base, content intact.
	for suffix, body := range plexFiles {
		p := filepath.Join(wantSeason, base+suffix)
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("missing moved file %s: %v", base+suffix, err)
			continue
		}
		if string(got) != body {
			t.Errorf("%s content = %q, want %q", base+suffix, got, body)
		}
	}
	// The scratch lesson dir is gone.
	if _, err := os.Stat(lessonDir); !os.IsNotExist(err) {
		t.Errorf("scratch lesson dir still present (stat err = %v), want removed", err)
	}
}

// TestMoveToLibraryPlexTVCrossFsFallback proves the per-file copy fallback runs
// when rename fails (cross-filesystem): files land in the season folder and the
// scratch source is removed.
func TestMoveToLibraryPlexTVCrossFsFallback(t *testing.T) {
	_, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(lessonDir)))), "lib")

	stubRename(t, func(oldpath, newpath string) error { return errInjectedRename })

	seasonDir, videoPath, err := movePlex(t, libraryDir, "Beginner Course", 1, 5, "Lesson Five", lessonDir)
	if err != nil {
		t.Fatalf("moveToLibraryPlexTV: %v", err)
	}
	base := "Beginner Course - s01e05 - Lesson Five"
	if videoPath != filepath.Join(seasonDir, base+".mp4") {
		t.Errorf("videoPath = %q, want %q", videoPath, filepath.Join(seasonDir, base+".mp4"))
	}
	for suffix, body := range plexFiles {
		got, err := os.ReadFile(filepath.Join(seasonDir, base+suffix))
		if err != nil {
			t.Errorf("missing copied file %s: %v", base+suffix, err)
			continue
		}
		if string(got) != body {
			t.Errorf("%s content = %q, want %q", base+suffix, got, body)
		}
	}
	if _, err := os.Stat(lessonDir); !os.IsNotExist(err) {
		t.Errorf("scratch lesson dir still present after copy fallback (stat err = %v), want removed", err)
	}
}

// TestMoveToLibraryPlexTVSharedSeason proves two episodes moved into the SAME show
// land flat in one shared "Season 01" folder, each under its own episode base, and
// neither move clobbers the other's files.
func TestMoveToLibraryPlexTVSharedSeason(t *testing.T) {
	tmp := t.TempDir()
	libraryDir := filepath.Join(tmp, "lib")
	mkScratch := func(idx int, title string) string {
		base := filepath.Base(filepathFor(idx, title))
		dir := filepath.Join(tmp, "dl", base)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir scratch: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, base+".mp4"), []byte("v"+title), 0o644); err != nil {
			t.Fatalf("write mp4: %v", err)
		}
		return dir
	}

	s1 := mkScratch(5, "Five")
	if _, _, err := movePlex(t, libraryDir, "Show", 1, 5, "Five", s1); err != nil {
		t.Fatalf("move e05: %v", err)
	}
	s2 := mkScratch(6, "Six")
	season, _, err := movePlex(t, libraryDir, "Show", 1, 6, "Six", s2)
	if err != nil {
		t.Fatalf("move e06: %v", err)
	}

	// Both episodes coexist in the one season folder.
	if _, err := os.Stat(filepath.Join(season, "Show - s01e05 - Five.mp4")); err != nil {
		t.Errorf("e05 missing from shared season: %v", err)
	}
	if _, err := os.Stat(filepath.Join(season, "Show - s01e06 - Six.mp4")); err != nil {
		t.Errorf("e06 missing from shared season: %v", err)
	}
}

// TestMoveToLibraryPlexTVSongVersions proves a song's two bracket-tagged video
// files share the same episode base (so Plex merges them as one episode with two
// versions) and that a subdirectory (resources/) survives the move, renamed with
// the episode-base prefix, instead of being deleted with the scratch dir.
func TestMoveToLibraryPlexTVSongVersions(t *testing.T) {
	tmp := t.TempDir()
	libraryDir := filepath.Join(tmp, "lib")
	scratchBase := "01 - Even Flow"
	lessonDir := filepath.Join(tmp, "dl", scratchBase)
	if err := os.MkdirAll(filepath.Join(lessonDir, "resources"), 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	files := map[string]string{
		scratchBase + " [Original].mp4": "orig-video",
		scratchBase + " [Drumless].mp4": "drumless-video",
		scratchBase + ".nfo":            "<episodedetails/>",
		scratchBase + "-poster.jpg":     "poster",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(lessonDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(lessonDir, "resources", "song.pdf"), []byte("pdf-bytes"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	seasonDir, videoPath, err := movePlex(t, libraryDir, "Songs", 1, 1, "Even Flow", lessonDir)
	if err != nil {
		t.Fatalf("moveToLibraryPlexTV: %v", err)
	}

	episodeBase := "Songs - s01e01 - Even Flow"
	// Both version files moved, sharing the episode base; content intact.
	for tag, body := range map[string]string{" [Original].mp4": "orig-video", " [Drumless].mp4": "drumless-video"} {
		p := filepath.Join(seasonDir, episodeBase+tag)
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("missing moved version file %s: %v", episodeBase+tag, err)
			continue
		}
		if string(got) != body {
			t.Errorf("%s content = %q, want %q", episodeBase+tag, got, body)
		}
	}
	// videoPath must be a real moved .mp4 (one of the two versions).
	if videoPath != filepath.Join(seasonDir, episodeBase+" [Drumless].mp4") &&
		videoPath != filepath.Join(seasonDir, episodeBase+" [Original].mp4") {
		t.Errorf("videoPath = %q, want one of the version files", videoPath)
	}
	if _, err := os.Stat(videoPath); err != nil {
		t.Errorf("returned videoPath does not exist: %v", err)
	}
	// The PDF survives in a renamed subdir, not deleted with the scratch dir.
	pdf := filepath.Join(seasonDir, episodeBase+" resources", "song.pdf")
	got, err := os.ReadFile(pdf)
	if err != nil {
		t.Errorf("PDF lost (subdir not preserved): %v", err)
	} else if string(got) != "pdf-bytes" {
		t.Errorf("PDF content = %q, want %q", got, "pdf-bytes")
	}
	// The scratch lesson dir is gone.
	if _, err := os.Stat(lessonDir); !os.IsNotExist(err) {
		t.Errorf("scratch lesson dir still present (stat err = %v), want removed", err)
	}
}

// filepathFor builds the scratch "NN - Sanitize(title)" base name a download would
// produce, used to seed shared-season scratch dirs in the test above.
func filepathFor(idx int, title string) string {
	return lessonDir("", idx, title)
}

// TestPlexEpisodeBase proves the shared episode-base helper produces the exact
// name the move uses (and that the worker must reuse to locate the nfo), and that
// the show/title components are sanitized.
func TestPlexEpisodeBase(t *testing.T) {
	if got, want := plexEpisodeBase("Beginner Course", "Lesson Five", 1, 5), "Beginner Course - s01e05 - Lesson Five"; got != want {
		t.Errorf("plexEpisodeBase = %q, want %q", got, want)
	}
	// show and title are sanitized (path separators replaced); season/episode zero-padded.
	if got, want := plexEpisodeBase("A/B", "C:D", 2, 13), musora.Sanitize("A/B")+" - s02e13 - "+musora.Sanitize("C:D"); got != want {
		t.Errorf("plexEpisodeBase sanitized = %q, want %q", got, want)
	}
}

// forceCopyFallback makes every rename fail, as across two filesystems, so the
// move takes its copy path.
func forceCopyFallback(t *testing.T) {
	t.Helper()
	stubRename(t, func(oldpath, newpath string) error { return errInjectedRename })
}

// forceCopyFallbackInto makes every rename into root from outside it fail,
// as across two filesystems, and lets every other rename through: a library
// on another filesystem than the downloads folder, where the private folders
// are. A rename within root (setting an entry aside in root's private folder,
// or putting it back) stays on one filesystem, so it goes through.
func forceCopyFallbackInto(t *testing.T, root string) {
	t.Helper()
	stubRename(t, func(oldpath, newpath string) error {
		if library.Inside(root, newpath) && !library.Inside(root, oldpath) {
			return errInjectedRename
		}
		return renameNoReplace(oldpath, newpath)
	})
}

// skipWithoutPermissionChecks skips the test up front where the OS does not
// enforce file permissions (root, Windows). Tests that break a download inside
// the worker call it first, so the skip in makeUnreadable/makeUndeletable never
// has to fire from inside a run.
func skipWithoutPermissionChecks(t *testing.T) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatalf("create %s: %v", probe, err)
	}
	makeUnreadable(t, probe)
}

// makeUnreadable removes every permission from the file at path, skipping the
// test where the OS does not enforce that (root, Windows) so it cannot pass
// without exercising the failure.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("file permissions are not enforced on Windows")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Skip("running with permission checks bypassed (root?): an unreadable file is readable")
	}
}

// makeUndeletable makes the folder at dir read-only, so nothing in it can be
// removed, skipping the test where the OS does not enforce that.
func makeUndeletable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions are not enforced on Windows")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	probe := filepath.Join(dir, ".probe")
	if err := os.WriteFile(probe, nil, 0o644); err == nil {
		_ = os.Remove(probe)
		t.Skip("running with permission checks bypassed (root?): a read-only folder is writable")
	}
}

// readDirNames lists dir's entry names, or nil if it does not exist.
func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestPlaceLessonFolderCopyFailsPartWayLeavesNoPartialCopy covers D52's first
// case: in the default layout the copy fails part-way (a file cannot be read).
// The placement reports no folder, the partial copy is gone from the library
// (the lesson folder it made included), and the whole lesson is still in its
// downloaded folder.
func TestPlaceLessonFolderCopyFailsPartWayLeavesNoPartialCopy(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
	forceCopyFallback(t)
	makeUnreadable(t, filepath.Join(lessonDir, "01 - L.nfo"))

	newDir, err := testPlace(t, downloadsDir, libraryDir, lessonDir, database.Lesson{})
	if err == nil {
		t.Fatal("placeLessonFolder = nil error, want the copy failure")
	}
	if newDir != "" {
		t.Errorf("newDir = %q, want \"\" (nothing placed)", newDir)
	}
	dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	if names := readDirNames(t, dstDir); names != nil {
		t.Errorf("library still holds a partial copy %v at %s, want it removed", names, dstDir)
	}
	for _, name := range lessonFiles {
		if _, err := os.Stat(filepath.Join(lessonDir, name)); err != nil {
			t.Errorf("downloads lost %s after a failed copy: %v", name, err)
		}
	}
}

// seedSongScratch writes a song's scratch folder (two versions, nfo, poster,
// resources/) under tmp/dl and returns it with the episode base the move gives
// it as episode 5 of "Songs", and the season folder.
func seedSongScratch(t *testing.T, tmp string) (lessonDir, episodeBase, seasonDir string) {
	t.Helper()
	scratchBase := "05 - Even Flow"
	lessonDir = filepath.Join(tmp, "dl", "Songs", scratchBase)
	if err := os.MkdirAll(filepath.Join(lessonDir, "resources"), 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	for _, name := range []string{
		scratchBase + " [Drumless].mp4", scratchBase + " [Original].mp4",
		scratchBase + "-poster.jpg", scratchBase + ".nfo", filepath.Join("resources", "song.pdf"),
	} {
		if err := os.WriteFile(filepath.Join(lessonDir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return lessonDir, "Songs - s01e05 - Even Flow", filepath.Join(tmp, "lib", "Songs", "Season 01")
}

// songScratchEntries is what seedSongScratch puts in the scratch folder.
var songScratchEntries = []string{
	"05 - Even Flow [Drumless].mp4", "05 - Even Flow [Original].mp4",
	"05 - Even Flow-poster.jpg", "05 - Even Flow.nfo", "resources",
}

// assertScratchWhole checks the scratch folder still holds every entry of the
// song, and assertNoEpisodeIn that the season folder holds none of it (only the
// names in keep).
func assertScratchWhole(t *testing.T, lessonDir string) {
	t.Helper()
	for _, name := range songScratchEntries {
		if _, err := os.Stat(filepath.Join(lessonDir, name)); err != nil {
			t.Errorf("scratch lost %s after an undone move: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(lessonDir, "resources", "song.pdf")); err != nil {
		t.Errorf("scratch lost resources/song.pdf: %v", err)
	}
}

func assertNoEpisodeIn(t *testing.T, seasonDir string, keep ...string) {
	t.Helper()
	got := readDirNames(t, seasonDir)
	if len(got) != len(keep) {
		t.Errorf("season folder holds %v, want only %v (no partial episode)", got, keep)
		return
	}
	for i := range keep {
		if got[i] != keep[i] {
			t.Errorf("season folder holds %v, want only %v (no partial episode)", got, keep)
			return
		}
	}
}

// TestMoveToLibraryPlexTVCopyFailsPartWayUndoesTheMove covers D52's second case:
// the plex-tv move copies file by file, and the fourth entry cannot be read.
// The three already copied are taken back out of the season folder, a sibling
// episode is untouched, nothing is returned to record in the library, and the
// whole song is still in scratch.
func TestMoveToLibraryPlexTVCopyFailsPartWayUndoesTheMove(t *testing.T) {
	tmp := t.TempDir()
	lessonDir, _, seasonDir := seedSongScratch(t, tmp)
	sibling := "Songs - s01e06 - Six.mp4"
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatalf("mkdir season: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seasonDir, sibling), []byte("six"), 0o644); err != nil {
		t.Fatalf("write sibling: %v", err)
	}
	forceCopyFallback(t)
	makeUnreadable(t, filepath.Join(lessonDir, "05 - Even Flow.nfo"))

	gotSeason, videoPath, err := movePlex(t, filepath.Join(tmp, "lib"), "Songs", 1, 5, "Even Flow", lessonDir)
	if err == nil {
		t.Fatal("moveToLibraryPlexTV = nil error, want the copy failure")
	}
	if gotSeason != "" || videoPath != "" {
		t.Errorf("returned (%q, %q), want empty (record the lesson in scratch)", gotSeason, videoPath)
	}
	assertNoEpisodeIn(t, seasonDir, sibling)
	assertScratchWhole(t, lessonDir)
}

// TestMoveToLibraryPlexTVUndoRenamesBack covers the same case when some entries
// were renamed rather than copied (their scratch source is gone): the [Drumless]
// version renames, the [Original] cannot be renamed or read. Undoing the move
// must rename [Drumless] back, so scratch is whole again.
func TestMoveToLibraryPlexTVUndoRenamesBack(t *testing.T) {
	tmp := t.TempDir()
	lessonDir, _, seasonDir := seedSongScratch(t, tmp)
	stubRename(t, func(oldpath, newpath string) error {
		if strings.Contains(oldpath, "[Original]") {
			return errInjectedRename
		}
		return renameNoReplace(oldpath, newpath)
	})
	makeUnreadable(t, filepath.Join(lessonDir, "05 - Even Flow [Original].mp4"))

	gotSeason, _, err := movePlex(t, filepath.Join(tmp, "lib"), "Songs", 1, 5, "Even Flow", lessonDir)
	if err == nil || gotSeason != "" {
		t.Fatalf("moveToLibraryPlexTV = (%q, %v), want (\"\", the copy failure)", gotSeason, err)
	}
	assertNoEpisodeIn(t, seasonDir)
	assertScratchWhole(t, lessonDir)
}
