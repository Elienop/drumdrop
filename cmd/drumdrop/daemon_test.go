package main

import (
	"flag"
	"testing"
	"time"
)

// TestParseInterval covers the --interval flag parsing: the 12h default and
// other valid durations succeed; an unparseable or non-positive value is
// rejected before the daemon ever opens the store.
func TestParseInterval(t *testing.T) {
	t.Run("default 12h", func(t *testing.T) {
		got, err := parseInterval("12h")
		if err != nil {
			t.Fatalf("parseInterval(12h) error: %v", err)
		}
		if got != 12*time.Hour {
			t.Errorf("parseInterval(12h) = %v, want 12h", got)
		}
	})

	t.Run("other valid durations", func(t *testing.T) {
		for _, c := range []struct {
			in   string
			want time.Duration
		}{
			{"30m", 30 * time.Minute},
			{"6h", 6 * time.Hour},
			{"90s", 90 * time.Second},
		} {
			got, err := parseInterval(c.in)
			if err != nil {
				t.Errorf("parseInterval(%q) error: %v", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("parseInterval(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	})

	t.Run("unparseable is rejected", func(t *testing.T) {
		if _, err := parseInterval("nonsense"); err == nil {
			t.Error("parseInterval(nonsense): want error, got nil")
		}
	})

	t.Run("non-positive is rejected", func(t *testing.T) {
		for _, in := range []string{"0", "0s", "-5m"} {
			if _, err := parseInterval(in); err == nil {
				t.Errorf("parseInterval(%q): want error for non-positive, got nil", in)
			}
		}
	})
}

// TestDaemonIntervalFlagSplitting is a regression test for the bug where the
// space-separated `--interval 6h` form (documented in README + usage) failed:
// splitArgs only carries the following token along for flags listed in
// valueFlags, and --interval/-interval were missing, so fs.Parse(["--interval"])
// errored with "flag needs an argument" before the daemon ever opened the store.
// It drives splitArgs + fs.Parse exactly as cmdDaemon does, mirroring
// TestFollowFlagSplitting.
func TestDaemonIntervalFlagSplitting(t *testing.T) {
	if !valueFlags["--interval"] || !valueFlags["-interval"] {
		t.Fatal("--interval/-interval must be registered in valueFlags so its value is not orphaned")
	}

	// parse mirrors cmdDaemon's flag handling: split positionals from flags, then
	// Parse only the flag tokens. It returns the resolved daemon flag values.
	parse := func(argv []string) (interval string, once, resourcesOnly bool, out, quality string, err error) {
		fs := flag.NewFlagSet("drumdrop daemon", flag.ContinueOnError)
		iv := fs.String("interval", "12h", "")
		o := fs.Bool("once", false, "")
		ro := fs.Bool("resources-only", false, "")
		outF := fs.String("out", "", "")
		q := fs.String("quality", "", "")
		_, flags := splitArgs(argv)
		if err = fs.Parse(flags); err != nil {
			return
		}
		return *iv, *o, *ro, *outF, *q, nil
	}

	t.Run("space-separated --interval keeps its value", func(t *testing.T) {
		interval, _, _, _, _, err := parse([]string{"--interval", "6h"})
		if err != nil {
			t.Fatalf("parse([--interval 6h]) error: %v", err)
		}
		if interval != "6h" {
			t.Errorf("interval = %q, want 6h", interval)
		}
		dur, err := parseInterval(interval)
		if err != nil {
			t.Fatalf("parseInterval(%q) error: %v", interval, err)
		}
		if dur != 6*time.Hour {
			t.Errorf("parsed duration = %v, want 6h", dur)
		}
	})

	t.Run("single-dash -interval keeps its value", func(t *testing.T) {
		interval, _, _, _, _, err := parse([]string{"-interval", "30m"})
		if err != nil {
			t.Fatalf("parse([-interval 30m]) error: %v", err)
		}
		if interval != "30m" {
			t.Errorf("interval = %q, want 30m", interval)
		}
	})

	t.Run("equals form still works", func(t *testing.T) {
		interval, _, _, _, _, err := parse([]string{"--interval=90s"})
		if err != nil {
			t.Fatalf("parse([--interval=90s]) error: %v", err)
		}
		if interval != "90s" {
			t.Errorf("interval = %q, want 90s", interval)
		}
	})

	t.Run("default 12h when omitted", func(t *testing.T) {
		interval, _, _, _, _, err := parse(nil)
		if err != nil {
			t.Fatalf("parse(nil) error: %v", err)
		}
		if interval != "12h" {
			t.Errorf("interval = %q, want 12h default", interval)
		}
	})

	t.Run("--interval mixed with other daemon flags", func(t *testing.T) {
		interval, once, resourcesOnly, out, _, err := parse(
			[]string{"--interval", "1h", "--once", "--out", "/tmp/dd", "--resources-only"})
		if err != nil {
			t.Fatalf("parse(mixed) error: %v", err)
		}
		if interval != "1h" {
			t.Errorf("interval = %q, want 1h", interval)
		}
		if !once {
			t.Error("once = false, want true")
		}
		if !resourcesOnly {
			t.Error("resourcesOnly = false, want true")
		}
		if out != "/tmp/dd" {
			t.Errorf("out = %q, want /tmp/dd", out)
		}
	})
}
