package musora

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// soundsliceBase is the soundslice origin. It is a var (not const) so tests can
// point ResolveSoundsliceRecordings at an httptest server via SetSoundsliceBase;
// production never reassigns it. (Same pattern as sanityBase / SetSanityBase.)
var soundsliceBase = "https://www.soundslice.com"

// SetSoundsliceBase repoints the soundslice origin at u and returns a func that
// restores the previous value, so tests can run hermetically against a stub.
// Production never calls it. (Same pattern as SetSanityBase.)
func SetSoundsliceBase(u string) (restore func()) {
	prev := soundsliceBase
	soundsliceBase = u
	return func() { soundsliceBase = prev }
}

// SoundsliceRecording is one playable recording attached to a soundslice score.
// A song's score carries one recording per mix: "Original" (with drums) and
// "Drumless" (without). YouTubeID is the recording's source video id.
type SoundsliceRecording struct {
	Name      string // "Original" (with drums) / "Drumless" (without)
	YouTubeID string // the YouTube video id (scoredata r[].sd)
}

// ResolveSoundsliceRecordings fetches the soundslice score's recordings (the
// YouTube-backed with/without-drums play-along tracks) for the given score slug.
//
// It GETs <base>/scores/<slug>/embed/scoredata/ with the embed Referer header
// that endpoint requires (no auth, no cookie). The scoredata JSON carries an
// r[] array of recordings; each r[].sd is the recording's YouTube video id and
// r[].name is the mix label. Recordings are returned in score order; any whose
// source id is empty (no playable video) are skipped.
//
// An empty result (the score has no recordings) is returned as (nil, nil) — NOT
// an error — so a video-less song is handled honestly by the caller rather than
// retried forever. A network/HTTP/JSON failure IS returned as an error so the
// caller can retry a transient soundslice outage.
func ResolveSoundsliceRecordings(slug string) ([]SoundsliceRecording, error) {
	// PathEscape the slug as defense-in-depth: slugs are numeric today, but this
	// keeps a stray value from breaking out of the /scores/<slug>/ path segment.
	esc := url.PathEscape(slug)
	endpoint := soundsliceBase + "/scores/" + esc + "/embed/scoredata/"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	// The embed Referer is the ONLY requirement to read scoredata; the eu query
	// param the embed page sometimes carries is irrelevant and is omitted.
	req.Header.Set("Referer", soundsliceBase+"/scores/"+esc+"/embed/?api=1")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("soundslice %d: %s", resp.StatusCode, truncate(string(data), 200))
	}

	var wrap struct {
		R []struct {
			Name string `json:"name"`
			SD   string `json:"sd"`
		} `json:"r"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return nil, err
	}

	var recs []SoundsliceRecording
	for _, r := range wrap.R {
		if r.SD == "" {
			continue // no playable source video for this recording
		}
		recs = append(recs, SoundsliceRecording{Name: r.Name, YouTubeID: r.SD})
	}
	return recs, nil
}
