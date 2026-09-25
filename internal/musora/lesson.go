package musora

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// flexSeconds decodes length_in_seconds into a whole-second int while tolerating
// the fractional value the resolve_lesson GROQ can emit: the field is
// coalesce(length_in_seconds, soundslice[0].soundslice_length_in_second), and
// soundslice durations (songs/play-alongs) can be fractional (e.g. 212.5). A
// plain int field would reject that and fail the entire lesson — the same failure
// class as the sheet-music array. A quoted number and null/absent are tolerated;
// the value is truncated to whole seconds (the NFO runtime is whole minutes).
type flexSeconds int

func (f *flexSeconds) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s = strings.TrimSpace(s); s == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*f = flexSeconds(int(v))
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = flexSeconds(int(v))
	return nil
}

// stringOrSlice decodes a JSON value that is either a single string or an array
// of strings into a []string. The resolve_lesson GROQ projection makes an
// assignment's sheet_music_image_url a scalar for legacy lessons
// (assignment_sheet_music_image) but an ARRAY — one entry per sheet-music page —
// for songs (the assignment_sheet_music_image_new[] projection). Decoding into a
// plain string therefore fails the whole lesson with
// "cannot unmarshal array into Go struct field ... of type string", which fails
// the download job. A null or absent value decodes to a nil slice. Array entries
// that are JSON null decode to "" (filtered by the downloader).
type stringOrSlice []string

func (s *stringOrSlice) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*s = nil
		return nil
	}
	if b[0] == '[' {
		var arr []string
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*s = stringOrSlice{one}
	return nil
}

// looseString decodes a JSON string, and reads null, an absent key or any
// other shape (a number, an object, an array) as "" without an error. It is
// for a field that only feeds optional output, the plex-tv show's artwork and
// tvshow.nfo: Musora changing that field's shape must never fail the lesson,
// and with it the download (hard rule 10).
type looseString string

func (s *looseString) UnmarshalJSON(b []byte) error {
	var v string
	if json.Unmarshal(b, &v) != nil {
		v = ""
	}
	*s = looseString(v)
	return nil
}

// looseInt decodes a JSON number (a fraction truncated) or a quoted one, and
// reads null, an absent key or any other shape as 0 without an error, for
// the same reason as looseString.
type looseInt int

func (n *looseInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if json.Unmarshal(b, &s) == nil {
			b = []byte(strings.TrimSpace(s))
		}
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil || math.IsNaN(v) || v > math.MaxInt32 || v < math.MinInt32 {
		v = 0
	}
	*n = looseInt(int(v))
	return nil
}

type Video struct {
	ExternalID     string `json:"external_id"`
	HLSManifestURL string `json:"hlsManifestUrl"`
	VideoPlayer    string `json:"video_player"`
	PosterImageURL string `json:"video_poster_image_url"`
}

type Resource struct {
	Name string `json:"resource_name"`
	URL  string `json:"resource_url"`
}

type Assignment struct {
	Title string `json:"title"`
	// SheetMusicImageURLs is one URL per sheet-music page. Musora sends a scalar
	// string for legacy lessons and an array for songs; stringOrSlice accepts both.
	SheetMusicImageURLs stringOrSlice `json:"sheet_music_image_url"`
}

// Instructor is one entry of a lesson's instructor[]. Slug, Biography and
// CoachCardImage only feed a plex-tv show's own files (tvshow.nfo and its
// poster), so they decode loosely: a shape Musora changes reads as empty,
// never as a failed lesson (hard rule 10).
type Instructor struct {
	Name           string      `json:"name"`
	Slug           looseString `json:"slug"`
	Biography      looseString `json:"biography"`
	CoachCardImage looseString `json:"coach_card_image"`
}

// ParentContent is one entry of a lesson's parent_content_data: a course (or
// pack, or collection) the lesson is in. ID only feeds a plex-tv show's own
// files, so it decodes loosely (looseInt).
type ParentContent struct {
	ID    looseInt `json:"id"`
	Title string   `json:"title"`
}

// SoundsliceRef is one entry of a song's soundslice[] array — a play-along
// score attached to the lesson. Slug is the soundslice SCORE id (e.g. "169230")
// used to fetch the score's YouTube-backed recordings. Length rides flexSeconds
// because soundslice durations are fractional, the same schema landmine that the
// coalesced length_in_seconds field guards against.
type SoundsliceRef struct {
	Slug   string      `json:"soundslice_slug"`
	Title  string      `json:"soundslice_title"`
	Length flexSeconds `json:"soundslice_length_in_second"`
}

type Lesson struct {
	ID               int          `json:"id"`
	Title            string       `json:"title"`
	Description      string       `json:"description"`
	DifficultyString string       `json:"difficulty_string"`
	Brand            string       `json:"brand"`
	PublishedOn      string       `json:"published_on"`
	LengthInSeconds  flexSeconds  `json:"length_in_seconds"`
	Thumbnail        string       `json:"thumbnail"`
	Video            Video        `json:"video"`
	Instructors      []Instructor `json:"instructor"`
	Genre            []struct {
		Name string `json:"name"`
	} `json:"genre"`
	Resources           []Resource      `json:"resources"`
	Assignments         []Assignment    `json:"assignments"`
	Mp3NoDrumsNoClick   string          `json:"mp3_no_drums_no_click_url"`
	Mp3NoDrumsYesClick  string          `json:"mp3_no_drums_yes_click_url"`
	Mp3YesDrumsNoClick  string          `json:"mp3_yes_drums_no_click_url"`
	Mp3YesDrumsYesClick string          `json:"mp3_yes_drums_yes_click_url"`
	ParentContentData   []ParentContent `json:"parent_content_data"`
	Soundslice          []SoundsliceRef `json:"soundslice"`
	// Type is the document's Sanity _type ("song", "course", ...), and
	// HeaderImageURL a guided course's header image (usually square). Both
	// only pick a plex-tv show's artwork (ShowArt), so both decode loosely.
	Type           looseString `json:"type"`
	HeaderImageURL looseString `json:"header_image_url"`
}

// SoundsliceSlug returns the first non-empty soundslice score slug for the
// lesson, or "" if the lesson has no soundslice play-along. A song's playable
// video is a YouTube recording referenced inside its soundslice score, so this
// slug is the entry point to resolving that video when the lesson carries no
// Musora/Vimeo HLS of its own.
func (l *Lesson) SoundsliceSlug() string {
	for _, s := range l.Soundslice {
		if s.Slug != "" {
			return s.Slug
		}
	}
	return ""
}

func ResolveLesson(id int, permIDs string) (*Lesson, error) {
	tpl, err := LoadQuery("resolve_lesson")
	if err != nil {
		return nil, err
	}
	raw, err := Query(WithID(tpl, id), permIDs)
	if err != nil {
		return nil, err
	}
	var res []Lesson
	if err := json.Unmarshal(raw, &res); err != nil || len(res) == 0 {
		return nil, err
	}
	return &res[0], nil
}
