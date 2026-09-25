package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
)

// The one-time rename of the plex-tv episode files placed before owner
// ruling #78: Plex reads an episode's image and nfo only under a video's own
// name, so
//   - a lesson's "<base>-poster.jpg" becomes "<base>.jpg" (ruling 4);
//   - a song's "<base>-poster.jpg" (or "<base>.jpg") and "<base>.nfo" become
//     one "<base> [Label].jpg" and "<base> [Label].nfo" per version video
//     (ruling 5b), the names episodeNames gives a new download.
//
// A lesson with no record (placed before v0.8.0) is recorded first, with
// exactly what its delete would remove today (library.Claims.Plan), and only
// when it has something to rename (ruling 4, the exception BACKLOG D65
// describes). So its files are only ever renamed once they are recorded, and
// a lesson without a record always keeps the shape the legacy name grammar
// reads.
//
// Each file is renamed by copy, never in place: every new name is created
// beside the old one (createOnly: flushed, never over anything), the record
// then swaps the old names for the new ones (compare-and-swap, refused while
// a delete holds the lesson), and only then are the old files removed. So at
// every point of a crash both names hold the file, the record names only
// files that are there, and nothing is lost. What a crash can leave is the
// old file beside its recorded copy, unrecorded; the next process's pass
// removes it once it has proved it an exact copy of the recorded one that no
// lesson claims (removeLeftovers). A new name that is already taken by a file
// that is not such a copy, or that another lesson claims, is left alone and
// the old file stays, recorded: a song's old files go only once every
// version has its own.
//
// The pass runs at the end of every cycle (Daemon.RunOnce) and after a sync,
// so a library that was not mounted is done once it is; it never creates a
// folder. Once a lesson has nothing left to rename it is not looked at again
// in this process, so a finished library costs one read of the lesson rows
// per cycle and nothing on disk.

// maxRenameBytes bounds what the pass reads of a file it copies: an episode's
// image or nfo is far smaller.
const maxRenameBytes = 64 << 20

// RenameEpisodeFiles is the one-time rename (see above) for every lesson row
// with files, in the plex-tv layout with a library folder. Nothing here
// fails the cycle: each problem is logged, and the next cycle tries again.
func (w *Worker) RenameEpisodeFiles(ctx context.Context) {
	if w.Cfg.Layout != LayoutPlexTV || w.Cfg.LibraryDir == "" {
		return
	}
	rows, err := w.Store.ListLessonsWithFiles(ctx)
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ episode files: the lessons could not be read: %v\n", err)
		return
	}
	lib, err := os.OpenRoot(w.Cfg.LibraryDir)
	if err != nil {
		return // no library folder (not mounted): nothing to rename
	}
	defer lib.Close()
	var claims *library.Claims
	for i, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if row.Deleting || w.renameDone(row.RailcontentID, false) {
			continue
		}
		if claims == nil {
			if claims, err = library.NewClaims(w.Cfg.LibraryDir, rows); err != nil {
				fmt.Fprintf(w.log(), "  ⚠ episode files: not renamed, the lessons' files are not known: %v\n", err)
				return
			}
		}
		if now := w.renameRow(ctx, lib, claims, row); now != nil {
			// The row records other names now: what every lesson claims changed.
			rows[i], claims = *now, nil
		}
	}
}

// renameDone reports whether lesson id was found, in this process, to have
// nothing left to rename (or nothing the pass can rename), and marks it so
// when mark is true.
func (w *Worker) renameDone(id int, mark bool) bool {
	w.showMu.Lock()
	defer w.showMu.Unlock()
	if mark {
		if w.renamed == nil {
			w.renamed = map[int]bool{}
		}
		w.renamed[id] = true
	}
	return w.renamed[id]
}

