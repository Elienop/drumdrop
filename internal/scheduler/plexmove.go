package scheduler

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
)

// maxNameBytes is the longest file or folder name the filesystems drumdrop
// writes to accept (ext4, btrfs, ZFS, APFS and NTFS all stop at 255).
const maxNameBytes = 255

// syncFile flushes an open file or folder to disk. A package variable so a test
// can prove a copy is flushed before its source is removed.
var syncFile = (*os.File).Sync

// plexLibrary is what the plex-tv move needs to know beyond the lesson itself.
type plexLibrary struct {
	// self is the lesson's row as it was before this download: its record (or,
	// for a legacy row, its season folder and video) says which library entries
	// are its previous files.
	self database.Lesson
	// claims indexes every lesson row that records files (self may be among
	// them), so the move never overwrites or removes an entry another lesson
	// claims. The caller builds it; one it could not build is a failed attempt.
	claims *library.Claims
	// roots are the folders the move may remove entries under; empty means the
	// library only.
	roots []string
	// episodeNFO, when set, replaces the scratch "<base>.nfo" just before the
	// entries are placed, so the season folder is never written to after its
	// entries are in place.
	episodeNFO []byte
}

// plexMoveResult says where a plex-tv move left the lesson.
type plexMoveResult struct {
	// seasonDir is the season folder holding every entry of the lesson, or ""
	// when the lesson is whole in the scratch folder (the move refused, or
	// failed and was undone).
	seasonDir string
	// videoPath is the episode video the move placed ("" with no video).
	videoPath string
	// episodeBase is the name every placed entry starts with.
	episodeBase string
	// placed are the entries this move put in the library (all still there).
	placed []string
	// kept are entries of the lesson's previous download that are still in the
	// library and still its own (the move refused before removing them, or
	// could not remove them), plus any placed entry an undo could not take back.
	kept []string
	// known is false when the move stopped before it could tell what the
	// lesson owns in the library (it could not start, or the lesson's own
	// record is damaged): the lesson's record must then stay exactly as it is.
	known bool
}

// record is what the worker records as the lesson's library entries: every
// entry it owns after the move (placed and kept), relative to the library
// folder root, never nil. It is nil, meaning "leave the record as it is", when
// the move did not learn what the lesson owns.
func (r plexMoveResult) record(root string) ([]string, error) {
	if !r.known {
		return nil, nil
	}
	return library.EntriesFor(root, append(append([]string(nil), r.placed...), r.kept...))
}

