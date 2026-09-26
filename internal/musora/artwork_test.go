package musora

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
)

// sanityImage is a Sanity CDN image URL of the given pixel size.
func sanityImage(hash, size, ext string) string {
	return "https://cdn.sanity.io/images/4032r8py/production_v2/" + hash + "-" + size + "." + ext
}

// TestResolveProjectsTheShowFields pins the resolve query's projection of what
// a plex-tv show's own files need: the guided course's header image (the
// only one of them resolve_lesson.groq did not project before), beside the
// fields it already had.
func TestResolveProjectsTheShowFields(t *testing.T) {
	q, err := LoadQuery("resolve_lesson")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`'header_image_url': header_image_url.asset->url`,
		`'thumbnail': thumbnail.asset->url`,
		`'type': _type`,
		`"coach_card_image": coach_card_image.asset->url`,
		`'thumbnail': thumbnail_url.asset->url`,
		`"biography": short_bio[0].children[0].text`,
		`'slug': slug.current`,
		`"id": railcontent_id`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("resolve_lesson.groq does not project %s", want)
		}
	}
}

// TestLessonDecodesShowFields proves the fields a plex-tv show's own files
// need decode from a resolve answer: the parent's id, the instructor's slug,
// biography and coach card, the type and the header image. They only feed
// optional files, so any other shape (a number, an object, an array, null)
// reads as empty and never fails the lesson, which would fail its download
// (hard rule 10).
func TestLessonDecodesShowFields(t *testing.T) {
	const good = `[{"id":7,"title":"L","type":"guided-course",
	  "header_image_url":"https://cdn.sanity.io/images/p/d/h-4500x4500.png",
	  "parent_content_data":[{"id":455014,"title":"Kick, Snare, Hat"},{"id":"42","title":"Two"}],
	  "instructor":[{"name":"Ash Soan","slug":"ash-soan","biography":"<p>Session drummer.</p>","coach_card_image":"https://cdn.sanity.io/images/p/d/c-1947x2832.png","thumbnail":"https://cdn.sanity.io/images/p/d/p-600x600.jpg"}]}]`
	var res []Lesson
	if err := json.Unmarshal([]byte(good), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	l := res[0]
	in := l.Instructors[0]
	if l.Type != "guided-course" || l.HeaderImageURL != "https://cdn.sanity.io/images/p/d/h-4500x4500.png" ||
		l.ParentContentData[0].ID != 455014 || l.ParentContentData[1].ID != 42 || l.ParentContentData[0].Title != "Kick, Snare, Hat" ||
		in.Slug != "ash-soan" || in.Biography != "<p>Session drummer.</p>" || in.CoachCardImage != "https://cdn.sanity.io/images/p/d/c-1947x2832.png" ||
		in.Thumbnail != "https://cdn.sanity.io/images/p/d/p-600x600.jpg" {
		t.Errorf("decoded %+v", l)
	}

	for _, odd := range []string{`null`, `12`, `{"a":1}`, `["x"]`, `true`, `"not-a-number"`, `1e99`} {
		doc := `[{"id":7,"title":"L","type":` + odd + `,"header_image_url":` + odd +
			`,"parent_content_data":[{"id":` + odd + `,"title":"P"}],"instructor":[{"name":"N","slug":` + odd +
			`,"biography":` + odd + `,"coach_card_image":` + odd + `,"thumbnail":` + odd + `}]}]`
		var res []Lesson
		if err := json.Unmarshal([]byte(doc), &res); err != nil {
			t.Errorf("%s: decode failed: %v", odd, err)
			continue
		}
		l := res[0]
		in := l.Instructors[0]
		if l.ID != 7 || l.ParentContentData[0].Title != "P" || in.Name != "N" {
			t.Errorf("%s: the rest of the lesson was lost: %+v", odd, l)
		}
		if odd != `"not-a-number"` && (l.Type != "" || l.HeaderImageURL != "" || in.Slug != "" || in.Biography != "" || in.CoachCardImage != "" || in.Thumbnail != "") {
			t.Errorf("%s: strings = %q %q %q %q %q %q, want all empty", odd, l.Type, l.HeaderImageURL, in.Slug, in.Biography, in.CoachCardImage, in.Thumbnail)
		}
		if l.ParentContentData[0].ID != 0 && odd != `12` {
			t.Errorf("%s: parent id = %d, want 0", odd, l.ParentContentData[0].ID)
		}
	}
}

// TestJPEGURL proves a Sanity image is asked for as JPEG (fm=jpg, any query
// it had kept), a protocol-relative URL as https, and any other URL is left
// as it is.
func TestJPEGURL(t *testing.T) {
	for in, want := range map[string]string{
		sanityImage("abc", "1920x1080", "png"):                   sanityImage("abc", "1920x1080", "png") + "?fm=jpg",
		sanityImage("abc", "1920x1080", "webp") + "?w=500":       sanityImage("abc", "1920x1080", "webp") + "?fm=jpg&w=500",
		sanityImage("abc", "1920x1080", "png") + "?fm=png":       sanityImage("abc", "1920x1080", "png") + "?fm=jpg",
		"https://i.vimeocdn.com/video/1-d_640.jpg":               "https://i.vimeocdn.com/video/1-d_640.jpg",
		"https://i.vimeocdn.com/video/1 d.jpg":                   "https://i.vimeocdn.com/video/1 d.jpg",
		"https://cdn.sanity.io/files/4032r8py/production_v2/a.x": "https://cdn.sanity.io/files/4032r8py/production_v2/a.x",
		"//cdn.sanity.io/images/p/d/abc-10x10.png":               "https://cdn.sanity.io/images/p/d/abc-10x10.png?fm=jpg",
		"//i.vimeocdn.com/video/1-d_640.jpg":                     "https://i.vimeocdn.com/video/1-d_640.jpg",
		"/images/p/d/abc-10x10.png":                              "/images/p/d/abc-10x10.png",
		"":                                                       "",
	} {
		if got := JPEGURL(in); got != want {
			t.Errorf("JPEGURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestShowArt pins owner ruling #78, 2: the poster is a square header image,
// else a song's own thumbnail, else the first instructor's coach card, else
// the first instructor's photo (the owner's "Photo, else crop"), else, so a
// show is never left without one, the thumbnail; the background is a wide
// thumbnail, and nothing when there is none.
func TestShowArt(t *testing.T) {
	wide := sanityImage("t", "1920x1080", "png")
	square := sanityImage("s", "1500x1500", "jpg")
	header := sanityImage("h", "4500x4500", "png")
	nearSquare := sanityImage("h", "1956x1916", "webp")
	banner := sanityImage("b", "3640x1120", "webp")
	coach := sanityImage("c", "1947x2832", "png")
	photo := sanityImage("p", "600x600", "jpg")
	withCoach := []Instructor{{Name: "No Card", Thumbnail: looseString(photo)}, {Name: "Ash Soan", CoachCardImage: looseString(coach)}}
	photoOnly := []Instructor{{Name: "Nothing"}, {Name: "Photo", Thumbnail: looseString(photo)}}
	for _, tc := range []struct {
		name           string
		doc            *Lesson
		poster, fanart string
	}{
		{"no document", nil, "", ""},
		{"guided course, square header", &Lesson{Thumbnail: wide, HeaderImageURL: looseString(header), Instructors: withCoach}, header, wide},
		{"near-square header", &Lesson{Thumbnail: wide, HeaderImageURL: looseString(nearSquare), Instructors: withCoach}, nearSquare, wide},
		{"wide header: the coach card", &Lesson{Thumbnail: wide, HeaderImageURL: looseString(banner), Instructors: withCoach}, coach, wide},
		{"course: the coach card", &Lesson{Thumbnail: wide, Instructors: withCoach}, coach, wide},
		{"song: its own thumbnail", &Lesson{Type: "song", Thumbnail: square, Instructors: withCoach}, square, ""},
		{"no coach card: the photo", &Lesson{Thumbnail: wide, HeaderImageURL: looseString(banner), Instructors: photoOnly}, photo, wide},
		{"instructor: no coach card, the photo", &Lesson{Instructors: photoOnly[1:]}, photo, ""},
		{"no coach card, no photo: the thumbnail", &Lesson{Thumbnail: wide, Instructors: []Instructor{{Name: "No Card"}}}, wide, wide},
		{"square course thumbnail: no background", &Lesson{Thumbnail: square}, square, ""},
		{"instructor: coach card, no background", &Lesson{Instructors: withCoach[1:]}, coach, ""},
		{"size unknown: no background", &Lesson{Thumbnail: "https://example.com/t.png"}, "https://example.com/t.png", ""},
	} {
		poster, fanart := ShowArt(tc.doc)
		if poster != tc.poster || fanart != tc.fanart {
			t.Errorf("%s: ShowArt = (%q, %q), want (%q, %q)", tc.name, poster, fanart, tc.poster, tc.fanart)
		}
	}
}

// jpegBytes starts like every JPEG file.
var jpegBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'J', 'F', 'I', 'F'}

// TestFetchJPEG proves FetchJPEG returns an image's bytes only when they are
// JPEG (every file it feeds is named .jpg), sends the User-Agent, and says by
// its error whether asking again could change anything: a gone image or one
// that is not JPEG never can (ErrImageMissing), a server that can not be
// reached means no other fetch can succeed now (ErrUnreachable), and anything
// else is worth another try later.
func TestFetchJPEG(t *testing.T) {
	var ua string
	srv := tlsImageServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		serveFetchJPEGCase(w, r)
	}))
	ctx := context.Background()

	got, err := FetchJPEG(ctx, srv.URL+"/ok.jpg")
	if err != nil || string(got) != string(jpegBytes) {
		t.Errorf("ok: %q, %v; want the JPEG bytes", got, err)
	}
	if got, err := FetchJPEG(ctx, srv.URL+"/max.jpg"); err != nil || len(got) != 16<<20 {
		t.Errorf("an image of exactly 16 MiB: %d bytes, %v; want it whole", len(got), err)
	}
	if ua != browserUA {
		t.Errorf("User-Agent = %q, want %q", ua, browserUA)
	}
	for path, want := range map[string]error{
		"/missing.jpg": ErrImageMissing,
		"/gone.jpg":    ErrImageMissing,
		"/png.jpg":     ErrImageMissing,
		"/big.jpg":     ErrImageMissing,
		"/page.jpg":    nil,
		"/busy.jpg":    nil,
	} {
		checkFetchJPEGFails(t, srv.URL, path, want)
	}

	down := httptest.NewTLSServer(http.NotFoundHandler())
	url := down.URL + "/ok.jpg"
	down.Close()
	if _, err := FetchJPEG(ctx, url); !errors.Is(err, ErrUnreachable) {
		t.Errorf("closed server: err = %v, want ErrUnreachable", err)
	}
}

// serveFetchJPEGCase answers each path TestFetchJPEG asks for.
func serveFetchJPEGCase(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/max.jpg", "/big.jpg":
		// 16 MiB, the cap, by value: far above any image of Musora's.
		body := make([]byte, 16<<20+map[bool]int{true: 1}[r.URL.Path == "/big.jpg"])
		copy(body, jpegBytes)
		_, _ = w.Write(body)
	case "/ok.jpg":
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegBytes)
	case "/png.jpg":
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n"))
	case "/page.jpg":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>maintenance</html>"))
	case "/gone.jpg":
		http.Error(w, "gone", http.StatusGone)
	case "/busy.jpg":
		http.Error(w, "busy", http.StatusServiceUnavailable)
	default:
		http.NotFound(w, r)
	}
}

