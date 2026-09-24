package scheduler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A download never writes where a lesson lives. yt-dlp runs with
// --force-overwrites (hard rule 8), which deletes an existing "<base>.mp4" at
// its output path before it downloads, and in the default setup (no library,
// or the library the downloads folder) that path would be the lesson's
// permanent folder: a re-download that then failed, or was skipped or
// canceled, lost the earlier video. So every download writes into a folder
// private to its job, and only a finished download is placed where the lesson
// lives (see placement).
//
// The private folders live under one hidden folder, privateRootName, in the
// downloads folder, so they are on the downloads filesystem:
//
//	<downloads>/.drumdrop-in-progress/.plexignore
//	<downloads>/.drumdrop-in-progress/job-<id>/<NN - Title>/...     a download
//	<root>/.drumdrop-in-progress/replaced-<id>[.<k>]/<n>/<name>    set aside
//
// A placement sets each entry it replaces aside in a replaced-<id> folder of
// the root the entry is under (downloads or library), and removes it only once
// the download is recorded (see asideArea).

// privateRootName is the hidden folder that holds what drumdrop has not
// finished with: downloads in progress, and entries a placement set aside. It
// is never a lesson: a lesson whose folder would be this name is refused.
const privateRootName = ".drumdrop-in-progress"

// plexIgnore is the .plexignore written in privateRootName, so a Plex library
// pointed at the downloads folder (or at a library that holds a set-aside
// entry) does not show a download in progress or a replaced file. Each line
// ignores one more level of folders.
const plexIgnore = "# DrumDrop: downloads in progress and replaced files. Nothing here is a lesson.\n*\n*/*\n*/*/*\n*/*/*/*\n*/*/*/*/*\n"

// jobFolderName is the name of job jobID's private download folder, and
// replacedFolderName that of the folder its placement sets entries aside in.
func jobFolderName(jobID int64) string { return "job-" + strconv.FormatInt(jobID, 10) }

func replacedFolderName(jobID int64) string { return "replaced-" + strconv.FormatInt(jobID, 10) }

// stagingEntry matches the folders a job leaves in privateRootName, and says
// which kind and which job: job-<id>, and replaced-<id> or replaced-<id>.<k>
// (asideArea.area).
var stagingEntry = regexp.MustCompile(`^(?:(job)-([0-9]+)|(replaced)-([0-9]+)(?:\.[0-9]+)?)$`)

// isPrivateRel reports whether rel, a path relative to the downloads or the
// library folder, is inside privateRootName, however it is cased (a
// case-insensitive disk).
func isPrivateRel(rel string) bool {
	first, _, _ := strings.Cut(filepath.ToSlash(filepath.Clean(rel)), "/")
	return strings.EqualFold(first, privateRootName)
}

// openStaging opens privateRootName inside root (both created if missing), and
// writes its .plexignore if it has none. It refuses a privateRootName that is
// not a real folder (a symlink), so nothing drumdrop writes there can be
// redirected elsewhere in root.
func openStaging(root string) (*os.Root, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create %q: %w", root, err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", root, err)
	}
	defer r.Close()
	if err := r.Mkdir(privateRootName, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create %q in %q: %w", privateRootName, root, err)
	}
	s, err := openRealDir(r, privateRootName)
	if err != nil {
		return nil, err
	}
	if f, err := s.OpenFile(".plexignore", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
		_, werr := f.WriteString(plexIgnore)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			s.Close()
			return nil, fmt.Errorf("write the .plexignore in %q: %w", s.Name(), werr)
		}
	} else if !errors.Is(err, fs.ErrExist) {
		s.Close()
		return nil, fmt.Errorf("write the .plexignore in %q: %w", s.Name(), err)
	}
	return s, nil
}

// openRealDir opens name inside r, refusing one that is not a real folder
// (a symlink, a file). OpenRoot follows a symlink inside r, so what it opened
// must be the very folder Lstat saw (os.SameFile): one swapped for a symlink
// in between (to another lesson's folder, say) is refused (security I3).
func openRealDir(r *os.Root, name string) (*os.Root, error) {
	path := filepath.Join(r.Name(), name)
	info, err := r.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("refusing %q: not a real folder (mode %s)", path, info.Mode().Type())
	}
	d, err := r.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	opened, err := d.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = errors.New("it was replaced while it was being opened")
	}
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("refusing %q: %w", path, err)
	}
	return d, nil
}

