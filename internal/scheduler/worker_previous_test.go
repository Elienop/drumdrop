package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// The tests below pin the owner's ruling 2026-09-24 (e) (round-5 fix seats:
// code M1, security M1): a lesson's previous folder at a DIFFERENT place from
// the new one (a title change, a library added after the download, the
// default layout or plex-tv) is removed only when the re-download brings back
// every file in it, at the corresponding path in the new lesson folder.
// Otherwise it stays where it is, untouched and no longer recorded, and the
// log names it and says why. A recorded plex-tv FILE at an old name is the
// lesson's by record (ruling #66) and still goes.

// previousCase is one place a lesson's previous download can be, away from
// where the re-download (partialRedownload of "Lesson A") is placed.
type previousCase struct {
	name          string
	setup, layout string
	// seed writes the previous download, with extra in its folder beside what
	// the re-download brings back, records it as lesson 100's, and returns the
	// folder the rule decides on and its files (slash paths).
	seed func(t *testing.T, w *Worker, store *fakeWorkerStore, extra map[string]string) (dir string, files map[string]string)
	// placedIn is the folder the re-download is recorded in.
	placedIn func(w *Worker) string
	// gone are the previous download's recorded files at old names, which go
	// whether the folder stays or not (plex-tv).
	gone func(w *Worker) []string
}

// broughtBack is what partialRedownload places, in a lesson folder named
// base: every file of a previous folder at these paths is brought back.
func broughtBack(base string) map[string]string {
	return map[string]string{
		base + ".mp4":          "old mp4",
		base + ".nfo":          "old nfo",
		"resources/a.pdf":      "old a",
		"resources/deep/x.pdf": "old x",
	}
}

// ownerExtras are what a kept previous folder holds beside what the
// re-download brings back: a file of the owner's, and a resource the
// re-download did not fetch again.
var ownerExtras = map[string]string{"notes.txt": "owner notes", "resources/b.pdf": "old b"}

// seedLessonFolder writes a default-layout previous folder dir named base and
// records it as lesson 100's.
func seedLessonFolder(t *testing.T, store *fakeWorkerStore, dir string, extra map[string]string) (string, map[string]string) {
	t.Helper()
	files := broughtBack(filepath.Base(dir))
	for k, v := range extra {
		files[k] = v
	}
	writeTree(t, dir, files)
	prev := database.Lesson{
		RailcontentID: 100, Status: database.StatusDownloaded,
		Position:  sql.NullInt64{Int64: 5, Valid: true},
		OutputDir: sql.NullString{String: dir, Valid: true},
		VideoPath: sql.NullString{String: filepath.Join(dir, filepath.Base(dir)+".mp4"), Valid: true},
	}
	store.lessons[100] = prev
	store.withFiles = append(store.withFiles, prev)
	return dir, files
}

