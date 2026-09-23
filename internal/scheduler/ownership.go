package scheduler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// moved into a season folder records the exact paths the move placed
// (lessons.library_entries), and a delete or a re-download acts on exactly
// those.
//
// A lesson moved before the record existed (library_entries NULL) falls back to
// name matching (legacyEpisodeEntry), which can not prove ownership; so the
// fallback skips every entry another lesson row claims, and reports it.

// LessonEntries is what one lesson owns in plex-tv season folders, as far as
// can be proven.
type LessonEntries struct {
	// Remove are the absolute paths that belong to the lesson and to no other.
	Remove []string
	// Kept are paths that look like the lesson's but that another lesson row
	// also claims. They are never removed; the caller reports them.
	Kept []string
}

// PlanLessonEntries works out which season-folder entries lesson self owns:
//   - with a record: exactly the recorded paths, except one another lesson's
//     record also names (Kept);
//   - without one, filed in a season folder (a legacy plex-tv lesson): the
//     folder's entries that the legacy name grammar gives to self, except any
//     another row claims (its record, or its own legacy match) (Kept);
//   - otherwise (a lesson's own folder, or no files): nothing.
//
// others is every lesson row with files; self is skipped if present. It fails,
// rather than guess, when a record is damaged or names something that is not an
// entry of a season folder, and when a legacy lesson's episode name can not be
// told apart from a look-alike (see legacyEpisodeBases).
func PlanLessonEntries(self database.Lesson, others []database.Lesson) (LessonEntries, error) {
	c, err := newClaims(self.RailcontentID, others)
	if err != nil {
		return LessonEntries{}, err
	}
	return c.lessonEntries(self)
}

// claims indexes what every OTHER lesson row claims in season folders.
type claims struct {
	// recorded maps a path to the ids of the lessons whose record names it.
	recorded map[string][]int
	// legacy maps a season folder to the rows filed there without a record.
	legacy map[string][]database.Lesson
	// listings caches each season folder's entries (name -> isDir), read on
	// first use; a missing folder lists as empty.
	listings map[string]map[string]bool
}

// newClaims indexes others, skipping the row whose id is self.
func newClaims(self int, others []database.Lesson) (*claims, error) {
	c := &claims{recorded: map[string][]int{}, legacy: map[string][]database.Lesson{}, listings: map[string]map[string]bool{}}
	for _, o := range others {
		if o.RailcontentID == self {
			continue
		}
		paths, recorded, err := o.PlacedEntries()
		if err != nil {
			return nil, err
		}
		if recorded {
			for _, p := range paths {
				c.recorded[p] = append(c.recorded[p], o.RailcontentID)
			}
			continue
		}
		if o.OutputDir.Valid && IsPlexSeasonDir(o.OutputDir.String) {
			dir := filepath.Clean(o.OutputDir.String)
			c.legacy[dir] = append(c.legacy[dir], o)
		}
	}
	return c, nil
}

