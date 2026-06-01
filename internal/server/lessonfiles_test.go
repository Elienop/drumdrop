package server

import (
	"os"
	"path/filepath"
	"testing"
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

// TestRemoveLessonFilesLibraryRooted asserts a library-rooted output_dir (the
// stored location when the library is on, post-move) is removed.
func TestRemoveLessonFilesLibraryRooted(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()

	outputDir := mkLessonDir(t, library, "Inst/Course/01 - Lesson")

	if err := removeLessonFiles("", downloads, library, outputDir, ""); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Errorf("library-rooted output_dir still present (err=%v)", err)
	}
}

// TestRemoveLessonFilesDownloadsRooted asserts a downloads-rooted output_dir (the
// no-library case) is removed.
func TestRemoveLessonFilesDownloadsRooted(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()

	outputDir := mkLessonDir(t, downloads, "01 - Lesson")

	if err := removeLessonFiles("", downloads, library, outputDir, ""); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Errorf("downloads-rooted output_dir still present (err=%v)", err)
	}
}

// TestRemoveLessonFilesEmptyLibraryRemovesDownloads asserts that with an empty
// libraryDir a downloads-rooted output_dir is still removed (and no error).
func TestRemoveLessonFilesEmptyLibraryRemovesDownloads(t *testing.T) {
	downloads := t.TempDir()

	outputDir := mkLessonDir(t, downloads, "03 - Lesson")

	if err := removeLessonFiles("", downloads, "", outputDir, ""); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Errorf("downloads copy still present (err=%v)", err)
	}
}

// TestRemoveLessonFilesRejectsOutsideBothRoots asserts an output_dir under
// NEITHER downloadsDir nor libraryDir is rejected with an error and NOT removed.
func TestRemoveLessonFilesRejectsOutsideBothRoots(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()
	outside := t.TempDir() // a third temp dir, under neither root

	victim := filepath.Join(outside, "keep")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatalf("MkdirAll victim: %v", err)
	}

	if err := removeLessonFiles("", downloads, library, victim, ""); err == nil {
		t.Error("removeLessonFiles on a path under neither root returned nil, want error")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("victim outside both roots was removed (err=%v), want left intact", err)
	}
}

