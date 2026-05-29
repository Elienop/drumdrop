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
