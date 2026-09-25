package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// The tests below pin the owner's ruling of 2026-09-24 (code seat Medium 1,
// security S1): placing a download over a lesson's existing folder merges its
// subfolders file by file, as the top level is. A re-download that fetched
// fewer resources than the earlier one (a fetch that failed without failing
// the download, a resource Musora dropped), or a file the owner added in
// resources/, never costs the file: only one at exactly a path the new
// download placed is replaced.

// partialRedownload is a finished re-download that produced fewer resources
// than the earlier one: the video, the nfo, resources/a.pdf and
// resources/deep/x.pdf, each "new …".
type partialRedownload struct{}

// partialFiles is what partialRedownload writes, relative to the lesson folder
// (resources/ is the download's own name for the folder).
var partialFiles = map[string]string{
	"resources/a.pdf":      "new a",
	"resources/deep/x.pdf": "new x",
}

func (partialRedownload) Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	base := fmt.Sprintf("%02d - %s", o.Index, musora.Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	files := map[string]string{base + ".mp4": "new mp4", base + ".nfo": "new nfo"}
	for rel, body := range partialFiles {
		files[rel] = body
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// earlierResources is the earlier download's subfolder, relative to it: two
// files the re-download produces again (a.pdf, deep/x.pdf), one it does not
// (b.pdf, deep/old.pdf), and a file the owner added (my-notes.txt).
var earlierResources = map[string]string{
	"a.pdf":        "old a",
	"b.pdf":        "old b",
	"my-notes.txt": "mine",
	"deep/x.pdf":   "old x",
	"deep/old.pdf": "old deep",
}

// mergeFixture is lesson 100's folder as the seats probed it: an earlier
// download (recorded as the lesson's, or not) with a subfolder the
// re-download places again, and a top-level file of the owner's.
type mergeFixture struct {
	dir string // the lesson's folder (plex-tv: the season folder)
	sub string // the earlier subfolder, where the re-download's resources/ goes
	top string // the earlier video, at the name the re-download places
}

// seedMerge writes mergeFixture where w places lesson 100, and records it as
// the lesson's when recorded.
func seedMerge(t *testing.T, w *Worker, store *fakeWorkerStore, recorded bool) mergeFixture {
	t.Helper()
	root := w.Cfg.LibraryDir
	if root == "" {
		root = w.Cfg.DownloadsDir
	}
	var f mergeFixture
	var prev database.Lesson
	if w.Cfg.LibraryDir != "" && w.Cfg.Layout == LayoutPlexTV {
		base := "Beginner Course - s01e05 - Lesson A"
		f.dir = filepath.Join(root, "Beginner Course", "Season 01")
		f.sub = filepath.Join(f.dir, base+" resources")
		f.top = filepath.Join(f.dir, base+".mp4")
		prev = recordedRow(100, f.dir, base+".mp4", base+" resources/")
	} else {
		f.dir = filepath.Join(root, "Beginner Course", "05 - Lesson A")
		f.sub = filepath.Join(f.dir, "resources")
		f.top = filepath.Join(f.dir, "05 - Lesson A.mp4")
		prev = database.Lesson{RailcontentID: 100, Status: database.StatusDownloaded, OutputDir: sql.NullString{String: f.dir, Valid: true}}
	}
	writeTree(t, f.sub, earlierResources)
	writeTree(t, f.dir, map[string]string{filepath.Base(f.top): "old mp4", "notes.txt": "owner notes"})
	if recorded {
		prev.Position = sql.NullInt64{Int64: 5, Valid: true}
		prev.VideoPath = sql.NullString{String: f.top, Valid: true}
		store.lessons[100] = prev
		store.withFiles = append(store.withFiles, prev)
	}
	return f
}

// writeTree writes every file of files (slash-separated paths) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// assertTree fails unless every file of files (slash-separated paths) under
// dir holds its body, and dir holds no other file.
func assertTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, p)
		got[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if fmt.Sprint(got) != fmt.Sprint(files) {
		t.Errorf("%s holds %v, want exactly %v", dir, got, files)
	}
}

// otherFS marks the separate-library setup whose library is on another
// filesystem than the downloads folder: every entry is copied.
const otherFS = "separate, another filesystem"

// forEachMergeSetup runs run with a worker over partialRedownload in every
// setup and layout the seats probed: no library, the library the downloads
// folder, a separate library (on the same filesystem, and on another), and
// plex-tv (which needs a library), each with the lesson folder recorded as the
// lesson's or not.
func forEachMergeSetup(t *testing.T, run func(t *testing.T, w *Worker, store *fakeWorkerStore, recorded bool)) {
	for _, setup := range append(append([]string(nil), setups...), otherFS) {
		for _, layout := range []string{"", LayoutPlexTV} {
			if setup == "none" && layout == LayoutPlexTV {
				continue
			}
			for _, recorded := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/layout=%s/recorded=%v", setup, layout, recorded), func(t *testing.T) {
					if setup == otherFS {
						w, store, _ := setupWorker(t, "separate", layout)
						forceCopyFallbackInto(t, w.Cfg.LibraryDir)
						w.Downloader = partialRedownload{}
						run(t, w, store, recorded)
						return
					}
					w, store, _ := setupWorker(t, setup, layout)
					w.Downloader = partialRedownload{}
					run(t, w, store, recorded)
				})
			}
		}
	}
}

