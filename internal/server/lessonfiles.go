package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elienop/drumdrop/internal/scheduler"
)

// removeLessonFiles deletes a downloaded lesson's on-disk files. What it removes
// depends on where the lesson was recorded, not on today's DRUMDROP_LAYOUT, so
// switching layouts can never turn a one-episode delete into a season wipe:
//   - a plex-tv season folder (scheduler.IsPlexSeasonDir), shared by every
//     episode of the show: only the entries the plex-tv move placed for this
//     episode are removed, and the folder stays (removePlexEpisodeFiles);
//   - anything else is the lesson's own folder (the default layout, or a
//     plex-tv lesson whose move failed and which stayed in downloads): the whole
//     folder is removed.
//
// It refuses to touch anything that does not sit safely under downloadsDir OR
// libraryDir: it relativizes the path it would remove against each configured root
// and rejects a rel of ".", "..", a ".."-prefix, or an absolute result (which
// would mean the path is the root itself or escapes it). It errors only if NEITHER
// root accepts the path, guarding a catastrophic delete outside or at the top of
// either tree. A missing path (already gone) is tolerated.
func removeLessonFiles(downloadsDir, libraryDir, outputDir, videoPath string) error {
	if scheduler.IsPlexSeasonDir(outputDir) || (videoPath != "" && scheduler.IsPlexSeasonDir(filepath.Dir(videoPath))) {
		return removePlexEpisodeFiles(downloadsDir, libraryDir, videoPath)
	}
	if outputDir == "" {
		return nil
	}
	if downloadsDir == "" {
		return fmt.Errorf("downloads dir is empty; refusing to remove %q", outputDir)
	}

	if !underRoot(downloadsDir, outputDir) && !underRoot(libraryDir, outputDir) {
		// outputDir is not a safe subpath of either configured root: it is the root
		// itself, escapes it, or is unrelated. Refuse rather than risk an os.RemoveAll
		// at/above a root that would wipe the whole tree.
		return fmt.Errorf("lesson dir %q is not safely under downloads dir %q or library dir %q", outputDir, downloadsDir, libraryDir)
	}

	// os.RemoveAll already treats a missing path as success, so an already-removed
	// dir is a no-op.
	if err := os.RemoveAll(outputDir); err != nil {
		return fmt.Errorf("remove lesson files %q: %w", outputDir, err)
	}
	return nil
}

// removePlexEpisodeFiles removes, from the shared season folder holding
// videoPath, every entry the plex-tv move placed for that episode (all version
// files, sidecars and subfolders), as recognised by scheduler.PlexEpisodeMatcher,
// the same predicate the move checks its own names against. A sibling episode's
// entries never match: "…s01e05" is not a prefix of "…s01e50", and a title that
// merely extends this one ("… Five Bonus") is not a suffix the move produces. It
// NEVER removes the season folder itself (other episodes live there).
//
// The season folder must be safely under downloadsDir or libraryDir (same guard
// as the default path). An empty videoPath (undownloaded / ResourcesOnly lesson)
// is a no-op: without the video there is no episode name to match on. A removal
// failure does not stop the rest; every failed path is reported.
func removePlexEpisodeFiles(downloadsDir, libraryDir, videoPath string) error {
	if videoPath == "" {
		return nil
	}
	seasonDir := filepath.Dir(videoPath)
	if downloadsDir == "" && libraryDir == "" {
		return fmt.Errorf("downloads and library dirs both empty; refusing to remove under %q", seasonDir)
	}
	if !underRoot(downloadsDir, seasonDir) && !underRoot(libraryDir, seasonDir) {
		return fmt.Errorf("episode dir %q is not safely under downloads dir %q or library dir %q", seasonDir, downloadsDir, libraryDir)
	}

	belongs, ok := scheduler.PlexEpisodeMatcher(videoPath)
	if !ok {
		// Not an episode of this show and season: matching on a guessed prefix
		// could sweep a sibling's files, so do nothing.
		return fmt.Errorf("video %q is not a plex-tv episode of its season folder; refusing to guess its files", videoPath)
	}
	entries, err := os.ReadDir(seasonDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // already gone: best-effort no-op
		}
		return fmt.Errorf("read season dir %q: %w", seasonDir, err)
	}
	var errs []error
	for _, e := range entries {
		if !belongs(e.Name(), e.IsDir()) {
			continue
		}
		// RemoveAll removes a folder with its contents, a file, or a symlink itself
		// (never its target), and treats an already-missing entry as success.
		p := filepath.Join(seasonDir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, fmt.Errorf("remove episode entry %q: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// underRoot reports whether path is a safe subpath strictly inside root: a
// non-empty root, with a filepath.Rel that is not ".", "..", a ".."-prefix, or
// absolute. An empty root (feature off) is never a valid root, and root == path
// (rel ".") is rejected so the catastrophic root-wipe is impossible.
func underRoot(root, path string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}
