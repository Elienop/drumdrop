package musora

import (
	"strings"
	"testing"
)

func TestBuildNFO(t *testing.T) {
	l := &Lesson{
		ID: 409918, Title: "Course Kick-Off", Description: "<p>Welcome &amp; enjoy</p>",
		DifficultyString: "Intermediate", Brand: "drumeo", PublishedOn: "2024-06-11T15:00:00.000000Z",
		LengthInSeconds: 90, Instructors: []Instructor{{Name: "El Estepario Siberiano"}},
		ParentContentData: []struct {
			Title string `json:"title"`
		}{{Title: "30-Day Independence"}},
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
		ParentContentData: []struct {
			Title string `json:"title"`
		}{{Title: "30-Day Independence"}},
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
