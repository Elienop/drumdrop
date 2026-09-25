package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// runArgs runs args through run against a throwaway config folder, so no
// command reads or writes the real one, and returns the exit code, what run
// wrote to stdout and stderr, and the folder.
func runArgs(t *testing.T, args ...string) (code int, stdout, stderr, configDir string) {
	t.Helper()
	configDir = t.TempDir()
	t.Setenv("DRUMDROP_CONFIG_DIR", configDir)
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String(), configDir
}

// TestRunWithoutArgumentsPrintsUsageAndFails pins the bare `drumdrop`: the
// usage on stdout, nothing on stderr, exit 1.
func TestRunWithoutArgumentsPrintsUsageAndFails(t *testing.T) {
	code, stdout, stderr, _ := runArgs(t)
	if code != 1 || stdout != usage || stderr != "" {
		t.Errorf("run() = %d, stdout %d bytes (usage is %d), stderr %q; want 1, the usage, nothing",
			code, len(stdout), len(usage), stderr)
	}
}

// TestRunDownloadWithoutATargetPrintsUsageAndFails pins a download command
// line that names no lesson or course (only its flags): the usage on stdout,
// nothing on stderr, exit 1, the same answer as a bare `drumdrop`.
func TestRunDownloadWithoutATargetPrintsUsageAndFails(t *testing.T) {
	code, stdout, stderr, _ := runArgs(t, "--quality", "720")
	if code != 1 || stdout != usage || stderr != "" {
		t.Errorf("run(--quality 720) = %d, stdout %d bytes (usage is %d), stderr %q; want 1, the usage, nothing",
			code, len(stdout), len(usage), stderr)
	}
}

// TestRunHelpPrintsUsage pins -h and --help: the usage on stdout, exit 0.
func TestRunHelpPrintsUsage(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			code, stdout, stderr, _ := runArgs(t, arg)
			if code != 0 || stdout != usage || stderr != "" {
				t.Errorf("run(%s) = %d, stdout %d bytes (usage is %d), stderr %q; want 0, the usage, nothing",
					arg, code, len(stdout), len(usage), stderr)
			}
		})
	}
}

// TestRunReportsAFailedCommand proves each command name reaches its own
// command: every case fails early, and run prints the error on stderr after
// "✖ " and exits 1. The default case is a download, so a command whose case
// is missing becomes a download of its name; each case therefore expects an
// error the download does not give for the same arguments. None of them
// reaches the network or the real config folder. sync is pinned by
// TestRunSyncDryRunSucceeds instead: every sync flag is a download flag
// too, and Go's flag errors do not name the flag set, so a sync flag error
// reads word for word like the download's.
func TestRunReportsAFailedCommand(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  []string
		setup func(t *testing.T)
		want  string // the error, or the start of a flag error
	}{
		{"login", []string{"login"}, stubRefusedLogin, "login failed: Invalid credentials"},
		{"whoami", []string{"whoami"}, nil, "not logged in — run `drumdrop login` first"},
		{"follow", []string{"follow"}, nil, "follow: provide a lesson/course id or URL, or @slug / --instructor slug"},
		{"unfollow", []string{"unfollow"}, nil, "unfollow: provide a follow id (see `drumdrop follows`)"},
		// The download defines no --once: it would answer "flag provided but
		// not defined: -once".
		{"daemon", []string{"daemon", "--once=maybe"}, nil, `invalid boolean value "maybe" for -once`},
		// 192.0.2.1 is TEST-NET-1 (RFC 5737), reserved for documentation and
		// on no interface: if GuardListen ever let this bind through, the
		// listen fails at once with another error, rather than opening a
		// tokenless API and serving until the test times out.
		{"serve", []string{"serve", "--listen", "192.0.2.1:0"}, noAPIToken,
			`refusing to bind "192.0.2.1:0" without an API token: set DRUMDROP_API_TOKEN or listen on loopback`},
		{"download", []string{"not-an-id"}, nil, "could not parse a content id from: not-an-id"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.setup != nil {
				c.setup(t)
			}
			code, stdout, stderr, _ := runArgs(t, c.args...)
			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			if !strings.HasPrefix(stderr, "✖  ") || !strings.HasSuffix(stderr, "\n") || !strings.Contains(stderr, c.want) {
				t.Errorf("stderr = %q, want \"✖  \" then an error containing %q, then a newline", stderr, c.want)
			}
		})
	}
}

// stubRefusedLogin sets the credentials login reads from the environment and
// points Musora's auth endpoint at a server that refuses them, and fails the
// test unless that server was asked.
func stubRefusedLogin(t *testing.T) {
	t.Setenv("MUSORA_EMAIL", "a@b.com")
	t.Setenv("MUSORA_PASSWORD", "pw")
	var asked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Store(r.URL.Path == "/sessions")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid credentials"}`))
	}))
	t.Cleanup(srv.Close)
	prev := musora.AuthBase
	musora.AuthBase = srv.URL
	t.Cleanup(func() { musora.AuthBase = prev })
	t.Cleanup(func() {
		if !asked.Load() {
			t.Error("login never asked Musora's /sessions")
		}
	})
}

