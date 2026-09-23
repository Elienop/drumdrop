package library

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// fiveLookAlikes are four lessons that share episode 5 of one show, each title
// the first plus a tag or a suffix: the collision the name matcher could not
// tell apart.
var fiveLookAlikes = map[string][]string{
	"Five":           {"Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo", "Show - s01e05 - Five-poster.jpg", "Show - s01e05 - Five resources/"},
	"Five [Live]":    {"Show - s01e05 - Five [Live].mp4", "Show - s01e05 - Five [Live].nfo", "Show - s01e05 - Five [Live]-poster.jpg", "Show - s01e05 - Five [Live] resources/"},
	"Five-Part Fill": {"Show - s01e05 - Five-Part Fill.mp4", "Show - s01e05 - Five-Part Fill.nfo", "Show - s01e05 - Five-Part Fill resources/"},
	"Five.5":         {"Show - s01e05 - Five.5.mp4", "Show - s01e05 - Five.5.nfo", "Show - s01e05 - Five.5.en.vtt"},
}

var fiveTitles = []string{"Five", "Five [Live]", "Five-Part Fill", "Five.5"}

// TestPlanByRecordIsExact covers the owner's ruling for recorded lessons: each
// of four look-alikes at one episode number owns exactly its recorded entries,
// in every direction, and no other lesson's.
func TestPlanByRecordIsExact(t *testing.T) {
	season := filepath.Join(t.TempDir(), "lib", "Show", "Season 01")
	var rows []database.Lesson
	for i, title := range fiveTitles {
		seedSeason(t, season, fiveLookAlikes[title]...)
		rows = append(rows, recordedRow(i+1, season, fiveLookAlikes[title]...))
	}
	for i, title := range fiveTitles {
		got, err := plan(t, libraryOf(season), rows[i], rows)
		if err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		if want := sorted(paths(season, fiveLookAlikes[title]...)); !reflect.DeepEqual(sorted(got.Remove), want) || len(got.Kept) != 0 {
			t.Errorf("%s owns %v (kept %v), want exactly %v", title, got.Remove, got.Kept, want)
		}
	}
}

// TestPlanKeepsAPathTwoRecordsName proves an entry two records name (identical
// titles at one episode) is ambiguous: neither lesson may remove it.
func TestPlanKeepsAPathTwoRecordsName(t *testing.T) {
	season := filepath.Join(t.TempDir(), "Show", "Season 01")
	a := recordedRow(1, season, "Show - s01e05 - Same.mp4", "Show - s01e05 - A-only.nfo")
	b := recordedRow(2, season, "Show - s01e05 - Same.mp4")
	got, err := plan(t, libraryOf(season), a, []database.Lesson{a, b})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !reflect.DeepEqual(got.Remove, paths(season, "Show - s01e05 - A-only.nfo")) || !reflect.DeepEqual(got.Kept, paths(season, "Show - s01e05 - Same.mp4")) {
		t.Errorf("plan = %+v, want the shared mp4 kept and only A's nfo removed", got)
	}
}

// TestPlanKeepsTheSameFileUnderAnotherSpelling covers a filesystem that reads
// two names as one file (case-insensitive: "Groove" and "GROOVE"): an entry
// that IS a file another lesson records, under another spelling, is claimed.
// A hard link stands in for the case fold: two names, one file.
func TestPlanKeepsTheSameFileUnderAnotherSpelling(t *testing.T) {
	season := filepath.Join(t.TempDir(), "lib", "Show", "Season 01")
	seedSeason(t, season, "Show - s01e05 - Groove.mp4", "Show - s01e05 - GROOVE.nfo")
	if err := os.Link(filepath.Join(season, "Show - s01e05 - Groove.mp4"), filepath.Join(season, "Show - s01e05 - GROOVE.mp4")); err != nil {
		t.Skipf("hard links unsupported here: %v", err)
	}
	a := recordedRow(1, season, "Show - s01e05 - Groove.mp4")
	b := recordedRow(2, season, "Show - s01e05 - GROOVE.mp4", "Show - s01e05 - GROOVE.nfo")
	got, err := plan(t, libraryOf(season), b, []database.Lesson{a, b})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !reflect.DeepEqual(got.Kept, paths(season, "Show - s01e05 - GROOVE.mp4")) ||
		!reflect.DeepEqual(got.Remove, paths(season, "Show - s01e05 - GROOVE.nfo")) {
		t.Errorf("plan = %+v, want the file lesson 1 records (under another name) kept", got)
	}
	// And the move's question: an existing entry at a name no record spells
	// the same way is still claimed when it is the same file.
	c, err := NewClaims(libraryOf(season), []database.Lesson{a})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := c.Claimants(filepath.Join(season, "Show - s01e05 - GROOVE.mp4"), 2, true)
	if err != nil || !reflect.DeepEqual(ids, []int{1}) {
		t.Errorf("Claimants = (%v, %v), want [1]", ids, err)
	}
	// A different file at that name is not.
	if ids, err := c.Claimants(filepath.Join(season, "Show - s01e05 - GROOVE.nfo"), 2, true); err != nil || len(ids) != 0 {
		t.Errorf("Claimants of an unclaimed file = (%v, %v), want none", ids, err)
	}
}

