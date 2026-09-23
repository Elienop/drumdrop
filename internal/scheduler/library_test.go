package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
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

// movePlex runs the plex-tv move with no lesson on record (no previous
// download, no other lesson), returning the season folder and the video.
func movePlex(libraryDir, show string, season, episode int, title, lessonDir string) (string, string, error) {
	c, err := library.NewClaims(libraryDir, nil)
	if err != nil {
		return "", "", err
	}
	res, err := moveToLibraryPlexTV(libraryDir, show, season, episode, title, lessonDir, plexLibrary{claims: c, downloads: filepath.Dir(lessonDir)})
	return res.seasonDir, res.videoPath, err
}

// lessonFiles is the four file names seedLesson writes.
var lessonFiles = []string{"01 - L.mp4", "01 - L.nfo", "01 - L-poster.jpg", "01 - L.en.vtt"}

// TestMoveToLibraryRenames proves a successful move leaves nothing in downloads,
// every file present in the library at the same relative path, and content
// intact.
func TestMoveToLibraryRenames(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib") // same tmp fs

	newDir, err := testMoveToLibrary(t, downloadsDir, libraryDir, lessonDir)
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
	stubRename(t, func(oldpath, newpath string) error { return errInjectedRename })

	newDir, err := testMoveToLibrary(t, downloadsDir, libraryDir, lessonDir)
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

	newDir, err := testMoveToLibrary(t, downloadsDir, libraryDir, lessonDir)
	if newDir != dstDir || err == nil || !strings.Contains(err.Error(), "which no lesson records") {
		t.Fatalf("moveToLibrary = (%q, %v), want %q and a note that an untracked leftover was replaced", newDir, err, dstDir)
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

	newDir, err := testMoveToLibrary(t, downloadsDir, libraryDir, lessonDir)
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

	if _, err := testMoveToLibrary(t, downloadsDir, libraryDir, outside); err == nil {
		t.Fatal("testMoveToLibrary(t, outside-root) = nil, want an error")
	}
	// The root-equal case (lessonDir == downloadsDir, rel ".") must also reject.
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		t.Fatalf("mkdir downloads: %v", err)
	}
	if _, err := testMoveToLibrary(t, downloadsDir, libraryDir, downloadsDir); err == nil {
		t.Fatal("testMoveToLibrary(t, root-equal) = nil, want an error")
	}
	// And no library was created by either rejection.
	if _, err := os.Stat(libraryDir); !os.IsNotExist(err) {
		t.Errorf("library dir created despite rejection (stat err = %v)", err)
	}
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

	seasonDir, videoPath, err := movePlex(libraryDir, "Beginner Course", 1, 5, "Lesson Five", lessonDir)
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

	seasonDir, videoPath, err := movePlex(libraryDir, "Beginner Course", 1, 5, "Lesson Five", lessonDir)
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
	if _, _, err := movePlex(libraryDir, "Show", 1, 5, "Five", s1); err != nil {
		t.Fatalf("move e05: %v", err)
	}
	s2 := mkScratch(6, "Six")
	season, _, err := movePlex(libraryDir, "Show", 1, 6, "Six", s2)
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

	seasonDir, videoPath, err := movePlex(libraryDir, "Songs", 1, 1, "Even Flow", lessonDir)
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

// TestMoveToLibraryCopyFailsPartWayLeavesNoPartialCopy covers D52's first case:
// in the default layout the copy fails part-way (the last file, in walk order,
// cannot be read). The move reports no library folder, the partial copy is gone
// from the library, and the whole lesson is still in downloads.
func TestMoveToLibraryCopyFailsPartWayLeavesNoPartialCopy(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
	forceCopyFallback(t)
	makeUnreadable(t, filepath.Join(lessonDir, "01 - L.nfo"))

	newDir, err := testMoveToLibrary(t, downloadsDir, libraryDir, lessonDir)
	if err == nil {
		t.Fatal("moveToLibrary = nil error, want the copy failure")
	}
	if newDir != "" {
		t.Errorf("newDir = %q, want \"\" (the lesson must be recorded in downloads)", newDir)
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

// TestMoveToLibrarySourceNotRemovableKeepsLibraryCopy covers D52's third case in
// the default layout: the copy is complete but the downloads folder cannot be
// removed. The move returns the library folder (so it is recorded), with an
// error that names the downloads leftover.
func TestMoveToLibrarySourceNotRemovableKeepsLibraryCopy(t *testing.T) {
	downloadsDir, lessonDir := seedLesson(t)
	libraryDir := filepath.Join(filepath.Dir(downloadsDir), "lib")
	forceCopyFallback(t)
	makeUndeletable(t, lessonDir)

	newDir, err := testMoveToLibrary(t, downloadsDir, libraryDir, lessonDir)
	wantDir := filepath.Join(libraryDir, "Inst", "Course", "01 - L")
	if newDir != wantDir {
		t.Errorf("newDir = %q, want the complete library copy %q", newDir, wantDir)
	}
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("leftover at %q", lessonDir)) {
		t.Errorf("err = %v, want an error naming the downloads leftover %q", err, lessonDir)
	}
	for _, name := range lessonFiles {
		if _, err := os.Stat(filepath.Join(wantDir, name)); err != nil {
			t.Errorf("library copy is missing %s: %v", name, err)
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

	gotSeason, videoPath, err := movePlex(filepath.Join(tmp, "lib"), "Songs", 1, 5, "Even Flow", lessonDir)
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
		return os.Rename(oldpath, newpath)
	})
	makeUnreadable(t, filepath.Join(lessonDir, "05 - Even Flow [Original].mp4"))

	gotSeason, _, err := movePlex(filepath.Join(tmp, "lib"), "Songs", 1, 5, "Even Flow", lessonDir)
	if err == nil || gotSeason != "" {
		t.Fatalf("moveToLibraryPlexTV = (%q, %v), want (\"\", the copy failure)", gotSeason, err)
	}
	assertNoEpisodeIn(t, seasonDir)
	assertScratchWhole(t, lessonDir)
}

// TestMoveToLibraryPlexTVSourceNotRemovableKeepsLibraryCopy covers D52's third
// case in plex-tv: every entry is copied, but the scratch folder cannot be fully
// removed (its resources/ is read-only). The move returns the season folder and
// the video (so they are recorded), every entry is in the library, and the
// error names the downloads leftover.
func TestMoveToLibraryPlexTVSourceNotRemovableKeepsLibraryCopy(t *testing.T) {
	tmp := t.TempDir()
	lessonDir, episodeBase, seasonDir := seedSongScratch(t, tmp)
	forceCopyFallback(t)
	makeUndeletable(t, filepath.Join(lessonDir, "resources"))

	gotSeason, videoPath, err := movePlex(filepath.Join(tmp, "lib"), "Songs", 1, 5, "Even Flow", lessonDir)
	if gotSeason != seasonDir {
		t.Errorf("seasonDir = %q, want %q (the complete library copy)", gotSeason, seasonDir)
	}
	if want := filepath.Join(seasonDir, episodeBase+" [Drumless].mp4"); videoPath != want {
		t.Errorf("videoPath = %q, want %q", videoPath, want)
	}
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("leftover at %q", lessonDir)) {
		t.Errorf("err = %v, want an error naming the downloads leftover %q", err, lessonDir)
	}
	for _, name := range []string{" [Drumless].mp4", " [Original].mp4", "-poster.jpg", ".nfo", " resources/song.pdf"} {
		if _, err := os.Stat(filepath.Join(seasonDir, episodeBase+name)); err != nil {
			t.Errorf("library copy is missing %s: %v", episodeBase+name, err)
		}
	}
}