// noAPIToken unsets the API token, so serve refuses a non-loopback bind
// before it listens.
func noAPIToken(t *testing.T) { t.Setenv("DRUMDROP_API_TOKEN", "") }

// failIfMusoraAsked points Musora's auth and GROQ endpoints at a server that
// fails the test if anything asks it, so a command that must not reach the
// network proves it did not.
func failIfMusoraAsked(t *testing.T) {
	var asked atomic.Pointer[string] // the first path asked
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		asked.CompareAndSwap(nil, &path)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	prev := musora.AuthBase
	musora.AuthBase = srv.URL
	t.Cleanup(func() { musora.AuthBase = prev })
	t.Cleanup(musora.SetSanityBase(srv.URL))
	t.Cleanup(func() {
		if p := asked.Load(); p != nil {
			t.Errorf("Musora was asked %s", *p)
		}
	})
}

// TestRunReportsASucceededCommand proves a command that succeeds exits 0 with
// nothing on run's stdout or stderr, and that logout and follows reach their
// own commands: logout removes the saved session and credentials, follows
// opens the database. (sync: TestRunSyncDryRunSucceeds.)
func TestRunReportsASucceededCommand(t *testing.T) {
	t.Run("logout", func(t *testing.T) {
		dir := t.TempDir()
		saved := []string{filepath.Join(dir, "session.cookie"), filepath.Join(dir, "credentials.enc")}
		for _, p := range saved {
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("DRUMDROP_CONFIG_DIR", dir)
		var out, errOut bytes.Buffer
		if code := run([]string{"logout"}, &out, &errOut); code != 0 || out.Len() != 0 || errOut.Len() != 0 {
			t.Fatalf("run(logout) = %d, stdout %q, stderr %q; want 0, nothing, nothing", code, out.String(), errOut.String())
		}
		for _, p := range saved {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("%s still there after logout (stat err %v)", filepath.Base(p), err)
			}
		}
	})
	t.Run("follows", func(t *testing.T) {
		code, stdout, stderr, dir := runArgs(t, "follows")
		if code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("run(follows) = %d, stdout %q, stderr %q; want 0, nothing, nothing", code, stdout, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "drumdrop.db")); err != nil {
			t.Errorf("follows opened no database in the config folder: %v", err)
		}
	})
}

// TestRunSyncDryRunSucceeds proves sync reaches its own command, as
// TestRunReportsASucceededCommand does for logout and follows: exit 0 with
// nothing on run's stdout or stderr, and the database opened. A dry run over a
// new database has no follow to expand, so it asks Musora nothing, and it
// downloads nothing; --out keeps even the downloads folder it would resolve
// out of the package directory. Without the sync case this is a download of
// "sync", which fails: "could not parse a content id from: sync".
func TestRunSyncDryRunSucceeds(t *testing.T) {
	failIfMusoraAsked(t)
	code, stdout, stderr, dir := runArgs(t, "sync", "--dry-run", "--out", t.TempDir())
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("run(sync --dry-run) = %d, stdout %q, stderr %q; want 0, nothing, nothing", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "drumdrop.db")); err != nil {
		t.Errorf("sync opened no database in the config folder: %v", err)
	}
}

// TestRunCommandHelpExitsZero pins a command's own -h or --help: its flag set
// prints that command's usage on the process's stderr, as before, and run
// exits 0 with nothing on its own stdout or stderr, like `drumdrop -h`. The
// usage header names the flag set, so each case also proves the command name
// reached its own command.
func TestRunCommandHelpExitsZero(t *testing.T) {
	for _, c := range []struct {
		args   []string
		header string // the first line of the usage the flag set prints
	}{
		{[]string{"follow", "-h"}, "Usage of drumdrop follow:"},
		{[]string{"sync", "-h"}, "Usage of drumdrop sync:"},
		{[]string{"daemon", "--help"}, "Usage of drumdrop daemon:"},
		{[]string{"serve", "-h"}, "Usage of drumdrop serve:"},
		{[]string{"123", "-h"}, "Usage of drumdrop:"},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			var code int
			var stdout, stderr string
			printed := captureStderr(t, func() { code, stdout, stderr, _ = runArgs(t, c.args...) })
			if code != 0 || stdout != "" || stderr != "" {
				t.Errorf("run(%v) = %d, stdout %q, stderr %q; want 0, nothing, nothing", c.args, code, stdout, stderr)
			}
			if !strings.HasPrefix(printed, c.header+"\n") {
				t.Errorf("the process's stderr = %q, want the usage starting %q", printed, c.header)
			}
		})
	}
}

// captureStderr runs fn with os.Stderr swapped for a pipe, and returns what fn
// wrote to it. The flag sets write their usage there (SetOutput(os.Stderr) is
// read when each command builds its flag set, inside fn).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	read := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		_ = r.Close()
		read <- string(b)
	}()
	prev := os.Stderr
	os.Stderr = w
	func() {
		defer func() {
			os.Stderr = prev
			_ = w.Close()
		}()
		fn()
	}()
	return <-read
}