// TestRecordSurvivesTheLibraryMoving proves a record is read under the library
// folder configured NOW: a record written while the library was at old/ still
// names the lesson's files after it moved to new/, and a record never depends
// on how the library path was spelled.
func TestRecordSurvivesTheLibraryMoving(t *testing.T) {
	tmp := t.TempDir()
	oldSeason := filepath.Join(tmp, "old", "Show", "Season 01")
	newSeason := filepath.Join(tmp, "new", "Show", "Season 01")
	seedSeason(t, newSeason, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo")
	self := recordedRow(1, oldSeason, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo")
	got, err := plan(t, filepath.Join(tmp, "new"), self, []database.Lesson{self})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if want := sorted(paths(newSeason, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo")); !reflect.DeepEqual(sorted(got.Remove), want) {
		t.Errorf("plan = %+v, want the files under the new library %v", got, want)
	}
	// A relative library root reads the same files.
	t.Chdir(tmp)
	got, err = plan(t, "new", self, []database.Lesson{self})
	if err != nil || !reflect.DeepEqual(sorted(got.Remove), sorted(paths(newSeason, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo"))) {
		t.Errorf("relative root: plan = (%+v, %v), want the same absolute files", got, err)
	}
}

// TestLegacyRowIsReadUnderTheLibraryToday proves a lesson moved before the
// record existed is found in its season folder under the library configured
// now, as the move filed it at <library>/<show>/<Season NN>.
func TestLegacyRowIsReadUnderTheLibraryToday(t *testing.T) {
	tmp := t.TempDir()
	oldSeason := filepath.Join(tmp, "old", "Show", "Season 01")
	newSeason := filepath.Join(tmp, "new", "Show", "Season 01")
	seedSeason(t, newSeason, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo", "Show - s01e06 - Six.mp4")
	self := legacyRow(1, "Five", 5, oldSeason, "Show - s01e05 - Five.mp4")
	got, err := plan(t, filepath.Join(tmp, "new"), self, []database.Lesson{self})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if want := sorted(paths(newSeason, "Show - s01e05 - Five.mp4", "Show - s01e05 - Five.nfo")); !reflect.DeepEqual(sorted(got.Remove), want) {
		t.Errorf("plan = %+v, want %v", got, want)
	}
}

// TestPlanRefusesWithoutALibrary proves a lesson with files in a season folder
// is refused, not guessed at, when no library folder is configured; a lesson
// in its own folder needs none.
func TestPlanRefusesWithoutALibrary(t *testing.T) {
	season := "/lib/Show/Season 01"
	for _, self := range []database.Lesson{
		recordedRow(1, season, "Show - s01e05 - Five.mp4"),
		legacyRow(1, "Five", 5, season, "Show - s01e05 - Five.mp4"),
	} {
		if got, err := plan(t, "", self, []database.Lesson{self}); err == nil {
			t.Errorf("planned %+v with no library, want a refusal", got)
		}
	}
	own := database.Lesson{RailcontentID: 1, OutputDir: sql.NullString{String: "/dl/F/05 - A", Valid: true}, LibraryEntries: sql.NullString{String: "[]", Valid: true}}
	if got, err := plan(t, "", own, nil); err != nil || len(got.Remove)+len(got.Kept) != 0 {
		t.Errorf("own folder, empty record, no library: plan = (%+v, %v), want nothing", got, err)
	}
}

// TestRecordRefusesADamagedRecord proves a record that is not a list of plain
// "<show>/Season NN/<name>" entries inside the library is refused, never acted
// on, and that another lesson's damaged record refuses too: its claims are
// unknown.
func TestRecordRefusesADamagedRecord(t *testing.T) {
	season := "/lib/Show/Season 01"
	for _, bad := range []string{
		`["/lib/Show/Season 01/x.mp4"]`,
		`["/etc/passwd"]`,
		`["Show/Season 01/../../etc"]`,
		`["../Show/Season 01/x.mp4"]`,
		`["../Season 01/x.mp4"]`,
		`["Show/Season 01"]`,
		`["Show/Season 01/"]`,
		`["Show/Season 01/a/b"]`,
		`["Show/05 - A/x.mp4"]`,
		`["./Show/Season 01/x.mp4"]`,
		`["Show//Season 01/x.mp4"]`,
		`[""]`,
		`null`,
		`not json`,
	} {
		l := database.Lesson{RailcontentID: 1, OutputDir: sql.NullString{String: season, Valid: true}, LibraryEntries: sql.NullString{String: bad, Valid: true}}
		if got, err := plan(t, "/lib", l, nil); err == nil {
			t.Errorf("record %s planned %+v, want a refusal", bad, got)
		}
	}
	good := recordedRow(1, season, "Show - s01e05 - A.mp4")
	other := database.Lesson{RailcontentID: 2, LibraryEntries: sql.NullString{String: "{", Valid: true}}
	if _, err := NewClaims("/lib", []database.Lesson{good, other}); err == nil {
		t.Error("indexed another lesson's damaged record, want a refusal")
	}
}

// TestEntryForAndResolveRoundTrip pins the record's form: relative to the
// library, forward slashes, and refused outside a season folder.
func TestEntryForAndResolveRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lib")
	p := filepath.Join(root, "Show", "Season 01", "Show - s01e05 - A [B].mp4")
	e, err := EntryFor(root, p)
	if err != nil || e != "Show/Season 01/Show - s01e05 - A [B].mp4" {
		t.Fatalf("EntryFor = (%q, %v)", e, err)
	}
	if got := Resolve(root, e); got != p {
		t.Errorf("Resolve = %q, want %q", got, p)
	}
	for _, bad := range []string{root, filepath.Join(root, "Show"), filepath.Join(root, "Show", "Season 01"), filepath.Join(root, "Show", "05 - A", "x.mp4"), filepath.Join(root, "..", "x", "Season 01", "y")} {
		if e, err := EntryFor(root, bad); err == nil {
			t.Errorf("EntryFor(%q) = %q, want a refusal", bad, e)
		}
	}
	if got, err := EntriesFor(root, nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("EntriesFor(nil) = (%#v, %v), want an empty, non-nil record", got, err)
	}
}

// TestLegacyEpisodeBases pins how a lesson moved before the record existed
// finds its own episode name, including brackets anywhere (a title, the show,
// a soundslice version label) and a title changed on Musora since.
func TestLegacyEpisodeBases(t *testing.T) {
	season := "/lib/Show/Season 01"
	bracketShow := "/lib/Drum Fills [Beginner]/Season 01"
	cases := []struct {
		name      string
		row       database.Lesson
		seasonDir string
		listing   map[string]bool
		want      []string
		exact     bool
	}{
		{"lesson", legacyRow(1, "Five", 5, season, "Show - s01e05 - Five.mp4"), season, nil,
			[]string{"Show - s01e05 - Five"}, true},
		{"song version", legacyRow(1, "Even Flow", 5, season, "Show - s01e05 - Even Flow [Drumless].mp4"), season, nil,
			[]string{"Show - s01e05 - Even Flow"}, true},
		{"title ending in a tag", legacyRow(1, "Groove [Live]", 7, season, "Show - s01e07 - Groove [Live].mp4"), season, nil,
			[]string{"Show - s01e07 - Groove [Live]"}, true},
		{"label with an open bracket", legacyRow(1, "Song", 5, season, "Show - s01e05 - Song [Mix [Live].mp4"), season, nil,
			[]string{"Show - s01e05 - Song"}, true},
		{"label with nested brackets", legacyRow(1, "Song", 5, season, "Show - s01e05 - Song [Original (Live) [HD]].mp4"), season, nil,
			[]string{"Show - s01e05 - Song"}, true},
		{"show and title with brackets", legacyRow(1, "Fill[1]", 5, bracketShow, "Drum Fills [Beginner] - s01e05 - Fill[1].mp4"), bracketShow, nil,
			[]string{"Drum Fills [Beginner] - s01e05 - Fill[1]"}, true},
		{"renamed since, told apart by its nfo", legacyRow(1, "Even Flow (2024)", 5, season, "Show - s01e05 - Even Flow [Drumless].mp4"), season,
			map[string]bool{"Show - s01e05 - Even Flow.nfo": false},
			[]string{"Show - s01e05 - Even Flow"}, true},
		{"renamed since, no nfo", legacyRow(1, "Even Flow (2024)", 5, season, "Show - s01e05 - Even Flow [Drumless].mp4"), season, nil,
			[]string{"Show - s01e05 - Even Flow [Drumless]", "Show - s01e05 - Even Flow", "Show - s01e05 - Even Flow (2024)"}, false},
		{"renamed since, two nfos", legacyRow(1, "X", 5, season, "Show - s01e05 - A [B].mp4"), season,
			map[string]bool{"Show - s01e05 - A.nfo": false, "Show - s01e05 - A [B].nfo": false},
			[]string{"Show - s01e05 - A [B]", "Show - s01e05 - A", "Show - s01e05 - X"}, false},
		{"renamed since, one plain name", legacyRow(1, "New", 5, season, "Show - s01e05 - Old.mp4"), season, nil,
			[]string{"Show - s01e05 - Old"}, true},
		{"no video", legacyRow(1, "Resources Only", 3, season, ""), season, nil,
			[]string{"Show - s01e03 - Resources Only"}, true},
		{"no position", database.Lesson{RailcontentID: 1, Title: "T", OutputDir: sql.NullString{String: season, Valid: true}}, season, nil,
			[]string{"Show - s01e01 - T"}, true},
		{"video of another show", legacyRow(1, "Five", 5, season, "Other - s01e05 - Five.mp4"), season, nil,
			[]string{"Show - s01e05 - Five"}, false},
		{"video not an mp4", legacyRow(1, "Five", 5, season, "Show - s01e05 - Five.mkv"), season, nil,
			[]string{"Show - s01e05 - Five"}, false},
	}
	for _, c := range cases {
		got, exact := legacyEpisodeBases(c.row, c.seasonDir, c.listing)
		if !reflect.DeepEqual(got, c.want) || exact != c.exact {
			t.Errorf("%s: = (%q, %v), want (%q, %v)", c.name, got, exact, c.want, c.exact)
		}
	}
	// A video filed in another folder than the lesson's season folder.
	row := legacyRow(1, "Five", 5, season, "Show - s01e05 - Five.mp4")
	row.VideoPath.String = "/lib/Show/Season 02/Show - s01e05 - Five.mp4"
	if got, exact := legacyEpisodeBases(row, season, nil); exact || !reflect.DeepEqual(got, []string{"Show - s01e05 - Five"}) {
		t.Errorf("video elsewhere: = (%q, %v), want ([derived], false)", got, exact)
	}
}

// TestLegacyEpisodeEntry pins the legacy name grammar: exactly the shapes
// DownloadLesson produces, never another title that starts with this one.
func TestLegacyEpisodeEntry(t *testing.T) {
	base := "S - s01e05 - Five"
	listing := map[string]bool{base + " [Live].nfo": false}
	yes := []struct {
		name  string
		isDir bool
	}{
		{base + ".mp4", false}, {base + ".nfo", false}, {base + "-poster.jpg", false},
		{base + ".en.vtt", false}, {base + ".pt-BR.srt", false}, {base + ".live_chat.json3", false},
		{base + " [Drumless].mp4", false}, {base + " [Mix [Live].mp4", false},
		{base + " resources", true}, {base + " play-along", true}, {base + " sheet-music", true},
	}
	for _, c := range yes {
		if !legacyEpisodeEntry(base, c.name, c.isDir, listing) {
			t.Errorf("%q (dir=%v) not recognised, want it", c.name, c.isDir)
		}
	}
	no := []struct {
		name  string
		isDir bool
	}{
		{"S - s01e50 - Fifty.mp4", false}, {base + "-Part Fill.mp4", false}, {base + ".5.mp4", false},
		{base + ".5.en.vtt", false}, {base + ".en.mp4", false}, {base + " Bonus.mp4", false},
		{base + " [Live].mp4", false}, // its own nfo says it is another lesson
		{base + " [].mp4", false}, {base + " [Live].nfo", false}, {base + " [Live]-poster.jpg", false},
		{base + ". .vtt", false}, {base + "..vtt", false},
		{base + " Bonus resources", true}, {base + " sheet music", true}, {base + " resources", false},
		{base + ".mp4", true}, {base + " [Live] resources", true},
	}
	for _, c := range no {
		if legacyEpisodeEntry(base, c.name, c.isDir, listing) {
			t.Errorf("%q (dir=%v) recognised, want not", c.name, c.isDir)
		}
	}
}

// TestLegacyEpisodeEntryLeavesASongOfAnotherLesson covers a song whose title
// extends a legacy lesson's ("Five [Live]" beside "Five"): its version files
// "<Five> [Live] [Drumless].mp4" read as "Five" plus a label, but the "[Live]"
// lesson's own nfo says whose they are.
func TestLegacyEpisodeEntryLeavesASongOfAnotherLesson(t *testing.T) {
	base := "S - s01e05 - Five"
	listing := map[string]bool{base + " [Live].nfo": false}
	for _, name := range []string{base + " [Live] [Drumless].mp4", base + " [Live] [Original].mp4", base + " [Live].mp4"} {
		if legacyEpisodeEntry(base, name, false, listing) {
			t.Errorf("%q given to %q, want it left to the [Live] lesson", name, base)
		}
	}
	// With no such lesson, they are this song's versions.
	if !legacyEpisodeEntry(base, base+" [Live] [Drumless].mp4", false, nil) {
		t.Errorf("a version label holding a bracket not recognised with no other lesson")
	}
}

// TestPlanLegacyFallback covers a lesson moved before the record existed: a
// song still loses every version, its nfo, poster and folders (the D51 point),
// while every entry another lesson row claims (by record, or by its own legacy
// match) is kept and reported, in both directions.
func TestPlanLegacyFallback(t *testing.T) {
	season := filepath.Join(t.TempDir(), "lib", "Show", "Season 01")
	root := libraryOf(season)
	song := []string{
		"Show - s01e05 - Five [Drumless].mp4", "Show - s01e05 - Five [Original].mp4",
		"Show - s01e05 - Five.nfo", "Show - s01e05 - Five-poster.jpg",
		"Show - s01e05 - Five resources/", "Show - s01e05 - Five play-along/", "Show - s01e05 - Five sheet-music/",
	}
	// "Five [Live]" is a legacy lesson whose nfo write failed, so the song's
	// grammar alone would take its video as a "[Live]" version.
	live := []string{"Show - s01e05 - Five [Live].mp4", "Show - s01e05 - Five [Live]-poster.jpg"}
	part := fiveLookAlikes["Five-Part Fill"]
	point5 := fiveLookAlikes["Five.5"]
	seedSeason(t, season, song...)
	seedSeason(t, season, live...)
	seedSeason(t, season, part...)
	seedSeason(t, season, point5...)
	songRow := legacyRow(1, "Five", 5, season, "Show - s01e05 - Five [Drumless].mp4")
	liveRow := legacyRow(2, "Five [Live]", 5, season, "Show - s01e05 - Five [Live].mp4")
	partRow := recordedRow(3, season, part...)
	point5Row := legacyRow(4, "Five.5", 5, season, "Show - s01e05 - Five.5.mp4")
	rows := []database.Lesson{songRow, liveRow, partRow, point5Row}

	got, err := plan(t, root, songRow, rows)
	if err != nil {
		t.Fatalf("song: %v", err)
	}
	if want := sorted(paths(season, song...)); !reflect.DeepEqual(sorted(got.Remove), want) {
		t.Errorf("song removes %v, want %v", got.Remove, want)
	}
	if want := paths(season, "Show - s01e05 - Five [Live].mp4"); !reflect.DeepEqual(got.Kept, want) {
		t.Errorf("song keeps %v, want %v (another lesson claims it)", got.Kept, want)
	}

	// The other direction: by names alone "Five [Live].mp4" could be the song's
	// version too, so the live lesson keeps it as well. Ambiguous: nobody
	// removes it.
	got, err = plan(t, root, liveRow, rows)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	if !reflect.DeepEqual(got.Remove, paths(season, "Show - s01e05 - Five [Live]-poster.jpg")) ||
		!reflect.DeepEqual(got.Kept, paths(season, "Show - s01e05 - Five [Live].mp4")) {
		t.Errorf("live plan = %+v, want its poster removed and its ambiguous video kept", got)
	}

	got, err = plan(t, root, point5Row, rows)
	if err != nil {
		t.Fatalf("five.5: %v", err)
	}
	if want := sorted(paths(season, point5...)); !reflect.DeepEqual(sorted(got.Remove), want) || len(got.Kept) != 0 {
		t.Errorf("five.5 plan = %+v, want exactly %v", got, want)
	}

	// Without the other rows (they were deleted), the song's grammar alone
	// still never reaches the '-' and '.' look-alikes.
	got, err = plan(t, root, songRow, nil)
	if err != nil {
		t.Fatalf("song alone: %v", err)
	}
	for _, p := range got.Remove {
		if strings.Contains(p, "Part Fill") || strings.Contains(p, "Five.5") {
			t.Errorf("song alone removes %q", p)
		}
	}
}

// TestPlanLegacyRefusesToGuess proves a legacy lesson whose episode name can
// not be told apart is refused, not guessed.
func TestPlanLegacyRefusesToGuess(t *testing.T) {
	season := filepath.Join(t.TempDir(), "Show", "Season 01")
	seedSeason(t, season, "Show - s01e05 - A [B].mp4", "Show - s01e05 - A [B].nfo", "Show - s01e05 - A.nfo")
	row := legacyRow(1, "Renamed", 5, season, "Show - s01e05 - A [B].mp4")
	if got, err := plan(t, libraryOf(season), row, nil); err == nil {
		t.Errorf("planned %+v, want a refusal", got)
	}
	// A lesson whose own folder is not a season folder owns no season entries.
	own := database.Lesson{RailcontentID: 1, OutputDir: sql.NullString{String: "/dl/F/05 - A", Valid: true}}
	if got, err := plan(t, "/lib", own, nil); err != nil || len(got.Remove)+len(got.Kept) != 0 {
		t.Errorf("default-layout lesson plan = (%+v, %v), want nothing", got, err)
	}
}

// TestForgetDropsALessonsClaims proves a lesson whose files a delete removed
// no longer counts against the next lesson: a path two records named goes
// with the second.
func TestForgetDropsALessonsClaims(t *testing.T) {
	season := filepath.Join(t.TempDir(), "lib", "Show", "Season 01")
	seedSeason(t, season, "Show - s01e05 - Same.mp4")
	a := recordedRow(1, season, "Show - s01e05 - Same.mp4")
	b := recordedRow(2, season, "Show - s01e05 - Same.mp4")
	c, err := NewClaims(libraryOf(season), []database.Lesson{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Plan(b); len(got.Remove) != 0 {
		t.Fatalf("before Forget: %+v, want the shared path kept", got)
	}
	c.Forget(1)
	if got, _ := c.Plan(b); !reflect.DeepEqual(got.Remove, paths(season, "Show - s01e05 - Same.mp4")) {
		t.Errorf("after Forget(1): %+v, want the path removed", got)
	}
	if ids := c.Holds(season, 2); len(ids) != 0 {
		t.Errorf("Holds after Forget = %v, want none", ids)
	}
}

// TestHoldsAndIsLessonFolder pin the two checks a delete makes before it
// removes a lesson's own folder whole: the folder is named like one, and holds
// nothing another row records.
func TestHoldsAndIsLessonFolder(t *testing.T) {
	season := "/lib/Show/Season 01"
	rows := []database.Lesson{
		recordedRow(1, season, "Show - s01e05 - A.mp4"),
		{RailcontentID: 2, OutputDir: sql.NullString{String: "/dl/F/05 - B", Valid: true}, VideoPath: sql.NullString{String: "/dl/F/05 - B/05 - B.mp4", Valid: true}},
	}
	c, err := NewClaims("/lib", rows)
	if err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string][]int{
		"/lib/Show":        {1},
		"/lib":             {1},
		"/dl/F":            {2},
		"/dl/F/05 - B":     {2},
		"/dl/F/05 - B2":    nil,
		"/lib/Show/Seas":   nil,
		"/dl/F/05 - B/sub": nil,
	} {
		if got := c.Holds(dir, 0); len(got)+len(want) > 0 && !reflect.DeepEqual(got, want) {
			t.Errorf("Holds(%q) = %v, want %v", dir, got, want)
		}
	}
	if got := c.Holds("/dl/F/05 - B", 2); len(got) != 0 {
		t.Errorf("Holds counts the lesson itself: %v", got)
	}
	for dir, want := range map[string]bool{"/dl/F/05 - B": true, "/dl/F/123 - X": true, "/lib/Show": false, "/lib/Show/Season 01": false, "/dl/F/5 - B": false, "/dl/F/05 -": false, "/dl/F/05 - ": false} {
		if got := IsLessonFolder(dir); got != want {
			t.Errorf("IsLessonFolder(%q) = %v, want %v", dir, got, want)
		}
	}
}