// renameRow is RenameEpisodeFiles for one row, which no delete held when it
// was read: a legacy row is recorded first (recordLegacy). It returns the
// row as the store now has it when the pass changed its record, else nil.
func (w *Worker) renameRow(ctx context.Context, lib *os.Root, c *library.Claims, row database.Lesson) *database.Lesson {
	entries, recorded, err := library.Record(row)
	switch {
	case err != nil:
		w.renameDone(row.RailcontentID, true)
		return nil
	case recorded:
		return w.renameRecorded(ctx, lib, c, row, entries)
	case !row.OutputDir.Valid || !library.IsSeasonDir(row.OutputDir.String):
		w.renameDone(row.RailcontentID, true) // not in a season folder: nothing of plex-tv's
		return nil
	}
	return w.recordLegacy(ctx, lib, c, row)
}

// recordLegacy records the files of row, a lesson with no record filed in a
// season folder, as its delete would remove them today (library.Claims.Plan:
// the exact entries the legacy name grammar gives it that no other lesson
// claims), then renames them (renameRecorded). It records nothing when there
// is nothing to rename, when the lesson's files can't be told (Plan fails),
// or when its season folder is not there under the library folder (not
// mounted, say; the next cycle looks again).
func (w *Worker) recordLegacy(ctx context.Context, lib *os.Root, c *library.Claims, row database.Lesson) *database.Lesson {
	id := row.RailcontentID
	root := absLibrary(w.Cfg.LibraryDir)
	seasonRel := filepath.Join(filepath.Base(filepath.Dir(filepath.Clean(row.OutputDir.String))), filepath.Base(row.OutputDir.String))
	season, err := openSeason(lib, seasonRel)
	if err != nil {
		return nil
	}
	season.Close()
	plan, err := c.Plan(row)
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ episode files %d: not renamed: %v\n", id, err)
		w.renameDone(id, true)
		return nil
	}
	names := make([]string, 0, len(plan.Remove))
	for _, p := range plan.Remove {
		names = append(names, filepath.Base(p))
	}
	if len(renamesOf(names)) == 0 {
		w.claimedLeftAlone(id, plan.Kept)
		w.renameDone(id, true)
		return nil
	}
	entries, err := library.EntriesFor(root, plan.Remove)
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ episode files %d: not renamed: %v\n", id, err)
		w.renameDone(id, true)
		return nil
	}
	if err := w.Store.SwapLibraryEntries(ctx, row, entries); err != nil {
		w.swapRefused(id, err)
		return nil
	}
	row.LibraryEntries = database.EncodeLibraryEntries(entries)
	fmt.Fprintf(w.log(), "  ✎ episode files %d: recorded %d file(s) to rename\n", id, len(entries))
	w.claimedLeftAlone(id, plan.Kept)
	if now := w.renameRecorded(ctx, lib, c, row, entries); now != nil {
		return now
	}
	return &row
}

// claimedLeftAlone says, once per process (the row is then recorded, or done
// for this process), which of legacy lesson id's files the pass left
// unrecorded and unconverted because another lesson's name match claims them
// too (library.Entries.Kept): their per-version or renamed files come only
// with a re-download.
func (w *Worker) claimedLeftAlone(id int, kept []string) {
	if len(kept) == 0 {
		return
	}
	names := make([]string, 0, len(kept))
	for _, p := range kept {
		names = append(names, filepath.Base(p))
	}
	fmt.Fprintf(w.log(), "  ⚠ episode files %d: not renamed: %q are claimed by another lesson too\n", id, names)
}

// swapRefused logs a record write the store refused. A row that is gone is
// not looked at again; any other refusal (a delete holds it, it recorded
// other files meanwhile, the database failed) is left to the next cycle.
func (w *Worker) swapRefused(id int, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		w.renameDone(id, true)
		return
	}
	fmt.Fprintf(w.log(), "  ⚠ episode files %d: not renamed this time: %v\n", id, err)
}

// fileRename is one file of an episode to take new names: from, a name in
// the season folder, becomes every name of to.
type fileRename struct {
	from string
	to   []string
}

