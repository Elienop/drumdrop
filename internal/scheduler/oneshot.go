package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
)

// oneShotJobID is the job id a one-shot download's placement files its
// set-aside entries under (replaced-0): the jobs table numbers from 1, so it
// is never a daemon job's.
const oneShotJobID = 0

// maxOneShotFolders bounds the search for a free private folder name.
const maxOneShotFolders = 1000

// DownloadOneShot is the one-shot "drumdrop <url>" download of one lesson,
// which has no database: it downloads the lesson into a private folder
// (<root>/.drumdrop-in-progress/run-<pid>-<k>/), and only once d.Download
// succeeded places it in <root>/<courseRel>/NN - Title, the same placement
// the worker makes (placeLessonFolder), with no records to consult. A failed
// or canceled download, or one stopped by ctx before it is placed, changes
// nothing at the destination: yt-dlp's --force-overwrites deletes only the
// private copy. A placement replaces only the entries at the names the
// download produced, and keeps every other entry of the lesson's folder.
//
// o.Dir and o.Root are set here; the rest of o is passed to d as is. It
// returns the entries the placement replaced. The private folder is removed
// whatever happens; a kill that stops the process leaves it, hidden from Plex
// by the private root's .plexignore, and nothing sweeps a run- folder (the
// daemon's sweep only knows its own job- folders).
func DownloadOneShot(ctx context.Context, d Downloader, l *musora.Lesson, root, courseRel string, o musora.DownloadOpts) (replaced []string, err error) {
	if l == nil {
		return nil, errors.New("no lesson to download")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", root, err)
	}
	rel := filepath.Join(courseRel, filepath.Base(lessonDir("", o.Index, l.Title)))
	if !filepath.IsLocal(rel) || filepath.Dir(rel) == "." || isPrivateRel(rel) {
		return nil, fmt.Errorf("refusing to download into %q under %q: not a lesson folder inside it", rel, abs)
	}
	private, name, err := startOneShot(abs)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr := dropOneShot(abs, name); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}()

	o.Dir, o.Root = private, private
	if err := d.Download(ctx, l, o); err != nil {
		return nil, err
	}
	// A download stopped once it had finished is not placed either: an
	// interrupt means leave the destination as it is.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	privateLesson := filepath.Join(private, filepath.Base(rel))
	cleanupPartials(privateLesson)
	src, err := openScratch(abs, privateLesson)
	if err != nil {
		return nil, fmt.Errorf("not placed: %w", err)
	}
	defer src.close()
	claims, err := library.NewClaims("", nil)
	if err != nil {
		return nil, fmt.Errorf("not placed: %w", err)
	}
	pl, err := placeLessonFolder(abs, rel, src, database.Lesson{}, claims, nil, oneShotJobID)
	if err != nil {
		return nil, fmt.Errorf("not placed: %w", err)
	}
	entries, cerr := pl.commit()
	for _, e := range entries {
		replaced = append(replaced, e.path)
	}
	if cerr != nil {
		return replaced, fmt.Errorf("placed, but what it replaced could not all be removed: %w", cerr)
	}
	return replaced, nil
}

// startOneShot makes a new private folder for a one-shot download in root's
// private root, under a name nothing holds yet (Mkdir refuses an existing
// one, so two runs never share a folder, and a folder a killed run left is
// never reused). It returns its path and its name there.
func startOneShot(root string) (path, name string, err error) {
	s, err := openStaging(root)
	if err != nil {
		return "", "", err
	}
	defer s.Close()
	prefix := "run-" + strconv.Itoa(os.Getpid()) + "-"
	for k := 1; k <= maxOneShotFolders; k++ {
		name = prefix + strconv.Itoa(k)
		err := s.Mkdir(name, 0o755)
		if err == nil {
			return filepath.Join(s.Name(), name), name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", "", fmt.Errorf("create %q: %w", filepath.Join(s.Name(), name), err)
		}
	}
	return "", "", fmt.Errorf("create a private folder in %q: %d run- folders of this process id are already there", s.Name(), maxOneShotFolders)
}

// dropOneShot removes the one-shot private folder name from root's private
// root.
func dropOneShot(root, name string) error {
	s, err := openExistingStaging(root)
	if err == nil {
		defer s.Close()
		err = s.RemoveAll(name)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the private download folder %q could not be removed: %w", filepath.Join(root, privateRootName, name), err)
	}
	return nil
}
