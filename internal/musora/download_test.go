package musora

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFormatSelector(t *testing.T) {
	// No audio-language preference: byte-for-byte the historical strings, so the
	// common single-track lesson downloads exactly as before.
	if FormatSelector("", "") != "bv*+ba/b" || FormatSelector("best", "") != "bv*+ba/b" {
		t.Fatal("default")
	}
	if FormatSelector("720", "") != "bv*[height<=720]+ba/b[height<=720]/bv*+ba/b" {
		t.Fatal("cap")
	}
}

// With an audio language, the selector PREFERS audio that is tagged with that
// language or has no language tag at all (the "?" none-inclusive flag), so the
// original/English rendition wins over explicitly-tagged dubs, while every
// historical fallback alternative is preserved verbatim after it.
func TestFormatSelectorAudioLang(t *testing.T) {
	if got, want := FormatSelector("", "en"), "bv*+ba[language^=?en]/bv*+ba/b"; got != want {
		t.Fatalf("best+en = %q, want %q", got, want)
	}
	if got, want := FormatSelector("best", "en"), "bv*+ba[language^=?en]/bv*+ba/b"; got != want {
		t.Fatalf("best+en = %q, want %q", got, want)
	}
	want1080 := "bv*[height<=1080]+ba[language^=?en]/bv*[height<=1080]+ba/b[height<=1080]/bv*+ba[language^=?en]/bv*+ba/b"
	if got := FormatSelector("1080", "en"); got != want1080 {
		t.Fatalf("1080+en = %q, want %q", got, want1080)
	}
	// A non-default language is honoured.
	if got, want := FormatSelector("720", "pt"), "bv*[height<=720]+ba[language^=?pt]/bv*[height<=720]+ba/b[height<=720]/bv*+ba[language^=?pt]/bv*+ba/b"; got != want {
		t.Fatalf("720+pt = %q, want %q", got, want)
	}
	// Garbage / non-ISO values fall back to no language preference (never emit a
	// broken filter), matching the no-pref strings exactly.
	if got, want := FormatSelector("720", "english"), "bv*[height<=720]+ba/b[height<=720]/bv*+ba/b"; got != want {
		t.Fatalf("720+garbage = %q, want %q", got, want)
	}
	if got, want := FormatSelector("720", ""), "bv*[height<=720]+ba/b[height<=720]/bv*+ba/b"; got != want {
		t.Fatalf("720+empty = %q, want %q", got, want)
	}
}

func TestYtDlpArgs(t *testing.T) {
	args := YtDlpArgs("https://m3u8", "720", "en", "/out/%(ext)s")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--write-subs", "--referer https://player.vimeo.com/", "height<=720", "language^=?en", "https://m3u8"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
	}
}

// The end-of-options token "--" must appear immediately before the URL so a URL
// beginning with a dash cannot be parsed as a yt-dlp option.
func TestYtDlpArgsEndOfOptionsBeforeURL(t *testing.T) {
	const hls = "-evil://m3u8"
	args := YtDlpArgs(hls, "720", "en", "/out/%(ext)s")
	if len(args) < 2 {
		t.Fatalf("args too short: %v", args)
	}
	if args[len(args)-1] != hls {
		t.Fatalf("URL must be the final arg: got %q, want %q", args[len(args)-1], hls)
	}
	if args[len(args)-2] != "--" {
		t.Fatalf("expected %q immediately before URL, got %q (args: %v)", "--", args[len(args)-2], args)
	}
	// "--" must appear exactly once and only as the penultimate arg.
	for i := 0; i < len(args)-2; i++ {
		if args[i] == "--" {
			t.Fatalf("stray %q token at index %d: %v", "--", i, args)
		}
	}
}

// A non-http(s) HLS URL must be rejected before yt-dlp is ever invoked.
func TestDownloadLessonRejectsNonHTTPHLS(t *testing.T) {
	for _, bad := range []string{"-evil://m3u8", "file:///etc/passwd", "ftp://x/y.m3u8", "javascript:alert(1)"} {
		l := &Lesson{ID: 7, Title: "Bad", Video: Video{HLSManifestURL: bad}}
		err := DownloadLesson(context.Background(), l, DownloadOpts{Dir: t.TempDir(), Index: 1})
		if err == nil {
			t.Fatalf("DownloadLesson accepted non-http(s) HLS URL %q, want error", bad)
		}
		if !strings.Contains(err.Error(), bad) {
			t.Errorf("error for %q should mention the URL: %v", bad, err)
		}
	}
}