// checkFetchJPEGFails pins that FetchJPEG of path on the server at base
// fails with no bytes: with want (ErrImageMissing) when it is given, else
// with an error worth trying again (neither missing nor unreachable).
func checkFetchJPEGFails(t *testing.T, base, path string, want error) {
	t.Helper()
	got, err := FetchJPEG(context.Background(), base+path)
	if err == nil || got != nil {
		t.Errorf("%s: %q, %v; want an error and no bytes", path, got, err)
		return
	}
	if want != nil && !errors.Is(err, want) {
		t.Errorf("%s: err = %v, want %v", path, err, want)
	}
	if want == nil && (errors.Is(err, ErrImageMissing) || errors.Is(err, ErrUnreachable)) {
		t.Errorf("%s: err = %v, want one worth trying again", path, err)
	}
}

// tlsImageServer is an https test server serving h, which FetchJPEG's client
// trusts (and waits for no longer than the real one) until the test ends.
func tlsImageServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	prev := httpClient
	c := srv.Client()
	c.Timeout = prev.Timeout
	httpClient = c
	t.Cleanup(func() {
		httpClient = prev
		srv.Close()
	})
	return srv
}

// TestFetchJPEGRefusesAURLItNeverFetches pins that an image URL the client
// can never fetch (empty, not https, no host name, not a URL) is a missing
// image, asked for nowhere: never "could not be reached", which would stop
// the whole show-file step, cycle after cycle, over one bad field. A host
// with a port and no name is no host: the client would dial this machine.
func TestFetchJPEGRefusesAURLItNeverFetches(t *testing.T) {
	asked := 0
	srv := tlsImageServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		_, _ = w.Write(jpegBytes)
	}))
	plain := "http" + strings.TrimPrefix(srv.URL, "https") + "/ok.jpg"
	for _, u := range []string{"", "https:///x.jpg", "https://:/x.jpg", "https://:443/x.jpg", "//:443/x.jpg", plain, "ftp://cdn.sanity.io/x.jpg", "https://cdn.sanity.io/%zz"} {
		got, err := FetchJPEG(context.Background(), u)
		var ue *neturl.Error
		if got != nil || !errors.Is(err, ErrImageMissing) || errors.Is(err, ErrUnreachable) || errors.As(err, &ue) {
			t.Errorf("FetchJPEG(%q) = %q, %v; want ErrImageMissing only", u, got, err)
		}
	}
	if asked != 0 {
		t.Errorf("%d requests made, want none", asked)
	}
}

