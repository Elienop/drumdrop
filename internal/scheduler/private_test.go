package scheduler

import (
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestOpenRealDirNeverOpensWhatASwapPutsThere (security I3) proves
// openRealDir only ever opens the folder its Lstat saw: with a real folder
// swapped for a symlink to another folder and back, as fast as the OS allows,
// every folder it does open is the real one (os.Root follows a symlink inside
// the root, so without the identity check a swap between the two calls opens
// the other folder). A race, so it runs for a fixed time; each open that
// lands on the other folder is a failure.
func TestOpenRealDirNeverOpensWhatASwapPutsThere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	writeTree(t, filepath.Join(dir, "d"), map[string]string{"real.txt": "real"})
	writeTree(t, filepath.Join(dir, "other"), map[string]string{"other.txt": "other"})
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		d, aside, link := filepath.Join(dir, "d"), filepath.Join(dir, "d.aside"), filepath.Join(dir, "d.link")
		if err := os.Symlink("other", link); err != nil {
			t.Error(err)
			return
		}
		for !stop.Load() {
			// d is the real folder, then (for a moment) a symlink to other.
			_ = os.Rename(d, aside)
			_ = os.Rename(link, d)
			_ = os.Rename(d, link)
			_ = os.Rename(aside, d)
		}
	}()

	opened, wrong := 0, 0
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
		r, err := openRealDir(root, "d")
		if err != nil {
			continue // refused: the swap was seen
		}
		opened++
		if _, err := r.Lstat("real.txt"); err != nil {
			wrong++
		}
		r.Close()
	}
	stop.Store(true)
	<-done
	if wrong > 0 {
		t.Errorf("openRealDir opened the swapped-in folder %d times out of %d opens", wrong, opened)
	}
	if opened == 0 {
		t.Error("openRealDir never opened the real folder; the race never let it through")
	}
}