// fetchAuxArtifacts must surface a failing fetch as an auxFailure while still
// attempting (and succeeding at) the other artifacts.
func TestFetchAuxArtifactsSurfacesFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "broken") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	l := &Lesson{
		ID:        1,
		Title:     "My Lesson",
		Thumbnail: srv.URL + "/thumb.jpg",
		Resources: []Resource{
			{Name: "Good", URL: srv.URL + "/good.pdf"},
			{Name: "Broken", URL: srv.URL + "/broken.pdf"}, // 404 -> failure
		},
	}

	dir := t.TempDir()
	base := "01 - My Lesson"
	if err := os.MkdirAll(filepath.Join(dir, base), 0o755); err != nil {
		t.Fatal(err)
	}
	failures := fetchAuxArtifacts(l, filepath.Join(dir, base), base)

	if len(failures) != 1 {
		t.Fatalf("failures = %v, want exactly 1", failures)
	}
	f := failures[0]
	if f.Artifact != "resource" {
		t.Errorf("failure artifact = %q, want %q", f.Artifact, "resource")
	}
	if f.URL != srv.URL+"/broken.pdf" {
		t.Errorf("failure URL = %q, want %q", f.URL, srv.URL+"/broken.pdf")
	}
	if f.Err == nil {
		t.Error("failure Err is nil")
	}
	// The good artifacts must still have been written despite the failing one.
	for _, p := range []string{
		filepath.Join(dir, base, base+"-poster.jpg"),
		filepath.Join(dir, base, "resources", "Good"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected good artifact missing: %s (%v)", p, err)
		}
	}
	// The broken resource must not have left a file behind.
	if _, err := os.Stat(filepath.Join(dir, base, "resources", "Broken")); err == nil {
		t.Error("broken resource should not have produced a file")
	}
}

// Even when an aux fetch fails, DownloadLesson (video disabled) must succeed and
// still produce the completed layout (NFO + the artifacts that did fetch).
func TestDownloadLessonAuxFailureNonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "broken") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	l := &Lesson{
		ID:        2,
		Title:     "Mostly OK",
		Thumbnail: srv.URL + "/thumb.jpg",
		Resources: []Resource{
			{Name: "Good", URL: srv.URL + "/good.pdf"},
			{Name: "Broken", URL: srv.URL + "/broken.pdf"}, // 500 -> failure, non-fatal
		},
	}

	dir := t.TempDir()
	if err := DownloadLesson(nil, l, DownloadOpts{Dir: dir, Index: 4, ResourcesOnly: true}); err != nil {
		t.Fatalf("DownloadLesson must not fail on aux fetch failure: %v", err)
	}

	base := "04 - Mostly OK"
	lessonDir := filepath.Join(dir, base)
	for _, p := range []string{
		filepath.Join(lessonDir, base+".nfo"),        // completed layout written
		filepath.Join(lessonDir, base+"-poster.jpg"), // good artifact fetched
		filepath.Join(lessonDir, "resources", "Good"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file missing after non-fatal aux failure: %s (%v)", p, err)
		}
	}
}

// parseProgressLine must recognise DRUMDROP-templated lines, parse the percent
// (with optional whitespace and trailing '%'), bytes, and speed, and reject any
// line that is not a DRUMDROP progress line.
func TestParseProgressLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		ok   bool
		want DownloadProgress
	}{
		{
			name: "mid download",
			line: "DRUMDROP| 23.4%|12345|54321|1.20MiB/s",
			ok:   true,
			want: DownloadProgress{Pct: 23.4, Downloaded: 12345, Total: 54321, Speed: "1.20MiB/s"},
		},
		{
			name: "complete",
			line: "DRUMDROP|100%|54321|54321|2.00MiB/s",
			ok:   true,
			want: DownloadProgress{Pct: 100, Downloaded: 54321, Total: 54321, Speed: "2.00MiB/s"},
		},
		{
			name: "total NA leaves total zero",
			line: "DRUMDROP|  5.0%|1000|NA|512KiB/s",
			ok:   true,
			want: DownloadProgress{Pct: 5, Downloaded: 1000, Total: 0, Speed: "512KiB/s"},
		},
		{
			name: "downloaded NA leaves downloaded zero",
			line: "DRUMDROP|  0.0%|NA|NA|Unknown",
			ok:   true,
			want: DownloadProgress{Pct: 0, Downloaded: 0, Total: 0, Speed: "Unknown"},
		},
		{
			name: "plain yt-dlp line is ignored",
			line: "[download]  23.4% of 1.00MiB at 1.00MiB/s ETA 00:01",
			ok:   false,
		},
		{
			name: "empty line ignored",
			line: "",
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseProgressLine(c.line)
			if ok != c.ok {
				t.Fatalf("parseProgressLine(%q) ok = %v, want %v", c.line, ok, c.ok)
			}
			if !c.ok {
				return
			}
			if got != c.want {
				t.Fatalf("parseProgressLine(%q) = %+v, want %+v", c.line, got, c.want)
			}
		})
	}
}

