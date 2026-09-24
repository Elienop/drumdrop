package library

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// Which entries of a plex-tv season folder belong to which lesson.
//
// A season folder is shared by every episode of a show, and two different
// lessons can share an episode number in one show (positions are first-come per
// lesson), so a name can not say whose an entry is. The owner's ruling
// (decision #66): files are known by record, not guessed from names. A lesson
// moved into a season folder records the exact entries the move placed
// (lessons.library_entries, see Record), and a delete or a re-download acts on
// exactly those.
//
// A lesson moved before the record existed (library_entries NULL, a "legacy"
// row) falls back to name matching (legacyEpisodeEntry), which can not prove
// ownership; so the fallback skips every entry another lesson row claims, and
// reports it.
//
// "Claims" is decided by the real file and the real folder, not by spelling,
// at every level: an existing entry that is the same file (os.SameFile) as one
// another lesson claims under another spelling, as on a case-insensitive
// filesystem ("Groove" and "GROOVE"), is claimed too, and so is one whose
// season or show folder is spelled another way ("Show" and "SHOW") but is the
// same folder. A folder "holds" what another lesson records inside it by the
// same rule (Holds).

// Entries is what one lesson owns in plex-tv season folders, as far as can be
// proven. Both lists hold absolute paths under the library folder.
type Entries struct {
	// Remove are the entries that belong to the lesson and to no other.
	Remove []string
	// Kept are entries that look like the lesson's but that another lesson row
	// also claims. They are never removed; the caller reports them.
	Kept []string
}

// Claims indexes what every lesson row claims in the library, for the
// ownership questions of one move or one delete. Build it once, from every row
// that records files (database.Store.ListLessonsWithFiles); ask about one
// lesson at a time, which never counts that lesson's own claims against it.
type Claims struct {
	// root is the library folder configured now ("" when none is).
	root string
	// recorded maps an entry's absolute path to the ids of the lessons whose
	// record names it.
	recorded map[string][]int
	// byID is each lesson's recorded entries (absolute), for Forget.
	byID map[int][]string
	// legacy maps a season folder to the rows filed there without a record,
	// each read as it would be under root today (see legacyRow).
	legacy map[string][]database.Lesson
	// paths is every row's output_dir and video_path (absolute), for Holds.
	paths []ownedPath
	// listings caches each season folder's entries (name -> isDir), read on
	// first use; a missing folder lists as empty.
	listings map[string]map[string]bool
	// folders caches, per season folder, the entries every lesson claims there
	// or in the same folder under another spelling (by record, or by a legacy
	// row's name match), keyed by name.
	folders map[string]map[string][]claim
	// stats caches Lstat of claimed entries for the identity checks (nil for
	// one that does not exist).
	stats map[string]os.FileInfo
	// dirStats caches Stat of folders for the folder identity checks (nil for
	// one that does not exist or can not be read).
	dirStats map[string]os.FileInfo
}

// claim is one path a lesson row claims in a season folder, and whose it is.
type claim struct {
	path string
	ids  []int
}

// ownedPath is one path a lesson row records outside its record.
type ownedPath struct {
	id   int
	path string
}

// NewClaims indexes rows under the library folder root (absolute, or "" when
// no library is configured). It fails, rather than guess, when a row's record
// is damaged: that lesson's claims are unknown, so nothing may be decided.
func NewClaims(root string, rows []database.Lesson) (*Claims, error) {
	if root != "" {
		root = absPath(root)
	}
	c := &Claims{
		root:     root,
		recorded: map[string][]int{},
		byID:     map[int][]string{},
		legacy:   map[string][]database.Lesson{},
		listings: map[string]map[string]bool{},
		folders:  map[string]map[string][]claim{},
		stats:    map[string]os.FileInfo{},
		dirStats: map[string]os.FileInfo{},
	}
	for _, row := range rows {
		entries, recorded, err := Record(row)
		if err != nil {
			return nil, err
		}
		id := row.RailcontentID
		for _, p := range []sql.NullString{row.OutputDir, row.VideoPath} {
			if p.Valid && p.String != "" {
				c.paths = append(c.paths, ownedPath{id: id, path: absPath(p.String)})
			}
		}
		if recorded {
			if root == "" {
				continue // Plan and the move refuse to act without a library
			}
			for _, e := range entries {
				p := Resolve(root, e)
				c.recorded[p] = append(c.recorded[p], id)
				c.byID[id] = append(c.byID[id], p)
			}
			continue
		}
		if row.OutputDir.Valid && IsSeasonDir(row.OutputDir.String) {
			norm := c.legacyRow(row)
			c.legacy[norm.OutputDir.String] = append(c.legacy[norm.OutputDir.String], norm)
		}
	}
	return c, nil
}

