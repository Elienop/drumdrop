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
