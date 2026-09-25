package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
)

// A plex-tv show folder holds, beside its season folders, the show's own
// files: tvshow.nfo, poster.jpg and fanart.jpg (owner ruling #78). They are
// the show's, not a lesson's, like the season folder: drumdrop only ever
// creates one that is missing, never replaces or removes one, and records
// none (a record names only "<show>/Season NN/<name>", library.Record). A
// file the owner put at any name Plex reads for the same slot fills it.
//
// tvshow.nfo is written last, once every image slot has its file or has none
// to get: it marks the show done, so a show that has one costs nothing more,
// no Musora request above all. An owner's own tvshow.nfo marks it done too.
// To have drumdrop write a show's files again, remove tvshow.nfo and the
// images to replace.

// The names drumdrop gives a show's files.
const (
	showNFOName    = "tvshow.nfo"
	showPosterName = "poster.jpg"
	showFanartName = "fanart.jpg"
)

// The names Plex (and XBMCnfoTVImporter) read for a show's poster and its
// background, by stem, compared without case: a poster has one of
// posterExts, a background any extension.
var (
	posterStems = map[string]bool{"poster": true, "folder": true, "show": true}
	posterExts  = map[string]bool{"jpg": true, "jpeg": true, "png": true, "tbn": true}
	fanartStems = map[string]bool{"fanart": true, "art": true, "backdrop": true, "background": true}
)

// showSlots says which of a show folder's slots already hold something.
type showSlots struct {
	poster, fanart, nfo bool
}

// slotsOf is the slots the entries names of a show folder fill. Anything at
// such a name fills its slot, whatever it is: drumdrop never writes there.
func slotsOf(names []string) showSlots {
	var s showSlots
	for _, n := range names {
		lower := strings.ToLower(n)
		stem, ext, _ := strings.Cut(lower, ".")
		switch {
		case lower == showNFOName:
			s.nfo = true
		case posterStems[stem] && posterExts[ext]:
			s.poster = true
		case fanartStems[stem] && ext != "":
			s.fanart = true
		}
	}
	return s
}

