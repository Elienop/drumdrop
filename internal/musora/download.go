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

// YtDlpArgs builds the yt-dlp argv for a lesson's HLS manifest.
func YtDlpArgs(hls, quality, outTemplate string) []string {
	return []string{
		"--user-agent", browserUA,
		"--referer", "https://player.vimeo.com/",
		"-f", FormatSelector(quality),
		"--merge-output-format", "mp4",
		"--write-subs", "--sub-langs", "all",
		"--no-warnings", "--newline",
		"-o", outTemplate,
		hls,
	}
}

var reUnsafe = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]`)

func Sanitize(name string) string {
	s := reUnsafe.ReplaceAllString(name, "-")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimRight(s, ". ")
	if s == "" {
		return "untitled"
	}
	if len(s) > 150 {
		s = s[:150]
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

// DownloadLesson downloads video (yt-dlp) + resources/stems/sheet-music/poster + writes NFO.
func DownloadLesson(l *Lesson, o DownloadOpts) error {
	base := fmt.Sprintf("%02d - %s", o.Index, Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if !o.ResourcesOnly && l.Video.HLSManifestURL != "" {
		args := YtDlpArgs(l.Video.HLSManifestURL, o.Quality, filepath.Join(dir, base+".%(ext)s"))
		cmd := exec.Command("yt-dlp", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	if thumb := firstNonEmpty(l.Thumbnail, l.Video.PosterImageURL); thumb != "" {
		_ = fetchToFile(thumb, filepath.Join(dir, base+"-poster.jpg"))
	}
	for _, r := range l.Resources {
		if r.URL != "" {
			_ = fetchToFile(r.URL, filepath.Join(dir, "resources", Sanitize(r.Name)))
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
			_ = fetchToFile(u, filepath.Join(dir, "play-along", name))
		}
	}
	for i, a := range l.Assignments {
		if a.SheetMusicImageURL != "" {
			_ = fetchToFile(a.SheetMusicImageURL, filepath.Join(dir, "sheet-music", fmt.Sprintf("%02d - %s", i+1, Sanitize(a.Title))))
		}
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
