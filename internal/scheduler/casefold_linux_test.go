//go:build linux

package scheduler

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fsCasefoldFL is FS_CASEFOLD_FL (linux/fs.h), which x/sys does not name:
// set on an empty folder of a filesystem mounted with casefold support, it
// makes every name in it, and in every folder made in it, case-insensitive.
const fsCasefoldFL = 0x40000000

// casefoldChildEnv names, in the child process runCasefold starts, the folder
// to mount a casefold tmpfs on.
const casefoldChildEnv = "DRUMDROP_TEST_CASEFOLD_DIR"

// casefoldChildTimeout bounds the child test run: a hung child panics with
// its goroutines' stacks instead of outliving the parent.
const casefoldChildTimeout = 2 * time.Minute

// runCasefold runs the test t again in a child process, in its own user and
// mount namespaces, where it is root: the child mounts a tmpfs with casefold
// support and calls run with a case-insensitive folder in it. It skips where
// that can not be had (unprivileged user namespaces refused, a kernel whose
// tmpfs has no casefold). A test calls it first, and returns at once when it
// reports it ran as the parent.
//
// The child selects exactly t (each level of its name quoted, so a name with
// regexp characters can not select nothing), and the parent fails unless the
// child's output reports t passed: a child that ran no test is not a pass.
// The child is killed if the parent dies (Pdeathsig, sent when the thread
// that started it exits, so that thread is held for the run), and it times
// out on its own (casefoldChildTimeout). Its output is shown on failure.
func runCasefold(t *testing.T, run func(t *testing.T, dir string)) {
	t.Helper()
	if mnt := os.Getenv(casefoldChildEnv); mnt != "" {
		run(t, casefoldDir(t, mnt))
		return
	}
	mnt := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run="+runPattern(t.Name()), "-test.v", "-test.count=1", "-test.timeout="+casefoldChildTimeout.String())
	cmd.Env = append(os.Environ(), casefoldChildEnv+"="+mnt)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		Pdeathsig:   syscall.SIGKILL,
	}
	runtime.LockOSThread()
	out, err := cmd.CombinedOutput()
	runtime.UnlockOSThread()
	switch v, why := judgeChild(t.Name(), string(out), err); v {
	case childSkipped:
		t.Skip(why)
	case childFailed:
		t.Fatal(why)
	}
}

// childVerdict is what the parent makes of the child's run.
type childVerdict int

const (
	childPassed childVerdict = iota
	childSkipped
	childFailed
)

// judgeChild reads the child's run of the test named name, from its output
// and exit error: skipped when it could not start (no user namespace) or it
// reports that very test skipped, and exited 0; failed when it exited
// non-zero, whatever its output says (a subtest that skipped beside one that
// failed, or a message quoting "--- SKIP", is still a failure), or it doesn't
// report that test passed; passed otherwise. why says which, with the output.
func judgeChild(name, out string, err error) (childVerdict, string) {
	var exit *exec.ExitError
	switch {
	case err != nil && !errors.As(err, &exit):
		return childSkipped, fmt.Sprintf("no user namespace for a casefold mount: %v", err)
	case err != nil:
		return childFailed, fmt.Sprintf("the child failed: %v\n%s", err, out)
	case strings.Contains(out, "--- SKIP: "+name+" ("):
		return childSkipped, fmt.Sprintf("the child skipped:\n%s", out)
	case !strings.Contains(out, "--- PASS: "+name+" ("):
		return childFailed, fmt.Sprintf("the child did not report %s as passed:\n%s", name, out)
	}
	return childPassed, ""
}

// TestJudgeCasefoldChild (round-5c security L2) proves a child that failed
// is never taken for one that skipped: its exit status is read before any
// "--- SKIP" in its output, and only a skip of the test itself counts.
func TestJudgeCasefoldChild(t *testing.T) {
	const name = "TestX/a+b_(c)"
	exit := exec.Command("false").Run()
	var exitErr *exec.ExitError
	if !errors.As(exit, &exitErr) {
		t.Fatalf("no exit error to test with: %v", exit)
	}
	for _, c := range []struct {
		why  string
		out  string
		err  error
		want childVerdict
	}{
		{"passed", "=== RUN   TestX/a+b_(c)\n    --- PASS: TestX/a+b_(c) (0.00s)\nPASS\n", nil, childPassed},
		{"skipped", "    --- SKIP: TestX/a+b_(c) (0.00s)\n        no casefold\nPASS\n", nil, childSkipped},
		{"could not start", "", errors.New("fork/exec: operation not permitted"), childSkipped},
		{"a subtest skipped, another failed", "        --- SKIP: TestX/a+b_(c)/skips (0.00s)\n        --- FAIL: TestX/a+b_(c)/fails (0.00s)\n    --- FAIL: TestX/a+b_(c) (0.00s)\n    --- SKIP: TestX/a+b_(c) (0.00s)\nFAIL\n", exit, childFailed},
		{"its failure quotes a skip", "    x_test.go:1: unexpected output:\n        --- SKIP: TestX/a+b_(c) (0.00s)\n    --- FAIL: TestX/a+b_(c) (0.00s)\nFAIL\n", exit, childFailed},
		{"a subtest of it skipped", "        --- SKIP: TestX/a+b_(c)/sub (0.00s)\n    --- PASS: TestX/a+b_(c) (0.00s)\nPASS\n", nil, childPassed},
		{"another test skipped, this one never ran", "--- SKIP: TestY (0.00s)\nPASS\n", nil, childFailed},
	} {
		t.Run(c.why, func(t *testing.T) {
			if got, why := judgeChild(name, c.out, c.err); got != c.want {
				t.Errorf("verdict = %d (%s), want %d", got, why, c.want)
			}
		})
	}
}

// runPattern is the -test.run pattern that selects exactly the test named
// name ("TestX", or "TestX/sub/…" for a subtest): each level quoted and
// anchored.
func runPattern(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}

// TestRunCasefoldRunsASubtestNamedWithRegexpCharacters (round-5b security I1)
// proves the child runs a subtest whose name holds regexp characters, for
// which a bare -test.run of the name selected nothing (and the child then
// passed without running it). The child's body checks that the folder it is
// given is case-insensitive.
func TestRunCasefoldRunsASubtestNamedWithRegexpCharacters(t *testing.T) {
	t.Run("a+b (c)", func(t *testing.T) {
		runCasefold(t, func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "Five"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "FIVE")); err != nil {
				t.Errorf("the folder is not case-insensitive: %v", err)
			}
		})
	})
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

		res, err := testMovePlexTV(t, lib, plexEpisode{"Show", 1, 5, "Five"}, lessonDir,
			plexLibrary{self: recordedRow(1, season, old+".mp4", old+" resources/")})
		if err != nil || res.seasonDir != season {
			t.Fatalf("move = (%+v, %v)", res, err)
		}
		// The recorded folder IS the one placed at: it was merged, not decided
		// on as a previous folder elsewhere and logged as left behind.
		if len(res.pending.kept) != 0 {
			t.Errorf("kept %+v, want none", res.pending.kept)
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