// readShowSlots is the slots of the show folder dir holds open.
func readShowSlots(dir *os.Root) (showSlots, error) {
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return showSlots{}, fmt.Errorf("read the show folder %q: %w", dir.Name(), err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return slotsOf(names), nil
}

// openShowDir opens the show folder name in the library folder libraryDir, a
// real folder (openRealDir): never created, and never through a symlink.
func openShowDir(libraryDir, name string) (*os.Root, error) {
	lib, err := os.OpenRoot(libraryDir)
	if err != nil {
		return nil, fmt.Errorf("open the library folder %q: %w", libraryDir, err)
	}
	defer lib.Close()
	return openRealDir(lib, name)
}

// showFiles are the files to create in one show folder, each nil when there
// is none to write.
type showFiles struct {
	poster, fanart []byte
	// nfo is nil while an image slot is still to get (its fetch failed for
	// now), so a later cycle tries it again.
	nfo []byte
}

// writeIn creates each of the files in the show folder dir holds open, whose
// slot is still empty, the nfo last: never over an entry, never through one,
// never truncated (createOnly). A slot filled meanwhile is left as it is. The
// nfo is left out when an image could not be written, so a later cycle tries
// again. It returns the names it created.
func (sf *showFiles) writeIn(dir *os.Root) (created []string, err error) {
	have, err := readShowSlots(dir)
	if err != nil {
		return nil, err
	}
	var errs []error
	write := func(filled bool, name string, data []byte) {
		if filled || data == nil {
			return
		}
		ok, werr := createOnly(dir, name, data)
		if werr != nil {
			errs = append(errs, werr)
		}
		if ok {
			created = append(created, name)
		}
	}
	write(have.poster, showPosterName, sf.poster)
	write(have.fanart, showFanartName, sf.fanart)
	if len(errs) == 0 {
		write(have.nfo, showNFOName, sf.nfo)
	}
	return created, errors.Join(errs...)
}

// write is writeIn for the show folder name in libraryDir, which the
// placement has just made. A folder that is not a real one gets nothing.
func (sf *showFiles) write(libraryDir, name string) error {
	dir, err := openShowDir(libraryDir, name)
	if err != nil {
		return fmt.Errorf("the show's own files were not written: %w", err)
	}
	defer dir.Close()
	if _, err := sf.writeIn(dir); err != nil {
		return fmt.Errorf("the show's own files were not all written: %w", err)
	}
	return nil
}

// createOnly creates the file name in dir holding data, only if nothing is
// there: data goes to a new hidden file beside it (O_EXCL), is flushed, and
// that file is renamed to name without replacing anything (renameAt), so a
// crash never leaves a truncated file at name (which, never replaced, would
// stay truncated), and an entry that appears at name meanwhile is kept.
// created is false, with no error, when an entry is already at name.
func createOnly(dir *os.Root, name string, data []byte) (created bool, err error) {
	path := filepath.Join(dir.Name(), name)
	if _, err := dir.Lstat(name); err == nil {
		return false, nil
	}
	tmp := "." + name + musora.TempSuffix
	if err := dir.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("write %q: a leftover is in the way: %w", path, err)
	}
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return false, fmt.Errorf("write %q: %w", path, err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = syncFile(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = renameAt(dir, tmp, dir, name)
		if errors.Is(err, fs.ErrExist) {
			_ = dir.Remove(tmp)
			return false, nil
		}
	}
	if err == nil {
		err = syncIn(dir, ".")
		return err == nil, wrapWrite(path, err)
	}
	_ = dir.Remove(tmp)
	return false, wrapWrite(path, err)
}

// wrapWrite is err as a failed write of path, or nil.
func wrapWrite(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("write %q: %w", path, err)
}

// errShowOffline stops the show-file step: Musora or its image server could
// not be reached, so no other show's files can be had now either.
var errShowOffline = errors.New("could not reach Musora or its image server")

// offline is err as errShowOffline when it says Musora or its image server
// could not be reached (a request that never got an answer), else err.
func offline(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) || errors.Is(err, musora.ErrUnreachable) {
		return fmt.Errorf("%w: %v", errShowOffline, err)
	}
	return err
}

// showOffline reports whether the show-file step was stopped this cycle.
func (w *Worker) showOffline() bool {
	w.showMu.Lock()
	defer w.showMu.Unlock()
	return w.showDown
}

// setShowOffline stops the show-file step for the rest of this cycle.
func (w *Worker) setShowOffline() {
	w.showMu.Lock()
	defer w.showMu.Unlock()
	w.showDown = true
}

// takeShowOffline reports whether the show-file step was stopped this cycle,
// and starts the next cycle afresh.
func (w *Worker) takeShowOffline() bool {
	w.showMu.Lock()
	defer w.showMu.Unlock()
	down := w.showDown
	w.showDown = false
	return down
}

// unknownShow reports whether the show folder name was found, in this
// process, to be named after nothing its lessons lead to, and marks it so
// when mark is true. Such a folder is not asked about again until drumdrop
// restarts, so it costs no request every cycle.
func (w *Worker) unknownShow(name string, mark bool) bool {
	w.showMu.Lock()
	defer w.showMu.Unlock()
	if mark {
		if w.showUnknown == nil {
			w.showUnknown = map[string]bool{}
		}
		w.showUnknown[name] = true
	}
	return w.showUnknown[name]
}

// showDoc is the Musora document the plex-tv show of a lesson in follow f is
// named after, by plexShow's own branches: the lesson's parent course for an
// instructor follow (or, for a lesson in no course, the followed
// instructor), the followed node for any other follow, and for a lesson with
// no follow its parent course, or the lesson itself. nil when there is none
// (the nfo then names the show and nothing more). lesson may be nil for a
// node follow; the node is then asked for even if it is the lesson.
func (w *Worker) showDoc(f database.Follow, lesson *musora.Lesson) (*musora.Lesson, error) {
	switch {
	case hasFollow(f) && f.Kind == "instructor":
		if lessonParentTitle(lesson) != "" {
			return w.resolveShowDoc(int(lesson.ParentContentData[0].ID))
		}
		return instructorDoc(f, lesson), nil
	case hasFollow(f):
		if !f.RailcontentID.Valid {
			return nil, nil
		}
		id := int(f.RailcontentID.Int64)
		if lesson != nil && lesson.ID == id {
			return lesson, nil
		}
		return w.resolveShowDoc(id)
	case lessonParentTitle(lesson) != "":
		return w.resolveShowDoc(int(lesson.ParentContentData[0].ID))
	}
	return lesson, nil
}

