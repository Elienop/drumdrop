package musora

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ResolveSoundsliceRecordings must fetch the score's scoredata, map each
// recording's name + source id (r[].sd, the YouTube id) in score order, and
// carry the embed Referer header soundslice requires.
func TestResolveSoundsliceRecordings(t *testing.T) {
	const slug = "169230"
	var gotReferer, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"Even Flow","a":"Pearl Jam","r":[
		  {"id":295759,"name":"Original","sd":"MIdvUCCh8sA"},
		  {"id":936560,"name":"Drumless","sd":"J_CFgiS4C3U"}
		]}`))
	}))
	defer srv.Close()
	defer SetSoundsliceBase(srv.URL)()

	recs, err := ResolveSoundsliceRecordings(slug)
	if err != nil {
		t.Fatalf("ResolveSoundsliceRecordings: %v", err)
	}
	want := []SoundsliceRecording{
		{Name: "Original", YouTubeID: "MIdvUCCh8sA"},
		{Name: "Drumless", YouTubeID: "J_CFgiS4C3U"},
	}
	if len(recs) != len(want) {
		t.Fatalf("recordings = %d, want %d: %+v", len(recs), len(want), recs)
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Errorf("recording[%d] = %+v, want %+v", i, recs[i], want[i])
		}
	}
	if wantPath := "/scores/" + slug + "/embed/scoredata/"; gotPath != wantPath {
		t.Errorf("request path = %q, want %q", gotPath, wantPath)
	}
	if wantRef := srv.URL + "/scores/" + slug + "/embed/?api=1"; gotReferer != wantRef {
		t.Errorf("Referer = %q, want %q", gotReferer, wantRef)
	}
}

// A recording with an empty source id is skipped (no playable YouTube video).
func TestResolveSoundsliceRecordingsSkipsEmptySD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"r":[
		  {"id":1,"name":"Original","sd":"abc123"},
		  {"id":2,"name":"NoVideo","sd":""}
		]}`))
	}))
	defer srv.Close()
	defer SetSoundsliceBase(srv.URL)()

	recs, err := ResolveSoundsliceRecordings("1")
	if err != nil {
		t.Fatalf("ResolveSoundsliceRecordings: %v", err)
	}
	if len(recs) != 1 || recs[0].YouTubeID != "abc123" {
		t.Fatalf("recordings = %+v, want only the abc123 recording", recs)
	}
}

// An empty recordings array is an honest "no video", returned as (nil, nil) so
// the caller treats the song as video-less rather than retrying forever.
func TestResolveSoundsliceRecordingsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"x","r":[]}`))
	}))
	defer srv.Close()
	defer SetSoundsliceBase(srv.URL)()

	recs, err := ResolveSoundsliceRecordings("1")
	if err != nil {
		t.Fatalf("empty recordings must not be an error: %v", err)
	}
	if recs != nil {
		t.Fatalf("recordings = %+v, want nil for an empty score", recs)
	}
}

// A non-200 response IS an error so the caller (DownloadLesson) can retry a
// transient soundslice failure rather than silently producing no video.
func TestResolveSoundsliceRecordingsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer SetSoundsliceBase(srv.URL)()

	if _, err := ResolveSoundsliceRecordings("1"); err == nil {
		t.Fatal("ResolveSoundsliceRecordings on a 500 returned nil error, want an error")
	}
}

// SetSoundsliceBase restores the previous origin so tests do not leak the stub
// base into one another.
func TestSetSoundsliceBaseRestores(t *testing.T) {
	orig := soundsliceBase
	restore := SetSoundsliceBase("https://stub.example")
	if soundsliceBase != "https://stub.example" {
		t.Fatalf("base not overridden: %q", soundsliceBase)
	}
	restore()
	if soundsliceBase != orig {
		t.Fatalf("base not restored: %q, want %q", soundsliceBase, orig)
	}
}