var previousCases = []previousCase{
	{
		name: "title changed, no library", setup: "none",
		seed: func(t *testing.T, w *Worker, store *fakeWorkerStore, extra map[string]string) (string, map[string]string) {
			return seedLessonFolder(t, store, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Old Title"), extra)
		},
		placedIn: func(w *Worker) string { return filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A") },
	},
	{
		name: "title changed, separate library", setup: "separate",
		seed: func(t *testing.T, w *Worker, store *fakeWorkerStore, extra map[string]string) (string, map[string]string) {
			return seedLessonFolder(t, store, filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Old Title"), extra)
		},
		placedIn: func(w *Worker) string { return filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Lesson A") },
	},
	{
		name: "library added later", setup: "separate",
		seed: func(t *testing.T, w *Worker, store *fakeWorkerStore, extra map[string]string) (string, map[string]string) {
			return seedLessonFolder(t, store, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A"), extra)
		},
		placedIn: func(w *Worker) string { return filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "05 - Lesson A") },
	},
	{
		name: "plex-tv library added later", setup: "separate", layout: LayoutPlexTV,
		seed: func(t *testing.T, w *Worker, store *fakeWorkerStore, extra map[string]string) (string, map[string]string) {
			return seedLessonFolder(t, store, filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A"), extra)
		},
		placedIn: func(w *Worker) string { return filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "Season 01") },
	},
	{
		// The recorded episode files under the old title go (ruling #66); the
		// recorded resources folder is what the rule decides on.
		name: "plex-tv title changed", setup: "separate", layout: LayoutPlexTV,
		seed: func(t *testing.T, w *Worker, store *fakeWorkerStore, extra map[string]string) (string, map[string]string) {
			season := filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "Season 01")
			old := "Beginner Course - s01e05 - Old Title"
			seedSeason(t, season, old+".mp4", old+".nfo")
			files := map[string]string{"a.pdf": "old a", "deep/x.pdf": "old x"}
			for k, v := range extra {
				files[strings.TrimPrefix(k, "resources/")] = v
			}
			dir := filepath.Join(season, old+" resources")
			writeTree(t, dir, files)
			prev := recordedRow(100, season, old+".mp4", old+".nfo", old+" resources/")
			prev.Position = sql.NullInt64{Int64: 5, Valid: true}
			prev.VideoPath = sql.NullString{String: filepath.Join(season, old+".mp4"), Valid: true}
			store.lessons[100] = prev
			store.withFiles = append(store.withFiles, prev)
			return dir, files
		},
		placedIn: func(w *Worker) string { return filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "Season 01") },
		gone: func(w *Worker) []string {
			return paths(filepath.Join(w.Cfg.LibraryDir, "Beginner Course", "Season 01"),
				"Beginner Course - s01e05 - Old Title.mp4", "Beginner Course - s01e05 - Old Title.nfo")
		},
	},
}

// previousWorker is a worker over partialRedownload in c's setup, with its
// log.
func previousWorker(t *testing.T, c previousCase) (*Worker, *fakeWorkerStore, *bytes.Buffer) {
	t.Helper()
	w, store, _ := setupWorker(t, c.setup, c.layout)
	w.Downloader = partialRedownload{}
	w.Cfg.MaxAttempts = 1
	var log bytes.Buffer
	w.Log = &log
	return w, store, &log
}

// TestWorkerKeepsAPreviousFolderTheDownloadDoesNotFullyBringBack proves the
// previous folder, holding a file of the owner's and a resource the
// re-download missed, stays exactly as it was, is no longer recorded, and is
// named in the log with why; the re-download is placed and recorded where the
// lesson lives now.
func TestWorkerKeepsAPreviousFolderTheDownloadDoesNotFullyBringBack(t *testing.T) {
	for _, c := range previousCases {
		t.Run(c.name, func(t *testing.T) {
			w, store, log := previousWorker(t, c)
			dir, files := c.seed(t, w, store, ownerExtras)

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			rec := onlyRecord(t, store)
			if rec.outputDir != c.placedIn(w) {
				t.Errorf("recorded %q, want %q", rec.outputDir, c.placedIn(w))
			}
			assertTree(t, dir, files)
			if c.gone != nil {
				assertExist(t, false, c.gone(w)...)
			}
			if want := fmt.Sprintf("⚠ 100 left its previous folder %q where it was, no longer recorded: the new download did not bring back", dir); !strings.Contains(log.String(), want) {
				t.Errorf("log %q does not say %s", log.String(), want)
			}
			if rel, err := filepath.Rel(w.Cfg.LibraryDir, dir); err == nil && slices.Contains(rec.entries, filepath.ToSlash(rel)) {
				t.Errorf("entries %v still record the kept folder", rec.entries)
			}
			assertNoReplacedArea(t, w)
		})
	}
}

// TestWorkerRemovesAPreviousFolderTheDownloadFullyBringsBack proves the
// previous folder goes once the re-download is recorded when the re-download
// brings back every file in it, and that a stop landing during the placement
// puts it back exactly as it was.
func TestWorkerRemovesAPreviousFolderTheDownloadFullyBringsBack(t *testing.T) {
	for _, c := range previousCases {
		for _, stop := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stopped=%v", c.name, stop), func(t *testing.T) {
				w, store, log := previousWorker(t, c)
				dir, files := c.seed(t, w, store, nil)
				if stop {
					store.skipped = map[int64]bool{}
					store.onConfirm = func() { store.skipped[1] = true }
				}

				if _, err := w.RunOnce(context.Background(), 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				if stop {
					if len(store.markDownloaded) != 0 {
						t.Errorf("recorded %+v, want nothing", store.markDownloaded)
					}
					assertTree(t, dir, files)
					for _, body := range []string{"new mp4", "new a"} {
						if p := findContent(t, c.placedIn(w), body); p != "" {
							t.Errorf("the download's %q is left at %q", body, p)
						}
					}
				} else {
					if rec := onlyRecord(t, store); rec.outputDir != c.placedIn(w) {
						t.Errorf("recorded %q, want %q", rec.outputDir, c.placedIn(w))
					}
					assertExist(t, false, dir)
					if c.gone != nil {
						assertExist(t, false, c.gone(w)...)
					}
					if strings.Contains(log.String(), "left its previous folder") {
						t.Errorf("log %q says a folder was kept", log.String())
					}
				}
				assertNoReplacedArea(t, w)
			})
		}
	}
}

// assertNoReplacedArea fails if a replaced-<id> folder is left under the
// downloads folder or the library.
func assertNoReplacedArea(t *testing.T, w *Worker) {
	t.Helper()
	for _, root := range []string{w.Cfg.DownloadsDir, w.Cfg.LibraryDir} {
		if root != "" {
			assertExist(t, false, filepath.Join(root, privateRootName, replacedFolderName(1)))
		}
	}
}
