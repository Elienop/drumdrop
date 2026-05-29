package musora

import "encoding/json"

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
	Title              string `json:"title"`
	SheetMusicImageURL string `json:"sheet_music_image_url"`
}

type Instructor struct {
	Name string `json:"name"`
}

type Lesson struct {
	ID               int          `json:"id"`
	Title            string       `json:"title"`
	Description      string       `json:"description"`
	DifficultyString string       `json:"difficulty_string"`
	Brand            string       `json:"brand"`
	PublishedOn      string       `json:"published_on"`
	LengthInSeconds  int          `json:"length_in_seconds"`
	Thumbnail        string       `json:"thumbnail"`
	Video            Video        `json:"video"`
	Instructors      []Instructor `json:"instructor"`
	Genre            []struct {
		Name string `json:"name"`
	} `json:"genre"`
	Resources           []Resource   `json:"resources"`
	Assignments         []Assignment `json:"assignments"`
	Mp3NoDrumsNoClick   string       `json:"mp3_no_drums_no_click_url"`
	Mp3NoDrumsYesClick  string       `json:"mp3_no_drums_yes_click_url"`
	Mp3YesDrumsNoClick  string       `json:"mp3_yes_drums_no_click_url"`
	Mp3YesDrumsYesClick string       `json:"mp3_yes_drums_yes_click_url"`
	ParentContentData   []struct {
		Title string `json:"title"`
	} `json:"parent_content_data"`
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