// moveToLibraryPlexTV moves the finished lesson's files out of the scratch
// lessonDir into <libraryDir>/<Sanitize(show)>/Season 0N/, renaming each entry
// from its scratch "NN - Title" base to the episode base
// "<Sanitize(show)> - s0Ne0M - <Sanitize(title)>" while preserving the suffix
// (".mp4", ".en.vtt", ".nfo", "-poster.jpg", " [Drumless].mp4"); each subfolder
// becomes "<episodeBase> <folder>". Files end up FLAT in the season folder,
// which is shared across the show's episodes. A song's version files keep one
// episode base, so Plex merges them as one episode with several versions.
// Entries are processed in sorted name order, so videoPath (the first real
// lesson video) is deterministic.
//
// The season folder may hold other lessons' entries, and two lessons can share
// an episode number, so the move acts only on what it can prove (see
// library.Claims), in this order, writing nothing until the checks pass:
//  1. It refuses, leaving the lesson whole in scratch and the library
//     untouched, if a destination is claimed by another lesson: named by its
//     record, matched by a legacy row's name, or the same file as one of those
//     under another spelling (a case-insensitive filesystem).
//  2. It removes the lesson's previous download (its recorded entries, or a
//     legacy row's name-matched ones that no other lesson claims), even under
//     an old title, so a re-download replaces instead of merging.
//  3. It replaces an existing entry at one of its names that no lesson claims
//     (a leftover drumdrop no longer tracks), and says so (owner ruling #66).
//  4. It places each entry (rename, or copy across filesystems), undoing
//     every placed entry if one fails, and flushes copies to disk.
//  5. It removes the scratch folder.
//
// Everything it writes or removes in the library goes through os.Root, so a
// symlink planted in the library can not send a write or a removal outside it:
// the season folder must resolve inside the library, and a copy creates each
// entry afresh (O_EXCL), never writing through whatever is there.
//
// Whatever fails, one complete copy of the lesson is left in one place, and the
// result says where to record it: seasonDir == "" means the lesson is whole in
// lessonDir; seasonDir != "" means every entry is in the season folder, and an
// error alongside it names a scratch leftover or a note to log. The result's
// record is what the lesson owns in the library either way. The error is for
// the caller to LOG; the move is non-fatal and must never fail the job.
func moveToLibraryPlexTV(libraryDir, show string, season, episode int, title, lessonDir string, lib plexLibrary) (plexMoveResult, error) {
	scratchBase := filepath.Base(lessonDir)
	// A malformed scratch base (root, escape, absolute) would make the per-file
	// TrimPrefix meaningless and could read an unexpected dir; refuse it.
	if scratchBase == "." || scratchBase == ".." || strings.HasPrefix(scratchBase, ".."+string(filepath.Separator)) || filepath.IsAbs(scratchBase) {
		return plexMoveResult{}, fmt.Errorf("malformed scratch lesson dir %q", lessonDir)
	}
	roots := lib.roots
	if len(roots) == 0 {
		roots = []string{libraryDir}
	}

	seasonRel := filepath.Join(musora.Sanitize(show), library.SeasonName(season))
	seasonDir := filepath.Join(libraryDir, seasonRel)
	plan, err := planPlexTVMove(lessonDir, scratchBase, seasonDir, show, title, season, episode)
	if err != nil {
		return plexMoveResult{}, err
	}
	if _, _, rerr := library.Record(lib.self); rerr != nil {
		return plexMoveResult{}, fmt.Errorf("refusing to move: the lesson's own record of its library files is damaged, so the lesson stays whole in downloads and its record as it is: %w", rerr)
	}
	c := lib.claims
	if c == nil {
		return plexMoveResult{}, errors.New("refusing to move: the other lessons' claims were not read")
	}

	self := lib.self.RailcontentID
	var notes []error
	previous, perr := c.Plan(lib.self)
	if perr != nil {
		notes = append(notes, fmt.Errorf("the previous download's library files are not known, so none were removed: %w", perr))
		previous = library.Entries{}
	}
	for _, p := range previous.Kept {
		notes = append(notes, fmt.Errorf("left %q in the library: it looks like this lesson's previous download, but another lesson claims it", p))
	}
	// Refused before anything was removed, the lesson still owns its previous
	// entries; if those are not known, its record stays as it is.
	refused := plexMoveResult{kept: previous.Remove, known: perr == nil}

	if err := checkPlexConflicts(plan.steps, c, self, previous.Remove); err != nil {
		return refused, errors.Join(append([]error{err}, notes...)...)
	}
	seasonRoot, closeSeason, err := openLibraryParent(libraryDir, seasonRel)
	if err != nil {
		return refused, errors.Join(append([]error{err}, notes...)...)
	}
	defer closeSeason()

	// 2. The previous download goes first, so an undone move leaves nothing of
	// the episode behind.
	var left []string
	for _, p := range previous.Remove {
		if err := library.Remove(roots, p); err != nil {
			left = append(left, p)
			notes = append(notes, fmt.Errorf("previous download could not be removed from the library, left at %q: %w", p, err))
		}
	}
	if len(left) > 0 {
		return plexMoveResult{kept: left, known: true}, errors.Join(notes...)
	}

	// 3. A leftover no lesson claims at one of this episode's names (step 1
	// refused every claimed one).
	for _, st := range plan.steps {
		name := filepath.Base(st.dst)
		if _, err := seasonRoot.Lstat(name); err != nil {
			continue
		}
		if err := seasonRoot.RemoveAll(name); err != nil {
			return plexMoveResult{known: true}, errors.Join(append(notes, fmt.Errorf("an entry no lesson records is in the way at %q and could not be removed: %w", st.dst, err))...)
		}
		notes = append(notes, fmt.Errorf("replaced %q, which no lesson records", st.dst))
	}

	if lib.episodeNFO != nil {
		if err := writeScratchNFO(filepath.Join(lessonDir, scratchBase+".nfo"), lib.episodeNFO); err != nil {
			notes = append(notes, err)
		}
	}

	// 4. Place every entry, or none.
	placed := make([]placedStep, 0, len(plan.steps))
	copied := false
	for _, st := range plan.steps {
		renamed, err := placePlexStep(seasonRoot, st)
		if err != nil {
			stuck, uerr := undoPlexSteps(seasonRoot, placed)
			return plexMoveResult{kept: stuck, known: true}, errors.Join(append([]error{err, uerr}, notes...)...)
		}
		copied = copied || !renamed
		placed = append(placed, placedStep{plexMoveStep: st, renamed: renamed})
	}
	if copied {
		if err := syncIn(seasonRoot, "."); err != nil {
			stuck, uerr := undoPlexSteps(seasonRoot, placed)
			err = fmt.Errorf("flush season folder %q after the copy: %w", seasonDir, err)
			return plexMoveResult{kept: stuck, known: true}, errors.Join(append([]error{err, uerr}, notes...)...)
		}
	}

	res := plexMoveResult{seasonDir: seasonDir, videoPath: plan.videoPath, episodeBase: plan.episodeBase, known: true}
	for _, st := range plan.steps {
		res.placed = append(res.placed, st.dst)
	}
	// 5. Every entry is in the library. Drop the scratch folder: it still holds
	// the sources of copied entries, plus any non-regular file the plan left out.
	if rmerr := os.RemoveAll(lessonDir); rmerr != nil {
		notes = append([]error{downloadsLeftoverErr(lessonDir, rmerr)}, notes...)
	}
	return res, errors.Join(notes...)
}

