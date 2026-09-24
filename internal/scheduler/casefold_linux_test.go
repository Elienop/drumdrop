//go:build linux

package scheduler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// fsCasefoldFL is FS_CASEFOLD_FL (linux/fs.h), which x/sys does not name:
// set on an empty folder of a filesystem mounted with casefold support, it
// makes every name in it, and in every folder made in it, case-insensitive.
const fsCasefoldFL = 0x40000000

// casefoldChildEnv names, in the child process runCasefold starts, the folder
// to mount a casefold tmpfs on.
const casefoldChildEnv = "DRUMDROP_TEST_CASEFOLD_DIR"

// runCasefold runs the test t again in a child process, in its own user and
// mount namespaces, where it is root: the child mounts a tmpfs with casefold
// support and calls run with a case-insensitive folder in it. It skips where
// that can not be had (unprivileged user namespaces refused, a kernel whose
// tmpfs has no casefold). A test calls it first, and returns at once when it
// reports it ran as the parent.
func runCasefold(t *testing.T, run func(t *testing.T, dir string)) {
	t.Helper()
	if mnt := os.Getenv(casefoldChildEnv); mnt != "" {
		run(t, casefoldDir(t, mnt))
		return
	}
	mnt := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), casefoldChildEnv+"="+mnt)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err != nil && !errors.As(err, &exit):
		t.Skipf("no user namespace for a casefold mount: %v", err)
	case strings.Contains(string(out), "--- SKIP"):
		t.Skipf("the child skipped:\n%s", out)
	case err != nil:
		t.Fatalf("the child failed: %v\n%s", err, out)
	}
}

// casefoldDir mounts a casefold tmpfs on mnt (in the child's own mount
// namespace) and returns a case-insensitive folder in it, checked with a
// control: a file made as "AbC" is found as "abc".
func casefoldDir(t *testing.T, mnt string) string {
	t.Helper()
	if err := syscall.Mount("tmpfs", mnt, "tmpfs", 0, "casefold"); err != nil {
		t.Skipf("no casefold tmpfs: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Unmount(mnt, syscall.MNT_DETACH) })
	dir := filepath.Join(mnt, "cf")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = unix.IoctlSetPointerInt(int(f.Fd()), unix.FS_IOC_SETFLAGS, fsCasefoldFL)
	f.Close()
	if err != nil {
		t.Skipf("chattr +F refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AbC"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "abc")); err != nil {
		t.Skipf("the folder is not case-insensitive: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "AbC")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPlexTVCaseOnlyTitleChangeMergesOnACaseInsensitiveDisk (round-5 fix
// security L1) proves that in plex-tv, on a case-insensitive disk, a lesson
// whose title changed only in case ("five" to "Five") merges its recorded
// resources folder with the new one, as ruling (a) wants for the same entry:
// the owner's file in it stays beside the new one. Whether a recorded entry
// is being placed is decided by identity, not by its spelling.
func TestPlexTVCaseOnlyTitleChangeMergesOnACaseInsensitiveDisk(t *testing.T) {
	runCasefold(t, func(t *testing.T, tmp string) {
		lib := filepath.Join(tmp, "lib")
		season := filepath.Join(lib, "Show", "Season 01")
		old := "Show - s01e05 - five"
		seedSeason(t, season, old+".mp4")
		writeTree(t, filepath.Join(season, old+" resources"), map[string]string{"my-notes.txt": "mine"})
		lessonDir := scratchLesson(t, tmp, 5, "Five", []string{".mp4", ".nfo"}, "resources")

		res, err := testMovePlexTV(t, lib, "Show", 1, 5, "Five", lessonDir,
			plexLibrary{self: recordedRow(1, season, old+".mp4", old+" resources/")})
		if err != nil || res.seasonDir != season {
			t.Fatalf("move = (%+v, %v)", res, err)
		}
		assertTree(t, filepath.Join(season, "Show - s01e05 - Five resources"), map[string]string{
			"my-notes.txt": "mine",
			"new.pdf":      "new",
		})
		if got, err := os.ReadFile(filepath.Join(season, "Show - s01e05 - Five.mp4")); err != nil || string(got) != "new .mp4" {
			t.Errorf("video = %q, %v; want the new download's", got, err)
		}
	})
}
