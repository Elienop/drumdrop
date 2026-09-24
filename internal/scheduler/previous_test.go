package scheduler

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// TestDownloadTreeMissing pins what "the download brings back every file in
// the previous folder" means (owner ruling 2026-09-24 (e)): the same path, of
// the same kind, with a top-level file named after the old lesson folder
// matched to the one named after the new one; anything else, a symlink
// included, is not brought back.
func TestDownloadTreeMissing(t *testing.T) {
	download := map[string]string{"05 - New.mp4": "v", "resources/a.pdf": "a", "resources/deep/x.pdf": "x"}
	cases := []struct {
		name string
		old  map[string]string
		// into and the bases, as previousStays passes them.
		into, oldBase string
		link          string // a symlink planted at this path of old
		dir           string // an empty folder planted at this path of old
		want          string
	}{
		{name: "all brought back, renamed", old: map[string]string{"05 - Old.mp4": "", "resources/a.pdf": "", "resources/deep/x.pdf": ""}, oldBase: "05 - Old"},
		{name: "a subset", old: map[string]string{"resources/deep/x.pdf": ""}, oldBase: "05 - Old"},
		{name: "an owner file", old: map[string]string{"05 - Old.mp4": "", "notes.txt": ""}, oldBase: "05 - Old", want: "notes.txt"},
		{name: "a missed resource", old: map[string]string{"resources/b.pdf": ""}, oldBase: "05 - Old", want: "resources/b.pdf"},
		{name: "a nested file not renamed", old: map[string]string{"resources/05 - Old.pdf": ""}, oldBase: "05 - Old", want: "resources/05 - Old.pdf"},
		{name: "a file where a folder is placed", old: map[string]string{"resources/deep": ""}, oldBase: "05 - Old", want: "resources/deep"},
		{name: "a folder where a file is placed", dir: "05 - Old.mp4", oldBase: "05 - Old", want: "05 - Old.mp4"},
		{name: "an empty folder the download has not", dir: "extra", oldBase: "05 - Old", want: "extra"},
		{name: "a symlink", link: "resources/a.pdf", oldBase: "05 - Old", want: "resources/a.pdf"},
		{name: "a plex-tv folder", old: map[string]string{"a.pdf": "", "deep/x.pdf": ""}, into: "resources"},
		{name: "a plex-tv folder with more", old: map[string]string{"a.pdf": "", "b.pdf": ""}, into: "resources", want: "b.pdf"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmp := t.TempDir()
			src, old := filepath.Join(tmp, "src", "05 - New"), filepath.Join(tmp, "old")
			writeTree(t, src, download)
			if err := os.MkdirAll(old, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTree(t, old, c.old)
			if c.dir != "" {
				if err := os.MkdirAll(filepath.Join(old, c.dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if c.link != "" {
				if runtime.GOOS == "windows" {
					t.Skip("symlinks need privileges on Windows")
				}
				target := filepath.Join(tmp, "outside.pdf")
				writeTree(t, tmp, map[string]string{"outside.pdf": "outside"})
				p := filepath.Join(old, filepath.FromSlash(c.link))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			s, err := openScratch(filepath.Join(tmp, "src"), src)
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			tree, err := readDownloadTree(s.dir)
			if err != nil {
				t.Fatal(err)
			}
			r, err := os.OpenRoot(old)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			got, err := tree.missing(r, c.into, c.oldBase, s.base)
			if err != nil || got != c.want {
				t.Errorf("missing = %q, %v; want %q", got, err, c.want)
			}
		})
	}
}

// TestPlexFolderInto proves a plex-tv folder under an old episode name
// corresponds to the download's folder its name ends in, the longest one when
// two do, and to none when none does. The steps are in the order production
// lists them (planPlexTVMove sorts by name), where the shorter match comes
// last, so "the last match wins" fails here.
func TestPlexFolderInto(t *testing.T) {
	steps := []plexMoveStep{{name: "05 - X.mp4"}, {name: "extra resources", dir: true}, {name: "resources", dir: true}}
	if !sort.SliceIsSorted(steps, func(i, j int) bool { return steps[i].name < steps[j].name }) {
		t.Fatal("the steps are not in planPlexTVMove's order")
	}
	for name, want := range map[string]string{
		"Show - s01e05 - Old resources":       "resources",
		"Show - s01e05 - Old extra resources": "extra resources",
		"Show - s01e05 - Old.mp4":             "",
		"Show - s01e05 - Old sheets":          "",
	} {
		got, ok := plexFolderInto(steps)(name)
		if got != want || ok != (want != "") {
			t.Errorf("plexFolderInto(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
}

// TestPlacedAtIsByIdentity proves placedAt finds the step placing at a
// recorded entry spelled another way (here through a symlinked show folder,
// which any filesystem has), and none for an entry no step places at.
func TestPlacedAtIsByIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	season := filepath.Join(tmp, "Show", "Season 01")
	seedSeason(t, season, "Show - s01e05 - Five resources/", "Show - s01e05 - Old.mp4")
	if err := os.Symlink("Show", filepath.Join(tmp, "Alias")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(season, "Show - s01e05 - Five resources")
	steps := []plexMoveStep{{name: "resources", dst: dst, dir: true}}
	alias := filepath.Join(tmp, "Alias", "Season 01", "Show - s01e05 - Five resources")
	for p, want := range map[string]string{
		dst:   dst,
		alias: dst,
		filepath.Join(season, "Show - s01e05 - Old.mp4"): "",
		filepath.Join(season, "missing"):                 "",
	} {
		if got := placedAt(p, steps); got != want {
			t.Errorf("placedAt(%q) = %q, want %q", p, got, want)
		}
	}
}

// TestPreviousStaysKeepsWhatIsNotARealFolder proves the rule's answers for
// what is at a previous place: nothing there may "go" (there is nothing to
// set aside); a recorded plex-tv file goes (ruling #66); anything else that is
// not a real folder (a file or a symlink where a lesson folder was) stays.
func TestPreviousStaysKeepsWhatIsNotARealFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	writeTree(t, tmp, map[string]string{"Course/05 - File": "a file", "elsewhere/05 - Old/a.pdf": "a"})
	if err := os.Symlink(filepath.Join(tmp, "elsewhere", "05 - Old"), filepath.Join(tmp, "Course", "05 - Link")); err != nil {
		t.Fatal(err)
	}
	tree := downloadTree{"a.pdf": false}
	for _, c := range []struct {
		name      string
		fileIsOwn bool
		want      string
	}{
		{"05 - Missing", false, ""},
		{"05 - File", true, ""},
		{"05 - File", false, "it is not a real folder"},
		{"05 - Link", false, "it is not a real folder"},
		{"05 - Link", true, ""},
	} {
		h := heldPath{root: tmp, path: filepath.Join(tmp, "Course", c.name)}
		if got := tree.previousStays(h, lessonFolderInto, "", "", c.fileIsOwn); got != c.want {
			t.Errorf("previousStays(%s, fileIsOwn=%v) = %q, want %q", c.name, c.fileIsOwn, got, c.want)
		}
	}
}
