package server

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// The tests below pin owner ruling 2026-09-24 (y) for the deletes (security
// round 5h F2, the seat's TestR5hDeleteAfterRootMove): a plex-tv lesson placed
// while the library was <media>/drumeo, then DRUMDROP_LIBRARY_DIR moved up to
// <media> with the files not moved. The delete read the season folder under
// the library configured now, found nothing there, and answered "deleted"
// while every file stayed on disk, recorded by nothing (`main` removed them:
// it acted on the absolute folder). It refuses now and removes nothing.

// leftBehindNames are the episode's files in its season folder.
var leftBehindNames = []string{"Beginner Course - s01e05 - Lesson A.mp4", "Beginner Course - s01e05 - Lesson A.nfo", "Beginner Course - s01e05 - Lesson A.en.vtt"}

// seedSeasonLesson files lesson rcID of follow f in the season folder
// recordedSeason, as the download path records it (with a record of its
// entries, or as a legacy row without one), and creates its files in
// filesSeason.
func seedSeasonLesson(t *testing.T, store *database.Store, f int64, rcID int, recordedSeason, filesSeason string, recorded bool) {
	t.Helper()
	if err := store.UpsertLesson(t.Context(), rcID, "Lesson A", sql.NullInt64{}, "drumeo", sql.NullInt64{Int64: 5, Valid: true}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
		t.Fatalf("UpsertLesson: %v", err)
	}
	seedEntries(t, filesSeason, leftBehindNames...)
	rec := database.DownloadRecord{Quality: "1080", OutputDir: recordedSeason, VideoPath: filepath.Join(recordedSeason, leftBehindNames[0]), Bytes: 5}
	if recorded {
		rec.LibraryEntries = recordOf(recordedSeason, leftBehindNames...)
	}
	finishWithNewJob(t, store, f, rcID, rec)
}

// assertOnDisk fails unless every name in dir is there (want) or gone.
func assertOnDisk(t *testing.T, want bool, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		_, err := os.Lstat(filepath.Join(dir, n))
		if got := err == nil; got != want {
			t.Errorf("%s on disk = %v, want %v", n, got, want)
		}
	}
}

// assertUnchanged fails unless lesson id still records exactly what before
// did, reads 'downloaded', and is no longer held by a delete.
func assertUnchanged(t *testing.T, store *database.Store, before database.Lesson) {
	t.Helper()
	after := mustLesson(t, store, before.RailcontentID)
	if after.Status != database.StatusDownloaded || after.OutputDir != before.OutputDir || after.VideoPath != before.VideoPath || after.LibraryEntries != before.LibraryEntries || after.Deleting {
		t.Errorf("lesson %d = status %q, output_dir %v, video %v, entries %v, deleting %v; want it unchanged: %v, %v, %v, not deleting",
			before.RailcontentID, after.Status, after.OutputDir, after.VideoPath, after.LibraryEntries, after.Deleting, before.OutputDir, before.VideoPath, before.LibraryEntries)
	}
}

// libraryMove is one way the library setting relates to where a lesson's
// season folder was recorded, relative to a temporary folder.
type libraryMove struct {
	name string
	// lib is the setting now; recorded is the season folder the row records;
	// files is where the files are.
	lib, recorded, files string
	// link, when set, is a symlink made there to "media/drumeo".
	link string
	// leftBehind: the files stayed behind in a folder the setting no longer
	// points at.
	leftBehind bool
}

var libraryMoves = []libraryMove{
	{name: "moved up, files left", lib: "media", recorded: "media/drumeo/Beginner Course/Season 01", files: "media/drumeo/Beginner Course/Season 01", leftBehind: true},
	{name: "nothing moved", lib: "media/drumeo", recorded: "media/drumeo/Beginner Course/Season 01", files: "media/drumeo/Beginner Course/Season 01"},
	{name: "remounted with its files", lib: "newlib", recorded: "media/drumeo/Beginner Course/Season 01", files: "newlib/Beginner Course/Season 01"},
	{name: "the setting through a symlink to the same folder", lib: "linked", link: "linked", recorded: "media/drumeo/Beginner Course/Season 01", files: "media/drumeo/Beginner Course/Season 01"},
}

