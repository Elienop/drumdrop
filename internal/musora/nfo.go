package musora

import (
	"fmt"
	"regexp"
	"strings"
)

var reTags = regexp.MustCompile(`<[^>]+>`)

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func decodeEntities(s string) string {
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&#039;", "'", "&apos;", "'", "&amp;", "&")
	return r.Replace(s)
}

func dateOnly(s string) string {
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		return s[:10]
	}
	return ""
}

// BuildNFO renders a Kodi/Plex <movie> nfo for a lesson. This is the default-layout
// nfo written at download time; plex-tv mode overwrites it with BuildEpisodeNFO.
func BuildNFO(l *Lesson) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n<movie>\n")
	fmt.Fprintf(&b, "  <title>%s</title>\n", esc(l.Title))
	if len(l.ParentContentData) > 0 && l.ParentContentData[0].Title != "" {
		fmt.Fprintf(&b, "  <set><name>%s</name></set>\n", esc(l.ParentContentData[0].Title))
	}
	writePlot(&b, l)
	// <movie> uses <premiered>YYYY-MM-DD</premiered> + <year>YYYY</year>; the
	// episode variant swaps this for a single <aired>.
	if d := dateOnly(l.PublishedOn); d != "" {
		fmt.Fprintf(&b, "  <premiered>%s</premiered>\n  <year>%s</year>\n", d, d[:4])
	}
	writeRuntimeStudioTag(&b, l)
	writeGenresActors(&b, l)
	writeUniqueID(&b, l)
	b.WriteString("</movie>\n")
	return b.String()
}

// BuildEpisodeNFO renders a Kodi/Plex <episodedetails> nfo for a lesson treated as
// a TV episode (plex-tv layout). It reuses BuildNFO's field extraction and escape
// helpers, but with root <episodedetails>, the episode <title> = the lesson title,
// <showtitle> = show (when non-empty), <season>/<episode>, and <aired> (from
// dateOnly(PublishedOn)) in place of the movie's <premiered>/<year>. This supplies
// the local episode metadata a Plex TV-Shows library needs for an unmatched show.
func BuildEpisodeNFO(l *Lesson, show string, season, episode int) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n<episodedetails>\n")
	fmt.Fprintf(&b, "  <title>%s</title>\n", esc(l.Title))
	if show != "" {
		fmt.Fprintf(&b, "  <showtitle>%s</showtitle>\n", esc(show))
	}
	fmt.Fprintf(&b, "  <season>%d</season>\n  <episode>%d</episode>\n", season, episode)
	writePlot(&b, l)
	if d := dateOnly(l.PublishedOn); d != "" {
		fmt.Fprintf(&b, "  <aired>%s</aired>\n", d)
	}
	writeRuntimeStudioTag(&b, l)
	writeGenresActors(&b, l)
	writeUniqueID(&b, l)
	b.WriteString("</episodedetails>\n")
	return b.String()
}

// writePlot writes the <plot> tag from the lesson description (HTML tags stripped,
// entities decoded, then single-escaped), if any survives the cleanup.
func writePlot(b *strings.Builder, l *Lesson) {
	if l.Description != "" {
		plot := strings.TrimSpace(decodeEntities(reTags.ReplaceAllString(l.Description, "")))
		if plot != "" {
			fmt.Fprintf(b, "  <plot>%s</plot>\n", esc(plot))
		}
	}
}

// writeRuntimeStudioTag writes <runtime> (ceil minutes), <studio> (brand), and the
// difficulty <tag>, each when present. Shared by the movie and episode nfos.
func writeRuntimeStudioTag(b *strings.Builder, l *Lesson) {
	if l.LengthInSeconds > 0 {
		fmt.Fprintf(b, "  <runtime>%d</runtime>\n", (l.LengthInSeconds+59)/60)
	}
	if l.Brand != "" {
		fmt.Fprintf(b, "  <studio>%s</studio>\n", esc(l.Brand))
	}
	if l.DifficultyString != "" {
		fmt.Fprintf(b, "  <tag>%s</tag>\n", esc(l.DifficultyString))
	}
}

// writeGenresActors writes a <genre> per named genre and an instructor <actor>
// (with a <role>Instructor</role> child) per named instructor.
func writeGenresActors(b *strings.Builder, l *Lesson) {
	for _, g := range l.Genre {
		if g.Name != "" {
			fmt.Fprintf(b, "  <genre>%s</genre>\n", esc(g.Name))
		}
	}
	for _, in := range l.Instructors {
		if in.Name != "" {
			fmt.Fprintf(b, "  <actor><name>%s</name><role>Instructor</role></actor>\n", esc(in.Name))
		}
	}
}

// writeUniqueID writes the <uniqueid type="musora" default="true"> tag from the
// lesson ID when non-zero.
func writeUniqueID(b *strings.Builder, l *Lesson) {
	if l.ID != 0 {
		fmt.Fprintf(b, "  <uniqueid type=\"musora\" default=\"true\">%d</uniqueid>\n", l.ID)
	}
}
