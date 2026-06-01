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

	if err := removeLessonFiles(downloads, library, outputDir); err != nil {
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

	if err := removeLessonFiles(downloads, library, outputDir); err != nil {
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

	if err := removeLessonFiles(downloads, "", outputDir); err != nil {
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

	if err := removeLessonFiles(downloads, library, victim); err == nil {
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
	if err := removeLessonFiles(downloads, library, downloads); err == nil {
		t.Error("removeLessonFiles(outputDir==downloadsDir) returned nil, want error (would wipe the root)")
	}
	// output_dir == library root.
	if err := removeLessonFiles(downloads, library, library); err == nil {
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
	if err := removeLessonFiles(downloads, library, filepath.Join(downloads, "04 - Gone")); err != nil {
		t.Errorf("removeLessonFiles on a missing downloads dir = %v, want nil (best-effort)", err)
	}
	// A never-created path under the library.
	if err := removeLessonFiles(downloads, library, filepath.Join(library, "Inst/05 - Gone")); err != nil {
		t.Errorf("removeLessonFiles on a missing library dir = %v, want nil (best-effort)", err)
	}
}
