package musora

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
//
// --force-overwrites makes a retry/re-download always produce a fresh, complete
// file: without it yt-dlp skips an output that already exists, so a truncated
// .mp4 left by a cancelled/partial prior attempt could be recorded as a success.
func YtDlpArgs(hls, quality, audioLang, outTemplate string) []string {
	return []string{
		"--user-agent", browserUA,
		"--referer", "https://player.vimeo.com/",
		"-f", FormatSelector(quality, audioLang),
		"--merge-output-format", "mp4",
		"--write-subs", "--sub-langs", "all",
		"--no-warnings", "--newline",
		"--force-overwrites",
		"-o", outTemplate,
		"--",
		hls,
	}
}

// YtDlpArgsYouTube builds the yt-dlp argv for a YouTube watch URL (a soundslice
// song recording). It mirrors YtDlpArgs — same format selector, mp4 merge, and
// the "--" end-of-options token before the URL — but deliberately drops two
// things: the Vimeo referer (wrong origin for YouTube) and subtitle fetching
// (YouTube auto-captions would spew a sidecar file per language).
//
// --force-overwrites is kept (as in YtDlpArgs): a retry/re-download must never
// reuse a partial file left by a cancelled prior attempt.
func YtDlpArgsYouTube(videoURL, quality, audioLang, outTemplate string) []string {
	return []string{
		"--user-agent", browserUA,
		"-f", FormatSelector(quality, audioLang),
		"--merge-output-format", "mp4",
		"--no-warnings", "--newline",
		"--force-overwrites",
		"-o", outTemplate,
		"--",
		videoURL,
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

// PosterSuffix ends the name DownloadLesson gives a lesson's image,
// "<base>-poster.jpg". The default layout keeps it; the plex-tv layout places
// the image as "<episode base>.jpg" instead (the scheduler's episodeSuffix).
const PosterSuffix = "-poster.jpg"

// fetchToFile downloads url to dest, a path inside the open folder root. The
// media/asset URLs are open-read and need no auth: a User-Agent header is
// enough, no session cookie is attached.
func fetchToFile(ctx context.Context, url string, root *os.Root, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", browserUA)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %d %s", resp.StatusCode, url)
	}
	return writeInRoot(root, dest, resp.Body)
}

// TempSuffix names the file writeInRoot writes before it takes its place. The
// worker's partial-file cleanup matches it, so one a crash left behind is
// never moved into the library.
const TempSuffix = ".drumdrop-part"

