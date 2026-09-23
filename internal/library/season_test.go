package library

import "testing"

// TestIsSeasonDir pins which folder names count as a shared plex-tv season
// folder: only what SeasonName produces, never a default-layout lesson
// folder.
func TestIsSeasonDir(t *testing.T) {
	cases := map[string]bool{
		"/lib/Show/Season 01":   true,
		"/lib/Show/Season 12":   true,
		"/lib/Show/Season 100":  true,
		"/lib/Show/Season 1":    false,
		"/lib/Show/Season +01":  false,
		"/lib/Show/Season -1":   false,
		"/lib/Show/Season 01 x": false,
		"/lib/C/05 - Season 01": false,
		"":                      false,
	}
	for dir, want := range cases {
		if got := IsSeasonDir(dir); got != want {
			t.Errorf("IsSeasonDir(%q) = %v, want %v", dir, got, want)
		}
	}
}