// privateDir is job jobID's private download folder.
func (w *Worker) privateDir(jobID int64) string {
	return filepath.Join(w.Cfg.DownloadsDir, privateRootName, jobFolderName(jobID))
}

// startPrivate makes job jobID's private download folder, holding an empty
// lesson folder lessonBase ("NN - Title", where the download writes):
// whatever an earlier run of the same job left there (a crash) goes first. It
// returns the private folder's path.
func (w *Worker) startPrivate(jobID int64, lessonBase string) (string, error) {
	s, err := openStaging(w.Cfg.DownloadsDir)
	if err != nil {
		return "", err
	}
	defer s.Close()
	name := jobFolderName(jobID)
	if err := s.RemoveAll(name); err != nil {
		return "", fmt.Errorf("remove what an earlier run left in %q: %w", filepath.Join(s.Name(), name), err)
	}
	if err := s.Mkdir(name, 0o755); err != nil {
		return "", fmt.Errorf("create %q: %w", filepath.Join(s.Name(), name), err)
	}
	if err := s.Mkdir(filepath.Join(name, lessonBase), 0o755); err != nil {
		return "", fmt.Errorf("create %q: %w", filepath.Join(s.Name(), name, lessonBase), err)
	}
	return w.privateDir(jobID), nil
}

// dropPrivate removes job jobID's private download folder whole: what a
// download wrote there never became the lesson's, and what a placement took
// out of it is no longer in it.
func (w *Worker) dropPrivate(jobID int64) {
	s, err := openExistingStaging(w.Cfg.DownloadsDir)
	if err == nil {
		defer s.Close()
		err = s.RemoveAll(jobFolderName(jobID))
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(w.log(), "  ⚠ job %d: its private download folder %q could not be removed: %v\n", jobID, w.privateDir(jobID), err)
	}
}

// openExistingStaging opens root's privateRootName without creating anything
// (fs.ErrNotExist when there is none).
func openExistingStaging(root string) (*os.Root, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return openRealDir(r, privateRootName)
}

// SweepPrivate is part of the startup recovery: it removes the private
// download folder of every job this worker is not running (a download a crash
// or a kill stopped; its job is requeued and starts over in a fresh folder),
// in the downloads folder and in the library. It keeps every replaced-<id>
// folder and logs it: one is left only when a placement was stopped by a
// crash between setting entries aside and recording the download, so it may
// hold a lesson's only copy of an earlier file. Anything else in the private
// root was not made by drumdrop and is left alone.
func (w *Worker) SweepPrivate() {
	roots := []string{w.Cfg.DownloadsDir}
	if w.Cfg.LibraryDir != "" && filepath.Clean(w.Cfg.LibraryDir) != filepath.Clean(w.Cfg.DownloadsDir) {
		roots = append(roots, w.Cfg.LibraryDir)
	}
	for _, root := range roots {
		if root != "" {
			w.sweepStaging(root)
		}
	}
}

func (w *Worker) sweepStaging(root string) {
	s, err := openExistingStaging(root)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Fprintf(w.log(), "startup: the private folder in %q could not be read: %v\n", root, err)
		return
	}
	defer s.Close()
	entries, err := fs.ReadDir(s.FS(), ".")
	if err != nil {
		fmt.Fprintf(w.log(), "startup: the private folder %q could not be read: %v\n", s.Name(), err)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		m := stagingEntry.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		replaced := m[3] != ""
		id, err := strconv.ParseInt(m[2]+m[4], 10, 64)
		if err != nil || w.isRunning(id) {
			continue
		}
		path := filepath.Join(s.Name(), e.Name())
		if replaced {
			fmt.Fprintf(w.log(), "startup: kept %q: files a placement set aside when DrumDrop stopped. Check them, then delete the folder\n", path)
			continue
		}
		if err := s.RemoveAll(e.Name()); err != nil {
			fmt.Fprintf(w.log(), "startup: a stopped download's folder %q could not be removed: %v\n", path, err)
			continue
		}
		fmt.Fprintf(w.log(), "startup: removed %q, left by a download that stopped\n", path)
	}
}

// isRunning reports whether this worker is running job jobID now.
func (w *Worker) isRunning(jobID int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.running[jobID]
	return ok
}
