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
// A song's soundslice_slug arrives in one of two forms, and they live under
// DIFFERENT path segments (each 404s on the other, verified live):
//   - a numeric score id  (e.g. "169230", "225924") → <base>/scores/<id>/embed/scoredata/
//   - an alphanumeric hash (e.g. "8qnVc")            → <base>/slices/<hash>/embed/scoredata/
//
// We pick the segment by slug form (all-digits → scores, else → slices) and, to
// be robust against a mis-classified slug, fall back to the OTHER segment on a
// 404. The request carries the embed Referer that endpoint requires (no auth,
// no cookie). The scoredata JSON carries an r[] array of recordings; each
// r[].sd is the recording's YouTube video id and r[].name is the mix label.
// Recordings are returned in score order; any whose source id is empty (no
// playable video) are skipped.
//
// An empty result (the score has no recordings) is returned as (nil, nil) — NOT
// an error — so a video-less song is handled honestly by the caller rather than
// retried forever. A network/HTTP/JSON failure IS returned as an error so the
// caller can retry a transient soundslice outage.
func ResolveSoundsliceRecordings(slug string) ([]SoundsliceRecording, error) {
	primary, fallback := "slices", "scores"
	if isAllDigits(slug) {
		primary, fallback = "scores", "slices"
	}

	data, status, err := fetchScoredata(primary, slug)
	if err != nil {
		return nil, err
	}
	// The slug form is a strong hint, not a guarantee; if the chosen segment 404s,
	// try the other once before giving up.
	if status == http.StatusNotFound {
		if data, status, err = fetchScoredata(fallback, slug); err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("soundslice %d: %s", status, truncate(string(data), 200))
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

// fetchScoredata GETs the soundslice scoredata JSON for slug under the given path
// segment ("scores" or "slices"), returning the body and HTTP status (or a
// transport error). The slug is PathEscaped as defense-in-depth so a stray value
// cannot break out of the /<seg>/<slug>/ path.
func fetchScoredata(seg, slug string) (body []byte, status int, err error) {
	esc := url.PathEscape(slug)
	endpoint := soundsliceBase + "/" + seg + "/" + esc + "/embed/scoredata/"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	// The embed Referer is the ONLY requirement to read scoredata; the eu query
	// param the embed page sometimes carries is irrelevant and is omitted.
	req.Header.Set("Referer", soundsliceBase+"/"+seg+"/"+esc+"/embed/?api=1")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

// isAllDigits reports whether s is non-empty and contains only ASCII digits — the
// test for a numeric soundslice score id (/scores/) versus a public hash slug
// (/slices/).
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