// writeInRoot writes r to name, a path inside the open folder root, creating
// its folders. Everything goes through root (os.Root), so nothing is written
// outside it, whatever symlinks the folders hold. The bytes go to a new file
// (O_EXCL: never through a file or a symlink already at that name), which is
// then renamed over name: a symlink planted at name is replaced, never
// followed, so the write can not land on another file inside root either. A
// failed write removes its new file.
func writeInRoot(root *os.Root, name string, r io.Reader) error {
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	tmp := name + TempSuffix
	if err := root.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err // a leftover of a crash, or something in the way
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
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
	Dir string
	// Root, when set, is the folder the lesson's files must stay inside (the
	// worker passes the job's private folder, which is also Dir): Dir must be
	// inside it, and every file DrumDrop itself writes (poster, resources,
	// play-along, sheet music, the nfo) goes through it (os.Root), never
	// through a symlink out of it. Empty means Dir. yt-dlp writes its video by
	// path: see DownloadLesson.
	Root    string
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
// resources, mp3 play-along stems, sheet-music) into dir, a folder inside the
// open folder root, and returns the list of (artifact, url, error) failures.
// It does not return early on failure: it attempts all artifacts so a single
// bad URL never hides the rest. ctx cancels every fetch: once the download is
// stopped, each remaining one fails at once instead of writing on.
func fetchAuxArtifacts(ctx context.Context, l *Lesson, root *os.Root, dir, base string) []auxFailure {
	var failures []auxFailure
	for _, f := range auxFetches(l, dir, base) {
		if err := fetchToFile(ctx, f.url, root, f.dest); err != nil {
			failures = append(failures, auxFailure{Artifact: f.artifact, URL: f.url, Err: err})
		}
	}
	return failures
}

// auxFetch is one auxiliary artifact to fetch: its kind (as an auxFailure
// names it), its URL, and the file it is written to, relative to the open root.
type auxFetch struct {
	artifact, url, dest string
}

// auxFetches lists a lesson's auxiliary artifacts in the order they are
// fetched: the poster, the resources, the play-along stems, the sheet music.
func auxFetches(l *Lesson, dir, base string) []auxFetch {
	var out []auxFetch
	if thumb := firstNonEmpty(l.Thumbnail, l.Video.PosterImageURL); thumb != "" {
		// Named .jpg, so asked for as JPEG: Musora stores many thumbnails as
		// PNG, which Sanity converts on request (JPEGURL).
		out = append(out, auxFetch{artifact: "poster", url: JPEGURL(thumb), dest: filepath.Join(dir, base+PosterSuffix)})
	}
	out = append(out, resourceFetches(l.Resources, dir)...)
	out = append(out, playAlongFetches(l, dir)...)
	return append(out, sheetMusicFetches(l.Assignments, dir)...)
}

// resourceFetches lists the lesson's attached resources, skipping any without
// a URL.
func resourceFetches(resources []Resource, dir string) []auxFetch {
	var out []auxFetch
	for _, r := range resources {
		if r.URL != "" {
			// Node: sanitizeName(r.resource_name || r.resource_url.split('/').pop())
			name := firstNonEmpty(r.Name, urlBasename(r.URL))
			out = append(out, auxFetch{artifact: "resource", url: r.URL, dest: filepath.Join(dir, "resources", Sanitize(name))})
		}
	}
	return out
}

// playAlongFetches lists the mp3 play-along stems the lesson has.
func playAlongFetches(l *Lesson, dir string) []auxFetch {
	mp3s := map[string]string{
		"play-along (no drums, no click).mp3": l.Mp3NoDrumsNoClick,
		"play-along (no drums, click).mp3":    l.Mp3NoDrumsYesClick,
		"play-along (drums, no click).mp3":    l.Mp3YesDrumsNoClick,
		"play-along (drums, click).mp3":       l.Mp3YesDrumsYesClick,
	}
	var out []auxFetch
	for name, u := range mp3s {
		if u != "" {
			out = append(out, auxFetch{artifact: "mp3", url: u, dest: filepath.Join(dir, "play-along", name)})
		}
	}
	return out
}

// sheetMusicFetches lists every sheet-music page of the lesson's assignments.
// Songs carry multi-page sheet music (an array of page URLs); legacy lessons
// carry a single page (a one-element slice). The number keeps counting across
// pages and assignments so every file is uniquely numbered, and a (pN) page
// suffix is added only for multi-page assignments — single-page names stay
// byte-for-byte identical to before. An empty page URL is skipped and takes
// no number.
func sheetMusicFetches(assignments []Assignment, dir string) []auxFetch {
	var out []auxFetch
	sheetNo := 0
	for _, a := range assignments {
		title := Sanitize(firstNonEmpty(a.Title, "assignment"))
		pages := a.SheetMusicImageURLs
		for pi, u := range pages {
			if u == "" {
				continue
			}
			sheetNo++
			// Node: ext = url.split('?')[0].split('.').pop() (|| 'png'), .slice(0,4)
			ext := sheetExt(u)
			name := fmt.Sprintf("%02d - %s.%s", sheetNo, title, ext)
			if len(pages) > 1 {
				name = fmt.Sprintf("%02d - %s (p%d).%s", sheetNo, title, pi+1, ext)
			}
			out = append(out, auxFetch{artifact: "sheet-music", url: u, dest: filepath.Join(dir, "sheet-music", name)})
		}
	}
	return out
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
// die. It cancels the auxiliary fetches too, and a download canceled before its
// nfo is written returns ctx's error. A nil ctx is treated as context.Background(), preserving the original CLI
// behaviour byte-for-byte (the run is never canceled out from under it).
//
// The lesson folder and every file DownloadLesson writes itself go through the
// open folder o.Root (see DownloadOpts.Root). yt-dlp is a separate program
// given an output path: it writes the video by path, so the folder it writes
// in must be trusted not to hold a symlink planted by someone else.
func DownloadLesson(ctx context.Context, l *Lesson, o DownloadOpts) error {
	if ctx == nil {
		ctx = context.Background()
	}
	base := fmt.Sprintf("%02d - %s", o.Index, Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	rootDir := o.Root
	if rootDir == "" {
		rootDir = o.Dir
	}
	rel, err := filepath.Rel(rootDir, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("refusing to download into %q: it is not inside %q", dir, rootDir)
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(rel, 0o755); err != nil {
		return err
	}
	if !o.ResourcesOnly {
		if l.Video.HLSManifestURL != "" {
			// A lesson with its own Musora/Vimeo HLS: download it as the single
			// "<base>.mp4" video.
			hls := l.Video.HLSManifestURL
			if !strings.HasPrefix(hls, "http://") && !strings.HasPrefix(hls, "https://") {
				return fmt.Errorf("refusing to invoke yt-dlp: HLS URL is not http(s): %q", hls)
			}
			args := YtDlpArgs(hls, o.Quality, o.AudioLang, filepath.Join(dir, base+".%(ext)s"))
			if o.OnProgress != nil {
				args = append(progressArgs(), args...)
			}
			if err := runYtDlp(ctx, args, o.OnProgress); err != nil {
				return err
			}
		} else if slug := l.SoundsliceSlug(); slug != "" {
			// A song has no Musora/Vimeo video of its own; its playable videos are
			// the YouTube-backed recordings referenced inside its soundslice score.
			// Download EACH recording (e.g. Original + Drumless) as a bracket-tagged
			// version file: "<base> [Original].mp4" / "<base> [Drumless].mp4".
			recs, err := ResolveSoundsliceRecordings(slug)
			if err != nil {
				// Fatal so the job retries a transient soundslice failure rather than
				// silently marking the song downloaded with no video.
				return fmt.Errorf("resolve soundslice %s: %w", slug, err)
			}
			for i, rec := range recs {
				label := Sanitize(rec.Name)
				if label == "" {
					label = fmt.Sprintf("recording %d", i+1)
				}
				out := filepath.Join(dir, fmt.Sprintf("%s [%s].%%(ext)s", base, label))
				args := YtDlpArgsYouTube("https://www.youtube.com/watch?v="+rec.YouTubeID, o.Quality, o.AudioLang, out)
				if o.OnProgress != nil {
					args = append(progressArgs(), args...)
				}
				if err := runYtDlp(ctx, args, o.OnProgress); err != nil {
					return fmt.Errorf("download recording %q (%s): %w", rec.Name, rec.YouTubeID, err)
				}
			}
			// recs empty -> the song honestly has no video; not an error.
		}
	}
	for _, f := range fetchAuxArtifacts(ctx, l, root, rel, base) {
		fmt.Fprintf(os.Stderr, "drumdrop: lesson %d: failed to fetch %s %s: %v\n", l.ID, f.Artifact, f.URL, f.Err)
	}
	// A download stopped during the fetches is not finished, whatever they
	// wrote: it says so, and writes nothing more.
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeInRoot(root, filepath.Join(rel, base+".nfo"), strings.NewReader(BuildNFO(l)))
}

// runYtDlp executes one yt-dlp invocation with the given argv and returns its
// error (nil on success). It is shared byte-for-byte by the HLS and the
// soundslice-song download paths so both honour the same cancellation and
// progress semantics:
//
//   - ctx cancels the run: the command runs under exec.CommandContext and is
//     killed by process group (configureCancelKill) so yt-dlp and its ffmpeg
//     child both die.
//   - when onProgress is non-nil, stdout is captured and scanned for DRUMDROP
//     progress lines (still mirrored to os.Stdout); when nil, stdout goes
//     straight to os.Stdout — the exact pre-callback behaviour.
func runYtDlp(ctx context.Context, args []string, onProgress func(DownloadProgress)) error {
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	// On context cancel, kill yt-dlp AND its ffmpeg child. The mechanism is
	// platform-specific (process-group SIGKILL on Unix; the os/exec default on
	// Windows) — see configureCancelKill in proc_kill_{unix,windows}.go.
	configureCancelKill(cmd)
	cmd.Stderr = os.Stderr
	if onProgress != nil {
		// Capture stdout so progress lines can be parsed; scanProgress still
		// mirrors every line to os.Stdout, so the CLI output is preserved.
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		scanProgress(stdout, onProgress)
		return cmd.Wait()
	}
	cmd.Stdout = os.Stdout
	return cmd.Run()
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
