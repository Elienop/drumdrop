package main

import (
	"bytes"
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
// command: every case fails early, with an error only that command gives, and
// run prints it on stderr after "✖ " and exits 1. The default case is a
// download. None of them reaches the network or the real config folder.
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
		// Only a flag the command defines gives "invalid value": --limit is
		// sync's alone, --once daemon's alone.
		{"sync", []string{"sync", "--limit=x"}, nil, `invalid value "x" for flag -limit`},
		{"daemon", []string{"daemon", "--once=maybe"}, nil, `invalid boolean value "maybe" for -once`},
		{"serve", []string{"serve", "--listen", "0.0.0.0:0"}, noAPIToken,
			`refusing to bind "0.0.0.0:0" without an API token: set DRUMDROP_API_TOKEN or listen on loopback`},
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

// TestRunReportsASucceededCommand proves a command that succeeds exits 0 with
// nothing on run's stdout or stderr, and that logout and follows reach their
// own commands: logout removes the saved session and credentials, follows
// opens the database.
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
