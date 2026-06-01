package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// removeLessonFiles deletes a downloaded lesson's on-disk files: the downloads
// copy (outputDir, the container path stored in lessons.output_dir) AND its
// library mirror, when a libraryDir is configured. The mirror path is computed
// exactly as scheduler.mirrorToLibrary created it —
// filepath.Join(libraryDir, rel(downloadsDir, outputDir)) — so the same folder
// that was hardlinked into the library is the one removed.
//
// It is best-effort: a missing path (already gone, never mirrored) is tolerated.
// It refuses to touch anything that does not sit under downloadsDir, reusing the
// same filepath.Rel + ".."/IsAbs escape guard as mirrorToLibrary, so a stray or
// malformed output_dir can never trigger an os.RemoveAll outside the roots. When
// libraryDir is empty only the downloads copy is removed.
//
// mirrorToLibrary is unexported in package scheduler, so the guard is
// re-implemented here rather than shared; the two must stay in agreement.
func removeLessonFiles(downloadsDir, libraryDir, outputDir string) error {
	if outputDir == "" {
		return nil
	}
	if downloadsDir == "" {
		return fmt.Errorf("downloads dir is empty; refusing to remove %q", outputDir)
	}

	rel, err := filepath.Rel(downloadsDir, outputDir)
	if err != nil {
		return fmt.Errorf("relativize %q under %q: %w", outputDir, downloadsDir, err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		// rel == "." means outputDir == downloadsDir: an os.RemoveAll there would
		// wipe the entire downloads (and library) root. output_dir is always a deep
		// lesson subpath in practice, but this guards a catastrophic delete if a
		// malformed/empty-derived path ever resolved to the root itself.
		return fmt.Errorf("lesson dir %q is not safely under downloads dir %q", outputDir, downloadsDir)
	}

	// os.RemoveAll already treats a missing path as success, so a never-mirrored
	// or already-removed dir is a no-op.
	if err := os.RemoveAll(outputDir); err != nil {
		return fmt.Errorf("remove downloads copy %q: %w", outputDir, err)
	}
	if libraryDir != "" {
		libPath := filepath.Join(libraryDir, rel)
		if err := os.RemoveAll(libPath); err != nil {
			return fmt.Errorf("remove library mirror %q: %w", libPath, err)
		}
	}
	return nil
}
