package main

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name            string
		argv            []string
		wantPositionals []string
		wantFlags       []string
	}{
		{
			name:            "flag after id (the broken case)",
			argv:            []string{"409875", "--dry-run"},
			wantPositionals: []string{"409875"},
			wantFlags:       []string{"--dry-run"},
		},
		{
			name:            "flag before id",
			argv:            []string{"--dry-run", "409875"},
			wantPositionals: []string{"409875"},
			wantFlags:       []string{"--dry-run"},
		},
		{
			name:            "value flag after id keeps its value",
			argv:            []string{"409875", "--out", "/tmp/x", "--limit", "3"},
			wantPositionals: []string{"409875"},
			wantFlags:       []string{"--out", "/tmp/x", "--limit", "3"},
		},
		{
			name:            "equals form is self-contained",
			argv:            []string{"409875", "--out=/tmp/x"},
			wantPositionals: []string{"409875"},
			wantFlags:       []string{"--out=/tmp/x"},
		},
		{
			name:            "interleaved positional and flags",
			argv:            []string{"--whole-course", "409875", "--quality", "1080"},
			wantPositionals: []string{"409875"},
			wantFlags:       []string{"--whole-course", "--quality", "1080"},
		},
		{
			name:            "no flags",
			argv:            []string{"409875"},
			wantPositionals: []string{"409875"},
			wantFlags:       nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPos, gotFlags := splitArgs(tt.argv)
			if !reflect.DeepEqual(gotPos, tt.wantPositionals) {
				t.Errorf("positionals = %#v, want %#v", gotPos, tt.wantPositionals)
			}
			if !reflect.DeepEqual(gotFlags, tt.wantFlags) {
				t.Errorf("flags = %#v, want %#v", gotFlags, tt.wantFlags)
			}
		})
	}
}

// TestFlagOrderingAfterID proves the regression is fixed end-to-end: splitting
// then parsing must honor flags placed after the positional id, exactly as the
// Node reference (src/cli.mjs parseArgs) does. It drives parseDownloadArgs — the
// SAME parser cmdDownload runs — so a real parser regression is caught.
func TestFlagOrderingAfterID(t *testing.T) {
	parse := func(argv []string) (id string, out, quality string, limit int, whole, resourcesOnly, dryRun bool, err error) {
		args, err := parseDownloadArgs(argv)
		if err != nil {
			return "", "", "", 0, false, false, false, err
		}
		if len(args.positionals) > 0 {
			id = args.positionals[0]
		}
		return id, args.out, args.quality, args.limit, args.whole, args.resourcesOnly, args.dryRun, nil
	}

	t.Run("dry-run after id", func(t *testing.T) {
		id, _, _, _, _, _, dryRun, err := parse([]string{"409875", "--dry-run"})
		if err != nil {
			t.Fatal(err)
		}
		if id != "409875" {
			t.Errorf("id = %q, want 409875", id)
		}
		if !dryRun {
			t.Error("dryRun = false, want true (flag after id must be honored)")
		}
	})

	t.Run("whole-course after url", func(t *testing.T) {
		url := "https://app.musora.com/drumeo/lessons/course/409875/409918"
		id, _, _, _, whole, _, _, err := parse([]string{url, "--whole-course"})
		if err != nil {
			t.Fatal(err)
		}
		if id != url {
			t.Errorf("id = %q, want %q", id, url)
		}
		if !whole {
			t.Error("whole = false, want true")
		}
	})

	t.Run("value flags after id", func(t *testing.T) {
		id, out, quality, limit, _, resourcesOnly, _, err := parse(
			[]string{"409875", "--out", "/tmp/x", "--quality", "1080", "--limit", "3", "--resources-only"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if id != "409875" {
			t.Errorf("id = %q, want 409875", id)
		}
		if out != "/tmp/x" {
			t.Errorf("out = %q, want /tmp/x", out)
		}
		if quality != "1080" {
			t.Errorf("quality = %q, want 1080", quality)
		}
		if limit != 3 {
			t.Errorf("limit = %d, want 3", limit)
		}
		if !resourcesOnly {
			t.Error("resourcesOnly = false, want true")
		}
	})

	t.Run("flags before id still work", func(t *testing.T) {
		id, _, quality, _, _, _, dryRun, err := parse([]string{"--quality", "720", "--dry-run", "409875"})
		if err != nil {
			t.Fatal(err)
		}
		if id != "409875" {
			t.Errorf("id = %q, want 409875", id)
		}
		if quality != "720" {
			t.Errorf("quality = %q, want 720", quality)
		}
		if !dryRun {
			t.Error("dryRun = false, want true")
		}
	})
}
