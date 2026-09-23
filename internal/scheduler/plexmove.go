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

// episodeTempSuffix names the file writeScratchNFO writes before it takes the
// nfo's place.
const episodeTempSuffix = ".drumdrop-episode"

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
	// roots are the downloads and library folders: the lesson's previous
	// folder (previousFolder) is set aside only if it is inside one of them.
	roots []string
	// episodeNFO, when set, replaces the downloaded "<base>.nfo" just before
	// the entries are placed, so the season folder is never written to after
	// its entries are in place.
	episodeNFO []byte
	// jobID names the folder replaced entries are set aside in (asideArea).
	jobID int64
}

// plexMoveResult says where a plex-tv move left the lesson.
type plexMoveResult struct {
	// seasonDir is the season folder holding every entry of the lesson, or ""
	// when nothing was placed (the move refused, or failed and was undone):
	// the lesson is whole in its private folder.
	seasonDir string
	// videoPath is the episode video the move placed ("" with no video).
	videoPath string
	// episodeBase is the name every placed entry starts with.
	episodeBase string
	// placed are the entries this move put in the library (all still there).
	placed []string
	// kept are entries of the lesson's previous download that are still its
	// own when nothing was placed (the move refused, or was undone), plus any
	// placed entry an undo could not take back.
	kept []string
	// known is false when the move stopped before it could tell what the
	// lesson owns in the library (it could not start, or the lesson's own
	// record is damaged): the lesson's record must then stay exactly as it is.
	known bool
	// pending is the placement to commit once the download is recorded, or
	// to undo if it is not; nil when nothing was placed.
	pending *placement
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

// moveToLibraryPlexTV places the finished lesson's entries from its private
// folder src into <libraryDir>/<Sanitize(show)>/Season 0N/, renaming each entry
// from its "NN - Title" base to the episode base
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
//  1. It refuses, leaving the library untouched, if a destination is claimed
//     by another lesson: named by its record, matched by a legacy row's name,
//     or the same file as one of those under another spelling (a
//     case-insensitive filesystem).
//  2. It sets the lesson's previous download aside (its recorded entries, or
//     a legacy row's name-matched ones that no other lesson claims), even
//     under an old title, so a re-download replaces instead of merging.
//  3. It sets aside an existing entry at one of its names that no lesson
//     claims (a leftover drumdrop no longer tracks) (owner ruling #66).
//  4. It places each entry (rename, or copy across filesystems), and flushes
//     copies to disk.
//
// If any step fails, everything is undone: placed entries go back to src and
// set-aside ones go back where they were. On success the result's pending
// placement is the worker's to commit (the set-aside entries are removed) once
// the download is recorded, or to undo if it is not. Nothing of the lesson's
// earlier download is removed before then.
//
// Everything it reads, writes or removes goes through folders it holds open
// (os.Root), never through a path read again later: the season folder must
// resolve inside the library, a rename acts on the two open folders
// (renameAt; by path only where the platform has no such call, Windows), and
// a copy creates each entry afresh (O_EXCL), never writing through whatever is
// there. So a symlink planted in either folder, or swapped in for one of them
// after it was opened, can not send a write or a removal outside.
//
// The error is for the caller to LOG; the move is non-fatal and must never
// fail the job. With seasonDir == "" the result's record is what the lesson
// still owns in the library.
func moveToLibraryPlexTV(libraryDir, show string, season, episode int, title string, src *scratchDir, lib plexLibrary) (plexMoveResult, error) {
	if _, _, rerr := library.Record(lib.self); rerr != nil {
		return plexMoveResult{}, fmt.Errorf("refusing to move: the lesson's own record of its library files is damaged, so the lesson is not placed and its record stays as it is: %w", rerr)
	}
	c := lib.claims
	if c == nil {
		return plexMoveResult{}, errors.New("refusing to move: the other lessons' claims were not read")
	}

	seasonRel := filepath.Join(musora.Sanitize(show), library.SeasonName(season))
	seasonDir := filepath.Join(libraryDir, seasonRel)
	plan, err := planPlexTVMove(src, src.dir.Name(), seasonDir, show, title, season, episode)
	if err != nil {
		return plexMoveResult{}, err
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
	// Refused, or undone, the lesson still owns its previous entries; if those
	// are not known, its record stays as it is.
	refused := plexMoveResult{kept: previous.Remove, known: perr == nil}

	if err := checkPlexConflicts(plan.steps, c, self, previous.Remove); err != nil {
		return refused, errors.Join(append([]error{err}, notes...)...)
	}
	seasonRoot, err := openLibraryParent(libraryDir, seasonRel)
	if err != nil {
		return refused, errors.Join(append([]error{err}, notes...)...)
	}

	aside := newAsideArea(lib.jobID)
	fail := func(err error, stuck []string) (plexMoveResult, error) {
		seasonRoot.Close()
		_, rerr := aside.restore(false)
		ferr := aside.finish(rerr != nil)
		res := refused
		res.kept = append(append([]string(nil), refused.kept...), stuck...)
		res.known = refused.known || len(stuck) > 0
		return res, errors.Join(append([]error{err, rerr, ferr}, notes...)...)
	}

	// 2. The previous download is set aside first, so an undone move puts it
	// back as it was.
	for _, p := range previous.Remove {
		if err := aside.setAsidePath(libraryDir, p, true); err != nil {
			return fail(fmt.Errorf("the previous download could not be set aside, so the lesson is not placed: %w", err), nil)
		}
	}
	// A previous download in the default layout (a lesson folder its row
	// records, such as one kept in downloads when a move was refused) is
	// replaced too.
	if prev, ok := previousFolder(lib.self, seasonDir, c, lib.roots); ok {
		if err := aside.setAsidePath(prev.root, prev.path, true); err != nil {
			return fail(fmt.Errorf("the previous download could not be set aside, so the lesson is not placed: %w", err), nil)
		}
	}

	// 3. A leftover no lesson claims at one of this episode's names (step 1
	// refused every claimed one).
	for _, st := range plan.steps {
		name := filepath.Base(st.dst)
		if _, err := seasonRoot.Lstat(name); err != nil {
			continue
		}
		if err := aside.setAside(libraryDir, seasonRoot, name, false); err != nil {
			return fail(fmt.Errorf("an entry no lesson records is in the way at %q: %w", st.dst, err), nil)
		}
	}

	if lib.episodeNFO != nil {
		if err := writeScratchNFO(src.dir, src.base+".nfo", lib.episodeNFO); err != nil {
			notes = append(notes, err)
		}
	}

	// 4. Place every entry, or none.
	placed, stuck, err := placeSteps(seasonRoot, src.dir, plan.steps)
	if err != nil {
		return fail(err, stuck)
	}

	res := plexMoveResult{seasonDir: seasonDir, videoPath: plan.videoPath, episodeBase: plan.episodeBase, known: true}
	for _, st := range plan.steps {
		res.placed = append(res.placed, st.dst)
	}
	res.pending = &placement{dir: seasonDir, placed: res.placed, dest: seasonRoot, src: src.dir, steps: placed, aside: aside}
	return res, errors.Join(notes...)
}

// scratchDir is a finished download's lesson folder in its private folder,
// held open.
type scratchDir struct {
	// base is the lesson folder's name ("NN - Title"), which every file the
	// download wrote starts with.
	base string
	// dir is the lesson folder itself.
	dir *os.Root
}

// openScratch opens the downloaded lesson folder lessonDir through the
// downloads folder: lessonDir must be strictly inside downloads, reached
// without a symlink that leads out of it, and itself a real folder (not a
// symlink).
func openScratch(downloads, lessonDir string) (*scratchDir, error) {
	if downloads == "" {
		return nil, fmt.Errorf("refusing to place %q: no downloads folder was given", lessonDir)
	}
	rel, err := filepath.Rel(downloads, lessonDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("refusing to place: lesson folder %q is not inside downloads %q", lessonDir, downloads)
	}
	dl, err := os.OpenRoot(downloads)
	if err != nil {
		return nil, fmt.Errorf("open downloads %q: %w", downloads, err)
	}
	defer dl.Close()
	parent, err := dl.OpenRoot(filepath.Dir(rel))
	if err != nil {
		return nil, fmt.Errorf("open the folder of %q inside downloads: %w", lessonDir, err)
	}
	defer parent.Close()
	base := filepath.Base(rel)
	dir, err := openRealDir(parent, base)
	if err != nil {
		return nil, fmt.Errorf("open the downloaded lesson: %w", err)
	}
	return &scratchDir{base: base, dir: dir}, nil
}

func (s *scratchDir) close() {
	s.dir.Close()
}

// writeScratchNFO replaces the download's own nfo (name, in the scratch
// folder dir) with the episode nfo, so the move places it like every other
// entry. It writes only over a regular file the download produced; none (or
// anything else) is a note, not a failure. It never writes through what is at
// name: it writes a new file beside it (O_EXCL) and renames that over name, in
// the folder it holds open, so a symlink swapped in at name after the check is
// replaced, not followed. A file left at the temporary name by a run that died
// is removed first, as musora's writeInRoot does.
func writeScratchNFO(dir *os.Root, name string, content []byte) error {
	info, err := dir.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("the download wrote no %q, so no episode nfo was written", name)
	}
	tmp := name + episodeTempSuffix
	if err := dir.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("write the episode nfo: a leftover is in the way: %w", err)
	}
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("write the episode nfo: %w", err)
	}
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = dir.Rename(tmp, name)
	}
	if err != nil {
		_ = dir.Remove(tmp)
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

// placeStep puts one entry at its destination in the folder season: by a
// rename from the scratch folder (renameAt, on the two open folders), or, when
// the two are on different filesystems (and only then), by a copy made inside
// season, leaving the source for the final scratch-folder removal. Any other
// rename error is a refusal: an entry that appeared at the destination after
// the checks is kept, never copied over. A copy that fails part-way takes back
// out what it created, and nothing else.
func placeStep(season, scratch *os.Root, st plexMoveStep) (renamed bool, err error) {
	name := filepath.Base(st.dst)
	rerr := renameAt(scratch, st.name, season, name)
	if rerr == nil {
		return true, nil
	}
	if !crossDevice(rerr) {
		return false, fmt.Errorf("refusing to place %q: %w", st.dst, rerr)
	}
	var cerr error
	if st.dir {
		cerr = copyTreeInto(season, name, scratch, st.name)
	} else {
		cerr = copyFileInto(season, name, scratch, st.name, st.mode)
	}
	if cerr != nil {
		return false, fmt.Errorf("copy %q -> %q (across filesystems): %w", st.src, st.dst, cerr)
	}
	return false, nil
}

// undoSteps takes placed entries back out of the destination folder, newest
// first: a renamed entry is renamed back into the scratch folder (it has no
// other copy), a copied one is removed (its source never left). It returns the
// entries still in the destination, each also reported in the error.
func undoSteps(season, scratch *os.Root, placed []placedStep) (stuck []string, err error) {
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
		if rerr := renameAt(season, filepath.Base(st.dst), scratch, st.name); rerr != nil {
			stuck = append(stuck, st.dst)
			errs = append(errs, fmt.Errorf("could not return %q to its private folder; its only copy is left at %q: %w", st.src, st.dst, rerr))
		}
	}
	return stuck, errors.Join(errs...)
}