// renamesOf is what the one-time rename does in a season folder whose
// entries (of one lesson) are names:
//   - "<base>-poster.jpg" becomes the episode's image by episodeNames:
//     "<base>.jpg", or for a song one "<base> [L].jpg" per version;
//   - for a song, "<base>.jpg" becomes one image per version too, and
//     "<base>.nfo" one nfo per version.
//
// A song is a base whose names hold version videos "<base> [L].mp4" and no
// "<base>.mp4": these are one lesson's own entries (its record, or what the
// legacy grammar gives it), so the versions are its own.
func renamesOf(names []string) []fileRename {
	var out []fileRename
	for _, n := range names {
		base, ext, ok := sharedFile(n)
		if !ok {
			continue
		}
		labels := songVersions(base, names)
		if ext != musora.PosterSuffix && len(labels) == 0 {
			continue // a lesson's own "<base>.jpg" or ".nfo" already has its name
		}
		kind := ext
		if kind == library.EpisodeImageSuffix {
			kind = musora.PosterSuffix
		}
		r := fileRename{from: n}
		for _, s := range episodeNames(kind, labels) {
			r.to = append(r.to, base+s)
		}
		out = append(out, r)
	}
	return out
}

// sharedFile splits name into an episode base and the suffix of a file the
// one-time rename may rename: the image under its old name "-poster.jpg", or
// "<base>.jpg" and "<base>.nfo" (renamed only for a song).
func sharedFile(name string) (base, ext string, ok bool) {
	for _, ext := range []string{musora.PosterSuffix, library.EpisodeImageSuffix, ".nfo"} {
		if base, ok := strings.CutSuffix(name, ext); ok && base != "" {
			return base, ext, true
		}
	}
	return "", "", false
}

// songVersions is the versions of the song base among names (library.Versions),
// or nil when base is not a song (it has a "<base>.mp4").
func songVersions(base string, names []string) []string {
	if slices.Contains(names, base+".mp4") {
		return nil
	}
	return library.Versions(base, names)
}

// seasonGroup is a lesson's recorded entries in one season folder: the
// folder, relative to the library ("<show>/Season NN"), and the entries'
// names in it.
type seasonGroup struct {
	rel   string
	names []string
}

// groupBySeason groups record entries ("<show>/Season NN/<name>") by season
// folder, in the record's order.
func groupBySeason(entries []string) []seasonGroup {
	var out []seasonGroup
	for _, e := range entries {
		dir, name := path.Split(e)
		dir = strings.TrimSuffix(dir, "/")
		i := slices.IndexFunc(out, func(g seasonGroup) bool { return g.rel == dir })
		if i < 0 {
			out = append(out, seasonGroup{rel: dir})
			i = len(out) - 1
		}
		out[i].names = append(out[i].names, name)
	}
	return out
}

// renameRecorded is the one-time rename of the files row's record names
// (entries). With nothing to rename it looks for what a crash left behind
// (removeLeftovers), and once that is clean the row is done for this
// process. It returns the row as the store now has it when it swapped the
// record, else nil.
func (w *Worker) renameRecorded(ctx context.Context, lib *os.Root, c *library.Claims, row database.Lesson, entries []string) *database.Lesson {
	id := row.RailcontentID
	var (
		work   []seasonWork
		opened []*os.Root
	)
	defer func() {
		for _, s := range opened {
			s.Close()
		}
	}()
	clean := true
	for _, g := range groupBySeason(entries) {
		season, err := openSeason(lib, filepath.FromSlash(g.rel))
		if err != nil {
			clean = false // not there (not mounted): the next cycle looks again
			continue
		}
		opened = append(opened, season)
		if jobs := renamesOf(g.names); len(jobs) > 0 {
			work = append(work, w.createNames(c, season, g, id, jobs))
		} else if !w.removeLeftovers(c, season, g) {
			clean = false
		}
	}
	var done []seasonWork
	for _, sw := range work {
		if len(sw.done) > 0 {
			done = append(done, sw)
		}
	}
	if len(done) == 0 {
		if clean && len(work) == 0 {
			w.renameDone(id, true)
		}
		return nil
	}
	next := swapNames(entries, done)
	if err := w.Store.SwapLibraryEntries(ctx, row, next); err != nil {
		for _, sw := range done {
			sw.takeBack()
		}
		w.swapRefused(id, err)
		return nil
	}
	for _, sw := range done {
		sw.retire(w, id)
	}
	row.LibraryEntries = database.EncodeLibraryEntries(next)
	return &row
}

