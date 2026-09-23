package library

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// seedSeason creates dir and, inside it, every name: a folder (holding one
// file) when the name ends in "/", else a file whose content is its name.
func seedSeason(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, name := range names {
		if folder, ok := strings.CutSuffix(name, "/"); ok {
			if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", folder, err)
			}
			if err := os.WriteFile(filepath.Join(dir, folder, "f.pdf"), []byte(folder), 0o644); err != nil {
				t.Fatalf("write in %s: %v", folder, err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// paths joins every name (a trailing "/" dropped) onto dir.
func paths(dir string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(dir, strings.TrimSuffix(n, "/")))
	}
	return out
}

// libraryOf is the library folder a season folder <lib>/<show>/<Season NN>
// sits in.
func libraryOf(seasonDir string) string {
	return filepath.Dir(filepath.Dir(seasonDir))
}

// recordedRow is a lesson filed in seasonDir whose record names exactly names
// (relative to the library folder, as the move writes them).
func recordedRow(id int, seasonDir string, names ...string) database.Lesson {
	entries := make([]string, 0, len(names))
	prefix := filepath.Base(filepath.Dir(seasonDir)) + "/" + filepath.Base(seasonDir) + "/"
	for _, n := range names {
		entries = append(entries, prefix+strings.TrimSuffix(n, "/"))
	}
	return database.Lesson{
		RailcontentID:  id,
		Status:         database.StatusDownloaded,
		OutputDir:      sql.NullString{String: seasonDir, Valid: true},
		LibraryEntries: database.EncodeLibraryEntries(entries),
	}
}

// legacyRow is a lesson moved into seasonDir before the record existed: no
// library_entries, only its title, position and video.
func legacyRow(id int, title string, position int, seasonDir, video string) database.Lesson {
	l := database.Lesson{
		RailcontentID: id,
		Title:         title,
		Status:        database.StatusDownloaded,
		Position:      sql.NullInt64{Int64: int64(position), Valid: true},
		OutputDir:     sql.NullString{String: seasonDir, Valid: true},
	}
	if video != "" {
		l.VideoPath = sql.NullString{String: filepath.Join(seasonDir, video), Valid: true}
	}
	return l
}

// plan is NewClaims(root, rows).Plan(self).
func plan(t *testing.T, root string, self database.Lesson, rows []database.Lesson) (Entries, error) {
	t.Helper()
	c, err := NewClaims(root, rows)
	if err != nil {
		return Entries{}, err
	}
	return c.Plan(self)
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func assertExist(t *testing.T, want bool, ps ...string) {
	t.Helper()
	for _, p := range ps {
		_, err := os.Lstat(p)
		if got := err == nil; got != want {
			t.Errorf("%s exists = %v, want %v (err=%v)", p, got, want, err)
		}
	}
}