// writeScratchNFO replaces the download's own nfo in the scratch folder with
// the episode nfo, so the move places it like every other entry. It writes
// only over a regular file the download produced; none (or anything else) is
// a note, not a failure.
func writeScratchNFO(path string, content []byte) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("the download wrote no %q, so no episode nfo was written", filepath.Base(path))
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("write the episode nfo: %w", err)
	}
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write the episode nfo: %w", err)
	}
	return nil
}

// checkPlexConflicts refuses destinations another lesson claims (see
// library.Claims.Claimants): one another lesson's record names (existing or
// not, so two records never share a path), an existing entry another lesson
// claims by name (a legacy row), or one that is the same file as a claimed
// one under another spelling. ours are the lesson's own previous entries
// (its plan's Remove), which it may replace: no other record names one of
// them, and a legacy row's name match on one does not outrank this lesson's
// record, as in the delete.
func checkPlexConflicts(steps []plexMoveStep, c *library.Claims, self int, ours []string) error {
	own := make(map[string]bool, len(ours))
	for _, p := range ours {
		own[p] = true
	}
	var errs []error
	for _, st := range steps {
		if own[st.dst] {
			continue
		}
		ids, err := c.Claimants(st.dst, self, true)
		if err != nil {
			return fmt.Errorf("refusing to move: %w", err)
		}
		if len(ids) > 0 {
			errs = append(errs, fmt.Errorf("%q is claimed by lesson %v", st.dst, ids))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("refusing to move: another lesson already owns an entry at this episode's names, so the lesson stays whole in downloads: %w", errors.Join(errs...))
	}
	return nil
}

// placedStep is a plexMoveStep already in the season folder; renamed says how
// it got there, which decides how to undo it.
type placedStep struct {
	plexMoveStep
	renamed bool
}

// placePlexStep puts one entry at its destination in the season folder: by
// rename, or (on any rename error) by a copy made inside season, leaving the
// source for the final scratch-folder removal. A copy that fails part-way, a
// file or a folder, is removed again.
func placePlexStep(season *os.Root, st plexMoveStep) (renamed bool, err error) {
	rerr := rename(st.src, st.dst)
	if rerr == nil {
		return true, nil
	}
	name := filepath.Base(st.dst)
	var cerr error
	if st.dir {
		cerr = copyTreeInto(season, name, st.src)
	} else {
		cerr = copyFileInto(season, name, st.src, st.mode)
	}
	if cerr != nil {
		err := fmt.Errorf("copy %q -> %q (rename failed: %v): %w", st.src, st.dst, rerr, cerr)
		return false, errors.Join(err, discardPartialCopy(season, name))
	}
	return false, nil
}

// undoPlexSteps takes placed entries back out of the season folder, newest
// first: a renamed entry is renamed back into the scratch folder (it has no
// other copy), a copied one is removed (its source never left). It returns the
// entries still in the library, each also reported in the error.
func undoPlexSteps(season *os.Root, placed []placedStep) (stuck []string, err error) {
	var errs []error
	for i := len(placed) - 1; i >= 0; i-- {
		st := placed[i]
		if !st.renamed {
			if derr := discardPartialCopy(season, filepath.Base(st.dst)); derr != nil {
				stuck = append(stuck, st.dst)
				errs = append(errs, derr)
			}
			continue
		}
		if rerr := rename(st.dst, st.src); rerr != nil {
			stuck = append(stuck, st.dst)
			errs = append(errs, fmt.Errorf("could not return %q to downloads; its only copy is left in the library at %q: %w", st.src, st.dst, rerr))
		}
	}
	return stuck, errors.Join(errs...)
}

// discardPartialCopy removes a copy the move made in dir but will not record,
// so the library never holds a copy drumdrop does not track. nil if nothing is
// there (including a name too long to ever have been created); an error only
// for a copy that exists and could not be removed.
func discardPartialCopy(dir *os.Root, name string) error {
	path := filepath.Join(dir.Name(), name)
	if _, err := dir.Lstat(name); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENAMETOOLONG) {
			return nil
		}
		return fmt.Errorf("could not check for a partial copy at %q: %w", path, err)
	}
	if err := dir.RemoveAll(name); err != nil {
		return fmt.Errorf("partial copy could not be removed from the library, left at %q: %w", path, err)
	}
	return nil
}

