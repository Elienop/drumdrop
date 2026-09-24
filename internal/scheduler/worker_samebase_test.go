package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// TestWorkerPlexTvSameTitleReDownloadKeepsWhatItDidNotBringBack pins the
// owner's ruling 2026-09-24 (j) (round-5b security L4, code L6): in plex-tv,
// a re-download under the same title keeps the lesson's recorded entries at
// its episode base that it did not bring back (captions, a poster, a
// resources folder, each of which a re-download can fail to fetch again),
// untouched, and they stay in the lesson's record, so a later Delete with
// files removes them. What it did bring back is replaced, a folder merged.
// A recorded file at an old title's name still goes (ruling #66), even one
// whose name starts with this episode's base.
func TestWorkerPlexTvSameTitleReDownloadKeepsWhatItDidNotBringBack(t *testing.T) {
	const base = "Beginner Course - s01e05 - Lesson A"
	// Each re-download writes the video and nfo, plus the scratch entries
	// named here ("05 - Lesson A" is the scratch base).
	extras := map[string]string{
		"vtt":       "05 - Lesson A.en.vtt",
		"poster":    "05 - Lesson A-poster.jpg",
		"resources": "resources/new.pdf",
	}
	episode := map[string]string{
		"vtt":       base + ".en.vtt",
		"poster":    base + "-poster.jpg",
		"resources": base + " resources/",
	}
	for _, missing := range []string{"vtt", "poster", "resources"} {
		t.Run("missing "+missing, func(t *testing.T) {
			w, store, dl, lib, season := plexWorker(t)
			var log bytes.Buffer
			w.Log = &log
			dl.afterWrite = func(dir string) {
				for k, rel := range extras {
					if k == missing {
						continue
					}
					p := filepath.Join(dir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte("new "+k), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			oldTitle := base + " (old).mp4" // "Lesson A (old)", a title the lesson had
			mine := []string{base + ".mp4", base + ".nfo", episode["vtt"], episode["poster"], episode["resources"], oldTitle}
			seedSeason(t, season, mine...)
			prev := recordedRow(100, season, mine...)
			prev.Position = sql.NullInt64{Int64: 5, Valid: true}
			prev.VideoPath = sql.NullString{String: filepath.Join(season, base+".mp4"), Valid: true}
			store.lessons[100] = prev
			store.withFiles = []database.Lesson{prev}

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			rec := onlyRecord(t, store)
			// The one it missed is kept as it was; the others are the new
			// download's, the resources folder merged with the earlier one.
			assertSeeded(t, season, episode[missing])
			for k := range extras {
				if k == missing {
					continue
				}
				if k == "resources" {
					assertTree(t, filepath.Join(season, base+" resources"), map[string]string{"f.pdf": base + " resources", "new.pdf": "new resources"})
					continue
				}
				if got, err := os.ReadFile(filepath.Join(season, episode[k])); err != nil || string(got) != "new "+k {
					t.Errorf("%s = %q, %v; want the new download's", episode[k], got, err)
				}
			}
			assertExist(t, false, filepath.Join(season, oldTitle))
			want := recordOf(season, base+".mp4", base+".nfo", episode["vtt"], episode["poster"], episode["resources"])
			if got := slices.Sorted(slices.Values(rec.entries)); !reflect.DeepEqual(got, slices.Sorted(slices.Values(want))) {
				t.Errorf("entries = %v, want %v", got, want)
			}
			if strings.Contains(log.String(), "left its previous folder") {
				t.Errorf("log %q says a folder was left behind", log.String())
			}

			// A Delete with files removes what the record names: every entry
			// of the lesson, the kept one included.
			row := recordedRow(100, season)
			row.LibraryEntries = database.EncodeLibraryEntries(rec.entries)
			c, err := library.NewClaims(lib, []database.Lesson{row})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := c.Plan(row)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range plan.Remove {
				if err := library.Remove(library.Roots(w.Cfg.DownloadsDir, lib), p); err != nil {
					t.Errorf("remove %q: %v", p, err)
				}
			}
			assertExist(t, false, paths(season, mine...)...)
		})
	}
}
