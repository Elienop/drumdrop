package musora

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestBuildNFO(t *testing.T) {
	l := &Lesson{
		ID: 409918, Title: "Course Kick-Off", Description: "<p>Welcome &amp; enjoy</p>",
		DifficultyString: "Intermediate", Brand: "drumeo", PublishedOn: "2024-06-11T15:00:00.000000Z",
		LengthInSeconds: 90, Instructors: []Instructor{{Name: "El Estepario Siberiano"}},
		ParentContentData: []ParentContent{{Title: "30-Day Independence"}},
	}
	xml := BuildNFO(l)
	for _, want := range []string{
		"<movie>", // the default-layout nfo MUST stay a <movie> (not an episode)
		"<title>Course Kick-Off</title>",
		"<set><name>30-Day Independence</name></set>",
		"<plot>Welcome &amp; enjoy</plot>", // decoded then single-escaped (not &amp;amp;)
		"<premiered>2024-06-11</premiered>",
		`<actor><name>El Estepario Siberiano</name>`,
		`<uniqueid type="musora" default="true">409918</uniqueid>`,
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("NFO missing %q\n%s", want, xml)
		}
	}
	if strings.Contains(xml, "<p>") || strings.Contains(xml, "&amp;amp;") {
		t.Fatalf("entities not handled:\n%s", xml)
	}
}

func TestBuildEpisodeNFO(t *testing.T) {
	l := &Lesson{
		ID: 409918, Title: "Course Kick-Off", Description: "<p>Welcome &amp; enjoy</p>",
		DifficultyString: "Intermediate", Brand: "drumeo", PublishedOn: "2024-06-11T15:00:00.000000Z",
		LengthInSeconds: 90, Instructors: []Instructor{{Name: "El Estepario Siberiano"}},
		ParentContentData: []ParentContent{{Title: "30-Day Independence"}},
	}
	xml := BuildEpisodeNFO(l, "30-Day Independence", 1, 5)
	for _, want := range []string{
		"<episodedetails>",
		"<title>Course Kick-Off</title>",
		"<showtitle>30-Day Independence</showtitle>",
		"<season>1</season>",
		"<episode>5</episode>",
		"<aired>2024-06-11</aired>",
		"<plot>Welcome &amp; enjoy</plot>", // decoded then single-escaped (not &amp;amp;)
		`<actor><name>El Estepario Siberiano</name><role>Instructor</role></actor>`,
		`<uniqueid type="musora" default="true">409918</uniqueid>`,
		"</episodedetails>",
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("episode NFO missing %q\n%s", want, xml)
		}
	}
	// Episode nfo is NOT a <movie>, and uses <aired> instead of <premiered>/<year>.
	for _, bad := range []string{"<movie>", "<premiered>", "<year>", "<p>", "&amp;amp;"} {
		if strings.Contains(xml, bad) {
			t.Fatalf("episode NFO unexpectedly contains %q\n%s", bad, xml)
		}
	}
}

// TestBuildEpisodeNFONoShowTitle proves <showtitle> is omitted when show is empty.
func TestBuildEpisodeNFONoShowTitle(t *testing.T) {
	l := &Lesson{ID: 1, Title: "Solo"}
	xml := BuildEpisodeNFO(l, "", 1, 3)
	if strings.Contains(xml, "<showtitle>") {
		t.Fatalf("episode NFO should omit empty showtitle\n%s", xml)
	}
	if !strings.Contains(xml, "<episodedetails>") || !strings.Contains(xml, "<season>1</season>") || !strings.Contains(xml, "<episode>3</episode>") {
		t.Fatalf("episode NFO missing core tags\n%s", xml)
	}
}