// Integration: with a fake yt-dlp on PATH emitting DRUMDROP progress lines,
// DownloadLesson must deliver parsed DownloadProgress values to OnProgress.
func TestDownloadLessonOnProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake yt-dlp shell script is POSIX-only")
	}
	binDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo 'DRUMDROP|  0.0%|0|54321|512KiB/s'\n" +
		"echo '[download] some normal line'\n" +
		"echo 'DRUMDROP|100%|54321|54321|2.00MiB/s'\n"
	fake := filepath.Join(binDir, "yt-dlp")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var (
		mu   sync.Mutex
		seen []DownloadProgress
	)
	l := &Lesson{ID: 9, Title: "Prog", Video: Video{HLSManifestURL: "https://example.com/x.m3u8"}}
	err := DownloadLesson(context.Background(), l, DownloadOpts{
		Dir:   t.TempDir(),
		Index: 1,
		OnProgress: func(p DownloadProgress) {
			mu.Lock()
			seen = append(seen, p)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("DownloadLesson: %v", err)
	}
	want := []DownloadProgress{
		{Pct: 0, Downloaded: 0, Total: 54321, Speed: "512KiB/s"},
		{Pct: 100, Downloaded: 54321, Total: 54321, Speed: "2.00MiB/s"},
	}
	if len(seen) != len(want) {
		t.Fatalf("OnProgress called %d times, want %d: %+v", len(seen), len(want), seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("progress[%d] = %+v, want %+v", i, seen[i], want[i])
		}
	}
}

// A context canceled before DownloadLesson runs must abort the yt-dlp run
// promptly (CommandContext): even a fake yt-dlp that sleeps for seconds must
// not stall the call, and DownloadLesson must return a (non-nil) error rather
// than completing the download.
func TestDownloadLessonContextCanceled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake yt-dlp shell script is POSIX-only")
	}
	binDir := t.TempDir()
	// A yt-dlp that would block for 30s if it ran to completion; a canceled
	// context must kill it (process-group SIGKILL) so the call returns fast.
	script := "#!/bin/sh\nsleep 30\n"
	fake := filepath.Join(binDir, "yt-dlp")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	l := &Lesson{ID: 11, Title: "Canceled", Video: Video{HLSManifestURL: "https://example.com/x.m3u8"}}
	done := make(chan error, 1)
	go func() {
		done <- DownloadLesson(ctx, l, DownloadOpts{Dir: t.TempDir(), Index: 1})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("DownloadLesson with a canceled context returned nil, want a cancellation error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DownloadLesson did not return within 10s for a canceled context (yt-dlp not killed)")
	}
}

// When OnProgress is set, the yt-dlp argv must include a --progress-template
// carrying the DRUMDROP sentinel. The template is what the stdout scanner keys
// on, so its absence would silently disable progress.
func TestProgressArgsTemplate(t *testing.T) {
	joined := strings.Join(progressArgs(), " ")
	if !strings.Contains(joined, "--progress-template") {
		t.Fatalf("progressArgs missing --progress-template: %v", progressArgs())
	}
	if !strings.Contains(joined, "DRUMDROP|") {
		t.Fatalf("progressArgs missing DRUMDROP sentinel: %v", progressArgs())
	}
}

func TestSanitize(t *testing.T) {
	if got := Sanitize("Rock/Roll: 1"); got != "Rock-Roll- 1" {
		t.Fatalf("Sanitize = %q", got)
	}
	if Sanitize("") != "untitled" {
		t.Fatal("empty")
	}
}

// Control chars are removed (matching Node's \p{Cc} -> ”), not replaced with '-'.
func TestSanitizeControlChars(t *testing.T) {
	if got := Sanitize("Lesson\t"); got != "Lesson" {
		t.Fatalf("control char: Sanitize = %q, want %q", got, "Lesson")
	}
	if got := Sanitize("a\x00b\x1fc\x7fd"); got != "abcd" {
		t.Fatalf("control chars: Sanitize = %q, want %q", got, "abcd")
	}
}

