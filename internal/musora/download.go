package musora

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func FormatSelector(quality string) string {
	if quality == "" || quality == "best" {
		return "bv*+ba/b"
	}
	if h, err := strconv.Atoi(quality); err == nil {
		return fmt.Sprintf("bv*[height<=%d]+ba/b[height<=%d]/bv*+ba/b", h, h)
	}
	return "bv*+ba/b"
}

// YtDlpArgs builds the yt-dlp argv for a lesson's HLS manifest. A literal
// end-of-options token ("--") is inserted immediately before the URL so that
// an HLS URL beginning with a dash cannot be parsed as a yt-dlp option.
func YtDlpArgs(hls, quality, outTemplate string) []string {
	return []string{
		"--user-agent", browserUA,
		"--referer", "https://player.vimeo.com/",
		"-f", FormatSelector(quality),
		"--merge-output-format", "mp4",
		"--write-subs", "--sub-langs", "all",
		"--no-warnings", "--newline",
		"-o", outTemplate,
		"--",
		hls,
	}
}

var (
	// Node's sanitizeName removes control chars (\p{Cc}) entirely...
	reControl = regexp.MustCompile(`[\x00-\x1f\x7f-\x9f]`)
	// ...then replaces these filesystem-unsafe chars with '-'.
	reUnsafe = regexp.MustCompile(`[/\\:*?"<>|]`)
)

// Sanitize mirrors src/util.mjs:sanitizeName: strip control chars, replace
// unsafe chars with '-', collapse whitespace, trim trailing dots/spaces,
// fall back to "untitled", then rune-safely cap at 150 chars and trim again.
func Sanitize(name string) string {
	s := reControl.ReplaceAllString(name, "")
	s = reUnsafe.ReplaceAllString(s, "-")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimRight(s, ". ")
	if s == "" {
		s = "untitled"
	}
	if r := []rune(s); len(r) > 150 {
		s = strings.TrimSpace(string(r[:150]))
	}
	return s
}

func fetchToFile(url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", browserUA)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %d %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

type DownloadOpts struct {
	Dir           string
	Index         int
	Quality       string
	ResourcesOnly bool
}

// auxFailure records a single auxiliary artifact whose fetch failed. Auxiliary
// fetches are best-effort: a failure is surfaced (logged) but never fatal, so a
// permanently-missing resource can never trigger infinite lesson re-downloads.
type auxFailure struct {
	Artifact string
	URL      string
	Err      error
}

// fetchAuxArtifacts downloads every non-video artifact for a lesson (poster,
// resources, mp3 play-along stems, sheet-music) into dir and returns the list
// of (artifact, url, error) failures. It does not return early on failure: it
// attempts all artifacts so a single bad URL never hides the rest.
func fetchAuxArtifacts(l *Lesson, dir, base string) []auxFailure {
	var failures []auxFailure
	record := func(artifact, url string, err error) {
		if err != nil {
			failures = append(failures, auxFailure{Artifact: artifact, URL: url, Err: err})
		}
	}

	if thumb := firstNonEmpty(l.Thumbnail, l.Video.PosterImageURL); thumb != "" {
		record("poster", thumb, fetchToFile(thumb, filepath.Join(dir, base+"-poster.jpg")))
	}
	for _, r := range l.Resources {
		if r.URL != "" {
			// Node: sanitizeName(r.resource_name || r.resource_url.split('/').pop())
			name := r.Name
			if name == "" {
				name = urlBasename(r.URL)
			}
			record("resource", r.URL, fetchToFile(r.URL, filepath.Join(dir, "resources", Sanitize(name))))
		}
	}
	mp3s := map[string]string{
		"play-along (no drums, no click).mp3": l.Mp3NoDrumsNoClick,
		"play-along (no drums, click).mp3":    l.Mp3NoDrumsYesClick,
		"play-along (drums, no click).mp3":    l.Mp3YesDrumsNoClick,
		"play-along (drums, click).mp3":       l.Mp3YesDrumsYesClick,
	}
	for name, u := range mp3s {
		if u != "" {
			record("mp3", u, fetchToFile(u, filepath.Join(dir, "play-along", name)))
		}
	}
	sheetNo := 0
	for _, a := range l.Assignments {
		if a.SheetMusicImageURL == "" {
			continue
		}
		sheetNo++
		// Node: ext = url.split('?')[0].split('.').pop() (|| 'png'), .slice(0,4)
		ext := sheetExt(a.SheetMusicImageURL)
		title := a.Title
		if title == "" {
			title = "assignment"
		}
		name := fmt.Sprintf("%02d - %s.%s", sheetNo, Sanitize(title), ext)
		record("sheet-music", a.SheetMusicImageURL, fetchToFile(a.SheetMusicImageURL, filepath.Join(dir, "sheet-music", name)))
	}
	return failures
}

// DownloadLesson downloads video (yt-dlp) + resources/stems/sheet-music/poster + writes NFO.
//
// Only the yt-dlp video download is fatal: a non-nil return means the video
// could not be fetched. Auxiliary-artifact failures are logged to os.Stderr but
// never make DownloadLesson fail, so the lesson is not endlessly re-downloaded
// over a permanently-missing resource.
func DownloadLesson(l *Lesson, o DownloadOpts) error {
	base := fmt.Sprintf("%02d - %s", o.Index, Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if !o.ResourcesOnly && l.Video.HLSManifestURL != "" {
		hls := l.Video.HLSManifestURL
		if !strings.HasPrefix(hls, "http://") && !strings.HasPrefix(hls, "https://") {
			return fmt.Errorf("refusing to invoke yt-dlp: HLS URL is not http(s): %q", hls)
		}
		args := YtDlpArgs(hls, o.Quality, filepath.Join(dir, base+".%(ext)s"))
		cmd := exec.Command("yt-dlp", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	for _, f := range fetchAuxArtifacts(l, dir, base) {
		fmt.Fprintf(os.Stderr, "drumdrop: lesson %d: failed to fetch %s %s: %v\n", l.ID, f.Artifact, f.URL, f.Err)
	}
	return os.WriteFile(filepath.Join(dir, base+".nfo"), []byte(BuildNFO(l)), 0o644)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// urlBasename mirrors Node's url.split('/').pop(): the last '/'-delimited
// segment of the raw URL string (query string included, as in the reference).
func urlBasename(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// sheetExt mirrors Node's (url.split('?')[0].split('.').pop() || 'png').slice(0,4):
// drop the query, take the segment after the last '.', default to "png" when
// empty, then cap at 4 chars.
func sheetExt(u string) string {
	path := u
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	ext := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		ext = path[i+1:]
	}
	if ext == "" {
		ext = "png"
	}
	if r := []rune(ext); len(r) > 4 {
		ext = string(r[:4])
	}
	return ext
}
