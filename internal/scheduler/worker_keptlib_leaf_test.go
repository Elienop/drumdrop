package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// The tests below pin which folder a refused library placement's downloads
// fallback, and the placement itself, treat as the lesson's own (security
// round 5e S1 and S3, code L1 and L2).

// libraryFolderRow puts lesson 100's row in store recording dir, a lesson
// folder the default layout placed in the library, holding its video and a
// file of the owner's; it returns the files.
func libraryFolderRow(t *testing.T, store *fakeWorkerStore, dir string) map[string]string {
	t.Helper()
	files := map[string]string{"05 - Lesson A.mp4": "old mp4", "notes.txt": "owner notes"}
	writeTree(t, dir, files)
	prev := database.Lesson{
		RailcontentID: 100, Status: database.StatusDownloaded,
		Position:  sql.NullInt64{Int64: 5, Valid: true},
		OutputDir: sql.NullString{String: dir, Valid: true},
		VideoPath: sql.NullString{String: filepath.Join(dir, "05 - Lesson A.mp4"), Valid: true},
	}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}
	return files
}

// symlinkAt makes link a symlink to target, creating link's folder.
func symlinkAt(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestWorkerRefusedLibraryPlacementIgnoresASymlinkToTheLibraryFolder proves
// a symlink at the lesson's downloads path that leads to its recorded
// library folder does not make the downloads fallback "the lesson's own
// folder" (security round 5e S1, code L1). The placement would replace the
// symlink with a new folder, so the library folder would stay behind recorded
// by no lesson: the outcome ruling (f) declined. The attempt fails instead,
// and the library folder stays whole and recorded, in both layouts.
func TestWorkerRefusedLibraryPlacementIgnoresASymlinkToTheLibraryFolder(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		name := "default"
		if layout != "" {
			name = layout
		}
		t.Run(name, func(t *testing.T) {
			w, store, _, lib, season := plexWorker(t)
			w.Cfg.Layout = layout
			dir := filepath.Join(lib, "Beginner Course", "05 - Lesson A")
			files := libraryFolderRow(t, store, dir)
			fallback := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
			symlinkAt(t, dir, fallback)
			if layout == LayoutPlexTV {
				base := "Beginner Course - s01e05 - Lesson A"
				seedSeason(t, season, base+".mp4")
				store.withFiles = append(store.withFiles, recordedRow(200, season, base+".mp4"))
			} else {
				refuseFromJobInto(t, w, dir)
			}

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			assertTree(t, dir, files)
			assertKeptInLibrary(t, w, store)
			if got, err := os.Readlink(fallback); err != nil || got != dir {
				t.Errorf("%q links to %q, %v; want the symlink to %q left as it was", fallback, got, err, dir)
			}
		})
	}
}

// TestWorkerPlacementWithoutALibraryLeavesTheFolderASymlinkLedTo proves the
// placement itself (not only the fallback's gate) treats a symlink at the
// lesson's path by what it does with it: it replaces the symlink with a new
// folder, so the folder the symlink led to is the lesson's previous folder,
// not its destination. With DRUMDROP_LIBRARY_DIR removed after the default
// layout placed the lesson in the library (BACKLOG D58 (c)), that folder
// stays where it is, with the owner's file, and the log says it is no longer
// recorded, as it does without the symlink; the replaced symlink is logged as
// an entry no lesson recorded, not as the lesson's earlier download.
func TestWorkerPlacementWithoutALibraryLeavesTheFolderASymlinkLedTo(t *testing.T) {
	w, store, dl, lib, _ := plexWorker(t)
	w.Cfg.Layout, w.Cfg.LibraryDir = "", ""
	var log bytes.Buffer
	w.Log = &log
	dir := filepath.Join(lib, "Beginner Course", "05 - Lesson A")
	files := libraryFolderRow(t, store, dir)
	dst := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
	symlinkAt(t, dir, dst)

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(dl.calls) != 1 || len(store.markFailed) != 0 {
		t.Fatalf("downloads %d, failed %v; want one download, recorded (log %q)", len(dl.calls), store.markFailed, log.String())
	}
	if rec := onlyRecord(t, store); rec.outputDir != dst {
		t.Errorf("recorded %q, want the downloads folder %q", rec.outputDir, dst)
	}
	if info, err := os.Lstat(dst); err != nil || !info.IsDir() {
		t.Errorf("%q: %v, %v; want a real folder in the symlink's place", dst, info, err)
	}
	assertTree(t, dir, files)
	for _, want := range []string{
		fmt.Sprintf("⚠ 100 left its previous folder %q where it was, no longer recorded", dir),
		fmt.Sprintf("↻ 100 replaced %q (which no lesson recorded)", dst),
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log %q does not say %q", log.String(), want)
		}
	}
}

