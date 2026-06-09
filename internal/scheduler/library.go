package scheduler

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
// downloadsDir, returning the new (library) dir. It tries os.Rename first
// (instant and atomic on the same filesystem); on a cross-filesystem rename
// error it falls back to copying the tree then removing the source. The
// downloads folder is gone after a successful move.
//
// It rejects a lessonDir that is not under downloadsDir (rel ".", "..", an
// absolute Rel result) before any write, so a stray path can never land outside
// the library. The destination parent is created; an existing destination (a
// re-download) is removed first so the move replaces it. It returns the new dir
// and an error for the caller to LOG — the caller treats the move as non-fatal
// and must never fail the job on it.
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

	// Destination == source (e.g. libraryDir == downloadsDir): the lesson is
	// already where it would be moved to. Return it as a no-op success — the file
	// stays put. WITHOUT this guard the RemoveAll(dstDir) below would delete the
	// source before the rename, then both the rename and the copy fall-back fail
	// against a now-missing source and the lesson is permanently lost.
	if filepath.Clean(dstDir) == filepath.Clean(lessonDir) {
		return dstDir, nil
	}

	// Create the destination's PARENT (not dstDir itself) so the rename moves the
	// whole lesson folder in as the leaf. A pre-existing destination (re-download)
	// is removed so the move replaces it rather than failing or nesting.
	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		return "", fmt.Errorf("create library parent %q: %w", filepath.Dir(dstDir), err)
	}
	if err := os.RemoveAll(dstDir); err != nil {
		return "", fmt.Errorf("remove existing library dir %q: %w", dstDir, err)
	}

	if rerr := rename(lessonDir, dstDir); rerr == nil {
		return dstDir, nil
	} else if cerr := copyTree(lessonDir, dstDir); cerr != nil {
		// Cross-filesystem (or otherwise unrenamable): copy the tree, then drop the
		// source. A copy failure leaves the source in place (download still there).
		return "", fmt.Errorf("copy tree %q -> %q (rename failed: %v): %w", lessonDir, dstDir, rerr, cerr)
	}
	if rerr := os.RemoveAll(lessonDir); rerr != nil {
		// The library copy is complete; failing to drop the scratch source is a
		// warn-worthy leftover, not a lost file.
		return dstDir, fmt.Errorf("remove source after copy %q: %w", lessonDir, rerr)
	}
	return dstDir, nil
}

// plexEpisodeBase is the flat episode base name shared by the plex-tv move and the
// worker's episode-nfo write: "<Sanitize(show)> - s0Ne0M - <Sanitize(title)>". Both
// sites MUST agree on it so the nfo the worker writes lands at the exact path the
// move renamed the lesson's files to.
func plexEpisodeBase(show, title string, season, episode int) string {
	return fmt.Sprintf("%s - s%02de%02d - %s", musora.Sanitize(show), season, episode, musora.Sanitize(title))
}