// seasonWork is the rename's new names made in one season folder, not yet
// recorded: the jobs whose every new name is there (done), and the files
// this pass created for them (created, by name, as they were created: an
// undo removes only those).
type seasonWork struct {
	season  *os.Root
	rel     string
	done    []fileRename
	created map[string]os.FileInfo
	// old is each done job's old file as it was read, so it is removed only
	// if it is still that file.
	old map[string]os.FileInfo
}

// createNames creates, in season (the group g's folder), every new name of
// each job the lesson id's files can take: from must be a regular file; each
// new name is created with its content (createOnly), or is already the
// lesson's (its record names it), or already holds exactly that content and
// no lesson claims it (a copy a crash left before the record said so). A
// job one of whose names is taken otherwise is left as it is, its old file
// recorded, and said so once: the lesson is not looked at again in this
// process. What it created for such a job is taken back.
func (w *Worker) createNames(c *library.Claims, season *os.Root, g seasonGroup, id int, jobs []fileRename) seasonWork {
	sw := seasonWork{season: season, rel: g.rel, created: map[string]os.FileInfo{}, old: map[string]os.FileInfo{}}
	for _, j := range jobs {
		info, data, err := readRegular(season, j.from)
		if err != nil {
			fmt.Fprintf(w.log(), "  ⚠ episode files %d: %q not renamed: %v\n", id, path.Join(g.rel, j.from), err)
			w.renameDone(id, true)
			continue
		}
		mine := map[string]os.FileInfo{}
		why := ""
		for _, t := range j.to {
			if why = w.newName(c, season, g, id, t, data, mine); why != "" {
				break
			}
		}
		if why != "" {
			for n, fi := range mine {
				removeIfSame(season, n, fi)
			}
			fmt.Fprintf(w.log(), "  ⚠ episode files %d: %q not renamed: %s\n", id, path.Join(g.rel, j.from), why)
			w.renameDone(id, true)
			continue
		}
		for n, fi := range mine {
			sw.created[n] = fi
		}
		sw.old[j.from] = info
		sw.done = append(sw.done, j)
	}
	return sw
}

// newName readies the new name t of a file holding data, in season, for
// lesson id: "" when t holds data now (created here, recorded in mine), or
// already was the lesson's (its record names it and it is there) or an exact
// unclaimed copy; otherwise why not.
func (w *Worker) newName(c *library.Claims, season *os.Root, g seasonGroup, id int, t string, data []byte, mine map[string]os.FileInfo) string {
	if slices.Contains(g.names, t) {
		if fi, err := season.Lstat(t); err == nil && fi.Mode().IsRegular() {
			return "" // the lesson's own already, and there
		}
		// The record names it, but it is not there (the owner removed it):
		// it is made like any other name, so the old file is never removed
		// while the record names a file that is missing.
	}
	abs := filepath.Join(season.Name(), t)
	if ids, err := c.Claimants(abs, id, true); err != nil || len(ids) > 0 {
		return fmt.Sprintf("%q is another lesson's (%v, %v)", t, ids, err)
	}
	created, err := createOnly(season, t, data)
	if err != nil {
		return err.Error()
	}
	if created {
		fi, err := season.Lstat(t)
		if err != nil {
			return err.Error()
		}
		mine[t] = fi
		return ""
	}
	if _, have, err := readRegular(season, t); err != nil || !bytes.Equal(have, data) {
		return fmt.Sprintf("%q is already there", t)
	}
	return "" // an exact copy a crash left before the record named it
}

