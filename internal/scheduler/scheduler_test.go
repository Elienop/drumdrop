package scheduler

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", cfg.MaxAttempts)
	}
	want := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}
	if len(cfg.Backoff) != len(want) {
		t.Fatalf("Backoff len = %d, want %d", len(cfg.Backoff), len(want))
	}
	for i, d := range want {
		if cfg.Backoff[i] != d {
			t.Errorf("Backoff[%d] = %v, want %v", i, cfg.Backoff[i], d)
		}
	}
	// Unset fields default to the zero value.
	if cfg.DownloadsDir != "" || cfg.Quality != "" || cfg.ResourcesOnly {
		t.Errorf("DefaultConfig should leave DownloadsDir/Quality/ResourcesOnly zero, got %+v", cfg)
	}
}

func nodeFollow() database.Follow {
	return database.Follow{
		ID:            1,
		Kind:          "node",
		RailcontentID: sql.NullInt64{Int64: 4242, Valid: true},
		Title:         "Beginner Course",
		Brand:         "drumeo",
		Quality:       "1080",
	}
}

func instructorFollow() database.Follow {
	return database.Follow{
		ID:      2,
		Kind:    "instructor",
		Slug:    sql.NullString{String: "mike-johnston", Valid: true},
		Title:   "Mike Johnston",
		Brand:   "drumeo",
		Quality: "720",
	}
}

func TestFolderTitle(t *testing.T) {
	tests := []struct {
		name string
		f    database.Follow
		want string
	}{
		{"node with title", nodeFollow(), "Beginner Course"},
		{"instructor with title", instructorFollow(), "Mike Johnston"},
		{
			"node without title falls back to railcontent_id",
			database.Follow{Kind: "node", RailcontentID: sql.NullInt64{Int64: 99, Valid: true}},
			"99",
		},
		{
			"instructor without title falls back to @slug",
			database.Follow{Kind: "instructor", Slug: sql.NullString{String: "jared", Valid: true}},
			"@jared",
		},
		{
			"no title and no key falls back to dash",
			database.Follow{Kind: "node"},
			"-",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := folderTitle(tt.f); got != tt.want {
				t.Errorf("folderTitle = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutDirFor(t *testing.T) {
	cfg := Config{DownloadsDir: "/dl"}

	if got, want := outDirFor(cfg, nodeFollow()), filepath.Join("/dl", "Beginner Course"); got != want {
		t.Errorf("outDirFor(node) = %q, want %q", got, want)
	}
	if got, want := outDirFor(cfg, instructorFollow()), filepath.Join("/dl", "Mike Johnston"); got != want {
		t.Errorf("outDirFor(instructor) = %q, want %q", got, want)
	}

	// Folder title is sanitized: unsafe chars become '-'.
	dirty := database.Follow{Kind: "node", Title: "Foo/Bar: Baz", RailcontentID: sql.NullInt64{Int64: 5, Valid: true}}
	if got, want := outDirFor(cfg, dirty), filepath.Join("/dl", musora.Sanitize("Foo/Bar: Baz")); got != want {
		t.Errorf("outDirFor(dirty title) = %q, want %q", got, want)
	}
}

func TestQualityFor(t *testing.T) {
	node := nodeFollow()       // saved quality 1080
	inst := instructorFollow() // saved quality 720

	// No override: use the follow's saved quality.
	if got := qualityFor(Config{}, node); got != "1080" {
		t.Errorf("qualityFor(no override, node) = %q, want 1080", got)
	}
	if got := qualityFor(Config{}, inst); got != "720" {
		t.Errorf("qualityFor(no override, instructor) = %q, want 720", got)
	}

	// Override wins for every follow.
	override := Config{Quality: "best"}
	if got := qualityFor(override, node); got != "best" {
		t.Errorf("qualityFor(override, node) = %q, want best", got)
	}
	if got := qualityFor(override, inst); got != "best" {
		t.Errorf("qualityFor(override, instructor) = %q, want best", got)
	}
}
