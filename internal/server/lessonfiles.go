package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// layoutPlexTV is the DRUMDROP_LAYOUT value (lower-cased) selecting Plex's
// TV-Shows layout, mirroring scheduler.LayoutPlexTV. It is duplicated here rather
// than imported to keep the server package free of a scheduler dependency.
const layoutPlexTV = "plex-tv"

// removeLessonFiles deletes a downloaded lesson's on-disk files. In the default
// layout outputDir is the single per-lesson location (a downloads or, post-move,
// a library folder) and the whole folder is removed. In the plex-tv layout
// outputDir is a SHARED season folder, so only the target episode's files are
// removed — the folder and any sibling episodes stay — keyed off videoPath's base.
//
// It refuses to touch anything that does not sit safely under downloadsDir OR
// libraryDir: it relativizes the path it would remove against each configured root
// and rejects a rel of ".", "..", a ".."-prefix, or an absolute result (which
// would mean the path is the root itself or escapes it). It errors only if NEITHER
// root accepts the path, guarding a catastrophic delete outside or at the top of
// either tree. A missing path (already gone) is tolerated.
func removeLessonFiles(layout, downloadsDir, libraryDir, outputDir, videoPath string) error {
	if layout == layoutPlexTV {
		return removeLessonFilesPlexTV(downloadsDir, libraryDir, videoPath)
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

// removeLessonFilesPlexTV removes ONLY the target episode's files from a shared
// Plex season folder: every file in filepath.Dir(videoPath) whose name starts with
// the episode base (videoPath's filename minus ".mp4") AND whose next rune is '.'
// or '-' — so "…s01e05" never matches a sibling "…s01e50" or "…s01e051". It NEVER
// removes the folder (other episodes live there). The season folder must be safely
// under downloadsDir or libraryDir (same guard as the default path); an empty
// videoPath (undownloaded / ResourcesOnly lesson) is a no-op.
func removeLessonFilesPlexTV(downloadsDir, libraryDir, videoPath string) error {
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

	base := strings.TrimSuffix(filepath.Base(videoPath), ".mp4")
	if base == "" || base == "." {
		// A degenerate videoPath with no real episode base: matching on an empty
		// prefix would sweep unrelated files. Refuse to guess; do nothing.
		return nil
	}
	entries, err := os.ReadDir(seasonDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // already gone: best-effort no-op
		}
		return fmt.Errorf("read season dir %q: %w", seasonDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, base) {
			continue
		}
		// The rune right after the base must be a sidecar/extension boundary ('.' or
		// '-') so e05 never matches e50/e051 or any longer episode-number sibling.
		rest := name[len(base):]
		if rest != "" && rest[0] != '.' && rest[0] != '-' {
			continue
		}
		if err := os.Remove(filepath.Join(seasonDir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove episode file %q: %w", name, err)
		}
	}
	return nil
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