// downloadsLeftoverErr reports a scratch folder that could not be fully removed
// after its lesson was copied whole into the library.
func downloadsLeftoverErr(lessonDir string, err error) error {
	return fmt.Errorf("the library copy is complete, but the downloads copy could not be fully removed, leftover at %q: %w", lessonDir, err)
}

// plexMovePlan is what planPlexTVMove works out before the plex-tv move writes
// anything: each entry's destination, the video to record, and the episode base.
type plexMovePlan struct {
	steps       []plexMoveStep
	videoPath   string
	episodeBase string
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
// Any name is fine, brackets included: the lesson records exactly what it
// placed, so nothing ever has to parse these names back.
//
// The episode base is shortened (the title, then refused) so that every name
// fits in maxNameBytes, as a long show plus a long title would otherwise never
// reach the library. Non-regular files are left out (drumdrop only produces
// regular files; they go with the scratch folder). videoPath is the destination
// of the first real lesson video in name order (isLessonVideoName), which for a
// song is its [Drumless] version.
func planPlexTVMove(lessonDir, scratchBase, seasonDir, show, title string, season, episode int) (plexMovePlan, error) {
	entries, err := os.ReadDir(lessonDir)
	if err != nil {
		return plexMovePlan{}, fmt.Errorf("read scratch lesson dir %q: %w", lessonDir, err)
	}
	// Sort by name so the chosen videoPath (the first .mp4) is deterministic
	// across filesystems and across a song's multiple version files.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	type entry struct {
		step   plexMoveStep
		suffix string
		video  bool
	}
	var list []entry
	longest := 0
	for _, e := range entries {
		name := e.Name()
		en := entry{step: plexMoveStep{src: filepath.Join(lessonDir, name), dir: e.IsDir()}}
		if en.step.dir {
			en.suffix = " " + name
		} else {
			info, ierr := e.Info()
			if ierr != nil {
				return plexMovePlan{}, fmt.Errorf("stat scratch file %q: %w", name, ierr)
			}
			if !info.Mode().IsRegular() {
				continue // skip symlinks/devices: drumdrop only produces regular files
			}
			en.step.mode = info.Mode()
			en.suffix = strings.TrimPrefix(name, scratchBase)
			// The matcher runs on the SCRATCH name (against scratchBase), so
			// yt-dlp fragments and strays are never chosen as the video.
			en.video = isLessonVideoName(name, scratchBase)
		}
		longest = max(longest, len(en.suffix))
		list = append(list, en)
	}

	episodeBase, err := fitEpisodeBase(show, title, season, episode, longest)
	if err != nil {
		return plexMovePlan{}, err
	}
	plan := plexMovePlan{episodeBase: episodeBase}
	for _, en := range list {
		en.step.dst = filepath.Join(seasonDir, episodeBase+en.suffix)
		if en.video && plan.videoPath == "" {
			plan.videoPath = en.step.dst
		}
		plan.steps = append(plan.steps, en.step)
	}
	return plan, nil
}

