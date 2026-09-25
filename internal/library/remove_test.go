package library

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRemoveConfinesTheRemoval proves the removal can not leave the root it
// was given: through a symlinked folder, outside every root, or at a root
// itself it refuses; a symlink that is itself the entry goes, its target
// stays; a missing path is already gone.
func TestRemoveConfinesTheRemoval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	outside := filepath.Join(tmp, "outside")
	seedSeason(t, outside, "precious.nfo", "target/")
	if err := os.MkdirAll(filepath.Join(lib, "Show"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(lib, "Show", "Season 01")); err != nil {
		t.Fatal(err)
	}
	roots := []string{lib}

	if err := Remove(roots, filepath.Join(lib, "Show", "Season 01", "precious.nfo")); err == nil {
		t.Error("removed through a symlinked season folder, want a refusal")
	}
	assertExist(t, true, filepath.Join(outside, "precious.nfo"))
	for _, p := range []string{outside, filepath.Join(outside, "precious.nfo"), lib, filepath.Join(lib, ".."), ""} {
		if err := Remove(roots, p); err == nil {
			t.Errorf("Remove(%q) = nil, want a refusal", p)
		}
	}
	// A root itself is refused by name. (os.Root would refuse "." too, but only
	// as a bare "invalid argument" that names nothing.)
	if err := Remove(roots, lib); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Errorf("Remove(root) = %v, want the named refusal", err)
	}
	if err := Remove(nil, filepath.Join(lib, "Show")); err == nil {
		t.Error("removed with no root, want a refusal")
	}
	if err := Remove(roots, filepath.Join(lib, "Show", "missing.mp4")); err != nil {
		t.Errorf("missing path = %v, want nil", err)
	}
	// The symlink entry itself is removed; what it points at stays.
	if err := Remove(roots, filepath.Join(lib, "Show", "Season 01")); err != nil {
		t.Fatalf("remove the link: %v", err)
	}
	assertExist(t, false, filepath.Join(lib, "Show", "Season 01"))
	assertExist(t, true, filepath.Join(outside, "precious.nfo"), filepath.Join(outside, "target", "f.pdf"))
	// The second root is used when the first does not hold the path.
	dl := filepath.Join(tmp, "dl")
	seedSeason(t, dl, "05 - Lesson/")
	if err := Remove([]string{lib, dl}, filepath.Join(dl, "05 - Lesson")); err != nil {
		t.Fatalf("remove under the second root: %v", err)
	}
	assertExist(t, false, filepath.Join(dl, "05 - Lesson"))
}

// TestRemoveUsesTheLongestRoot covers nested roots: a library symlinked into
// place inside downloads. The path is removed through the root nearest to it,
// whichever order the roots come in, so the worker and the server (which list
// them in opposite orders) agree.
func TestRemoveUsesTheLongestRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	dl := filepath.Join(tmp, "dl")
	real := filepath.Join(tmp, "real-lib")
	seedSeason(t, filepath.Join(real, "Show", "Season 01"), "x.mp4")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(dl, "lib")
	if err := os.Symlink(real, lib); err != nil {
		t.Fatal(err)
	}
	for _, roots := range [][]string{{dl, lib}, {lib, dl}} {
		seedSeason(t, filepath.Join(real, "Show", "Season 01"), "x.mp4")
		if err := Remove(roots, filepath.Join(lib, "Show", "Season 01", "x.mp4")); err != nil {
			t.Errorf("roots %v: %v", roots, err)
		}
		assertExist(t, false, filepath.Join(real, "Show", "Season 01", "x.mp4"))
	}
}

// TestRemoveRefusesARootInsideAnotherRoot covers a library folder inside the
// downloads folder: removing the library folder itself is refused by name,
// whichever order the roots come in, and not read as an entry of the outer
// root, which would remove the whole library. A missing one is refused too,
// never reported as removed.
func TestRemoveRefusesARootInsideAnotherRoot(t *testing.T) {
	tmp := t.TempDir()
	dl := filepath.Join(tmp, "dl")
	lib := filepath.Join(dl, "lib")
	seedSeason(t, filepath.Join(lib, "Show", "Season 01"), "x.mp4")
	gone := filepath.Join(dl, "gone")
	for _, roots := range [][]string{{dl, lib, gone}, {gone, lib, dl}} {
		for _, root := range []string{lib, gone} {
			if err := Remove(roots, root); err == nil || !strings.Contains(err.Error(), "itself") {
				t.Errorf("roots %v: Remove(%q) = %v, want the named refusal", roots, root, err)
			}
		}
		assertExist(t, true, filepath.Join(lib, "Show", "Season 01", "x.mp4"))
	}
}

// TestRemoveFindsTheRootUnderAnotherSpelling covers a record or a folder
// written under another spelling of a root (a symlink to it, a second mount):
// it is removed through the real root, by identity; a path outside every
// root is refused, missing or not.
func TestRemoveFindsTheRootUnderAnotherSpelling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	alias := filepath.Join(tmp, "alias")
	seedSeason(t, filepath.Join(lib, "F"), "05 - Lesson/")
	if err := os.Symlink(lib, alias); err != nil {
		t.Fatal(err)
	}
	if err := Remove([]string{lib}, filepath.Join(alias, "F", "05 - Lesson")); err != nil {
		t.Fatalf("remove under an alias of the root: %v", err)
	}
	assertExist(t, false, filepath.Join(lib, "F", "05 - Lesson"))
	// Refused by name, as the root itself is (TestRemoveConfinesTheRemoval),
	// not only by os.Root's bare "invalid argument" for ".".
	if err := Remove([]string{lib}, alias); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Errorf("Remove(an alias of the root) = %v, want the named refusal", err)
	}
	assertExist(t, true, lib)

	// A missing path outside every root may be a folder that moved with a
	// library mounted elsewhere since (security review round 3, M1): it is
	// refused, never reported as removed.
	if err := Remove([]string{lib}, filepath.Join(tmp, "moved-away", "F", "05 - Old")); err == nil || !strings.Contains(err.Error(), "not reported as removed") {
		t.Errorf("missing path outside every root = %v, want the named refusal", err)
	}
	seedSeason(t, filepath.Join(tmp, "elsewhere"), "keep.txt")
	if err := Remove([]string{lib}, filepath.Join(tmp, "elsewhere", "keep.txt")); err == nil {
		t.Error("removed an existing path outside every root, want a refusal")
	}
	assertExist(t, true, filepath.Join(tmp, "elsewhere", "keep.txt"))
}

// TestRemoveReadsRelativePathsFromTheWorkingDirectory proves a relative root
// or path (DRUMDROP_DOWNLOADS_DIR's ./downloads default, stored as written)
// means what the OS reads it as.
func TestRemoveReadsRelativePathsFromTheWorkingDirectory(t *testing.T) {
	tmp := t.TempDir()
	seedSeason(t, filepath.Join(tmp, "downloads", "F"), "05 - A/", "06 - B/")
	t.Chdir(tmp)
	if err := Remove([]string{filepath.Join(tmp, "downloads")}, filepath.Join("downloads", "F", "05 - A")); err != nil {
		t.Fatalf("relative path, absolute root: %v", err)
	}
	if err := Remove([]string{"downloads"}, filepath.Join(tmp, "downloads", "F", "06 - B")); err != nil {
		t.Fatalf("absolute path, relative root: %v", err)
	}
	assertExist(t, false, filepath.Join(tmp, "downloads", "F", "05 - A"), filepath.Join(tmp, "downloads", "F", "06 - B"))
}