// moveToLibraryPlexTV moves the finished lesson's files out of the scratch
// lessonDir into <libraryDir>/<Sanitize(show)>/Season 0N/, renaming each entry
// from its scratch "NN - Title" base to the episode base
// "<Sanitize(show)> - s0Ne0M - <Sanitize(title)>" while preserving the suffix
// (".mp4", ".en.vtt", ".nfo", "-poster.jpg", …). Files end up FLAT in the season
// folder, which is shared across the show's episodes. It returns the season dir
// and a moved episode .mp4 path (empty if no .mp4 was present, e.g.
// ResourcesOnly).
//
// A song produces two bracket-tagged video files ("<base> [Original].mp4" /
// "<base> [Drumless].mp4"); both keep the same episode base after renaming
// ("<episodeBase> [Original].mp4" …) so Plex merges them as ONE episode with two
// versions. Entries are processed in sorted name order so videoPath
// (the FIRST moved .mp4) is deterministic.
//
// Subdirectories are PRESERVED: each is moved into the season folder renamed
// "<episodeBase> <dirname>" (e.g. "<episodeBase> resources" for a song's PDF),
// so the resource folders survive into the library instead of being deleted with
// the scratch dir.
//
// Move semantics mirror moveToLibrary: try the rename seam first, fall back to a
// copy (copyFile for files, copyTree for directories) + remove-source on ANY
// rename error (cross-filesystem). Unlike moveToLibrary it composes the
// destination from show/season directly rather than from a downloads-relative
// path, but it still guards the scratch lessonDir: a "." / ".." / ".."-prefixed /
// absolute Base would be a malformed scratch path, so it refuses before any
// write. The error is for the caller to LOG; the move is non-fatal and must
// never fail the job.
func moveToLibraryPlexTV(libraryDir, show string, season, episode int, title, lessonDir string) (seasonDir, videoPath string, err error) {
	scratchBase := filepath.Base(lessonDir)
	// A malformed scratch base (root, escape, absolute) would make the per-file
	// TrimPrefix below meaningless and could read an unexpected dir; refuse it.
	if scratchBase == "." || scratchBase == ".." || strings.HasPrefix(scratchBase, ".."+string(filepath.Separator)) || filepath.IsAbs(scratchBase) {
		return "", "", fmt.Errorf("malformed scratch lesson dir %q", lessonDir)
	}

	episodeBase := plexEpisodeBase(show, title, season, episode)
	seasonDir = filepath.Join(libraryDir, musora.Sanitize(show), fmt.Sprintf("Season %02d", season))
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create season dir %q: %w", seasonDir, err)
	}

	entries, err := os.ReadDir(lessonDir)
	if err != nil {
		return "", "", fmt.Errorf("read scratch lesson dir %q: %w", lessonDir, err)
	}
	// Sort by name so the chosen videoPath (the first moved .mp4) is deterministic
	// across filesystems and across a song's multiple version files.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		name := e.Name()
		src := filepath.Join(lessonDir, name)

		if e.IsDir() {
			// Preserve the subdir (e.g. resources/ holding a song's PDF), renamed
			// "<episodeBase> <dirname>" so it survives into the library.
			dst := filepath.Join(seasonDir, episodeBase+" "+name)
			if rerr := rename(src, dst); rerr != nil {
				// Cross-filesystem (or otherwise unrenamable): copy the tree then drop
				// the source. A copy failure leaves the source in place.
				if cerr := copyTree(src, dst); cerr != nil {
					return "", "", fmt.Errorf("copy tree %q -> %q (rename failed: %v): %w", src, dst, rerr, cerr)
				}
				if rmerr := os.RemoveAll(src); rmerr != nil {
					return "", "", fmt.Errorf("remove source after copy %q: %w", src, rmerr)
				}
			}
			continue
		}

		info, ierr := e.Info()
		if ierr != nil {
			return "", "", fmt.Errorf("stat scratch file %q: %w", name, ierr)
		}
		if !info.Mode().IsRegular() {
			continue // skip symlinks/devices: drumdrop only produces regular files
		}
		// Reuse the scratch base, swapping it for the episode base so the suffix
		// (and thus the sidecar's role: .nfo/.vtt/-poster.jpg, or a song's
		// " [Original].mp4" version tag) is preserved.
		newName := episodeBase + strings.TrimPrefix(name, scratchBase)
		dst := filepath.Join(seasonDir, newName)

		if rerr := rename(src, dst); rerr != nil {
			// Cross-filesystem (or otherwise unrenamable): copy the single file then
			// drop the source. A copy failure leaves the source in place.
			if cerr := copyFile(src, dst, info.Mode()); cerr != nil {
				return "", "", fmt.Errorf("copy %q -> %q (rename failed: %v): %w", src, dst, rerr, cerr)
			}
			if rmerr := os.Remove(src); rmerr != nil {
				return "", "", fmt.Errorf("remove source after copy %q: %w", src, rmerr)
			}
		}
		// Any moved .mp4 (regular "<base>.mp4" or a song's "<base> [Tag].mp4") is a
		// candidate episode video; keep the FIRST in sorted order.
		if videoPath == "" && strings.HasPrefix(name, scratchBase) && strings.HasSuffix(newName, ".mp4") {
			videoPath = dst
		}
	}

	// Remove the now-emptied scratch lesson dir (best-effort: a leftover scratch
	// dir is a warn-worthy stray, not a lost file). Any nested non-regular content
	// we skipped above stays in the source, so RemoveAll cleans the whole leaf.
	if rmerr := os.RemoveAll(lessonDir); rmerr != nil {
		return seasonDir, videoPath, fmt.Errorf("remove emptied scratch dir %q: %w", lessonDir, rmerr)
	}
	return seasonDir, videoPath, nil
}

// copyTree recursively copies the file tree at src into dst, recreating
// directories and copying regular files with their mode. It is the
// cross-filesystem fallback for moveToLibrary when os.Rename cannot move the
// folder across devices. Non-regular entries (symlinks, devices) are skipped.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil // skip symlinks/devices: drumdrop only produces regular files
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
}

// copyFile writes src's bytes into dst with the given mode, replacing dst if it
// exists. It is the per-file primitive copyTree uses for the cross-filesystem
// move fallback.
func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst) // do not leave a half-written file behind
		return err
	}
	return out.Close()
}
