package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
)

// LayoutPlexTV is the DRUMDROP_LAYOUT value (lower-cased) that selects Plex's
// TV-Shows library layout for the move target. Any other value (including "" and
// "default") keeps the per-lesson-subfolder layout.
const LayoutPlexTV = "plex-tv"

// rename is the move primitive moveToLibrary uses, isolated behind a package var
// so a test can force the cross-filesystem copy fallback. It defaults to
// os.Rename (which exists on every release target, Windows included); the
// copy-tree fallback on ANY rename error means no Unix-only errno is ever
// referenced, so the cross-platform builds stay green.
var rename = os.Rename

// moveToLibrary moves lessonDir into libraryDir at lessonDir's path relative to
// downloadsDir, returning the new (library) dir. It tries a rename first
// (instant and atomic on the same filesystem); on a cross-filesystem rename
// error it falls back to copying the tree then removing the source. The
// downloads folder is gone after a successful move.
//
// Whatever fails, one complete copy of the lesson is left in one place, and
// newDir says where to record it:
//   - newDir == "": the lesson is whole in lessonDir. A copy that failed
//     part-way is removed from the library first.
//   - newDir != "": the library holds the whole lesson. An error alongside it
//     means lessonDir could not be fully removed; the message names the leftover.
//
// A copied lesson is flushed to disk (every file and folder) before the
// downloads copy is removed, so a crash right after can not leave the only copy
// truncated. A library leftover that cannot be removed is named in the error.
//
// It rejects a lessonDir that is not under downloadsDir (rel ".", "..", an
// absolute Rel result) before any write, so a stray path can never land outside
// the library. Everything it creates or removes in the library goes through
// os.Root: the destination's parent must resolve inside the library (a
// symlinked course folder is refused, not followed), and an existing
// destination (a re-download) is removed there so the move replaces it. It
// returns the new dir and an error for the caller to LOG — the caller treats
// the move as non-fatal and must never fail the job on it.
func moveToLibrary(downloadsDir, libraryDir, lessonDir string) (newDir string, err error) {
	// Move to the same path relative to the downloads root. Reject a lessonDir
	// that escapes the root (".." prefix or an absolute Rel result) before any
	// write, so a stray path can never land outside the library.
	rel, err := filepath.Rel(downloadsDir, lessonDir)
	if err != nil {
		return "", fmt.Errorf("relativize %q under %q: %w", lessonDir, downloadsDir, err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("lesson dir %q is not under downloads dir %q", lessonDir, downloadsDir)
	}
	dstDir := filepath.Join(libraryDir, rel)

	// Destination == source (e.g. libraryDir == downloadsDir, spelled the same or
	// reached another way: a symlink, the same host folder bind-mounted twice):
	// the lesson is already where it would be moved to. Return it as a no-op
	// success — the file stays put. WITHOUT this guard the removal below would
	// delete the source before the rename, then both the rename and the copy
	// fall-back fail against a now-missing source and the lesson is permanently
	// lost. Identity (os.SameFile) decides, not spelling.
	if filepath.Clean(dstDir) == filepath.Clean(lessonDir) || sameDir(dstDir, lessonDir) {
		return dstDir, nil
	}

	// Open the destination's PARENT inside the library (created if missing), so
	// the rename moves the whole lesson folder in as the leaf, and nothing is
	// created or removed through a symlink leading out of the library.
	parent, closeParent, err := openLibraryParent(libraryDir, filepath.Dir(rel))
	if err != nil {
		return "", err
	}
	defer closeParent()
	leaf := filepath.Base(rel)
	if err := parent.RemoveAll(leaf); err != nil {
		return "", fmt.Errorf("remove existing library dir %q: %w", dstDir, err)
	}

	if rerr := rename(lessonDir, dstDir); rerr == nil {
		return dstDir, nil
	} else if cerr := copyTreeInto(parent, leaf, lessonDir); cerr != nil {
		// Cross-filesystem (or otherwise unrenamable): copy the tree, then drop the
		// source. The copy only read the source, so the whole lesson is still in
		// downloads; take the partial copy back out of the library.
		err := fmt.Errorf("copy tree %q -> %q (rename failed: %v): %w", lessonDir, dstDir, rerr, cerr)
		return "", errors.Join(err, discardPartialCopy(parent, leaf))
	}
	if err := syncIn(parent, "."); err != nil {
		err = fmt.Errorf("flush library folder %q after the copy: %w", filepath.Dir(dstDir), err)
		return "", errors.Join(err, discardPartialCopy(parent, leaf))
	}
	if rmerr := os.RemoveAll(lessonDir); rmerr != nil {
		return dstDir, downloadsLeftoverErr(lessonDir, rmerr)
	}
	return dstDir, nil
}

// openLibraryParent creates the folder rel inside libraryDir and opens it as
// an os.Root, refusing one that resolves outside the library (a symlinked
// folder on the way). closeFn releases both handles.
func openLibraryParent(libraryDir, rel string) (dir *os.Root, closeFn func(), err error) {
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create library folder %q: %w", libraryDir, err)
	}
	lib, err := os.OpenRoot(libraryDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open library folder %q: %w", libraryDir, err)
	}
	if err := lib.MkdirAll(rel, 0o755); err != nil {
		lib.Close()
		return nil, nil, fmt.Errorf("create %q inside the library %q: %w", rel, libraryDir, err)
	}
	dir, err = lib.OpenRoot(rel)
	if err != nil {
		lib.Close()
		return nil, nil, fmt.Errorf("open %q inside the library %q: %w", rel, libraryDir, err)
	}
	return dir, func() { dir.Close(); lib.Close() }, nil
}

// sameDir reports whether a and b both exist and are the same folder, however
// each is spelled.
func sameDir(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// CheckLibraryDir refuses a library folder that IS the downloads folder under
// another path (a symlink, or one host folder bind-mounted twice): the move
// would then delete a lesson's only copy while replacing it. The same path
// spelled the same way is allowed (every move is then a no-op), as is a library
// that does not exist yet.
func CheckLibraryDir(downloadsDir, libraryDir string) error {
	if downloadsDir == "" || libraryDir == "" || filepath.Clean(downloadsDir) == filepath.Clean(libraryDir) {
		return nil
	}
	if sameDir(downloadsDir, libraryDir) {
		return fmt.Errorf("DRUMDROP_LIBRARY_DIR %q is the downloads folder %q under another path; unset it, or give both the same path", libraryDir, downloadsDir)
	}
	return nil
}

// plexEpisodeBase is the flat episode base name of the plex-tv layout:
// "<Sanitize(show)> - s0Ne0M - <Sanitize(title)>". The move shortens it when a
// name would not fit (fitEpisodeBase) and reports the base it used, which the
// worker's episode-nfo write takes from the move's result.
func plexEpisodeBase(show, title string, season, episode int) string {
	return fmt.Sprintf("%s%02d - %s", library.EpisodePrefix(musora.Sanitize(show), season), episode, musora.Sanitize(title))
}

// isLessonVideoName reports whether name is a video file DownloadLesson produces
// for the lesson whose folder/file base is base: exactly "<base>.mp4" (regular
// lesson) or "<base> [Label].mp4" (a song version file). It deliberately rejects
// yt-dlp fragment files ("<base> [..].fNNN.mp4") and any unrelated "<base> X.mp4".
func isLessonVideoName(name, base string) bool {
	rem := strings.TrimPrefix(name, base)
	if rem == name {
		return false // base wasn't a prefix
	}
	return rem == ".mp4" || (strings.HasPrefix(rem, " [") && strings.HasSuffix(rem, "].mp4"))
}