// fitEpisodeBase is plexEpisodeBase, with the title cut short (at a rune
// boundary, trailing dots and spaces trimmed as Sanitize does) so that the base
// plus the longest suffix fits in maxNameBytes. It fails when even an empty
// title would not fit (the show name alone is too long).
func fitEpisodeBase(show, title string, season, episode, longestSuffix int) (string, error) {
	base := plexEpisodeBase(show, title, season, episode)
	if len(base)+longestSuffix <= maxNameBytes {
		return base, nil
	}
	prefix := strings.TrimSuffix(base, musora.Sanitize(title))
	budget := maxNameBytes - longestSuffix - len(prefix)
	runes := []rune(musora.Sanitize(title))
	for len(runes) > 0 && len(string(runes)) > budget {
		runes = runes[:len(runes)-1]
	}
	cut := strings.TrimRight(string(runes), ". ")
	if cut == "" {
		return "", fmt.Errorf("refusing to move: the show name %q leaves no room for an episode title within %d bytes", musora.Sanitize(show), maxNameBytes)
	}
	return prefix + cut, nil
}

// copyTreeInto copies the folder src into dir as the new folder name,
// recreating folders and copying regular files with their mode, then flushes
// every copied folder to disk. It is the cross-filesystem fallback for a move
// when a rename cannot move the folder across devices. Every folder and file is
// created afresh inside dir (os.Root, no following a symlink planted at a
// name): an existing name fails the copy. Non-regular entries inside (symlinks,
// devices) are skipped. src itself must be a real folder: a symlink to one
// would otherwise be walked as a single non-regular entry, copying nothing.
func copyTreeInto(dir *os.Root, name, src string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to copy %q: not a real folder (mode %s)", src, info.Mode().Type())
	}
	var dirs []string
	err = filepath.Walk(src, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(name, rel)
		if info.IsDir() {
			dirs = append(dirs, target)
			return dir.Mkdir(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil // skip symlinks/devices: drumdrop only produces regular files
		}
		return copyFileInto(dir, target, path, info.Mode())
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncIn(dir, dirs[i]); err != nil {
			return fmt.Errorf("flush %q: %w", filepath.Join(dir.Name(), dirs[i]), err)
		}
	}
	return nil
}

// copyFileInto writes src's bytes into the new file name inside dir with the
// given mode, and flushes it to disk before returning, so the source can be
// removed safely afterwards. The file is created afresh (O_EXCL, through
// os.Root): it never writes through a file or a symlink already at name. src
// must be a regular file. A failed copy removes what it created.
func copyFileInto(dir *os.Root, name, src string, mode fs.FileMode) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to copy %q: not a regular file (mode %s)", src, info.Mode().Type())
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = syncFile(out)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = dir.Remove(name) // do not leave a half-written file behind
		return err
	}
	return nil
}

// syncIn flushes the folder name inside dir ("." for dir itself) to disk: the
// names of what was just copied into it. Windows has no folder fsync, and some
// filesystems (network shares) refuse one; both are skipped, as the file
// contents were already flushed by copyFileInto.
func syncIn(dir *os.Root, name string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := dir.Open(name)
	if err != nil {
		return err
	}
	err = syncFile(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, errors.ErrUnsupported) {
		return nil
	}
	return err
}
