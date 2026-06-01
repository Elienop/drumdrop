package scheduler

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// hardlink is the link primitive mirrorToLibrary uses, isolated behind a package
// var so a test can record link order or force the copy fallback. It defaults to
// os.Link (which exists on every release target, Windows included); the
// copy-fallback on ANY link error means no Unix-only errno is ever referenced, so
// the cross-platform builds stay green.
var hardlink = os.Link

// mirrorToLibrary hardlinks (with a byte-copy fallback) every regular file in
// lessonDir into libraryDir at lessonDir's path relative to downloadsDir, linking
// the video file(s) LAST so a Plex scan landing mid-mirror can never see the .mp4
// before its sidecar metadata is already in place.
//
// It is best-effort and idempotent: a destination that is already the same inode
// (os.SameFile) is skipped; a stale destination is removed and re-linked; on any
// link error the file is copied instead (cross-filesystem dst, etc.). It returns
// an error for the caller to LOG — the caller treats library mirroring as
// non-fatal and must never fail the job on it. lessonDir must live under
// downloadsDir; otherwise it returns an error and writes nothing.
func mirrorToLibrary(downloadsDir, libraryDir, lessonDir string, log io.Writer) error {
	if log == nil {
		log = io.Discard
	}

	// Mirror at the same path relative to the downloads root. Reject a lessonDir
	// that escapes the root (".." prefix or an absolute Rel result) before any
	// write, so a stray path can never land outside the library.
	rel, err := filepath.Rel(downloadsDir, lessonDir)
	if err != nil {
		return fmt.Errorf("relativize %q under %q: %w", lessonDir, downloadsDir, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("lesson dir %q is not under downloads dir %q", lessonDir, downloadsDir)
	}
	dstDir := filepath.Join(libraryDir, rel)

	entries, err := os.ReadDir(lessonDir)
	if err != nil {
		return fmt.Errorf("read lesson dir %q: %w", lessonDir, err)
	}

	// Partition into sidecars (linked first) and videos (linked LAST). Skip
	// subdirectories and any leftover partial-download artifacts defensively, so
	// the library only ever gets finished files.
	var sidecars, videos []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if isPartial(name) {
			continue
		}
		if isVideo(name) {
			videos = append(videos, name)
		} else {
			sidecars = append(sidecars, name)
		}
	}

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("create library dir %q: %w", dstDir, err)
	}

	var linked, copied int
	var firstErr error
	mirror := func(name string) {
		src := filepath.Join(lessonDir, name)
		dst := filepath.Join(dstDir, name)
		viaCopy, err := mirrorFile(src, dst)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			fmt.Fprintf(log, "  ⚠ mirror %s: %v\n", name, err)
			return
		}
		if viaCopy {
			copied++
		} else {
			linked++
		}
	}
	// Sidecars first, then the video(s) — the metadata is always in place before
	// the .mp4 appears in the library.
	for _, name := range sidecars {
		mirror(name)
	}
	for _, name := range videos {
		mirror(name)
	}

	// Log the link-vs-copy outcome once per lesson so the operator can tell whether
	// they are getting true hardlinks (same dataset) or a copy fallback.
	fmt.Fprintf(log, "  → library %s: %d linked, %d copied\n", rel, linked, copied)
	return firstErr
}

// mirrorFile reflects one source file into dst. It reports whether the byte-copy
// fallback was used (false = a true hardlink). If dst already exists and is the
// same inode as src it is left untouched (idempotent); a stale dst is removed
// first. os.Link is tried first; on ANY link error the bytes are copied instead.
func mirrorFile(src, dst string) (viaCopy bool, err error) {
	si, err := os.Stat(src)
	if err != nil {
		return false, fmt.Errorf("stat %q: %w", src, err)
	}

	if di, derr := os.Stat(dst); derr == nil {
		if os.SameFile(si, di) {
			return false, nil // already the same inode: idempotent skip
		}
		// A stale destination (re-downloaded lesson, or an old copy): drop it so
		// the link/copy below refreshes the library.
		if rerr := os.Remove(dst); rerr != nil {
			return false, fmt.Errorf("remove stale %q: %w", dst, rerr)
		}
	}

	if lerr := hardlink(src, dst); lerr == nil {
		return false, nil
	}
	// Any link failure (cross-filesystem, unsupported, etc.) falls back to a copy.
	if cerr := copyFile(src, dst, si.Mode()); cerr != nil {
		return true, fmt.Errorf("copy %q -> %q: %w", src, dst, cerr)
	}
	return true, nil
}

// copyFile writes src's bytes into dst with the given mode, replacing dst if it
// exists. It is the cross-filesystem fallback when a hardlink cannot be made.
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

// isVideo reports whether name is a video container drumdrop produces, so it can
// be linked last (after the sidecar metadata). Matching is case-insensitive.
func isVideo(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".mkv", ".webm", ".mov", ".m4v":
		return true
	}
	return false
}

// isPartial reports whether name is a leftover yt-dlp partial-download artifact
// (*.part, *.ytdl, per-format *.f* fragments) that must never reach the library.
// It mirrors cleanupPartials' patterns so the two stay in agreement.
func isPartial(name string) bool {
	part, _ := filepath.Match("*.part", name)
	ytdl, _ := filepath.Match("*.ytdl", name)
	frag, _ := filepath.Match("*.f*", name)
	return part || ytdl || frag
}