// resolveShowDoc asks Musora for the document id (nil for no id).
func (w *Worker) resolveShowDoc(id int) (*musora.Lesson, error) {
	if id <= 0 {
		return nil, nil
	}
	doc, err := w.Resolver.Resolve(id, w.PermIDs)
	if err != nil {
		return nil, fmt.Errorf("asking Musora for the show's course %d: %w", id, err)
	}
	return doc, nil
}

// instructorDoc is the show document of a lesson in no course of instructor
// follow f: the lesson's entry for that instructor (its coach card, its
// biography), or nil when the lesson has none.
func instructorDoc(f database.Follow, lesson *musora.Lesson) *musora.Lesson {
	if lesson == nil || !f.Slug.Valid {
		return nil
	}
	for _, in := range lesson.Instructors {
		if string(in.Slug) == f.Slug.String {
			return &musora.Lesson{Title: in.Name, Description: string(in.Biography), Brand: f.Brand, Instructors: []musora.Instructor{in}}
		}
	}
	return nil
}

// fetchShowFiles fetches what a show folder whose slots are have is missing,
// for the show named title after doc (musora.ShowArt picks the images). A
// slot with no image is left empty; so is one whose image is gone
// (musora.ErrImageMissing), and the nfo is still written. One whose fetch
// failed otherwise leaves the nfo out, so a later cycle tries again. An
// image server that could not be reached returns errShowOffline.
func (w *Worker) fetchShowFiles(ctx context.Context, title string, doc *musora.Lesson, have showSlots) (*showFiles, error) {
	poster, fanart := musora.ShowArt(doc)
	sf := &showFiles{}
	complete := true
	fetched := map[string][]byte{}
	get := func(filled bool, u string) ([]byte, error) {
		if filled || u == "" {
			return nil, nil
		}
		if data, ok := fetched[u]; ok {
			return data, nil
		}
		data, err := w.Images.FetchJPEG(ctx, u)
		switch {
		case err == nil:
			fetched[u] = data
			return data, nil
		case errors.Is(err, musora.ErrImageMissing):
			fmt.Fprintf(w.log(), "  ⚠ show %q: no image from %s: %v\n", title, u, err)
			return nil, nil
		}
		if err = offline(err); errors.Is(err, errShowOffline) {
			return nil, err
		}
		complete = false
		fmt.Fprintf(w.log(), "  ⚠ show %q: the image %s could not be fetched now: %v\n", title, u, err)
		return nil, nil
	}
	var err error
	if sf.poster, err = get(have.poster, poster); err != nil {
		return nil, err
	}
	if sf.fanart, err = get(have.fanart, fanart); err != nil {
		return nil, err
	}
	if complete && !have.nfo {
		sf.nfo = []byte(musora.BuildShowNFO(title, doc))
	}
	return sf, nil
}

