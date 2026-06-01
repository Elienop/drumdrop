package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// removeLessonFiles deletes a downloaded lesson's on-disk files at outputDir, the
// single location stored in lessons.output_dir. When a library is configured the
// worker MOVES finished lessons into it, so output_dir is the library path; with
// no library it is the downloads path. There is only ever one copy.
//
// It refuses to touch anything that does not sit safely under downloadsDir OR
// libraryDir: it relativizes outputDir against each configured root and rejects a
// rel of ".", "..", a ".."-prefix, or an absolute result (which would mean
// outputDir is the root itself or escapes it). It errors only if NEITHER root
// accepts outputDir, guarding a catastrophic RemoveAll outside or at the top of
// either tree. A missing path (already gone) is tolerated — os.RemoveAll treats
// it as success.
func removeLessonFiles(downloadsDir, libraryDir, outputDir string) error {
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
