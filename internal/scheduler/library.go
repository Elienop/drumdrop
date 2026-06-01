package scheduler

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

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