// Forget drops every claim of lesson id: its files are gone (a delete
// tombstoned it), so later questions must not count them. The folder caches
// are dropped too, as the files it removed changed the listings. The folder
// identities (dirStats) are kept: removing files does not change which folder
// is which, and a stale one can only make two folders look the same, which
// keeps more, never less.
func (c *Claims) Forget(id int) {
	for _, p := range c.byID[id] {
		c.recorded[p] = slices.DeleteFunc(c.recorded[p], func(x int) bool { return x == id })
		if len(c.recorded[p]) == 0 {
			delete(c.recorded, p)
		}
	}
	delete(c.byID, id)
	for dir, rows := range c.legacy {
		c.legacy[dir] = slices.DeleteFunc(rows, func(l database.Lesson) bool { return l.RailcontentID == id })
	}
	c.paths = slices.DeleteFunc(c.paths, func(o ownedPath) bool { return o.id == id })
	c.listings = map[string]map[string]bool{}
	c.folders = map[string]map[string][]claim{}
	c.stats = map[string]os.FileInfo{}
}

// dirStat is os.Stat of a folder through the cache: nil when it does not exist
// or can not be read (no evidence of identity either way).
func (c *Claims) dirStat(dir string) os.FileInfo {
	if info, ok := c.dirStats[dir]; ok {
		return info
	}
	info, err := os.Stat(dir)
	if err != nil {
		info = nil
	}
	c.dirStats[dir] = info
	return info
}

// sameFolder reports whether a and b are one folder: the same path, or two
// existing folders that are the same one under different spellings.
func (c *Claims) sameFolder(a, b string) bool {
	if a == b {
		return true
	}
	ai, bi := c.dirStat(a), c.dirStat(b)
	return ai != nil && bi != nil && os.SameFile(ai, bi)
}

// lstat is os.Lstat through the cache: nil when path does not exist (or can
// not be read, which the identity check treats alike: no evidence).
func (c *Claims) lstat(path string) os.FileInfo {
	if info, ok := c.stats[path]; ok {
		return info
	}
	info, err := os.Lstat(path)
	if err != nil {
		info = nil
	}
	c.stats[path] = info
	return info
}

// seasonFolder is where a row's season folder is under the library folder
// configured now. The plex-tv move files every lesson at exactly
// <library>/<show>/<Season NN>, so the last two parts of the recorded folder
// say where it is, whatever the library was called when it was recorded.
func (c *Claims) seasonFolder(outputDir string) string {
	d := absPath(outputDir)
	if c.root == "" {
		return d
	}
	return filepath.Join(c.root, filepath.Base(filepath.Dir(d)), filepath.Base(d))
}

// legacyRow is a row without a record read under the library folder
// configured now: its output_dir becomes seasonFolder, and a video recorded in
// that folder moves with it.
func (c *Claims) legacyRow(l database.Lesson) database.Lesson {
	orig := absPath(l.OutputDir.String)
	dir := c.seasonFolder(l.OutputDir.String)
	l.OutputDir.String = dir
	if l.VideoPath.Valid && l.VideoPath.String != "" && filepath.Dir(absPath(l.VideoPath.String)) == orig {
		l.VideoPath.String = filepath.Join(dir, filepath.Base(l.VideoPath.String))
	}
	return l
}

// listing returns dir's entries (name -> isDir), reading it once.
func (c *Claims) listing(dir string) (map[string]bool, error) {
	if l, ok := c.listings[dir]; ok {
		return l, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read season dir %q: %w", dir, err)
	}
	l := make(map[string]bool, len(entries))
	for _, e := range entries {
		l[e.Name()] = e.IsDir()
	}
	c.listings[dir] = l
	return l, nil
}

