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