// listing returns dir's entries (name -> isDir), reading it once.
func (c *claims) listing(dir string) (map[string]bool, error) {
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

// claimants returns the ids of the other lessons that claim path: by record,
// or, for an entry that exists, by a legacy row's name match. A legacy row
// whose episode name is uncertain claims the entries of every candidate name,
// so the fallback errs towards keeping.
func (c *claims) claimants(path string) ([]int, error) {
	ids := append([]int(nil), c.recorded[path]...)
	dir, name := filepath.Dir(path), filepath.Base(path)
	rows := c.legacy[dir]
	if len(rows) == 0 {
		return ids, nil
	}
	listing, err := c.listing(dir)
	if err != nil {
		return nil, err
	}
	isDir, exists := listing[name]
	if !exists {
		return ids, nil
	}
	for _, row := range rows {
		bases, _ := legacyEpisodeBases(row, dir, listing)
		for _, b := range bases {
			if legacyEpisodeEntry(b, name, isDir, listing) {
				ids = append(ids, row.RailcontentID)
				break
			}
		}
	}
	return ids, nil
}

// lessonEntries is PlanLessonEntries over an existing index.
func (c *claims) lessonEntries(self database.Lesson) (LessonEntries, error) {
	var out LessonEntries
	paths, recorded, err := self.PlacedEntries()
	if err != nil {
		return out, err
	}
	if recorded {
		// A record is proof: only another RECORD naming the same path makes it
		// ambiguous. A legacy guess can not outrank it (every record was written
		// by a move that refused to overwrite what a legacy row claimed).
		for _, p := range paths {
			if err := checkRecordedEntry(self.RailcontentID, p); err != nil {
				return LessonEntries{}, err
			}
			if len(c.recorded[p]) > 0 {
				out.Kept = append(out.Kept, p)
			} else {
				out.Remove = append(out.Remove, p)
			}
		}
		return out, nil
	}
	if !self.OutputDir.Valid || !IsPlexSeasonDir(self.OutputDir.String) {
		return out, nil
	}
	dir := filepath.Clean(self.OutputDir.String)
	listing, err := c.listing(dir)
	if err != nil {
		return out, err
	}
	bases, exact := legacyEpisodeBases(self, dir, listing)
	if !exact {
		return LessonEntries{}, fmt.Errorf("lesson %d has no record of its files, and its episode in %q could be any of %q; refusing to guess", self.RailcontentID, dir, bases)
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
		ids, err := c.claimants(p)
		if err != nil {
			return LessonEntries{}, err
		}
		if len(ids) > 0 {
			out.Kept = append(out.Kept, p)
		} else {
			out.Remove = append(out.Remove, p)
		}
	}
	return out, nil
}

// checkRecordedEntry refuses a recorded path that is not a plain entry of a
// plex-tv season folder, so a damaged row can never aim a removal elsewhere.
func checkRecordedEntry(id int, p string) error {
	name := filepath.Base(p)
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || name == "." || name == ".." || !IsPlexSeasonDir(filepath.Dir(p)) {
		return fmt.Errorf("lesson %d records %q, which is not an entry of a season folder; refusing to touch it", id, p)
	}
	return nil
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
	// Every caller passes a folder IsPlexSeasonDir accepted.
	season, _ := plexSeasonNumber(filepath.Base(seasonDir))
	prefix := plexEpisodePrefix(filepath.Base(filepath.Dir(seasonDir)), season)
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

// legacyEpisodeEntry reports whether name is one of the entries the plex-tv
// move gave the episode base: exactly the shapes DownloadLesson produces, with
// the scratch base swapped for base:
//   - "<base>.mp4", "<base>.nfo", "<base>-poster.jpg", "<base>.<lang>.<subtitle>";
//   - "<base> [Label].mp4", a song version, unless "<base> [Label].nfo" exists
//     (then it is another lesson whose title is this one plus " [Label]");
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
		isDirNFO, hasNFO := listing[base+" ["+label+"].nfo"]
		return !hasNFO || isDirNFO
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

// RemoveUnderRoot removes path (a file, a symlink itself, or a folder with its
// contents) through the first of roots it sits strictly inside, using os.Root,
// so no symlinked folder on the way can make it act outside that root: such a
// path is refused ("path escapes from parent"). A missing path is success. It
// refuses a path inside none of the roots, and the roots themselves.
func RemoveUnderRoot(roots []string, path string) error {
	for _, root := range roots {
		rel, ok := relUnder(root, path)
		if !ok {
			continue
		}
		r, err := os.OpenRoot(root)
		if err != nil {
			return fmt.Errorf("open root %q to remove %q: %w", root, path, err)
		}
		defer r.Close()
		if err := r.RemoveAll(rel); err != nil {
			return fmt.Errorf("remove %q: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf("%q is not safely inside any of %q; refusing to remove it", path, roots)
}

// relUnder returns path relative to root when it is strictly inside it: a
// non-empty root, and a relative path that is not ".", "..", ".."-prefixed or
// absolute.
func relUnder(root, path string) (string, bool) {
	if root == "" || path == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}