// discardPartialCopy removes a copy the move made in dir but will not record,
// so no copy is left that drumdrop does not track. nil if nothing is
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
		return fmt.Errorf("partial copy could not be removed, left at %q: %w", path, err)
	}
	return nil
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
	// name is the entry's name in the scratch folder; src is its path, for
	// messages.
	name, src string
	// dst is where it goes in the season folder.
	dst  string
	dir  bool
	mode fs.FileMode
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
func planPlexTVMove(scratch *scratchDir, lessonDir, seasonDir, show, title string, season, episode int) (plexMovePlan, error) {
	entries, err := fs.ReadDir(scratch.dir.FS(), ".")
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
		en := entry{step: plexMoveStep{name: name, src: filepath.Join(lessonDir, name), dir: e.IsDir()}}
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
			en.suffix = strings.TrimPrefix(name, scratch.base)
			// The matcher runs on the SCRATCH name (against the scratch base), so
			// yt-dlp fragments and strays are never chosen as the video.
			en.video = isLessonVideoName(name, scratch.base)
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

// copyTreeInto copies the folder srcName of src into dir as the new folder
// name, recreating folders and copying regular files with their mode, then
// flushes every copied folder to disk. It is the cross-filesystem fallback for
// a move when a rename cannot move the folder across devices. Both sides are
// open folders (os.Root): the walk reads only inside src (a symlink in it is a
// non-regular entry, skipped, never followed), and every folder and file is
// created afresh inside dir (no following a symlink planted at a name): an
// existing name fails the copy. srcName itself must be a real folder: a
// symlink to one would otherwise be walked as a single non-regular entry,
// copying nothing. A copy that fails removes the folder name again only if it
// created it: one already there (the copy's first Mkdir refused it) is left
// exactly as it is.
func copyTreeInto(dir *os.Root, name string, src *os.Root, srcName string) (err error) {
	info, err := src.Lstat(srcName)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to copy %q: not a real folder (mode %s)", filepath.Join(src.Name(), srcName), info.Mode().Type())
	}
	var dirs []string
	defer func() {
		if err != nil && len(dirs) > 0 {
			err = errors.Join(err, discardPartialCopy(dir, name))
		}
	}()
	err = fs.WalkDir(src.FS(), srcName, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcName, filepath.FromSlash(path))
		if err != nil {
			return err
		}
		target := filepath.Join(name, rel)
		if d.IsDir() {
			if err := dir.Mkdir(target, 0o755); err != nil {
				return err
			}
			dirs = append(dirs, target) // created by this copy
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks/devices: drumdrop only produces regular files
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFileInto(dir, target, src, filepath.FromSlash(path), info.Mode())
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

// copyFileInto writes the bytes of srcName (in the open folder src) into the
// new file name inside dir with the given mode, and flushes it to disk before
// returning, so the source can be removed safely afterwards. The source is
// opened inside src (never outside it) and must be a regular file once open;
// the destination is created afresh (O_EXCL, through os.Root): it never writes
// through a file or a symlink already at name. A failed copy removes what it
// created.
func copyFileInto(dir *os.Root, name string, src *os.Root, srcName string, mode fs.FileMode) error {
	info, err := src.Lstat(srcName)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to copy %q: not a regular file (mode %s)", filepath.Join(src.Name(), srcName), info.Mode().Type())
	}
	in, err := src.Open(srcName)
	if err != nil {
		return err
	}
	defer in.Close()
	if st, err := in.Stat(); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("refusing to copy %q: not a regular file once opened (%v)", filepath.Join(src.Name(), srcName), err)
	}

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