// folderClaims returns, for the season folder dir, every entry a lesson claims
// there, keyed by name: the recorded entries in it, and the existing entries a
// legacy row filed there matches by name, counting dir under every spelling
// (sameFolder: "Show" and "SHOW" on a case-insensitive disk are one folder). A
// legacy row whose episode name is uncertain claims the entries of every
// candidate name, so the fallback errs towards keeping. With legacy false only
// the records count.
func (c *Claims) folderClaims(dir string, legacy bool) (map[string][]claim, error) {
	key := dir
	if !legacy {
		key = "\x00records\x00" + dir
	}
	if f, ok := c.folders[key]; ok {
		return f, nil
	}
	f := map[string][]claim{}
	for p, ids := range c.recorded {
		if c.sameFolder(filepath.Dir(p), dir) {
			f[filepath.Base(p)] = append(f[filepath.Base(p)], claim{path: p, ids: ids})
		}
	}
	if legacy {
		for folder, rows := range c.legacy {
			if len(rows) == 0 || !c.sameFolder(folder, dir) {
				continue
			}
			if err := c.legacyClaims(f, folder, rows); err != nil {
				return nil, err
			}
		}
	}
	c.folders[key] = f
	return f, nil
}

// legacyClaims adds to f the existing entries of folder that rows (legacy rows
// filed there) match by name.
func (c *Claims) legacyClaims(f map[string][]claim, folder string, rows []database.Lesson) error {
	listing, err := c.listing(folder)
	if err != nil {
		return err
	}
	for _, row := range rows {
		bases, _ := legacyEpisodeBases(row, folder, listing)
		for name, isDir := range listing {
			for _, b := range bases {
				if legacyEpisodeEntry(b, name, isDir, listing) {
					f[name] = append(f[name], claim{path: filepath.Join(folder, name), ids: []int{row.RailcontentID}})
					break
				}
			}
		}
	}
	return nil
}

// Claimants returns the ids of the lessons other than self that claim path, an
// entry of a season folder: one whose record names it; with legacy, one whose
// name match (a legacy row) reaches it; and, for an existing entry, one that
// claims another name in the same folder (under any spelling of it) that is
// the same file as path.
func (c *Claims) Claimants(path string, self int, legacy bool) ([]int, error) {
	path = absPath(path)
	dir, name := filepath.Dir(path), filepath.Base(path)
	claimed, err := c.folderClaims(dir, legacy)
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, cl := range claimed[name] {
		ids = append(ids, cl.ids...)
	}
	if info, lerr := os.Lstat(path); lerr == nil {
		for _, cls := range claimed {
			for _, cl := range cls {
				if cl.path == path {
					continue
				}
				if oinfo := c.lstat(cl.path); oinfo != nil && os.SameFile(info, oinfo) {
					ids = append(ids, cl.ids...)
				}
			}
		}
	}
	return withoutID(ids, self), nil
}

// withoutID returns ids minus self, sorted and without repeats.
func withoutID(ids []int, self int) []int {
	out := slices.DeleteFunc(ids, func(x int) bool { return x == self })
	slices.Sort(out)
	return slices.Compact(out)
}