// TestWorkerReDownloadMergesTheSubfolders (owner ruling 2026-09-24) proves a
// successful re-download that produced only resources/a.pdf and
// resources/deep/x.pdf keeps every other file of the earlier resources/
// folder, at every depth (the earlier b.pdf, deep/old.pdf, and the owner's
// my-notes.txt), replaces exactly the two files at the paths it placed, and
// logs each of those with ↻, in every setup and layout, recorded or not.
func TestWorkerReDownloadMergesTheSubfolders(t *testing.T) {
	forEachMergeSetup(t, func(t *testing.T, w *Worker, store *fakeWorkerStore, recorded bool) {
		var log bytes.Buffer
		w.Log = &log
		f := seedMerge(t, w, store, recorded)

		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if rec := onlyRecord(t, store); rec.outputDir != f.dir {
			t.Errorf("recorded %q, want %q", rec.outputDir, f.dir)
		}
		assertTree(t, f.sub, map[string]string{
			"a.pdf":        "new a",
			"b.pdf":        "old b",
			"my-notes.txt": "mine",
			"deep/x.pdf":   "new x",
			"deep/old.pdf": "old deep",
		})
		if got, _ := os.ReadFile(filepath.Join(f.dir, "notes.txt")); string(got) != "owner notes" {
			t.Errorf("notes.txt = %q, want the owner's file kept", got)
		}
		if got, _ := os.ReadFile(f.top); string(got) != "new mp4" {
			t.Errorf("%s = %q, want the new video", f.top, got)
		}
		whose := "which no lesson recorded"
		if recorded {
			whose = "the lesson's earlier download"
		}
		for _, p := range []string{f.top, filepath.Join(f.sub, "a.pdf"), filepath.Join(f.sub, "deep", "x.pdf")} {
			if want := fmt.Sprintf("↻ 100 replaced %q (%s)", p, whose); !strings.Contains(log.String(), want) {
				t.Errorf("log %q does not say %s", log.String(), want)
			}
		}
		if strings.Contains(log.String(), fmt.Sprintf("replaced %q", f.sub)) {
			t.Errorf("log %q says the whole subfolder was replaced", log.String())
		}
		for _, root := range []string{w.Cfg.DownloadsDir, w.Cfg.LibraryDir} {
			if root != "" {
				assertExist(t, false, filepath.Join(root, privateRootName, replacedFolderName(1)))
			}
		}
		assertExist(t, false, w.privateDir(1))
	})
}

