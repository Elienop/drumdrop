package musora

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatSelector(t *testing.T) {
	if FormatSelector("") != "bv*+ba/b" || FormatSelector("best") != "bv*+ba/b" {
		t.Fatal("default")
	}
	if FormatSelector("720") != "bv*[height<=720]+ba/b[height<=720]/bv*+ba/b" {
		t.Fatal("cap")
	}
}

func TestYtDlpArgs(t *testing.T) {
	args := YtDlpArgs("https://m3u8", "720", "/out/%(ext)s")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--write-subs", "--referer https://player.vimeo.com/", "height<=720", "https://m3u8"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
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
			{Title: "No Sheet Here", SheetMusicImageURL: ""}, // skipped, must not shift numbering
			{Title: "Intro", SheetMusicImageURL: srv.URL + "/a.png?t=1"},
			{Title: "", SheetMusicImageURL: srv.URL + "/b.jpeg"}, // empty title -> "assignment"
		},
		Mp3NoDrumsNoClick: srv.URL + "/m.mp3",
	}

	dir := t.TempDir()
	if err := DownloadLesson(l, DownloadOpts{Dir: dir, Index: 3, ResourcesOnly: true}); err != nil {
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
	if len(got) != 2 {
		t.Errorf("sheet-music files = %v, want 2", got)
	}
}
