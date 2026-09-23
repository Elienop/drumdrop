package musora

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A symlink planted at the name a lesson's nfo (or any file DownloadLesson
// writes itself) goes to is replaced, never written through: neither a file
// outside the downloads folder nor another file inside it changes.
func TestWriteInRootNeverWritesThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := filepath.Join(t.TempDir(), "precious.nfo")
	if err := os.WriteFile(outside, []byte("PRECIOUS OUTSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	downloads := t.TempDir()
	inside := filepath.Join(downloads, "other.nfo")
	if err := os.WriteFile(inside, []byte("PRECIOUS INSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(downloads, "05 - Five"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(downloads)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	for name, target := range map[string]string{"out.nfo": outside, "in.nfo": inside} {
		link := filepath.Join(downloads, "05 - Five", name)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := writeInRoot(root, filepath.Join("05 - Five", name), strings.NewReader("NEW")); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		info, err := os.Lstat(link)
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("%s is not a regular file after the write (%v, %v)", name, info, err)
		}
		if b, _ := os.ReadFile(link); string(b) != "NEW" {
			t.Errorf("%s = %q, want NEW", name, b)
		}
	}
	for _, p := range []string{outside, inside} {
		if b, _ := os.ReadFile(p); !strings.HasPrefix(string(b), "PRECIOUS") {
			t.Errorf("%s was written through a symlink: %q", p, b)
		}
	}
	if _, err := os.Lstat(filepath.Join(downloads, "05 - Five", "in.nfo"+tmpSuffix)); !os.IsNotExist(err) {
		t.Errorf("the temporary file is left behind: %v", err)
	}
}

// A lesson folder that is a symlink leading out of the downloads folder is
// refused: nothing is written outside it.
func TestDownloadLessonRefusesAFolderLeadingOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := t.TempDir()
	downloads := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(downloads, "Course")); err != nil {
		t.Fatal(err)
	}
	l := &Lesson{ID: 5, Title: "Five"}
	err := DownloadLesson(t.Context(), l, DownloadOpts{
		Dir: filepath.Join(downloads, "Course"), Root: downloads, Index: 5, ResourcesOnly: true,
	})
	if err == nil {
		t.Fatal("DownloadLesson through a symlinked folder leading out succeeded")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Errorf("wrote %d entries outside the downloads folder", len(entries))
	}
}

// A Dir that is not inside Root is refused before anything is written.
func TestDownloadLessonRefusesADirOutsideRoot(t *testing.T) {
	downloads := t.TempDir()
	elsewhere := t.TempDir()
	err := DownloadLesson(t.Context(), &Lesson{ID: 5, Title: "Five"}, DownloadOpts{
		Dir: elsewhere, Root: downloads, Index: 5, ResourcesOnly: true,
	})
	if err == nil || !strings.Contains(err.Error(), "not inside") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote %d entries outside the root", len(entries))
	}
}
