package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// TestDaemonRecoverRunsOnce (code L4) proves crash recovery runs once per
// daemon however often it is asked: serve runs it before taking requests, and
// Run asks again when it starts, which must not requeue a job the first
// cycle's worker already claimed.
func TestDaemonRecoverRunsOnce(t *testing.T) {
	store := newFakeDaemonStore()
	d := newTestDaemon(t, store)
	d.Recover(context.Background())
	d.Recover(context.Background())
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.requeueCalls != 1 {
		t.Errorf("RequeueStaleRunning called %d times, want 1", store.requeueCalls)
	}
}

// TestMoveRefusesALessonFolderThatIsASymlinkInsideDownloads proves the move
// takes only a real lesson folder: a symlink at the lesson folder's name,
// even one to a sibling folder inside downloads, is refused (the lesson stays
// where it is), never moved into the library as a link nor followed.
func TestMoveRefusesALessonFolderThatIsASymlinkInsideDownloads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	for _, layout := range []string{LayoutPlexTV, ""} {
		t.Run("layout="+layout, func(t *testing.T) {
			tmp := t.TempDir()
			dl, lib := filepath.Join(tmp, "dl"), filepath.Join(tmp, "lib")
			other := filepath.Join(dl, "Course", "99 - Real")
			seedSeason(t, other, "05 - Five.mp4", "05 - Five.nfo")
			lessonDir := filepath.Join(dl, "Course", "05 - Five")
			if err := os.MkdirAll(filepath.Dir(lessonDir), 0o755); err != nil {
				t.Fatal(err)
			}
			// A sibling, named relatively: os.Root opened on the course folder
			// follows it (an absolute or escaping one it refuses by itself).
			if err := os.Symlink("99 - Real", lessonDir); err != nil {
				t.Fatal(err)
			}
			var err error
			if layout == LayoutPlexTV {
				var res plexMoveResult
				res, err = testMovePlexTVFrom(t, dl, lib, plexEpisode{"Show", 1, 5, "Five"}, lessonDir, plexLibrary{})
				if res.seasonDir != "" {
					t.Errorf("seasonDir = %q, want none", res.seasonDir)
				}
			} else {
				var newDir string
				newDir, err = testPlace(t, dl, lib, lessonDir, database.Lesson{})
				if newDir != "" {
					t.Errorf("newDir = %q, want none", newDir)
				}
			}
			if err == nil {
				t.Error("moved a lesson folder that is a symlink, want a refusal")
			}
			assertExist(t, true, filepath.Join(other, "05 - Five.mp4"), lessonDir)
			if names := readDirNames(t, filepath.Join(lib, "Course")); len(names) != 0 {
				t.Errorf("the library holds %v, want nothing", names)
			}
			if names := readDirNames(t, filepath.Join(lib, "Show", "Season 01")); len(names) != 0 {
				t.Errorf("the season folder holds %v, want nothing", names)
			}
		})
	}
}
