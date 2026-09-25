package library

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// EpisodePrefix is the part of every episode base that names the show and
// season: "<showFolder> - s0Ne". The legacy fallback (legacyEpisodeBases) reads
// recorded videos against it, so it is the one place that format lives.
func EpisodePrefix(showFolder string, season int) string {
	return fmt.Sprintf("%s - s%02de", showFolder, season)
}

// SeasonName is the season folder the plex-tv move files a show's episodes
// under ("Season 01").
func SeasonName(season int) string {
	return fmt.Sprintf("Season %02d", season)
}

// SeasonNumber parses a season folder name back into its number. It accepts
// only a name SeasonName itself produces (checked by formatting the number
// back), so a default-layout lesson folder ("NN - Title") never parses.
func SeasonNumber(name string) (season int, ok bool) {
	digits, found := strings.CutPrefix(name, "Season ")
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || SeasonName(n) != name {
		return 0, false
	}
	return n, true
}

// IsSeasonDir reports whether dir is a plex-tv season folder, the folder the
// plex-tv move shares between every episode of a show. The delete uses it to tell
// a shared season folder (remove one episode's entries) from a lesson's own
// folder (remove the folder), whatever DRUMDROP_LAYOUT says today.
func IsSeasonDir(dir string) bool {
	_, ok := SeasonNumber(filepath.Base(dir))
	return ok
}