// Plan works out which season-folder entries lesson self owns:
//   - with a record: exactly the recorded entries, except one another lesson's
//     record also names, or that is the same file as one it names (Kept). A
//     legacy name match never outranks a record: every record was written by
//     a move that refused to overwrite what a legacy row claimed;
//   - without one, filed in a season folder (a legacy plex-tv lesson): the
//     folder's entries that the legacy name grammar gives to self, except any
//     another row claims (its record, or its own legacy match) (Kept);
//   - otherwise (a lesson's own folder, or no files): nothing.
//
// It fails, rather than guess, when self's record is damaged, when self's
// files are in a season folder but no library folder is configured, and when
// a legacy lesson's episode name can not be told apart from a look-alike (see
// legacyEpisodeBases).
func (c *Claims) Plan(self database.Lesson) (Entries, error) {
	var out Entries
	entries, recorded, err := Record(self)
	if err != nil {
		return out, err
	}
	season := self.OutputDir.Valid && IsSeasonDir(self.OutputDir.String)
	if (recorded && len(entries) > 0 || !recorded && season) && c.root == "" {
		return out, fmt.Errorf("lesson %d has files in a library season folder, but no library folder is configured (DRUMDROP_LIBRARY_DIR); refusing to guess where they are", self.RailcontentID)
	}
	if recorded {
		for _, e := range entries {
			p := Resolve(c.root, e)
			ids, err := c.Claimants(p, self.RailcontentID, false)
			if err != nil {
				return Entries{}, err
			}
			if len(ids) > 0 {
				out.Kept = append(out.Kept, p)
			} else {
				out.Remove = append(out.Remove, p)
			}
		}
		return out, nil
	}
	if !season {
		return out, nil
	}
	row := c.legacyRow(self)
	dir := row.OutputDir.String
	listing, err := c.listing(dir)
	if err != nil {
		return out, err
	}
	bases, exact := legacyEpisodeBases(row, dir, listing)
	if !exact {
		return Entries{}, fmt.Errorf("lesson %d has no record of its files, and its episode in %q could be any of %q; refusing to guess", self.RailcontentID, dir, bases)
	}
	names := make([]string, 0, len(listing))
	for name := range listing {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !legacyEpisodeEntry(bases[0], name, listing[name], listing) {
			continue
		}
		p := filepath.Join(dir, name)
		ids, err := c.Claimants(p, self.RailcontentID, true)
		if err != nil {
			return Entries{}, err
		}
		if len(ids) > 0 {
			out.Kept = append(out.Kept, p)
		} else {
			out.Remove = append(out.Remove, p)
		}
	}
	return out, nil
}

// Holds returns the ids of the lessons other than self that record a path at
// or inside dir: their output_dir, their video, or one of their library
// entries, written inside dir or inside the same folder under another
// spelling (a case-insensitive disk, a symlink, another mount). A lesson
// folder that holds another lesson's files is never one to remove whole.
func (c *Claims) Holds(dir string, self int) []int {
	dir = absPath(dir)
	var ids []int
	for _, o := range c.paths {
		if c.within(dir, o.path) {
			ids = append(ids, o.id)
		}
	}
	for p, pids := range c.recorded {
		if c.within(dir, p) {
			ids = append(ids, pids...)
		}
	}
	return withoutID(ids, self)
}