// TestFetchJPEGAsksAgainForAURLWithNoScheme pins that an image URL with no
// scheme at all (a path, or a host and path with no "//") is never fetched
// and never final: it may be Musora changing how it writes image URLs, so
// the error is one worth trying again later (the show gets no tvshow.nfo
// yet), and not "could not be reached" either, which would stop every other
// show's files.
func TestFetchJPEGAsksAgainForAURLWithNoScheme(t *testing.T) {
	asked := 0
	tlsImageServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		_, _ = w.Write(jpegBytes)
	}))
	for _, u := range []string{"/images/p/d/x-10x10.png", "cdn.sanity.io/images/p/d/x-10x10.png"} {
		got, err := FetchJPEG(context.Background(), u)
		var ue *neturl.Error
		if got != nil || err == nil || errors.Is(err, ErrImageMissing) || errors.Is(err, ErrUnreachable) || errors.As(err, &ue) {
			t.Errorf("FetchJPEG(%q) = %q, %v; want an error worth trying again", u, got, err)
		}
	}
	if asked != 0 {
		t.Errorf("%d requests made, want none", asked)
	}
}

// TestFetchJPEGReadsAProtocolRelativeURLAsHTTPS proves a URL with a host and
// no scheme ("//host/path") is fetched over https.
func TestFetchJPEGReadsAProtocolRelativeURLAsHTTPS(t *testing.T) {
	srv := tlsImageServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(jpegBytes)
	}))
	u := strings.TrimPrefix(srv.URL, "https:") + "/ok.jpg"
	if got, err := FetchJPEG(context.Background(), u); err != nil || string(got) != string(jpegBytes) {
		t.Errorf("FetchJPEG(%q) = %q, %v; want the JPEG bytes", u, got, err)
	}
}
