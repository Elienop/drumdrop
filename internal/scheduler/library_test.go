package scheduler

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// errInjectedRename is the canned failure the cross-fs fallback test forces from
// the injected rename seam, so moveToLibrary falls through to the copy-tree path.
var errInjectedRename = errors.New("injected rename failure")

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

// lessonFiles is the four file names seedLesson writes.
var lessonFiles = []string{"01 - L.mp4", "01 - L.nfo", "01 - L-poster.jpg", "01 - L.en.vtt"}

// TestMoveToLibraryRenames proves a successful move leaves nothing in downloads,
// every file present in the library at the same relative path, and content
// intact.
func TestMoveToLibraryRenames(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib") // same tmp fs

	newDir, err := moveToLibrary(downloadsDir, libraryDir, lessonDir)
	if err != nil {
		t.Fatalf("moveToLibrary: %v", err)
	}

	wantDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	if newDir != wantDir {
		t.Errorf("newDir = %q, want %q", newDir, wantDir)
	}
	// The scratch lesson dir is gone from downloads.
	if _, err := os.Stat(lessonDir); !os.IsNotExist(err) {
		t.Errorf("source lesson dir still present (stat err = %v), want removed", err)
	}
	// Every file is present in the library with its content intact.
	want := map[string]string{
		"01 - L.mp4":        "video-bytes",
		"01 - L.nfo":        "<nfo/>",
		"01 - L-poster.jpg": "poster-bytes",
		"01 - L.en.vtt":     "WEBVTT",
	}
	for _, name := range lessonFiles {
		got, err := os.ReadFile(filepath.Join(newDir, name))
		if err != nil {
			t.Errorf("missing moved file %s: %v", name, err)
			continue
		}
		if string(got) != want[name] {
			t.Errorf("%s content = %q, want %q", name, got, want[name])
		}
	}
}

// TestMoveToLibraryCrossFsFallback proves that when os.Rename fails (simulating a
// cross-filesystem move), moveToLibrary copies the tree into the library AND
// removes the source.
func TestMoveToLibraryCrossFsFallback(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")

	// Force the rename to fail so the copy-tree fallback runs.
	orig := rename
	rename = func(oldpath, newpath string) error { return errInjectedRename }
	t.Cleanup(func() { rename = orig })

	newDir, err := moveToLibrary(downloadsDir, libraryDir, lessonDir)
	if err != nil {
		t.Fatalf("moveToLibrary: %v", err)
	}

	// Files copied into the library, content intact.
	want := map[string]string{
		"01 - L.mp4":        "video-bytes",
		"01 - L.nfo":        "<nfo/>",
		"01 - L-poster.jpg": "poster-bytes",
		"01 - L.en.vtt":     "WEBVTT",
	}
	for _, name := range lessonFiles {
		got, err := os.ReadFile(filepath.Join(newDir, name))
		if err != nil {
			t.Errorf("missing copied file %s: %v", name, err)
			continue
		}
		if string(got) != want[name] {
			t.Errorf("%s content = %q, want %q", name, got, want[name])
		}
	}
	// Source removed after the copy.
	if _, err := os.Stat(lessonDir); !os.IsNotExist(err) {
		t.Errorf("source lesson dir still present after copy fallback (stat err = %v), want removed", err)
	}
}

// TestMoveToLibraryDestinationExistsReplaced proves a destination left over from
// a prior download is replaced by the new move (re-download semantics).
func TestMoveToLibraryDestinationExistsReplaced(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")

	// Pre-seed a stale destination with a sentinel file the new move must drop.
	dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir stale dst: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	newDir, err := moveToLibrary(downloadsDir, libraryDir, lessonDir)
	if err != nil {
		t.Fatalf("moveToLibrary: %v", err)
	}
	// The stale file is gone (destination was replaced, not merged).
	if _, err := os.Stat(filepath.Join(newDir, "stale.txt")); !os.IsNotExist(err) {
		t.Errorf("stale file survived the replace (stat err = %v), want removed", err)
	}
	// The fresh files are present.
	if _, err := os.Stat(filepath.Join(newDir, "01 - L.mp4")); err != nil {
		t.Errorf("fresh video missing after replace: %v", err)
	}
}

// TestMoveToLibraryDestEqualsSourceIsNoOp proves that when the library dir equals
// the downloads dir (destination resolves to the source) the lesson is preserved:
// moveToLibrary returns the source dir as a no-op success and every file is intact.
// WITHOUT the dest==source guard the RemoveAll(dstDir) deletes the source before
// the rename, then both the rename and the copy fall-back fail and the lesson is
// permanently lost while the worker still marks the job done.
func TestMoveToLibraryDestEqualsSourceIsNoOp(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	// Library == downloads: dstDir resolves to lessonDir.
	libraryDir := downloadsDir

	newDir, err := moveToLibrary(downloadsDir, libraryDir, lessonDir)
	if err != nil {
		t.Fatalf("moveToLibrary: %v", err)
	}
	if filepath.Clean(newDir) != filepath.Clean(lessonDir) {
		t.Errorf("newDir = %q, want the source dir %q (no-op)", newDir, lessonDir)
	}
	// The lesson and every file survive intact — nothing was deleted.
	want := map[string]string{
		"01 - L.mp4":        "video-bytes",
		"01 - L.nfo":        "<nfo/>",
		"01 - L-poster.jpg": "poster-bytes",
		"01 - L.en.vtt":     "WEBVTT",
	}
	for _, name := range lessonFiles {
		got, err := os.ReadFile(filepath.Join(lessonDir, name))
		if err != nil {
			t.Errorf("file %s lost despite no-op: %v", name, err)
			continue
		}
		if string(got) != want[name] {
			t.Errorf("%s content = %q, want %q", name, got, want[name])
		}
	}
}

// TestMoveToLibraryRejectsOutsideRoot proves a lessonDir not under downloadsDir
// (and the root-equal case) is rejected with an error and writes nothing.
func TestMoveToLibraryRejectsOutsideRoot(t *testing.T) {
	tmp := t.TempDir()
	downloadsDir := filepath.Join(tmp, "dl")
	libraryDir := filepath.Join(tmp, "lib")

	// A lesson dir that is NOT under downloadsDir.
	outside := filepath.Join(tmp, "elsewhere", "01 - L")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "01 - L.mp4"), []byte("v"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := moveToLibrary(downloadsDir, libraryDir, outside); err == nil {
		t.Fatal("moveToLibrary(outside-root) = nil, want an error")
	}
	// The root-equal case (lessonDir == downloadsDir, rel ".") must also reject.
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if _, err := moveToLibrary(downloadsDir, libraryDir, downloadsDir); err == nil {
		t.Fatal("moveToLibrary(root-equal) = nil, want an error")
	}
	// And no library was created by either rejection.
	if _, err := os.Stat(libraryDir); !os.IsNotExist(err) {
		t.Errorf("library dir created despite rejection (stat err = %v)", err)
	}
}
