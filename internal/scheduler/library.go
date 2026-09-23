package scheduler

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	return fmt.Sprintf("%s%02d - %s", plexEpisodePrefix(musora.Sanitize(show), season), episode, musora.Sanitize(title))
}

// plexEpisodePrefix is the part of every episode base that names the show and
// season: "<showFolder> - s0Ne". PlexEpisodeMatcher checks a recorded video
// against it, so it is the one place that format lives.
func plexEpisodePrefix(showFolder string, season int) string {
	return fmt.Sprintf("%s - s%02de", showFolder, season)
}

// plexSeasonName is the season folder the plex-tv move files a show's episodes
// under ("Season 01").
func plexSeasonName(season int) string {
	return fmt.Sprintf("Season %02d", season)
}

// plexSeasonNumber parses a season folder name back into its number. It accepts
// only a name plexSeasonName itself produces (checked by formatting the number
// back), so a default-layout lesson folder ("NN - Title") never parses.
func plexSeasonNumber(name string) (season int, ok bool) {
	digits, found := strings.CutPrefix(name, "Season ")
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || plexSeasonName(n) != name {
		return 0, false
	}
	return n, true
}

// IsPlexSeasonDir reports whether dir is a plex-tv season folder, the folder the
// plex-tv move shares between every episode of a show. The delete uses it to tell
// a shared season folder (remove one episode's entries) from a lesson's own
// folder (remove the folder), whatever DRUMDROP_LAYOUT says today.
func IsPlexSeasonDir(dir string) bool {
	_, ok := plexSeasonNumber(filepath.Base(dir))
	return ok
}

// PlexEpisodeMatcher returns a predicate recognising, among the entries of the
// season folder that holds videoPath, every entry the plex-tv move placed for
// that episode: each version file, each sidecar and each subfolder. ok is false
// when videoPath is not an episode video of that folder's show and season, so a
// caller never sweeps on a guessed prefix.
//
// The move checks every name it is about to create against this same predicate,
// built from the video it is about to record, and refuses to place anything the
// predicate would miss. That is what keeps a delete in step with the move.
func PlexEpisodeMatcher(videoPath string) (match func(name string, isDir bool) bool, ok bool) {
	seasonDir := filepath.Dir(videoPath)
	season, ok := plexSeasonNumber(filepath.Base(seasonDir))
	if !ok {
		return nil, false
	}
	base := plexEpisodeBaseOfVideo(filepath.Base(videoPath))
	if base == "" || !strings.HasPrefix(base, plexEpisodePrefix(filepath.Base(filepath.Dir(seasonDir)), season)) {
		return nil, false
	}
	return func(name string, isDir bool) bool { return isPlexEpisodeEntry(base, name, isDir) }, true
}

// plexEpisodeBaseOfVideo recovers the episode base from a moved video's file
// name: "<base>.mp4" for a lesson, "<base> [Label].mp4" for each of a song's
// versions, which share the untagged base. "" if the name is not an .mp4.
//
// A lesson whose own title ends in "[...]" yields a base one tag shorter than
// its real one. That is harmless: the real base plus its suffix still parses as
// a tag followed by a suffix (isPlexEpisodeSuffix), so every file still matches.
func plexEpisodeBaseOfVideo(videoName string) string {
	stem, ok := strings.CutSuffix(videoName, ".mp4")
	if !ok || stem == "" {
		return ""
	}
	if i := strings.LastIndex(stem, " ["); i > 0 && strings.HasSuffix(stem, "]") {
		return stem[:i]
	}
	return stem
}

// isPlexEpisodeEntry reports whether name is "<base><suffix>" with a suffix the
// plex-tv move produces (isPlexEpisodeSuffix).
func isPlexEpisodeEntry(base, name string, isDir bool) bool {
	rest, ok := strings.CutPrefix(name, base)
	return ok && isPlexEpisodeSuffix(rest, isDir)
}