// TestBuildShowNFO pins the tvshow.nfo of a plex-tv show: a <tvshow> document
// titled with the show's name (as its folder was named from it), with the
// plot as plain text, and every other field when known; nothing of one
// lesson's (runtime, difficulty) and no markup from Musora's HTML.
func TestBuildShowNFO(t *testing.T) {
	doc := &Lesson{
		ID: 455014, Title: "Kick, Snare, Hat (Musora's title)", Description: "<p>Groove &amp; feel with <b>Ash</b>.</p>",
		DifficultyString: "Beginner", Brand: "drumeo", PublishedOn: "2026-04-13T18:00:00.000Z", LengthInSeconds: 600,
		Instructors: []Instructor{{Name: "Ash Soan"}},
		Genre: []struct {
			Name string `json:"name"`
		}{{Name: "Pop & Rock"}},
	}
	want := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<tvshow>
  <title>Kick, Snare &amp; Hat</title>
  <plot>Groove &amp; feel with Ash.</plot>
  <premiered>2026-04-13</premiered>
  <studio>drumeo</studio>
  <genre>Pop &amp; Rock</genre>
  <actor><name>Ash Soan</name><role>Instructor</role></actor>
  <uniqueid type="musora" default="true">455014</uniqueid>
</tvshow>
`
	if got := BuildShowNFO("Kick, Snare & Hat", doc); got != want {
		t.Errorf("BuildShowNFO =\n%s\nwant\n%s", got, want)
	}
	titleOnly := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<tvshow>
  <title>content-7</title>
</tvshow>
`
	if got := BuildShowNFO("content-7", nil); got != titleOnly {
		t.Errorf("BuildShowNFO with no document =\n%s\nwant\n%s", got, titleOnly)
	}
}

// wellFormed fails unless doc is a well-formed XML 1.0 document, as
// encoding/xml reads one (which refuses every character XML forbids).
func wellFormed(t *testing.T, name, doc string) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	for {
		_, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Errorf("%s is not well-formed XML: %v\n%q", name, err, doc)
			return
		}
	}
}

// TestNFOTextIsAlwaysWellFormed pins that an nfo is well-formed XML whatever
// Musora sends: a character XML forbids even escaped (a C0 control, a
// surrogate's bytes, U+FFFF, a byte that is not UTF-8) becomes U+FFFD, in
// every text field of every nfo; and a published date is written only when
// it is a real "YYYY-MM-DD" (it goes in unescaped). tvshow.nfo is written
// once and never replaced, so an ill-formed one would stay ill-formed.
func TestNFOTextIsAlwaysWellFormed(t *testing.T) {
	bad := "a\x00b\x0bc\x1fd\uffffe\xed\xa0\x80f\xffg"
	doc := &Lesson{
		ID: 1, Title: "Title " + bad, Description: "<p>Plot " + bad + "</p>", Brand: "drumeo" + bad,
		DifficultyString: "Level " + bad, PublishedOn: "2024-02-29T00:00:00Z", LengthInSeconds: 60,
		Instructors: []Instructor{{Name: "Coach " + bad}},
		Genre: []struct {
			Name string `json:"name"`
		}{{Name: "Genre " + bad}},
		ParentContentData: []ParentContent{{Title: "Course " + bad}},
	}
	for name, out := range map[string]string{
		"tvshow.nfo":  BuildShowNFO("Show "+bad, doc),
		"episode nfo": BuildEpisodeNFO(doc, "Show "+bad, 1, 2),
		"movie nfo":   BuildNFO(doc),
	} {
		wellFormed(t, name, out)
		if !strings.Contains(out, "a\uFFFDb\uFFFDc\uFFFDd\uFFFDe") {
			t.Errorf("%s does not replace the forbidden characters with U+FFFD:\n%q", name, out)
		}
		if !strings.Contains(out, "2024-02-29") {
			t.Errorf("%s lost the real date 2024-02-29:\n%s", name, out)
		}
	}
	// Tab, newline and carriage return are XML characters: kept as they are.
	if got := esc("a\tb\nc\rd & <e>"); got != "a\tb\nc\rd &amp; &lt;e&gt;" {
		t.Errorf("esc = %q, want the whitespace kept and the markup escaped", got)
	}
	for _, date := range []string{"2024-13-01", "2024-02-30", "2024-1a-01", "20x4-01-01", "2024-01-0<", "2024/01/01", "2024-01-1", ""} {
		d := &Lesson{ID: 1, Title: "T", PublishedOn: date + "T00:00:00Z"}
		for name, out := range map[string]string{"tvshow.nfo": BuildShowNFO("T", d), "episode nfo": BuildEpisodeNFO(d, "S", 1, 1), "movie nfo": BuildNFO(d)} {
			if strings.Contains(out, "premiered>") || strings.Contains(out, "aired>") || strings.Contains(out, "<year>") {
				t.Errorf("%s writes the date %q, which is not a real YYYY-MM-DD:\n%s", name, date, out)
			}
		}
	}
}