// showFilesFor is what the plex-tv placement of run, into the show named
// show, creates in the show folder before the episode: the files its slots
// are missing, or nil when there are none to write (the show is done, a
// show-file step was stopped this cycle, or drumdrop is stopping). The show
// folder need not exist yet (the placement makes it). Whatever goes wrong
// here is logged and never stops the placement: the cycle's show-file step
// tries again.
func (w *Worker) showFilesFor(ctx context.Context, run *jobRun, show string) *showFiles {
	if w.Images == nil || ctx.Err() != nil || w.showOffline() {
		return nil
	}
	id := run.job.RailcontentID
	name := musora.Sanitize(show)
	have := showSlots{}
	if dir, err := openShowDir(w.Cfg.LibraryDir, name); err == nil {
		have, err = readShowSlots(dir)
		dir.Close()
		if err != nil {
			fmt.Fprintf(w.log(), "  ⚠ %d: the show's own files were not written: %v\n", id, err)
			return nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(w.log(), "  ⚠ %d: the show's own files were not written: %v\n", id, err)
		return nil
	}
	if have.nfo {
		return nil
	}
	doc, err := w.showDoc(run.follow, run.lesson)
	var sf *showFiles
	if err == nil {
		sf, err = w.fetchShowFiles(ctx, show, doc, have)
	}
	if err != nil {
		if err = offline(err); errors.Is(err, errShowOffline) {
			w.setShowOffline()
		}
		fmt.Fprintf(w.log(), "  ⚠ %d: the show's own files were not written: %v\n", id, err)
		return nil
	}
	return sf
}

// maxShowTries bounds how many of a show folder's lessons EnsureShowFiles
// asks Musora about to learn what the show is.
const maxShowTries = 3

// EnsureShowFiles is the cycle's show-file step in the plex-tv layout: every
// show folder a lesson row files its episodes in, under the library folder
// configured now, that has no tvshow.nfo gets its missing files (see the top
// of this file). It covers the shows no download of this cycle placed into,
// the ones already in the library above all. It never creates a folder, so a
// library that is not there (not mounted) gets nothing, and it writes into a
// real folder only (never through a symlink). A show that has its tvshow.nfo
// costs no request. When Musora or its image server can not be reached, the
// step stops until the next cycle. Nothing here fails the cycle: each
// problem is logged.
func (w *Worker) EnsureShowFiles(ctx context.Context) {
	if w.Cfg.Layout != LayoutPlexTV || w.Cfg.LibraryDir == "" || w.Images == nil {
		return
	}
	if w.takeShowOffline() {
		fmt.Fprintln(w.log(), "  ⚠ show files: skipped this cycle, Musora could not be reached")
		return
	}
	rows, err := w.Store.ListLessonsWithFiles(ctx)
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ show files: the lessons could not be read: %v\n", err)
		return
	}
	shows := showFolders(w.Cfg.LibraryDir, rows)
	names := make([]string, 0, len(shows))
	for n := range shows {
		names = append(names, n)
	}
	sort.Strings(names)
	lib, err := os.OpenRoot(w.Cfg.LibraryDir)
	if err != nil {
		return // no library folder (not mounted): nothing to write into
	}
	defer lib.Close()
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}
		if w.unknownShow(name, false) {
			continue
		}
		if err := w.ensureShow(ctx, lib, name, shows[name]); errors.Is(err, errShowOffline) {
			fmt.Fprintf(w.log(), "  ⚠ show files: stopped until the next cycle: %v\n", err)
			return
		}
	}
}

// ensureShow is EnsureShowFiles for the show folder name in the library
// folder lib holds open, whose episodes the lesson rows rows file. Only
// errShowOffline is returned: every other problem is logged.
func (w *Worker) ensureShow(ctx context.Context, lib *os.Root, name string, rows []database.Lesson) error {
	dir, err := openRealDir(lib, name)
	if err != nil {
		return nil // gone, or not a real folder: nothing is written
	}
	defer dir.Close()
	have, err := readShowSlots(dir)
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ show files: %v\n", err)
		return nil
	}
	if have.nfo {
		return nil
	}
	title, doc, known, err := w.showOf(ctx, name, rows)
	var sf *showFiles
	if err == nil && known {
		sf, err = w.fetchShowFiles(ctx, title, doc, have)
	}
	if ctx.Err() != nil {
		return nil // stopping: the next cycle picks the show up again
	}
	if err != nil {
		if err = offline(err); !errors.Is(err, errShowOffline) {
			fmt.Fprintf(w.log(), "  ⚠ show files %q: %v\n", name, err)
		}
		return err
	}
	if !known {
		w.unknownShow(name, true)
		fmt.Fprintf(w.log(), "  ⚠ show files %q: none of its lessons leads to a show of that name; not asked again until drumdrop restarts\n", name)
		return nil
	}
	created, err := sf.writeIn(dir)
	for _, c := range created {
		fmt.Fprintf(w.log(), "  + show %q: %s\n", name, c)
	}
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ show files %q: %v\n", name, err)
	}
	return nil
}

