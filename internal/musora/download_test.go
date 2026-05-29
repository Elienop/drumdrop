package musora

import (
	"strings"
	"testing"
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