// setUp makes m's folders under root and returns the library setting, the
// recorded season folder and the files' folder.
func (m libraryMove) setUp(t *testing.T, root string) (lib, recorded, files string) {
	t.Helper()
	abs := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	if err := os.MkdirAll(abs("media/drumeo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if m.link != "" {
		if err := os.Symlink(abs("media/drumeo"), abs(m.link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(abs(m.lib), 0o755); err != nil {
		t.Fatal(err)
	}
	return abs(m.lib), abs(m.recorded), abs(m.files)
}

// TestDeleteLessonRefusesFilesLeftBehindByALibraryMove proves DELETE
// /api/lessons/{id} answers 409 with the fix, removes nothing and leaves the
// row as it was (downloaded, recording its files, no longer held) when the
// lesson's files stayed behind after the library setting moved, recorded and
// legacy rows alike; and that the controls (nothing moved, a library
// remounted with its files, the setting spelled through a symlink) delete as
// before.
func TestDeleteLessonRefusesFilesLeftBehindByALibraryMove(t *testing.T) {
	for _, m := range libraryMoves {
		for _, recorded := range []bool{true, false} {
			t.Run(m.name+"/recorded="+strconv.FormatBool(recorded), func(t *testing.T) {
				log := captureLog(t)
				store := newTestStore(t)
				root := t.TempDir()
				lib, recordedSeason, files := m.setUp(t, root)
				f := addFollow(t, store, 4242)
				seedSeasonLesson(t, store, f, 1, recordedSeason, files, recorded)
				before := mustLesson(t, store, 1)
				srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: filepath.Join(root, "dl"), LibraryDir: lib}, "test")

				rec := serveDelete(t, srv, "/api/lessons/1")
				if !m.leftBehind {
					if rec.Code != http.StatusOK {
						t.Fatalf("DELETE = %d %s, want 200", rec.Code, rec.Body.String())
					}
					assertOnDisk(t, false, files, leftBehindNames...)
					if l := mustLesson(t, store, 1); l.Status != database.StatusSkipped || l.HasFiles() {
						t.Errorf("lesson = %q, has files %v; want tombstoned", l.Status, l.HasFiles())
					}
					return
				}
				wantError(t, rec, http.StatusConflict, msgLessonLeftBehind)
				assertOnDisk(t, true, files, leftBehindNames...)
				assertUnchanged(t, store, before)
				if !strings.Contains(log.String(), recordedSeason) {
					t.Errorf("log %q does not name the folder the files stayed in", log.String())
				}
			})
		}
	}
}

// TestDeleteFollowRefusesFilesLeftBehindByALibraryMove proves DELETE
// /api/follows/{id}?files=true removes nothing at all when any lesson's files
// stayed behind after the library setting moved: it answers 409 with the fix,
// the follow stays, and every lesson (the one left behind, and one whose
// files it could have removed) records its files as before, all on disk. The
// refusal is known before the first removal, so the follow is not half
// deleted. With nothing moved, the follow and the files go as before.
func TestDeleteFollowRefusesFilesLeftBehindByALibraryMove(t *testing.T) {
	for _, m := range []libraryMove{libraryMoves[0], libraryMoves[1]} {
		t.Run(m.name, func(t *testing.T) {
			captureLog(t)
			store := newTestStore(t)
			root := t.TempDir()
			lib, recordedSeason, files := m.setUp(t, root)
			downloads := filepath.Join(root, "dl")
			f := addFollow(t, store, 4242)
			// Lesson 3, left behind, sorts after lesson 2 (BeginFollowDelete
			// and ListLessonsByFollow order by railcontent_id). Here the
			// refusal comes up front, before BeginFollowDelete, so the
			// removal loop never runs and this test can't tell a check made
			// lesson by lesson inside it from one made before it:
			// TestDeleteFollowChecksAgainBeforeTheFirstRemoval pins that.
			seedSeasonLesson(t, store, f, 3, recordedSeason, files, true)
			// Lesson 2 of the same follow, kept in downloads in its own folder:
			// nothing about it is left behind.
			own := filepath.Join(downloads, "F", "06 - Lesson B")
			if err := store.UpsertLesson(t.Context(), 2, "Lesson B", sql.NullInt64{}, "drumeo", sql.NullInt64{Int64: 6, Valid: true}, sql.NullInt64{Int64: f, Valid: true}); err != nil {
				t.Fatal(err)
			}
			seedEntries(t, own, "06 - Lesson B.mp4")
			finishWithNewJob(t, store, f, 2, database.DownloadRecord{Quality: "1080", OutputDir: own, VideoPath: filepath.Join(own, "06 - Lesson B.mp4"), Bytes: 5})
			before1, before2 := mustLesson(t, store, 3), mustLesson(t, store, 2)
			srv := NewServer(store, Deps{}, nil, Config{DownloadsDir: downloads, LibraryDir: lib}, "test")

			rec := serveDelete(t, srv, "/api/follows/"+strconv.FormatInt(f, 10)+"?files=true")
			follows, err := store.ListFollows(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !m.leftBehind {
				if rec.Code != http.StatusNoContent || len(follows) != 0 {
					t.Fatalf("DELETE = %d %s, %d follows; want 204 and the follow gone", rec.Code, rec.Body.String(), len(follows))
				}
				assertOnDisk(t, false, files, leftBehindNames...)
				assertOnDisk(t, false, own, "06 - Lesson B.mp4")
				return
			}
			wantError(t, rec, http.StatusConflict, msgFollowLeftBehind)
			if len(follows) != 1 {
				t.Errorf("%d follows, want the follow kept", len(follows))
			}
			assertOnDisk(t, true, files, leftBehindNames...)
			assertOnDisk(t, true, own, "06 - Lesson B.mp4")
			assertUnchanged(t, store, before1)
			assertUnchanged(t, store, before2)
		})
	}
}