// takeBack removes every file the pass created in this folder: the record
// did not take them.
func (sw seasonWork) takeBack() {
	for n, fi := range sw.created {
		removeIfSame(sw.season, n, fi)
	}
}

// retire removes the old file of every done job, now that the record names
// the new ones instead, if it is still the file that was read. One that
// can't be removed stays beside its copy, unrecorded, for the next pass's
// leftover check (removeLeftovers).
func (sw seasonWork) retire(w *Worker, id int) {
	for _, j := range sw.done {
		if err := removeIfSame(sw.season, j.from, sw.old[j.from]); err != nil {
			fmt.Fprintf(w.log(), "  ⚠ episode files %d: %q is renamed, but the old file could not be removed: %v\n", id, path.Join(sw.rel, j.from), err)
			continue
		}
		fmt.Fprintf(w.log(), "  ↻ episode files %d: %q -> %q\n", id, path.Join(sw.rel, j.from), j.to)
	}
}

// swapNames is entries with every done job's old name (in its season folder)
// replaced, in place, by its new names the record does not name yet.
func swapNames(entries []string, done []seasonWork) []string {
	repl := map[string][]string{}
	for _, sw := range done {
		for _, j := range sw.done {
			var to []string
			for _, t := range j.to {
				if e := sw.rel + "/" + t; !slices.Contains(entries, e) {
					to = append(to, e)
				}
			}
			repl[sw.rel+"/"+j.from] = to
		}
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if to, ok := repl[e]; ok {
			out = append(out, to...)
			continue
		}
		out = append(out, e)
	}
	return out
}

// removeLeftovers removes, from season (the group g's folder, whose names
// the lesson records and none of which is to be renamed), each old file a
// crash left beside its recorded copy: "<base>-poster.jpg" beside a recorded
// "<base>.jpg", and a song's "<base>-poster.jpg", "<base>.jpg" and
// "<base>.nfo" beside a recorded file of one of its versions. Only an
// unrecorded regular file that holds exactly the recorded copy's content
// and that no lesson claims goes, so nothing but a duplicate is ever
// removed; a missing copy (a library that is gone) removes nothing. It
// reports whether nothing is left to look at.
func (w *Worker) removeLeftovers(c *library.Claims, season *os.Root, g seasonGroup) bool {
	clean := true
	for _, l := range leftoversOf(g.names) {
		if _, err := season.Lstat(l.name); err != nil {
			continue
		}
		_, want, err := readRegular(season, l.copyOf)
		if err != nil {
			continue
		}
		info, have, err := readRegular(season, l.name)
		if err != nil || !bytes.Equal(have, want) {
			continue
		}
		if ids, err := c.Claimants(filepath.Join(season.Name(), l.name), 0, true); err != nil || len(ids) > 0 {
			continue
		}
		if err := removeIfSame(season, l.name, info); err != nil {
			fmt.Fprintf(w.log(), "  ⚠ episode files: the renamed file's old copy %q could not be removed: %v\n", path.Join(g.rel, l.name), err)
			clean = false
			continue
		}
		fmt.Fprintf(w.log(), "  ↻ episode files: removed %q, an old copy of %q\n", path.Join(g.rel, l.name), path.Join(g.rel, l.copyOf))
	}
	return clean
}

// leftover is an old name the one-time rename may have left (name) beside
// the recorded file it was copied to (copyOf).
type leftover struct {
	name, copyOf string
}

