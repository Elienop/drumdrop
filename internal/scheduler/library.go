package scheduler

import (
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

// openLibraryParent creates the folder rel inside root (the library, or the
// downloads folder) and opens it as an os.Root, refusing one that resolves
// outside root (a symlinked folder on the way). The caller closes it.
func openLibraryParent(root, rel string) (*os.Root, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create folder %q: %w", root, err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open folder %q: %w", root, err)
	}
	defer r.Close()
	if err := r.MkdirAll(rel, 0o755); err != nil {
		return nil, fmt.Errorf("create %q inside %q: %w", rel, root, err)
	}
	dir, err := r.OpenRoot(rel)
	if err != nil {
		return nil, fmt.Errorf("open %q inside %q: %w", rel, root, err)
	}
	return dir, nil
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
// another path (a symlink, or one host folder bind-mounted twice). It was the
// guard for a move that replaced the library folder with the downloads folder
// it came from, deleting the lesson's only copy. A placement's source is now
// always the job's private folder, which is never a destination
// (placeLessonFolder refuses it), so that loss can not happen any more; the
// refusal stays because an alias gives every recorded path two spellings,
// which nothing here was built for. The same path spelled the same way is
// allowed (the library is then the downloads folder), as is a library that
// does not exist yet.
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
// name would not fit (fitEpisodeBase) and reports the base it used
// (plexMoveResult.episodeBase), which the worker reads to find the video a
// resources-only re-download keeps (keptVideo). The episode nfo needs no
// base: it is written in the private folder under the download's own name,
// before the entries are placed (writeScratchNFO), and takes the episode
// base as every other entry does.
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