// isPlexEpisodeSuffix is the grammar of what follows the episode base in a name
// the plex-tv move gives an entry:
//   - a file: a sidecar or extension starting with '.' or '-' (".mp4", ".nfo",
//     "-poster.jpg", ".en.vtt");
//   - a folder: one space, then the scratch folder's name, with no further
//     space (" resources", " play-along");
//   - either, after a bracketed tag (" [Drumless].mp4", or " [Live] resources"
//     when the tag is part of the title).
//
// A space is not a boundary on its own, so a sibling whose title merely extends
// this one ("… Five Bonus.mp4", "… Five Bonus resources") never matches, and
// neither does another episode number ("s01e05" is not a prefix of "s01e50 -").
func isPlexEpisodeSuffix(rest string, isDir bool) bool {
	if tagged, ok := strings.CutPrefix(rest, " ["); ok {
		i := strings.LastIndex(tagged, "]")
		return i >= 0 && isPlexEpisodeSuffix(tagged[i+1:], isDir)
	}
	if isDir {
		folder, ok := strings.CutPrefix(rest, " ")
		return ok && folder != "" && !strings.Contains(folder, " ")
	}
	return rest != "" && (rest[0] == '.' || rest[0] == '-')
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
//
// Every name is worked out before anything is written, and the move refuses
// (writing nothing) if a name would not be recognised by PlexEpisodeMatcher for
// the video it is about to record. So a delete, which uses that matcher, always
// finds everything a move placed.
func moveToLibraryPlexTV(libraryDir, show string, season, episode int, title, lessonDir string) (seasonDir, videoPath string, err error) {
	scratchBase := filepath.Base(lessonDir)
	// A malformed scratch base (root, escape, absolute) would make the per-file
	// TrimPrefix below meaningless and could read an unexpected dir; refuse it.
	if scratchBase == "." || scratchBase == ".." || strings.HasPrefix(scratchBase, ".."+string(filepath.Separator)) || filepath.IsAbs(scratchBase) {
		return "", "", fmt.Errorf("malformed scratch lesson dir %q", lessonDir)
	}

	episodeBase := plexEpisodeBase(show, title, season, episode)
	seasonDir = filepath.Join(libraryDir, musora.Sanitize(show), plexSeasonName(season))
	steps, videoPath, err := planPlexTVMove(lessonDir, scratchBase, seasonDir, episodeBase)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create season dir %q: %w", seasonDir, err)
	}

	for _, st := range steps {
		if rerr := rename(st.src, st.dst); rerr != nil {
			// Cross-filesystem (or otherwise unrenamable): copy then drop the
			// source. A copy failure leaves the source in place.
			if st.dir {
				if cerr := copyTree(st.src, st.dst); cerr != nil {
					return "", "", fmt.Errorf("copy tree %q -> %q (rename failed: %v): %w", st.src, st.dst, rerr, cerr)
				}
				if rmerr := os.RemoveAll(st.src); rmerr != nil {
					return "", "", fmt.Errorf("remove source after copy %q: %w", st.src, rmerr)
				}
				continue
			}
			if cerr := copyFile(st.src, st.dst, st.mode); cerr != nil {
				return "", "", fmt.Errorf("copy %q -> %q (rename failed: %v): %w", st.src, st.dst, rerr, cerr)
			}
			if rmerr := os.Remove(st.src); rmerr != nil {
				return "", "", fmt.Errorf("remove source after copy %q: %w", st.src, rmerr)
			}
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

// plexMoveStep is one entry of a scratch lesson folder and where the plex-tv
// move puts it.
type plexMoveStep struct {
	src, dst string
	dir      bool
	mode     fs.FileMode
}

// planPlexTVMove lists the scratch lesson folder in name order and names each
// entry's destination in the season folder, without writing anything:
//   - a file keeps its suffix, with the scratch base swapped for the episode
//     base (".nfo", "-poster.jpg", a song's " [Original].mp4");
//   - a folder becomes "<episodeBase> <folder>" (e.g. "<episodeBase> resources").
//
// Non-regular files are left out (drumdrop only produces regular files; they go
// with the scratch folder). videoPath is the destination of the first real
// lesson video in name order (isLessonVideoName), which for a song is its
// [Drumless] version.
//
// It fails if the delete would not recognise a destination name. That check is
// what keeps the move and the delete in step: rename a sidecar here, or add a
// folder whose name has a space, and the move refuses instead of leaving files
// a delete can never reach.
func planPlexTVMove(lessonDir, scratchBase, seasonDir, episodeBase string) (steps []plexMoveStep, videoPath string, err error) {
	entries, err := os.ReadDir(lessonDir)
	if err != nil {
		return nil, "", fmt.Errorf("read scratch lesson dir %q: %w", lessonDir, err)
	}
	// Sort by name so the chosen videoPath (the first .mp4) is deterministic
	// across filesystems and across a song's multiple version files.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		name := e.Name()
		st := plexMoveStep{src: filepath.Join(lessonDir, name), dir: e.IsDir()}
		if st.dir {
			st.dst = filepath.Join(seasonDir, episodeBase+" "+name)
		} else {
			info, ierr := e.Info()
			if ierr != nil {
				return nil, "", fmt.Errorf("stat scratch file %q: %w", name, ierr)
			}
			if !info.Mode().IsRegular() {
				continue // skip symlinks/devices: drumdrop only produces regular files
			}
			st.mode = info.Mode()
			st.dst = filepath.Join(seasonDir, episodeBase+strings.TrimPrefix(name, scratchBase))
			// The matcher runs on the SCRATCH name (against scratchBase), so
			// yt-dlp fragments and strays are never chosen as the video.
			if videoPath == "" && isLessonVideoName(name, scratchBase) {
				videoPath = st.dst
			}
		}
		steps = append(steps, st)
	}

	// Check every destination against what a delete will use: the matcher for
	// the recorded video, or (no video) the episode base itself.
	belongs := func(name string, isDir bool) bool { return isPlexEpisodeEntry(episodeBase, name, isDir) }
	if videoPath != "" {
		m, ok := PlexEpisodeMatcher(videoPath)
		if !ok {
			return nil, "", fmt.Errorf("refusing to move: a delete would not recognise the episode video %q", videoPath)
		}
		belongs = m
	}
	for _, st := range steps {
		if !belongs(filepath.Base(st.dst), st.dir) {
			return nil, "", fmt.Errorf("refusing to move: a delete would not recognise %q as part of episode %q", filepath.Base(st.dst), episodeBase)
		}
	}
	return steps, videoPath, nil
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