// TestRemoveLessonFilesRejectsRootEqual asserts that an output_dir equal to the
// downloads root OR the library root (filepath.Rel returns ".") is refused —
// guarding the catastrophic RemoveAll that would otherwise wipe an entire root —
// and removes nothing.
func TestRemoveLessonFilesRejectsRootEqual(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()

	// Sentinels in both roots that must survive a refused delete.
	if err := os.WriteFile(filepath.Join(downloads, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write downloads sentinel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(library, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write library sentinel: %v", err)
	}

	// output_dir == downloads root.
	if err := removeLessonFiles("", downloads, library, downloads, ""); err == nil {
		t.Error("removeLessonFiles(outputDir==downloadsDir) returned nil, want error (would wipe the root)")
	}
	// output_dir == library root.
	if err := removeLessonFiles("", downloads, library, library, ""); err == nil {
		t.Error("removeLessonFiles(outputDir==libraryDir) returned nil, want error (would wipe the root)")
	}

	if _, err := os.Stat(filepath.Join(downloads, "keep.txt")); err != nil {
		t.Errorf("downloads root was wiped (err=%v), want intact", err)
	}
	if _, err := os.Stat(filepath.Join(library, "keep.txt")); err != nil {
		t.Errorf("library root was wiped (err=%v), want intact", err)
	}
}

// TestRemoveLessonFilesMissingTolerated asserts an already-removed path under
// either root is a benign no-op (os.RemoveAll treats a missing path as success).
func TestRemoveLessonFilesMissingTolerated(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()

	// A never-created path under downloads.
	if err := removeLessonFiles("", downloads, library, filepath.Join(downloads, "04 - Gone"), ""); err != nil {
		t.Errorf("removeLessonFiles on a missing downloads dir = %v, want nil (best-effort)", err)
	}
	// A never-created path under the library.
	if err := removeLessonFiles("", downloads, library, filepath.Join(library, "Inst/05 - Gone"), ""); err != nil {
		t.Errorf("removeLessonFiles on a missing library dir = %v, want nil (best-effort)", err)
	}
}

// TestRemoveLessonFilesPlexTvRemovesOnlyEpisode proves the plex-tv delete removes
// ONLY the target episode's files from a SHARED season folder: a sibling episode's
// files and the season folder itself survive, and the folder is never RemoveAll'd.
// It exercises the boundary-rune check (lessonfiles.go: rest[0] must be '.' or '-')
// with a sibling whose name is a genuine SUPERSTRING of the target base past a
// non-boundary rune ("Show - s01e05 - Five Bonus.mp4") — that file passes the
// HasPrefix gate yet must survive; deleting the boundary check turns it RED. The e50
// "Fifty" entry is a separate non-prefix sibling (HasPrefix already false).
func TestRemoveLessonFilesPlexTvRemovesOnlyEpisode(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()

	season := filepath.Join(library, "Show", "Season 01")
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatalf("mkdir season: %v", err)
	}
	// e05 (the target) sidecars + video, e06 sibling, an e50 non-prefix sibling, and
	// "Five Bonus" — a true superstring of the target base "Show - s01e05 - Five"
	// whose next rune (' ') is NOT a boundary char, so it must NOT be removed. That
	// last file is what actually exercises the boundary-rune guard.
	files := []string{
		"Show - s01e05 - Five.mp4",
		"Show - s01e05 - Five.nfo",
		"Show - s01e05 - Five.en.vtt",
		"Show - s01e05 - Five-poster.jpg",
		"Show - s01e06 - Six.mp4",
		"Show - s01e06 - Six.nfo",
		"Show - s01e50 - Fifty.mp4",      // non-prefix sibling: "...e05" is NOT a prefix
		"Show - s01e05 - Five Bonus.mp4", // superstring of the base past a non-boundary rune
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(season, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	videoPath := filepath.Join(season, "Show - s01e05 - Five.mp4")
	if err := removeLessonFiles("plex-tv", downloads, library, season, videoPath); err != nil {
		t.Fatalf("removeLessonFiles(plex-tv): %v", err)
	}

	// All e05 files gone.
	for _, name := range []string{
		"Show - s01e05 - Five.mp4",
		"Show - s01e05 - Five.nfo",
		"Show - s01e05 - Five.en.vtt",
		"Show - s01e05 - Five-poster.jpg",
	} {
		if _, err := os.Stat(filepath.Join(season, name)); !os.IsNotExist(err) {
			t.Errorf("e05 file %q survived (stat err=%v), want removed", name, err)
		}
	}
	// Sibling e06, the e50 non-prefix sibling, the boundary-check superstring, and the
	// season folder all intact.
	for _, name := range []string{
		"Show - s01e06 - Six.mp4",
		"Show - s01e06 - Six.nfo",
		"Show - s01e50 - Fifty.mp4",
		"Show - s01e05 - Five Bonus.mp4",
	} {
		if _, err := os.Stat(filepath.Join(season, name)); err != nil {
			t.Errorf("sibling/superstring file %q was removed (err=%v), want intact", name, err)
		}
	}
	if _, err := os.Stat(season); err != nil {
		t.Errorf("season folder was removed (err=%v), want never RemoveAll'd", err)
	}
}

// TestRemoveLessonFilesPlexTvEmptyVideoPathNoOp proves plex-tv with no video_path
// is a no-op (an undownloaded/ResourcesOnly lesson has no episode files to target).
func TestRemoveLessonFilesPlexTvEmptyVideoPathNoOp(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()
	season := filepath.Join(library, "Show", "Season 01")
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatalf("mkdir season: %v", err)
	}
	keep := filepath.Join(season, "Show - s01e05 - Five.mp4")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := removeLessonFiles("plex-tv", downloads, library, season, ""); err != nil {
		t.Errorf("removeLessonFiles(plex-tv, empty video_path) = %v, want nil (no-op)", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("file removed despite empty video_path (err=%v), want intact", err)
	}
}

// TestRemoveLessonFilesPlexTvRejectsOutsideRoots proves the under-root guard still
// applies in plex-tv mode: a video_path dir under neither root is refused.
func TestRemoveLessonFilesPlexTvRejectsOutsideRoots(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()
	outside := t.TempDir()

	season := filepath.Join(outside, "Show", "Season 01")
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatalf("mkdir outside season: %v", err)
	}
	victim := filepath.Join(season, "Show - s01e05 - Five.mp4")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := removeLessonFiles("plex-tv", downloads, library, season, victim); err == nil {
		t.Error("removeLessonFiles(plex-tv outside both roots) = nil, want error")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("victim outside both roots was removed (err=%v), want intact", err)
	}
}
