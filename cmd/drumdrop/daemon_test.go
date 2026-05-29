package main

import (
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

// TestDaemonArgs drives the production parseDaemonArgs so a real parser
// regression is caught. It covers the regression where the space-separated
// `--interval 6h` form failed (splitArgs did not carry the value for the
// unregistered --interval/-interval), the equals/default/mixed forms, and that
// an invalid interval is rejected through the same entry point cmdDaemon uses.
func TestDaemonArgs(t *testing.T) {
	if !valueFlags["--interval"] || !valueFlags["-interval"] {
		t.Fatal("--interval/-interval must be registered in valueFlags so its value is not orphaned")
	}

	t.Run("space-separated --interval keeps its value", func(t *testing.T) {
		opts, err := parseDaemonArgs([]string{"--interval", "6h"})
		if err != nil {
			t.Fatalf("parseDaemonArgs([--interval 6h]) error: %v", err)
		}
		if opts.interval != 6*time.Hour {
			t.Errorf("interval = %v, want 6h", opts.interval)
		}
	})

	t.Run("single-dash -interval keeps its value", func(t *testing.T) {
		opts, err := parseDaemonArgs([]string{"-interval", "30m"})
		if err != nil {
			t.Fatalf("parseDaemonArgs([-interval 30m]) error: %v", err)
		}
		if opts.interval != 30*time.Minute {
			t.Errorf("interval = %v, want 30m", opts.interval)
		}
	})

	t.Run("equals form still works", func(t *testing.T) {
		opts, err := parseDaemonArgs([]string{"--interval=90s"})
		if err != nil {
			t.Fatalf("parseDaemonArgs([--interval=90s]) error: %v", err)
		}
		if opts.interval != 90*time.Second {
			t.Errorf("interval = %v, want 90s", opts.interval)
		}
	})

	t.Run("default 12h when omitted", func(t *testing.T) {
		opts, err := parseDaemonArgs(nil)
		if err != nil {
			t.Fatalf("parseDaemonArgs(nil) error: %v", err)
		}
		if opts.interval != 12*time.Hour {
			t.Errorf("interval = %v, want 12h default", opts.interval)
		}
	})

	t.Run("--interval mixed with other daemon flags", func(t *testing.T) {
		opts, err := parseDaemonArgs(
			[]string{"--interval", "1h", "--once", "--out", "/tmp/dd", "--resources-only"})
		if err != nil {
			t.Fatalf("parseDaemonArgs(mixed) error: %v", err)
		}
		if opts.interval != time.Hour {
			t.Errorf("interval = %v, want 1h", opts.interval)
		}
		if !opts.once {
			t.Error("once = false, want true")
		}
		if !opts.resourcesOnly {
			t.Error("resourcesOnly = false, want true")
		}
		if opts.out != "/tmp/dd" {
			t.Errorf("out = %q, want /tmp/dd", opts.out)
		}
	})

	t.Run("invalid interval is rejected through the parser", func(t *testing.T) {
		for _, in := range []string{"0", "nonsense", "-5m"} {
			if _, err := parseDaemonArgs([]string{"--interval", in}); err == nil {
				t.Errorf("parseDaemonArgs([--interval %q]): want error, got nil", in)
			}
		}
	})
}