// Truncation must be rune-safe (never split a multibyte rune) and re-trimmed.
func TestSanitizeRuneSafeTruncation(t *testing.T) {
	in := strings.Repeat("\U0001F600", 60) // 60 emoji, 240 bytes
	got := Sanitize(in)
	if !utf8.ValidString(got) {
		t.Fatalf("Sanitize produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 150 {
		t.Fatalf("Sanitize length = %d runes, want <= 150", n)
	}
}

func TestUrlBasename(t *testing.T) {
	cases := map[string]string{
		"https://cdn/x.pdf":            "x.pdf",
		"https://cdn/dir/pack.zip?a=1": "pack.zip?a=1",
		"noslash":                      "noslash",
	}
	for in, want := range cases {
		if got := urlBasename(in); got != want {
			t.Fatalf("urlBasename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSheetExt(t *testing.T) {
	cases := map[string]string{
		"https://cdn/sheet.png":         "png",
		"https://cdn/sheet.png?token=x": "png",
		"https://cdn/sheet.jpeg":        "jpeg",
		"https://cdn/sheet.PNGGG":       "PNGG", // capped at 4
		"https://cdn/sheet.":            "png",  // empty ext -> default
	}
	for in, want := range cases {
		if got := sheetExt(in); got != want {
			t.Fatalf("sheetExt(%q) = %q, want %q", in, got, want)
		}
	}
}

// Integration: exercise the DownloadLesson fan-out (resources/sheet-music/poster/nfo)
// against a local server, asserting the produced filenames match the Node reference.
func TestDownloadLessonLayout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	l := &Lesson{
		ID:        1,
		Title:     "My Lesson",
		Thumbnail: srv.URL + "/thumb.jpg",
		Resources: []Resource{
			{Name: "Chart", URL: srv.URL + "/chart.pdf"},
			{Name: "", URL: srv.URL + "/dir/pack.zip"}, // empty name -> URL basename
		},
		Assignments: []Assignment{
			{Title: "No Sheet Here", SheetMusicImageURLs: nil},                                       // skipped, must not shift numbering
			{Title: "Intro", SheetMusicImageURLs: []string{srv.URL + "/a.png?t=1"}},                  // single page -> no (pN) suffix
			{Title: "", SheetMusicImageURLs: []string{srv.URL + "/b.jpeg"}},                          // empty title -> "assignment"
			{Title: "Song", SheetMusicImageURLs: []string{srv.URL + "/p1.png", srv.URL + "/p2.png"}}, // multi-page song -> (p1)/(p2)
		},
		Mp3NoDrumsNoClick: srv.URL + "/m.mp3",
	}

	dir := t.TempDir()
	if err := DownloadLesson(nil, l, DownloadOpts{Dir: dir, Index: 3, ResourcesOnly: true}); err != nil {
		t.Fatalf("DownloadLesson: %v", err)
	}

	base := "03 - My Lesson"
	lessonDir := filepath.Join(dir, base)

	want := []string{
		filepath.Join(lessonDir, base+".nfo"),
		filepath.Join(lessonDir, base+"-poster.jpg"),
		filepath.Join(lessonDir, "resources", "Chart"),
		filepath.Join(lessonDir, "resources", "pack.zip"), // basename fallback, no collision on "untitled"
		filepath.Join(lessonDir, "play-along", "play-along (no drums, no click).mp3"),
		filepath.Join(lessonDir, "sheet-music", "01 - Intro.png"),       // numbering starts at 01 despite leading sheet-less assignment; ext from URL
		filepath.Join(lessonDir, "sheet-music", "02 - assignment.jpeg"), // empty title -> "assignment"
		filepath.Join(lessonDir, "sheet-music", "03 - Song (p1).png"),   // multi-page: sheetNo keeps counting across pages, (pN) suffix
		filepath.Join(lessonDir, "sheet-music", "04 - Song (p2).png"),
	}
	for _, p := range want {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file missing: %s (%v)", p, err)
		}
	}

	// No extensionless sheet-music file should exist.
	got, _ := filepath.Glob(filepath.Join(lessonDir, "sheet-music", "*"))
	sort.Strings(got)
	for _, p := range got {
		if filepath.Ext(p) == "" {
			t.Errorf("sheet-music file without extension: %s", p)
		}
	}
	if len(got) != 4 {
		t.Errorf("sheet-music files = %v, want 4", got)
	}
}