// within reports whether path is dir or inside it: as written, or because path
// or a folder above it is dir under another spelling (same folder by
// identity).
func (c *Claims) within(dir, path string) bool {
	if Inside(dir, path) {
		return true
	}
	if c.dirStat(dir) == nil {
		return false
	}
	for p := path; ; p = filepath.Dir(p) {
		if c.sameFolder(p, dir) {
			return true
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

// lessonFolderName is the shape of every lesson folder drumdrop creates:
// "NN - <title>" (see the scheduler's lessonDir).
var lessonFolderName = regexp.MustCompile(`^[0-9]{2,} - .`)

// IsLessonFolder reports whether dir is named like a lesson folder
// ("05 - Title"), the only kind of folder a delete removes whole.
func IsLessonFolder(dir string) bool {
	return lessonFolderName.MatchString(filepath.Base(dir))
}

// legacyEpisodeBases returns the candidate episode names ("<show> - s01e05 -
// <title>") of a lesson filed in seasonDir without a record, and whether
// exactly one was determined. The one derived from the row itself (the show
// folder, the lesson's position, its title) wins whenever the recorded video is
// named after it; otherwise every way of reading the video's name is a
// candidate ("<base>.mp4" for a lesson, "<base> [Label].mp4" for a song
// version, where either the title, the show or the label may hold " ["), and
// the one with its own "<base>.nfo" in the folder wins if it is the only one
// (every moved lesson has one; a song version has none of its own). When it is
// not exact, every candidate (the derived one included) is returned, so another
// lesson's claim errs towards covering too much.
func legacyEpisodeBases(l database.Lesson, seasonDir string, listing map[string]bool) (bases []string, exact bool) {
	// Every caller passes a folder IsSeasonDir accepted.
	season, _ := SeasonNumber(filepath.Base(seasonDir))
	prefix := EpisodePrefix(filepath.Base(filepath.Dir(seasonDir)), season)
	position := 1
	if l.Position.Valid {
		position = int(l.Position.Int64)
	}
	derived := fmt.Sprintf("%s%02d - %s", prefix, position, musora.Sanitize(l.Title))
	if !l.VideoPath.Valid || l.VideoPath.String == "" {
		return []string{derived}, true
	}
	video := l.VideoPath.String
	stem, isMP4 := strings.CutSuffix(filepath.Base(video), ".mp4")
	if filepath.Dir(video) != seasonDir || !isMP4 || !strings.HasPrefix(stem, prefix) {
		return []string{derived}, false
	}
	// The whole stem, or (for a name ending in "]") the stem cut before any
	// " [" after the show prefix.
	cands := []string{stem}
	if strings.HasSuffix(stem, "]") {
		for i := len(prefix); i < len(stem); i++ {
			if strings.HasPrefix(stem[i:], " [") {
				cands = append(cands, stem[:i])
			}
		}
	}
	for _, c := range cands {
		if c == derived {
			return []string{derived}, true
		}
	}
	if len(cands) == 1 {
		return cands, true
	}
	var withNFO []string
	for _, c := range cands {
		if isDir, ok := listing[c+".nfo"]; ok && !isDir {
			withNFO = append(withNFO, c)
		}
	}
	if len(withNFO) == 1 {
		return withNFO, true
	}
	return append(cands, derived), false
}

// subtitleExts are the subtitle formats yt-dlp writes next to a lesson video.
var subtitleExts = map[string]bool{"vtt": true, "srt": true, "ass": true, "ssa": true, "ttml": true, "lrc": true, "json3": true, "srv1": true, "srv2": true, "srv3": true}

// EpisodeEntry reports whether name, an entry of a season folder whose
// entries are listing (name -> isDir), is one of the entries the plex-tv move
// gives the episode base: the grammar legacyEpisodeEntry documents. It says
// nothing about whose the entry is; a caller that acts on it must know that
// from a record.
func EpisodeEntry(base, name string, isDir bool, listing map[string]bool) bool {
	return legacyEpisodeEntry(base, name, isDir, listing)
}

// legacyEpisodeEntry reports whether name is one of the entries the plex-tv
// move gave the episode base: exactly the shapes DownloadLesson produces, with
// the scratch base swapped for base:
//   - "<base>.mp4", "<base>.nfo", "<base>-poster.jpg", "<base>.<lang>.<subtitle>";
//   - "<base> [Label].mp4", a song version, unless another lesson's episode
//     starts inside it: some "<base> [X]" (X any prefix of the label that
//     ends in "]", the label itself included) has its own "<base> [X].nfo"
//     (then "<base> [X]" is another lesson, whose title is this one plus
//     " [X]", and this is one of ITS versions or its video);
//   - the folders "<base> resources", "<base> play-along", "<base> sheet-music".
//
// Anything else (another title that merely starts with this one, "…Five-Part
// Fill.mp4", "…Five.5.mp4", "…Five Bonus.mp4") is not the episode's.
func legacyEpisodeEntry(base, name string, isDir bool, listing map[string]bool) bool {
	rest, ok := strings.CutPrefix(name, base)
	if !ok {
		return false
	}
	if isDir {
		return rest == " resources" || rest == " play-along" || rest == " sheet-music"
	}
	switch rest {
	case ".mp4", ".nfo", "-poster.jpg":
		return true
	}
	if label, ok := strings.CutPrefix(rest, " ["); ok {
		label, ok = strings.CutSuffix(label, "].mp4")
		if !ok || label == "" {
			return false
		}
		// "<base> [" + label[:i] + "]" for every "]" in the label, and the whole
		// label: a lesson "<base> [Live]" owns "<base> [Live] [Drumless].mp4".
		for i := 0; i <= len(label); i++ {
			if i < len(label) && label[i] != ']' {
				continue
			}
			if isDirNFO, hasNFO := listing[base+" ["+label[:i]+"].nfo"]; hasNFO && !isDirNFO {
				return false
			}
		}
		return true
	}
	if sub, ok := strings.CutPrefix(rest, "."); ok {
		lang, ext, ok := strings.Cut(sub, ".")
		return ok && lang != "" && isLangCode(lang) && subtitleExts[ext]
	}
	return false
}

// isLangCode reports whether s looks like a yt-dlp subtitle language code
// ("en", "pt-BR", "live_chat").
func isLangCode(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
