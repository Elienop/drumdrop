package musora

import (
	"bufio"
	"context"
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

// reAudioLang accepts only an ISO 639-1/639-2 language code (2–3 letters), so a
// stray DRUMDROP_AUDIO_LANG value can never inject `/`, `]`, or spaces into the
// single-token -f format selector.
var reAudioLang = regexp.MustCompile(`^[a-z]{2,3}$`)

// audioLangFilter returns a yt-dlp format-filter fragment that restricts an
// audio stream to the preferred language, e.g. "[language^=?en]". It returns ""
// (no preference) for an empty or non-ISO value.
//
// The "^=" matches the language code as a prefix ("en" also matches "eng" and
// "en-US"). The "?" immediately AFTER the operator (yt-dlp's none-inclusive
// flag — it must precede the value, "language^=?en", not "language^=en?", which
// yt-dlp rejects as an invalid filter) makes a track whose language tag is
// absent (null) still pass. That is deliberate — Musora tags its dub renditions
// explicitly (es/pt) while the original/default rendition is typically
// English-tagged or untagged, so "<lang>-or-untagged" reliably keeps the
// original and drops the dubs across both tagging schemes.
func audioLangFilter(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if !reAudioLang.MatchString(lang) {
		return ""
	}
	return "[language^=?" + lang + "]"
}

// FormatSelector builds the yt-dlp -f selector for the given quality cap and
// preferred audio language. With no language preference it returns the exact
// historical strings. With a language it prepends a language-preferring
// alternative before each historical alternative, so when an audio track in
// that language (or an untagged one) exists it is chosen at the best available
// quality, and otherwise selection falls through to precisely the old behavior.
func FormatSelector(quality, audioLang string) string {
	af := audioLangFilter(audioLang)
	if quality == "" || quality == "best" {
		if af == "" {
			return "bv*+ba/b"
		}
		return "bv*+ba" + af + "/bv*+ba/b"
	}
	if h, err := strconv.Atoi(quality); err == nil {
		if af == "" {
			return fmt.Sprintf("bv*[height<=%d]+ba/b[height<=%d]/bv*+ba/b", h, h)
		}
		return fmt.Sprintf("bv*[height<=%d]+ba%s/bv*[height<=%d]+ba/b[height<=%d]/bv*+ba%s/bv*+ba/b", h, af, h, h, af)
	}
	if af == "" {
		return "bv*+ba/b"
	}
	return "bv*+ba" + af + "/bv*+ba/b"
}

// YtDlpArgs builds the yt-dlp argv for a lesson's HLS manifest. A literal
// end-of-options token ("--") is inserted immediately before the URL so that
// an HLS URL beginning with a dash cannot be parsed as a yt-dlp option.
func YtDlpArgs(hls, quality, audioLang, outTemplate string) []string {
	return []string{
		"--user-agent", browserUA,
		"--referer", "https://player.vimeo.com/",
		"-f", FormatSelector(quality, audioLang),
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

// fetchToFile downloads url to dest. The media/asset URLs are open-read and
// need no auth: a User-Agent header is enough, no session cookie is attached.
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

// DownloadProgress is a single yt-dlp progress observation for the active
// video download. Total is 0 when yt-dlp does not yet know the size.
type DownloadProgress struct {
	Pct        float64
	Downloaded int64
	Total      int64
	Speed      string
}

type DownloadOpts struct {
	Dir     string
	Index   int
	Quality string
	// AudioLang is the preferred audio-track language (ISO code, e.g. "en"); ""
	// means no preference. It feeds FormatSelector's language-preferring -f
	// selector so multi-audio lessons download the chosen language, not a dub.
	AudioLang     string
	ResourcesOnly bool
	// OnProgress, when non-nil, receives a DownloadProgress for each yt-dlp
	// progress line. When nil, yt-dlp's stdout goes straight to os.Stdout and
	// no progress template is requested (exact pre-callback behaviour).
	OnProgress func(DownloadProgress)
}

// progressSentinel prefixes the --progress-template output lines so the stdout
// scanner can distinguish machine progress lines from yt-dlp's normal output.
const progressSentinel = "DRUMDROP|"

// progressArgs returns the extra yt-dlp args that emit machine-parsable
// progress lines (one per render) prefixed with progressSentinel. The "download:"
// scope restricts the custom template to download progress, leaving other
// yt-dlp output formatting untouched.
func progressArgs() []string {
	return []string{
		"--progress-template",
		// _speed_str (not raw .speed) gives a pre-formatted rate like "3.15MiB/s"
		// — matching _percent_str and what parseProgressLine's tests expect — so
		// the SSE/UI shows a human speed, not a raw bytes/sec float.
		"download:" + progressSentinel + "%(progress._percent_str)s|%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress._speed_str)s",
	}
}

// parseProgressLine parses a single DRUMDROP-prefixed progress line into a
// DownloadProgress. It reports ok=false for any line that is not a DRUMDROP
// progress line. Fields that yt-dlp reports as "NA" (or are otherwise
// unparsable as numbers) are left at their zero value.
func parseProgressLine(line string) (DownloadProgress, bool) {
	if !strings.HasPrefix(line, progressSentinel) {
		return DownloadProgress{}, false
	}
	fields := strings.Split(strings.TrimPrefix(line, progressSentinel), "|")
	if len(fields) != 4 {
		return DownloadProgress{}, false
	}
	var p DownloadProgress
	pctStr := strings.TrimSuffix(strings.TrimSpace(fields[0]), "%")
	if v, err := strconv.ParseFloat(pctStr, 64); err == nil {
		p.Pct = v
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64); err == nil {
		p.Downloaded = v
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64); err == nil {
		p.Total = v
	}
	p.Speed = strings.TrimSpace(fields[3])
	return p, true
}

// scanProgress reads yt-dlp stdout line-by-line: every line is mirrored to
// os.Stdout (preserving the CLI's normal output) and any DRUMDROP progress line
// is additionally parsed and delivered to onProgress.
func scanProgress(r io.Reader, onProgress func(DownloadProgress)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(os.Stdout, line)
		if p, ok := parseProgressLine(line); ok {
			onProgress(p)
		}
	}
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
		title := a.Title
		if title == "" {
			title = "assignment"
		}
		// Songs carry multi-page sheet music (an array of page URLs); legacy
		// lessons carry a single page (a one-element slice). sheetNo keeps counting
		// across pages and assignments so every file is uniquely numbered, and a
		// (pN) page suffix is added only for multi-page assignments — single-page
		// names stay byte-for-byte identical to before.
		pages := a.SheetMusicImageURLs
		for pi, u := range pages {
			if u == "" {
				continue
			}
			sheetNo++
			// Node: ext = url.split('?')[0].split('.').pop() (|| 'png'), .slice(0,4)
			ext := sheetExt(u)
			name := fmt.Sprintf("%02d - %s.%s", sheetNo, Sanitize(title), ext)
			if len(pages) > 1 {
				name = fmt.Sprintf("%02d - %s (p%d).%s", sheetNo, Sanitize(title), pi+1, ext)
			}
			record("sheet-music", u, fetchToFile(u, filepath.Join(dir, "sheet-music", name)))
		}
	}
	return failures
}

// DownloadLesson downloads video (yt-dlp) + resources/stems/sheet-music/poster + writes NFO.
//
// Only the yt-dlp video download is fatal: a non-nil return means the video
// could not be fetched. Auxiliary-artifact failures are logged to os.Stderr but
// never make DownloadLesson fail, so the lesson is not endlessly re-downloaded
// over a permanently-missing resource.
//
// ctx cancels the yt-dlp run: the command runs under exec.CommandContext and is
// killed by process group (SIGKILL to -pid) so yt-dlp and its ffmpeg child both
// die. A nil ctx is treated as context.Background(), preserving the original CLI
// behaviour byte-for-byte (the run is never canceled out from under it).
func DownloadLesson(ctx context.Context, l *Lesson, o DownloadOpts) error {
	if ctx == nil {
		ctx = context.Background()
	}
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
		args := YtDlpArgs(hls, o.Quality, o.AudioLang, filepath.Join(dir, base+".%(ext)s"))
		if o.OnProgress != nil {
			args = append(progressArgs(), args...)
		}
		cmd := exec.CommandContext(ctx, "yt-dlp", args...)
		// On context cancel, kill yt-dlp AND its ffmpeg child. The mechanism is
		// platform-specific (process-group SIGKILL on Unix; the os/exec default on
		// Windows) — see configureCancelKill in proc_kill_{unix,windows}.go.
		configureCancelKill(cmd)
		cmd.Stderr = os.Stderr
		if o.OnProgress != nil {
			// Capture stdout so progress lines can be parsed; scanProgress still
			// mirrors every line to os.Stdout, so the CLI output is preserved.
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return err
			}
			if err := cmd.Start(); err != nil {
				return err
			}
			scanProgress(stdout, o.OnProgress)
			if err := cmd.Wait(); err != nil {
				return err
			}
		} else {
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				return err
			}
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
