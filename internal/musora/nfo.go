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

func BuildNFO(l *Lesson) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n<movie>\n")
	fmt.Fprintf(&b, "  <title>%s</title>\n", esc(l.Title))
	if len(l.ParentContentData) > 0 && l.ParentContentData[0].Title != "" {
		fmt.Fprintf(&b, "  <set><name>%s</name></set>\n", esc(l.ParentContentData[0].Title))
	}
	if l.Description != "" {
		plot := strings.TrimSpace(decodeEntities(reTags.ReplaceAllString(l.Description, "")))
		if plot != "" {
			fmt.Fprintf(&b, "  <plot>%s</plot>\n", esc(plot))
		}
	}
	if d := dateOnly(l.PublishedOn); d != "" {
		fmt.Fprintf(&b, "  <premiered>%s</premiered>\n  <year>%s</year>\n", d, d[:4])
	}
	if l.LengthInSeconds > 0 {
		fmt.Fprintf(&b, "  <runtime>%d</runtime>\n", (l.LengthInSeconds+59)/60)
	}
	if l.Brand != "" {
		fmt.Fprintf(&b, "  <studio>%s</studio>\n", esc(l.Brand))
	}
	if l.DifficultyString != "" {
		fmt.Fprintf(&b, "  <tag>%s</tag>\n", esc(l.DifficultyString))
	}
	for _, g := range l.Genre {
		if g.Name != "" {
			fmt.Fprintf(&b, "  <genre>%s</genre>\n", esc(g.Name))
		}
	}
	for _, in := range l.Instructors {
		if in.Name != "" {
			fmt.Fprintf(&b, "  <actor><name>%s</name><role>Instructor</role></actor>\n", esc(in.Name))
		}
	}
	if l.ID != 0 {
		fmt.Fprintf(&b, "  <uniqueid type=\"musora\" default=\"true\">%d</uniqueid>\n", l.ID)
	}
	b.WriteString("</movie>\n")
	return b.String()
}
