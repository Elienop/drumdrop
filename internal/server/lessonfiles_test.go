package server

import (
	"os"
	"path/filepath"
	"testing"
)

// mkLessonDir creates downloads/<rel> with a sample video file and returns the
// lesson dir, failing the test on error.
func mkLessonDir(t *testing.T, downloads, rel string) string {
	t.Helper()
	dir := filepath.Join(downloads, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "v.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}

// TestRemoveLessonFilesBothCopies asserts both the downloads copy and the
// library mirror are removed.
func TestRemoveLessonFilesBothCopies(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()

	outputDir := mkLessonDir(t, downloads, "01 - Lesson")
	libDir := filepath.Join(library, "01 - Lesson")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("MkdirAll library: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "v.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile library: %v", err)
	}

	if err := removeLessonFiles(downloads, library, outputDir); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}

	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Errorf("downloads copy still present (err=%v)", err)
	}
	if _, err := os.Stat(libDir); !os.IsNotExist(err) {
		t.Errorf("library mirror still present (err=%v)", err)
	}
}

// TestRemoveLessonFilesMissingLibraryTolerated asserts a never-mirrored library
// path is tolerated (no error) while the downloads copy is still removed.
func TestRemoveLessonFilesMissingLibraryTolerated(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir() // exists but has no mirror for this lesson

	outputDir := mkLessonDir(t, downloads, "02 - Lesson")

	if err := removeLessonFiles(downloads, library, outputDir); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Errorf("downloads copy still present (err=%v)", err)
	}
}

// TestRemoveLessonFilesEmptyLibraryRemovesOnlyDownloads asserts that with an
// empty libraryDir only the downloads copy is removed (and no error).
func TestRemoveLessonFilesEmptyLibraryRemovesOnlyDownloads(t *testing.T) {
	downloads := t.TempDir()

	outputDir := mkLessonDir(t, downloads, "03 - Lesson")

	if err := removeLessonFiles(downloads, "", outputDir); err != nil {
		t.Fatalf("removeLessonFiles: %v", err)
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Errorf("downloads copy still present (err=%v)", err)
	}
}

// TestRemoveLessonFilesRejectsOutsideRoot asserts an output_dir that does not
// sit under downloadsDir is rejected with an error and NOT removed.
func TestRemoveLessonFilesRejectsOutsideRoot(t *testing.T) {
	downloads := t.TempDir()
	outside := t.TempDir() // a sibling temp dir, not under downloads

	victim := filepath.Join(outside, "keep")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatalf("MkdirAll victim: %v", err)
	}

	if err := removeLessonFiles(downloads, "", victim); err == nil {
		t.Error("removeLessonFiles on a path outside downloads returned nil, want error")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("victim outside downloads was removed (err=%v), want left intact", err)
	}
}

// TestRemoveLessonFilesRejectsRootEqual asserts that an output_dir equal to the
// downloads root (filepath.Rel returns ".") is refused — guarding the
// catastrophic RemoveAll that would otherwise wipe the entire downloads AND
// library trees — and removes nothing.
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

	if err := removeLessonFiles(downloads, library, downloads); err == nil {
		t.Error("removeLessonFiles(outputDir==downloadsDir) returned nil, want error (would wipe the root)")
	}
	if _, err := os.Stat(filepath.Join(downloads, "keep.txt")); err != nil {
		t.Errorf("downloads root was wiped (err=%v), want intact", err)
	}
	if _, err := os.Stat(filepath.Join(library, "keep.txt")); err != nil {
		t.Errorf("library root was wiped (err=%v), want intact", err)
	}
}

// TestRemoveLessonFilesMissingDownloadsTolerated asserts an already-removed
// downloads copy is a benign no-op (os.RemoveAll treats a missing path as
// success).
func TestRemoveLessonFilesMissingDownloadsTolerated(t *testing.T) {
	downloads := t.TempDir()
	library := t.TempDir()

	// A path under downloads that was never created.
	outputDir := filepath.Join(downloads, "04 - Gone")

	if err := removeLessonFiles(downloads, library, outputDir); err != nil {
		t.Errorf("removeLessonFiles on a missing dir = %v, want nil (best-effort)", err)
	}
}