// TestWorkerRefusedLibraryPlacementOfALessonKeptInDownloads proves "in the
// library" is decided by the longest root holding the recorded folder, as
// previousFolder decides it (security round 5e S3). A lesson an earlier
// refused move kept in the downloads folder, under a title Musora has since
// changed, falls back to downloads when its library placement is refused
// again, whether the downloads folder is beside the library or inside it:
// its old folder is in downloads, not in the library, so ruling (e) decides
// it (it goes when the download brought back every file in it). When the
// library is the downloads folder (a tie) the folder counts as the
// library's, and the attempt fails with that copy kept (ruling (f)).
func TestWorkerRefusedLibraryPlacementOfALessonKeptInDownloads(t *testing.T) {
	for _, where := range []string{"the downloads folder beside the library", dlInLibrary, dlIsLibrary} {
		for _, owner := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/owner's file=%v", where, owner), func(t *testing.T) {
				w, store, dl, lib, _ := plexWorker(t)
				w.Cfg.Layout = ""
				var log bytes.Buffer
				w.Log = &log
				if where != "the downloads folder beside the library" {
					placeDownloads(t, w, lib, where)
				}
				old := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Old Title")
				files := map[string]string{"05 - Old Title.mp4": "old mp4"}
				if owner {
					files["notes.txt"] = "owner notes"
				}
				writeTree(t, old, files)
				prev := database.Lesson{
					RailcontentID: 100, Status: database.StatusDownloaded,
					Position:  sql.NullInt64{Int64: 5, Valid: true},
					OutputDir: sql.NullString{String: old, Valid: true},
					VideoPath: sql.NullString{String: filepath.Join(old, "05 - Old Title.mp4"), Valid: true},
				}
				store.lessons[100] = prev
				store.withFiles = []database.Lesson{prev}
				target := filepath.Join(lib, "Beginner Course", "05 - Lesson A")
				writeTree(t, target, map[string]string{"05 - Lesson A.mp4": "lesson 200"})
				store.withFiles = append(store.withFiles, database.Lesson{
					RailcontentID: 200, Status: database.StatusDownloaded,
					OutputDir: sql.NullString{String: target, Valid: true},
					VideoPath: sql.NullString{String: filepath.Join(target, "05 - Lesson A.mp4"), Valid: true},
				})

				if _, err := w.RunOnce(context.Background(), 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				assertTree(t, target, map[string]string{"05 - Lesson A.mp4": "lesson 200"})
				if where == dlIsLibrary {
					assertTree(t, old, files)
					assertKeptInLibrary(t, w, store)
					return
				}
				if len(dl.calls) != 1 || len(store.markFailed) != 0 {
					t.Fatalf("downloads %d, failed %v; want one download, recorded (log %q)", len(dl.calls), store.markFailed, log.String())
				}
				fallback := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A")
				if rec := onlyRecord(t, store); rec.outputDir != fallback {
					t.Errorf("recorded %q, want the downloads folder %q", rec.outputDir, fallback)
				}
				if got, err := os.ReadFile(filepath.Join(fallback, "05 - Lesson A.mp4")); err != nil || string(got) != "new mp4" {
					t.Errorf("video = %q, %v; want the new download", got, err)
				}
				if owner {
					assertTree(t, old, files)
				} else {
					assertExist(t, false, old)
				}
			})
		}
	}
}