// leftoversOf is every leftover the rename can have left among a lesson's
// recorded names: for each recorded episode image "<base>.jpg" (a song
// version's included, where it is never there), its "<base>-poster.jpg"; for
// each song base (a base
// "<base> [L].mp4" is cut at, with no "<base>.mp4"), its "<base>-poster.jpg"
// and "<base>.jpg" beside its first version's image, and its "<base>.nfo"
// beside its first version's nfo. A name the lesson records is never left:
// it is renamed instead, and removeLeftovers' claim check names the lesson.
func leftoversOf(names []string) []leftover {
	var out []leftover
	add := func(name, copyOf string) {
		if slices.Contains(names, copyOf) {
			out = append(out, leftover{name: name, copyOf: copyOf})
		}
	}
	for _, n := range names {
		if base, ok := strings.CutSuffix(n, library.EpisodeImageSuffix); ok && !strings.HasSuffix(n, musora.PosterSuffix) {
			add(base+musora.PosterSuffix, n)
		}
	}
	for _, base := range songBases(names) {
		labels := songVersions(base, names)
		first := base + " [" + labels[0] + "]"
		add(base+musora.PosterSuffix, first+library.EpisodeImageSuffix)
		add(base+library.EpisodeImageSuffix, first+library.EpisodeImageSuffix)
		add(base+".nfo", first+".nfo")
	}
	return out
}

// songBases is every base a version video "<base> [L].mp4" among names can
// be cut at (at any " [" of its name), that is a song (songVersions finds
// its versions), once each.
func songBases(names []string) []string {
	var out []string
	for _, n := range names {
		stem, ok := strings.CutSuffix(n, "].mp4")
		if !ok {
			continue
		}
		for i := 0; i+2 <= len(stem); i++ {
			if stem[i:i+2] != " [" {
				continue
			}
			if base := stem[:i]; base != "" && !slices.Contains(out, base) && len(songVersions(base, names)) > 0 {
				out = append(out, base)
			}
		}
	}
	return out
}

// openSeason opens the season folder rel ("<show>/Season NN") inside the
// library folder lib holds open, each level a real folder (openRealDir):
// never created, never through a symlink.
func openSeason(lib *os.Root, rel string) (*os.Root, error) {
	show, err := openRealDir(lib, filepath.Dir(rel))
	if err != nil {
		return nil, err
	}
	defer show.Close()
	return openRealDir(show, filepath.Base(rel))
}

// openRegular opens name in dir to read, never waiting: O_NONBLOCK, so a
// FIFO swapped in for a regular file after readRegular's Lstat opens at once
// (its Stat then differs) instead of blocking the cycle until a writer
// comes. O_NONBLOCK changes nothing for a regular file, and Windows ignores
// it. A package variable so a test can swap the file in that window.
var openRegular = func(dir *os.Root, name string) (*os.File, error) {
	return dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// readRegular reads name in dir: a regular file (not through a symlink),
// read up to maxRenameBytes, with its Lstat.
func readRegular(dir *os.Root, name string) (os.FileInfo, []byte, error) {
	info, err := dir.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%q is not a regular file", name)
	}
	f, err := openRegular(dir, name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !os.SameFile(st, info) {
		return nil, nil, fmt.Errorf("%q changed while it was read", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRenameBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxRenameBytes {
		return nil, nil, fmt.Errorf("%q is larger than %d bytes", name, maxRenameBytes)
	}
	return info, data, nil
}

// removeIfSame removes name from dir if it is still the file info described.
// Gone already is not an error.
func removeIfSame(dir *os.Root, name string, info os.FileInfo) error {
	now, err := dir.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info == nil || !os.SameFile(now, info) {
		return fmt.Errorf("%q was replaced meanwhile; left as it is", name)
	}
	return dir.Remove(name)
}

// absLibrary is the library folder as the lessons' claims read it (absolute),
// for the record entries of the paths they return.
func absLibrary(dir string) string {
	if a, err := filepath.Abs(dir); err == nil {
		return a
	}
	return filepath.Clean(dir)
}
