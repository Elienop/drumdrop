package musora

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// sanityImageHost serves every Musora image (thumbnail, header_image_url,
// coach_card_image). It converts an image on request: "?fm=jpg" returns JPEG
// bytes whatever the stored format (PNG and WebP are common).
const sanityImageHost = "cdn.sanity.io"

// maxImageBytes bounds an image FetchJPEG holds in memory. Musora's images
// are far smaller as JPEG (measured 2026-09-25: 354 KB for a 1500x1500
// poster, 270 KB for a 1920x1080 background); anything past this is not one.
const maxImageBytes = 16 << 20

// ErrImageMissing is an image that is not there to fetch: the server says it
// is gone (404, 410), it is too large, it is an image that could not be had
// as JPEG, or its URL is one FetchJPEG never fetches (empty, not a URL, a
// scheme other than https, or no host name). Asking again will not change
// the answer.
var ErrImageMissing = errors.New("the image is not available")

// ErrUnreachable is an image server that could not be reached at all (no
// connection, a timeout): every other fetch would fail the same way now.
var ErrUnreachable = errors.New("the image server could not be reached")

// JPEGURL is the URL that serves the image at u as JPEG bytes: a
// protocol-relative URL ("//host/path") is read as https, and a Sanity CDN
// image URL gets fm=jpg (Sanity converts on request); any other URL is
// returned unchanged, as nothing is known about converting it.
func JPEGURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	changed := false
	if p.Scheme == "" && p.Host != "" {
		p.Scheme = "https"
		changed = true
	}
	if p.Host == sanityImageHost && strings.HasPrefix(p.Path, "/images/") {
		q := p.Query()
		q.Set("fm", "jpg")
		p.RawQuery = q.Encode()
		changed = true
	}
	if !changed {
		return u
	}
	return p.String()
}

// ImageSize reads the pixel size a Sanity CDN image URL carries in its file
// name ("<hash>-<width>x<height>.<ext>", Sanity's asset naming). ok is false
// for any other URL.
func ImageSize(u string) (width, height int, ok bool) {
	p, err := url.Parse(u)
	if err != nil || p.Host != sanityImageHost {
		return 0, 0, false
	}
	name := path.Base(p.Path)
	name = strings.TrimSuffix(name, path.Ext(name))
	i := strings.LastIndexByte(name, '-')
	if i < 0 {
		return 0, 0, false
	}
	ws, hs, found := strings.Cut(name[i+1:], "x")
	w, werr := strconv.Atoi(ws)
	h, herr := strconv.Atoi(hs)
	if !found || werr != nil || herr != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// isSquare reports whether the image at u is square, give or take 10% (a
// guided course's header is 4500x4500, or 1956x1916).
func isSquare(u string) bool {
	w, h, ok := ImageSize(u)
	return ok && w*10 <= h*11 && h*10 <= w*11
}

// isWide reports whether the image at u is landscape, at least 4:3 (a
// course's thumbnail is 1920x1080).
func isWide(u string) bool {
	w, h, ok := ImageSize(u)
	return ok && w*3 >= h*4
}

// ShowArt picks the images of a plex-tv show from doc, the Musora document
// the show is named after (owner ruling #78, 2):
//   - poster: the document's square header image (a guided course); a song's
//     own thumbnail (a song has no course, and its thumbnail is square); else
//     the portrait coach card of the first instructor that has one; else the
//     photo (thumbnail_url, square) of the first instructor that has one
//     (the owner's "Photo, else crop", 2026-09-25); else the document's
//     thumbnail, whatever its shape (Plex crops it), so a course whose
//     instructors have neither still gets a poster;
//   - fanart: the document's thumbnail when it is wide (a course's 16:9); a
//     show with no wide image (a song, an instructor) gets none.
//
// "" is a slot with no image.
func ShowArt(doc *Lesson) (poster, fanart string) {
	if doc == nil {
		return "", ""
	}
	thumb := doc.Thumbnail
	if isWide(thumb) {
		fanart = thumb
	}
	switch header := string(doc.HeaderImageURL); {
	case isSquare(header):
		return header, fanart
	case string(doc.Type) == "song" && thumb != "":
		return thumb, fanart
	}
	for _, in := range doc.Instructors {
		if in.CoachCardImage != "" {
			return string(in.CoachCardImage), fanart
		}
	}
	for _, in := range doc.Instructors {
		if in.Thumbnail != "" {
			return string(in.Thumbnail), fanart
		}
	}
	return thumb, fanart
}

// FetchJPEG downloads the image at u as JPEG bytes (JPEGURL), for a file named
// ".jpg". Like the lesson's own files, it needs no session: a User-Agent is
// enough. The error says what asking again would do:
//   - ErrUnreachable: the server could not be reached (so no other fetch can
//     be now);
//   - ErrImageMissing: the image is not there, too large, or an image that
//     is not JPEG, or u is empty, not a URL, of a scheme other than https,
//     or has no host name (asking again gives the same; checked before any
//     request, since the client reports such a URL as it reports a server
//     out of reach);
//   - anything else (another status, a page that is not an image, a URL with
//     no scheme at all, which Musora changing how it writes image URLs would
//     give, and a later drumdrop may read): try again later.
func FetchJPEG(ctx context.Context, u string) ([]byte, error) {
	ju := JPEGURL(u)
	p, err := url.Parse(ju)
	switch {
	case u == "" || err != nil:
		return nil, fmt.Errorf("%w: %q is not a URL of an image", ErrImageMissing, u)
	case p.Scheme == "":
		// Never wraps a *url.Error: that reads as a server out of reach.
		return nil, fmt.Errorf("%q has no scheme: not fetched, asked again later", u)
	case p.Scheme != "https" || p.Hostname() == "":
		return nil, fmt.Errorf("%w: %q is not an https URL of an image", ErrImageMissing, u)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ju, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImageMissing, err)
	}
	req.Header.Set("User-Agent", browserUA)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		return nil, fmt.Errorf("%w: GET %d %s", ErrImageMissing, resp.StatusCode, u)
	default:
		return nil, fmt.Errorf("GET %d %s", resp.StatusCode, u)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", u, err)
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrImageMissing, u, maxImageBytes)
	}
	if !bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
		if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); strings.HasPrefix(mt, "image/") {
			return nil, fmt.Errorf("%w: %s is %s, not JPEG", ErrImageMissing, u, mt)
		}
		return nil, fmt.Errorf("%s did not answer with an image (Content-Type %q)", u, resp.Header.Get("Content-Type"))
	}
	return data, nil
}
