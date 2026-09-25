package musora

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var reTags = regexp.MustCompile(`<[^>]+>`)

// esc is s as XML 1.0 text: the five markup characters escaped, and every
// character XML forbids even escaped (C0 controls but tab, newline and
// carriage return; surrogates; U+FFFE and U+FFFF; bytes that are not UTF-8)
// replaced by U+FFFD, as encoding/xml.EscapeText does. An nfo is never
// ill-formed, which matters most for tvshow.nfo: it is written once and never
// replaced. Valid text comes out exactly as before (EscapeText itself would
// also escape newlines and use numeric references).
func esc(s string) string {
	s = strings.Map(func(r rune) rune {
		if isXMLChar(r) {
			return r
		}
		return '\uFFFD'
	}, s)
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// isXMLChar reports whether r may appear in an XML 1.0 document (its Char
// production; encoding/xml's isInCharacterRange, which is not exported).
func isXMLChar(r rune) bool {
	return r == 0x09 || r == 0x0A || r == 0x0D ||
		r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r <= 0xFFFD ||
		r >= 0x10000 && r <= 0x10FFFF
}

func decodeEntities(s string) string {
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&#039;", "'", "&apos;", "'", "&amp;", "&")
	return r.Replace(s)
}

// dateOnly is the date s starts with, "YYYY-MM-DD", when it is a real one
// (time.Parse checks the digits and the ranges: no month 13, no February 30),
// else "". It is written unescaped, so nothing else may get through.
func dateOnly(s string) string {
	if len(s) < 10 {
		return ""
	}
	if _, err := time.Parse(time.DateOnly, s[:10]); err != nil {
		return ""
	}
	return s[:10]
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

// BuildShowNFO renders the tvshow.nfo of a plex-tv show: a Kodi <tvshow>
// document, which Plex's "Plex NFO Series" agent and XBMCnfoTVImporter both
// read. title is the show's name as the show folder was named from it
// (unsanitized), and doc the Musora document the show is named after (nil
// when there is none: the nfo then holds only the title). It reuses the
// episode nfo's field helpers: <plot> (plain text), <premiered>, <studio>,
// <genre>, <actor> and <uniqueid type="musora" default="true">, each when
// known. <runtime> and the difficulty <tag> describe one lesson, not a show,
// so they are left out.
func BuildShowNFO(title string, doc *Lesson) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n<tvshow>\n")
	fmt.Fprintf(&b, "  <title>%s</title>\n", esc(title))
	if doc != nil {
		writePlot(&b, doc)
		if d := dateOnly(doc.PublishedOn); d != "" {
			fmt.Fprintf(&b, "  <premiered>%s</premiered>\n", d)
		}
		if doc.Brand != "" {
			fmt.Fprintf(&b, "  <studio>%s</studio>\n", esc(doc.Brand))
		}
		writeGenresActors(&b, doc)
		writeUniqueID(&b, doc)
	}
	b.WriteString("</tvshow>\n")
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