// TestWorkerStopDuringAMergePutsTheSubfolderBack (owner ruling 2026-09-24,
// D79) proves a Skip or a delete that lands while a re-download is being
// merged into the lesson's folder undoes the placement exactly: the merged
// subfolder holds its earlier contents again, at every depth, nothing of the
// download is left, and no set-aside area is. The one exception is a delete
// of a recorded lesson's files: the files of the lesson's own the placement
// replaced are what that delete removes, so they are not put back, and every
// other file is.
func TestWorkerStopDuringAMergePutsTheSubfolderBack(t *testing.T) {
	for _, stop := range []string{"skip", "delete"} {
		t.Run(stop, func(t *testing.T) {
			forEachMergeSetup(t, func(t *testing.T, w *Worker, store *fakeWorkerStore, recorded bool) {
				w.Cfg.MaxAttempts = 1
				f := seedMerge(t, w, store, recorded)
				store.skipped, store.gone = map[int64]bool{}, map[int64]bool{}
				store.onConfirm = func() {
					if stop == "skip" {
						store.skipped[1] = true
					} else {
						store.gone[1] = true
					}
				}

				if _, err := w.RunOnce(context.Background(), 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				if len(store.markDownloaded) != 0 {
					t.Errorf("%s: recorded %+v, want nothing", stop, store.markDownloaded)
				}
				want := map[string]string{}
				for rel, body := range earlierResources {
					want[rel] = body
				}
				if stop == "delete" && recorded {
					delete(want, "a.pdf")
					delete(want, "deep/x.pdf")
					assertExist(t, false, f.top)
				} else if got, _ := os.ReadFile(f.top); string(got) != "old mp4" {
					t.Errorf("%s: %s = %q, want the earlier video back", stop, f.top, got)
				}
				assertTree(t, f.sub, want)
				for _, root := range []string{w.Cfg.DownloadsDir, w.Cfg.LibraryDir} {
					if root == "" {
						continue
					}
					for _, c := range []string{"new mp4", "new nfo", "new a", "new x"} {
						if p := findContent(t, root, c); p != "" {
							t.Errorf("%s: the download's %q is left at %q", stop, c, p)
						}
					}
					assertExist(t, false, filepath.Join(root, privateRootName, replacedFolderName(1)))
				}
			})
		})
	}
}

// TestPlacementThatFailsAfterAMergeTakesItBack (owner ruling 2026-09-24)
// proves a placement that fails after it merged a subfolder (here the nfo's
// rename is refused, and resources/ sorts after it but is merged first, while
// the names are cleared) takes the merged entries back into the downloaded
// folder and puts the replaced ones back, in both layouts: the subfolder holds
// exactly its earlier files, and the download is whole in its folder.
func TestPlacementThatFailsAfterAMergeTakesItBack(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		t.Run("layout="+layout, func(t *testing.T) {
			tmp := t.TempDir()
			dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
			scratch := filepath.Join(dl, "Course", "05 - Five")
			download := map[string]string{"05 - Five.mp4": "new mp4", "05 - Five.nfo": "new nfo", "resources/a.pdf": "new a", "resources/deep/x.pdf": "new x"}
			writeTree(t, scratch, download)
			var sub string
			if layout == LayoutPlexTV {
				sub = filepath.Join(lib, "Show", "Season 01", "Show - s01e05 - Five resources")
			} else {
				sub = filepath.Join(lib, "Course", "05 - Five", "resources")
			}
			writeTree(t, sub, earlierResources)
			stubRename(t, func(oldpath, newpath string) error {
				if filepath.Ext(newpath) == ".nfo" {
					return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrPermission}
				}
				return renameNoReplace(oldpath, newpath)
			})

			var err error
			if layout == LayoutPlexTV {
				var res plexMoveResult
				res, err = testMovePlexTVFrom(t, dl, lib, plexEpisode{"Show", 1, 5, "Five"}, scratch,
					plexLibrary{self: database.Lesson{RailcontentID: 1}, roots: []string{lib, dl}})
				if res.seasonDir != "" {
					t.Errorf("move placed the lesson in %q, want a failure", res.seasonDir)
				}
			} else {
				_, err = testPlace(t, dl, lib, scratch, database.Lesson{})
			}
			if err == nil {
				t.Fatal("the placement succeeded, want the refused rename's failure")
			}
			assertTree(t, sub, earlierResources)
			assertTree(t, scratch, download)
			assertExist(t, false, filepath.Join(lib, privateRootName, replacedFolderName(7)), filepath.Join(lib, privateRootName, replacedFolderName(0)))
		})
	}
}
