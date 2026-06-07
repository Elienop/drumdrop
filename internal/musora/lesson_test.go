package musora

import (
	"encoding/json"
	"testing"
)

func TestLessonUnmarshal(t *testing.T) {
	const sample = `[{"id":409918,"title":"Course Kick-Off","brand":"drumeo",
	  "length_in_seconds":90,"video":{"external_id":"932525713","hlsManifestUrl":"https://player.vimeo.com/external/932525713.m3u8?s=x"},
	  "instructor":[{"name":"El Estepario Siberiano"}],"resources":[{"resource_name":"chart.pdf","resource_url":"https://cdn/x.pdf"}]}]`
	var res []Lesson
	if err := json.Unmarshal([]byte(sample), &res); err != nil {
		t.Fatal(err)
	}
	l := res[0]
	if l.Title != "Course Kick-Off" || l.Video.HLSManifestURL == "" || l.Instructors[0].Name == "" || l.Resources[0].URL == "" {
		t.Fatalf("unmarshal mismatch: %+v", l)
	}
}

// Songs carry multi-page sheet music, so the resolve_lesson GROQ projection makes
// assignments[].sheet_music_image_url an ARRAY of URLs (one per page); legacy
// lessons keep it a scalar string. The field must decode both shapes (plus null
// and an absent key) or the whole lesson — and thus the download job — fails.
// Regression for the song failure
// "json: cannot unmarshal array into Go struct field
// Assignment.assignments.sheet_music_image_url of type string".
func TestAssignmentSheetMusicShapes(t *testing.T) {
	const sample = `[{"id":1,"title":"Africa","brand":"drumeo","assignments":[
	  {"title":"Pages","sheet_music_image_url":["https://cdn/p1.png","https://cdn/p2.png"]},
	  {"title":"Legacy","sheet_music_image_url":"https://cdn/one.png"},
	  {"title":"Null","sheet_music_image_url":null},
	  {"title":"Absent"}
	]}]`
	var res []Lesson
	if err := json.Unmarshal([]byte(sample), &res); err != nil {
		t.Fatalf("song unmarshal failed (the bug): %v", err)
	}
	a := res[0].Assignments
	if len(a) != 4 {
		t.Fatalf("assignments = %d, want 4", len(a))
	}
	if got := []string(a[0].SheetMusicImageURLs); len(got) != 2 || got[0] != "https://cdn/p1.png" || got[1] != "https://cdn/p2.png" {
		t.Fatalf("array form = %v, want both pages", got)
	}
	if got := []string(a[1].SheetMusicImageURLs); len(got) != 1 || got[0] != "https://cdn/one.png" {
		t.Fatalf("scalar form = %v, want single-element slice", got)
	}
	if a[2].SheetMusicImageURLs != nil {
		t.Fatalf("null form = %v, want nil", a[2].SheetMusicImageURLs)
	}
	if a[3].SheetMusicImageURLs != nil {
		t.Fatalf("absent form = %v, want nil", a[3].SheetMusicImageURLs)
	}
}

// length_in_seconds is coalesce(length_in_seconds, soundslice[0].soundslice_length_in_second)
// in resolve_lesson.groq; soundslice-backed content (songs/play-alongs) can yield
// a FRACTIONAL duration, which a plain int field rejects — failing the whole
// lesson, the same class as the sheet-music array bug. The field must tolerate a
// fractional (and quoted, and null/absent) number, truncating to whole seconds.
func TestLessonLengthInSecondsShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"integer", `212`, 212},
		{"fractional soundslice", `212.5`, 212},
		{"decimal-formatted whole", `212.0`, 212},
		{"quoted", `"212"`, 212},
		{"null", `null`, 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			raw := `[{"id":1,"length_in_seconds":` + tt.raw + `}]`
			var res []Lesson
			if err := json.Unmarshal([]byte(raw), &res); err != nil {
				t.Fatalf("unmarshal length_in_seconds=%s failed: %v", tt.raw, err)
			}
			if got := int(res[0].LengthInSeconds); got != tt.want {
				t.Fatalf("LengthInSeconds(%s) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
	// Absent key -> zero value, no error.
	var res []Lesson
	if err := json.Unmarshal([]byte(`[{"id":1}]`), &res); err != nil {
		t.Fatalf("absent unmarshal failed: %v", err)
	}
	if res[0].LengthInSeconds != 0 {
		t.Fatalf("absent LengthInSeconds = %d, want 0", res[0].LengthInSeconds)
	}
}
