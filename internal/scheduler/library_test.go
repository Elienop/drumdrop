package scheduler

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errInjectedLink is the canned failure the copy-fallback test forces from the
// injected hardlink seam, so every file falls through to the byte copy.
var errInjectedLink = errors.New("injected link failure")

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

// sameFile reports whether a and b are the same inode (a true hardlink), used to
// distinguish a real link from an independent copy.
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		t.Fatalf("stat %s: %v", a, err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(ai, bi)
}

func TestMirrorToLibraryHardlinks(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib") // same tmp fs

	if err := mirrorToLibrary(downloadsDir, libraryDir, lessonDir, io.Discard); err != nil {
		t.Fatalf("mirrorToLibrary: %v", err)
	}

	dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	for _, name := range []string{"01 - L.mp4", "01 - L.nfo", "01 - L-poster.jpg", "01 - L.en.vtt"} {
		src := filepath.Join(lessonDir, name)
		dst := filepath.Join(dstDir, name)
		if _, err := os.Stat(dst); err != nil {
			t.Errorf("missing mirrored file %s: %v", name, err)
			continue
		}
		if !sameFile(t, src, dst) {
			t.Errorf("%s is not a hardlink (os.SameFile=false), want a true link", name)
		}
	}
}

func TestMirrorToLibraryVideoLast(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")

	// Record the order of destination links to assert the .mp4 is linked LAST.
	var order []string
	orig := hardlink
	hardlink = func(oldname, newname string) error {
		order = append(order, filepath.Base(newname))
		return orig(oldname, newname)
	}
	t.Cleanup(func() { hardlink = orig })

	if err := mirrorToLibrary(downloadsDir, libraryDir, lessonDir, io.Discard); err != nil {
		t.Fatalf("mirrorToLibrary: %v", err)
	}

	if len(order) == 0 {
		t.Fatal("no links recorded")
	}
	last := order[len(order)-1]
	if !strings.HasSuffix(strings.ToLower(last), ".mp4") {
		t.Errorf("last linked file = %q, want the .mp4 (video must be linked last)", last)
	}
	// And no .mp4 may appear before the final position.
	for i, n := range order[:len(order)-1] {
		if strings.HasSuffix(strings.ToLower(n), ".mp4") {
			t.Errorf("video %q linked at position %d, want last only", n, i)
		}
	}
}

func TestMirrorToLibraryCopyFallback(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")

	// Force every link to fail so the byte-copy fallback runs for every file.
	orig := hardlink
	hardlink = func(oldname, newname string) error { return errInjectedLink }
	t.Cleanup(func() { hardlink = orig })

	if err := mirrorToLibrary(downloadsDir, libraryDir, lessonDir, io.Discard); err != nil {
		t.Fatalf("mirrorToLibrary: %v", err)
	}

	dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	for _, name := range []string{"01 - L.mp4", "01 - L.nfo", "01 - L-poster.jpg", "01 - L.en.vtt"} {
		src := filepath.Join(lessonDir, name)
		dst := filepath.Join(dstDir, name)
		// Present and content-equal …
		want, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read src %s: %v", name, err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Errorf("missing copied file %s: %v", name, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%s content = %q, want %q", name, got, want)
		}
		// … but an INDEPENDENT copy, not a hardlink.
		if sameFile(t, src, dst) {
			t.Errorf("%s is os.SameFile, want an independent copy on the link-fail fallback", name)
		}
	}
}

func TestMirrorToLibraryIdempotent(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")

	if err := mirrorToLibrary(downloadsDir, libraryDir, lessonDir, io.Discard); err != nil {
		t.Fatalf("first mirror: %v", err)
	}

	// The second run must make no links (every dst is already the same inode) and
	// return no error.
	links := 0
	orig := hardlink
	hardlink = func(oldname, newname string) error {
		links++
		return orig(oldname, newname)
	}
	t.Cleanup(func() { hardlink = orig })

	if err := mirrorToLibrary(downloadsDir, libraryDir, lessonDir, io.Discard); err != nil {
		t.Fatalf("second mirror: %v", err)
	}
	if links != 0 {
		t.Errorf("second mirror made %d links, want 0 (idempotent skip on os.SameFile)", links)
	}
	// Still genuine hardlinks after the no-op second run.
	dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	src := filepath.Join(lessonDir, "01 - L.mp4")
	dst := filepath.Join(dstDir, "01 - L.mp4")
	if !sameFile(t, src, dst) {
		t.Errorf(".mp4 no longer a hardlink after the idempotent re-run")
	}
}

func TestMirrorToLibraryRejectsOutsideRoot(t *testing.T) {
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

	if err := mirrorToLibrary(downloadsDir, libraryDir, outside, io.Discard); err == nil {
		t.Fatal("mirrorToLibrary(outside-root) = nil, want an error")
	}
	// And it must not have written anything into the library.
	if _, err := os.Stat(libraryDir); !os.IsNotExist(err) {
		t.Errorf("library dir created despite rejection (stat err = %v)", err)
	}
}

// TestMirrorToLibrarySkipsPartialsAndSubdirs proves leftover partial artifacts
// and any subdirectory in the lesson folder are not mirrored.
func TestMirrorToLibrarySkipsPartialsAndSubdirs(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")

	// A leftover partial and a fragment, plus a nested subdir with a file.
	for _, name := range []string{"01 - L.mp4.part", "01 - L.f137.mp4", "01 - L.ytdl"} {
		if err := os.WriteFile(filepath.Join(lessonDir, name), []byte("junk"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	sub := filepath.Join(lessonDir, "extra")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write sub file: %v", err)
	}

	if err := mirrorToLibrary(downloadsDir, libraryDir, lessonDir, io.Discard); err != nil {
		t.Fatalf("mirrorToLibrary: %v", err)
	}

	dstDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	for _, name := range []string{"01 - L.mp4.part", "01 - L.f137.mp4", "01 - L.ytdl", "extra"} {
		if _, err := os.Stat(filepath.Join(dstDir, name)); !os.IsNotExist(err) {
			t.Errorf("partial/subdir %q was mirrored (stat err = %v), want skipped", name, err)
		}
	}
}