// showOf works out, from up to maxShowTries of the rows filed in the show
// folder name, the show's name (as plexShow gives it) and its document
// (showDoc). known is false when none of them leads to a show that is named
// name: nothing proves what the folder is.
func (w *Worker) showOf(ctx context.Context, name string, rows []database.Lesson) (title string, doc *musora.Lesson, known bool, err error) {
	for i, row := range rows {
		if i == maxShowTries {
			break
		}
		if err := ctx.Err(); err != nil {
			return "", nil, false, err
		}
		f, ferr := w.rowFollow(ctx, row)
		if ferr != nil {
			return "", nil, false, ferr
		}
		var lesson *musora.Lesson
		if !hasFollow(f) || f.Kind == "instructor" {
			// The show is named after the lesson's course: ask for the lesson.
			l, rerr := w.Resolver.Resolve(row.RailcontentID, w.PermIDs)
			if rerr != nil {
				return "", nil, false, fmt.Errorf("asking Musora for lesson %d: %w", row.RailcontentID, rerr)
			}
			if l == nil {
				continue
			}
			lesson = l
		}
		title = plexShow(f, database.Job{RailcontentID: row.RailcontentID}, lesson)
		if musora.Sanitize(title) != name {
			continue
		}
		doc, err = w.showDoc(f, lesson)
		return title, doc, err == nil, err
	}
	return "", nil, false, nil
}

// rowFollow is the follow of lesson row, or a zero Follow when it has none
// (or it was removed), as loadFollow reads a job's.
func (w *Worker) rowFollow(ctx context.Context, row database.Lesson) (database.Follow, error) {
	if !row.FollowID.Valid {
		return database.Follow{}, nil
	}
	f, err := w.Store.GetFollow(ctx, row.FollowID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return database.Follow{}, nil
	}
	if err != nil {
		return database.Follow{}, fmt.Errorf("the follow of lesson %d could not be read: %w", row.RailcontentID, err)
	}
	return f, nil
}

// showFolders groups the lesson rows by the show folder, in the library
// folder libraryDir configured now, that they file episodes in: the first
// part of a record's entries, or for a row with no record, the folder its
// season folder is in. A row filed anywhere else (downloads, the default
// layout, a library the setting no longer points at) is in no show.
func showFolders(libraryDir string, rows []database.Lesson) map[string][]database.Lesson {
	shows := map[string][]database.Lesson{}
	for _, row := range rows {
		if name := showFolderOf(libraryDir, row); name != "" {
			shows[name] = append(shows[name], row)
		}
	}
	for _, r := range shows {
		sort.Slice(r, func(i, j int) bool { return r[i].RailcontentID < r[j].RailcontentID })
	}
	return shows
}

// showFolderOf is the show folder row files its episodes in (showFolders),
// or "".
func showFolderOf(libraryDir string, row database.Lesson) string {
	entries, recorded, err := library.Record(row)
	switch {
	case err != nil:
		return ""
	case recorded && len(entries) > 0:
		first, _, _ := strings.Cut(entries[0], "/")
		return validShowName(first)
	case !row.OutputDir.Valid || !library.IsSeasonDir(row.OutputDir.String) || !library.Inside(libraryDir, row.OutputDir.String):
		return ""
	}
	rel, err := filepath.Rel(filepath.Clean(libraryDir), filepath.Clean(row.OutputDir.String))
	if err != nil || filepath.Dir(filepath.Dir(rel)) != "." {
		return ""
	}
	return validShowName(filepath.Dir(rel))
}

// validShowName is name, a single path part, or "" when it is none, or the
// private folder.
func validShowName(name string) string {
	if !filepath.IsLocal(name) || name == "." || strings.ContainsAny(name, `/\`) || isPrivateRel(name) {
		return ""
	}
	return name
}
