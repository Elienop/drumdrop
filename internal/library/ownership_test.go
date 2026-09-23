package library

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// seedSeason creates dir and, inside it, every name: a folder (holding one
// file) when the name ends in "/", else a file whose content is its name.
func seedSeason(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, name := range names {
		if folder, ok := strings.CutSuffix(name, "/"); ok {
			if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", folder, err)
			}
			if err := os.WriteFile(filepath.Join(dir, folder, "f.pdf"), []byte(folder), 0o644); err != nil {
				t.Fatalf("write in %s: %v", folder, err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// paths joins every name (a trailing "/" dropped) onto dir.
func paths(dir string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(dir, strings.TrimSuffix(n, "/")))
	}
	return out
}

// recordedRow is a lesson filed in seasonDir that records exactly names.
func recordedRow(id int, seasonDir string, names ...string) database.Lesson {
	return database.Lesson{
		RailcontentID:  id,
		Status:         database.StatusDownloaded,
		OutputDir:      sql.NullString{String: seasonDir, Valid: true},
		LibraryEntries: database.EncodeLibraryEntries(paths(seasonDir, names...)),
	}
}

// legacyRow is a lesson moved into seasonDir before the record existed: no
// library_entries, only its title, position and video.
func legacyRow(id int, title string, position int, seasonDir, video string) database.Lesson {
	l := database.Lesson{
		RailcontentID: id,
		Title:         title,
		Status:        database.StatusDownloaded,
		Position:      sql.NullInt64{Int64: int64(position), Valid: true},
		OutputDir:     sql.NullString{String: seasonDir, Valid: true},
	}
	if video != "" {
		l.VideoPath = sql.NullString{String: filepath.Join(seasonDir, video), Valid: true}
	}
	return l
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func assertExist(t *testing.T, want bool, ps ...string) {
	t.Helper()
	for _, p := range ps {
		_, err := os.Lstat(p)
		if got := err == nil; got != want {
			t.Errorf("%s exists = %v, want %v (err=%v)", p, got, want, err)
		}
	}
}

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

// TestPlanLessonEntriesByRecordIsExact covers the owner's ruling for recorded
// lessons: each of four look-alikes at one episode number owns exactly its
// recorded entries, in every direction, and no other lesson's.
func TestPlanLessonEntriesByRecordIsExact(t *testing.T) {
	season := filepath.Join(t.TempDir(), "lib", "Show", "Season 01")
	var rows []database.Lesson
	for i, title := range fiveTitles {
		seedSeason(t, season, fiveLookAlikes[title]...)
		rows = append(rows, recordedRow(i+1, season, fiveLookAlikes[title]...))
	}
	for i, title := range fiveTitles {
		got, err := PlanLessonEntries(rows[i], rows)
		if err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		if want := sorted(paths(season, fiveLookAlikes[title]...)); !reflect.DeepEqual(sorted(got.Remove), want) || len(got.Kept) != 0 {
			t.Errorf("%s owns %v (kept %v), want exactly %v", title, got.Remove, got.Kept, want)
		}
	}
}

// TestPlanLessonEntriesKeepsAPathTwoRecordsName proves an entry two records
// name (identical titles at one episode) is ambiguous: neither lesson may
// remove it.
func TestPlanLessonEntriesKeepsAPathTwoRecordsName(t *testing.T) {
	season := filepath.Join(t.TempDir(), "Show", "Season 01")
	a := recordedRow(1, season, "Show - s01e05 - Same.mp4", "Show - s01e05 - A-only.nfo")
	b := recordedRow(2, season, "Show - s01e05 - Same.mp4")
	got, err := PlanLessonEntries(a, []database.Lesson{a, b})
	if err != nil {
		t.Fatalf("PlanLessonEntries: %v", err)
	}
	if !reflect.DeepEqual(got.Remove, paths(season, "Show - s01e05 - A-only.nfo")) || !reflect.DeepEqual(got.Kept, paths(season, "Show - s01e05 - Same.mp4")) {
		t.Errorf("plan = %+v, want the shared mp4 kept and only A's nfo removed", got)
	}
}

// TestPlanLessonEntriesRefusesADamagedRecord proves a record that is not a
// list of plain season-folder entries is refused, never acted on.
func TestPlanLessonEntriesRefusesADamagedRecord(t *testing.T) {
	season := "/lib/Show/Season 01"
	for _, bad := range []string{
		`["/etc/passwd"]`,
		`["relative/Season 01/x.mp4"]`,
		`["/lib/Show/Season 01/../../etc"]`,
		`["/lib/Show/Season 01"]`,
		`["/lib/Show/Season 01/"]`,
		`not json`,
	} {
		l := database.Lesson{RailcontentID: 1, OutputDir: sql.NullString{String: season, Valid: true}, LibraryEntries: sql.NullString{String: bad, Valid: true}}
		if got, err := PlanLessonEntries(l, nil); err == nil {
			t.Errorf("record %s planned %+v, want a refusal", bad, got)
		}
	}
	// Another lesson's damaged record refuses too: its claims are unknown.
	good := recordedRow(1, season, "Show - s01e05 - A.mp4")
	other := database.Lesson{RailcontentID: 2, LibraryEntries: sql.NullString{String: "{", Valid: true}}
	if _, err := PlanLessonEntries(good, []database.Lesson{other}); err == nil {
		t.Error("planned with another lesson's damaged record, want a refusal")
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

// TestPlanLessonEntriesLegacyFallback covers a lesson moved before the record
// existed: a song still loses every version, its nfo, poster and folders (the
// D51 point), while every entry another lesson row claims (by record, or by its
// own legacy match) is kept and reported, in both directions.
func TestPlanLessonEntriesLegacyFallback(t *testing.T) {
	season := filepath.Join(t.TempDir(), "lib", "Show", "Season 01")
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

	got, err := PlanLessonEntries(songRow, rows)
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
	got, err = PlanLessonEntries(liveRow, rows)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	if !reflect.DeepEqual(got.Remove, paths(season, "Show - s01e05 - Five [Live]-poster.jpg")) ||
		!reflect.DeepEqual(got.Kept, paths(season, "Show - s01e05 - Five [Live].mp4")) {
		t.Errorf("live plan = %+v, want its poster removed and its ambiguous video kept", got)
	}

	got, err = PlanLessonEntries(point5Row, rows)
	if err != nil {
		t.Fatalf("five.5: %v", err)
	}
	if want := sorted(paths(season, point5...)); !reflect.DeepEqual(sorted(got.Remove), want) || len(got.Kept) != 0 {
		t.Errorf("five.5 plan = %+v, want exactly %v", got, want)
	}

	// Without the other rows (they were deleted), the song's grammar alone
	// still never reaches the '-' and '.' look-alikes.
	got, err = PlanLessonEntries(songRow, nil)
	if err != nil {
		t.Fatalf("song alone: %v", err)
	}
	for _, p := range got.Remove {
		if strings.Contains(p, "Part Fill") || strings.Contains(p, "Five.5") {
			t.Errorf("song alone removes %q", p)
		}
	}
}

// TestPlanLessonEntriesLegacyRefusesToGuess proves a legacy lesson whose
// episode name can not be told apart is refused, not guessed.
func TestPlanLessonEntriesLegacyRefusesToGuess(t *testing.T) {
	season := filepath.Join(t.TempDir(), "Show", "Season 01")
	seedSeason(t, season, "Show - s01e05 - A [B].mp4", "Show - s01e05 - A [B].nfo", "Show - s01e05 - A.nfo")
	row := legacyRow(1, "Renamed", 5, season, "Show - s01e05 - A [B].mp4")
	if got, err := PlanLessonEntries(row, nil); err == nil {
		t.Errorf("planned %+v, want a refusal", got)
	}
	// A lesson whose own folder is not a season folder owns no season entries.
	own := database.Lesson{RailcontentID: 1, OutputDir: sql.NullString{String: "/dl/F/05 - A", Valid: true}}
	if got, err := PlanLessonEntries(own, nil); err != nil || len(got.Remove)+len(got.Kept) != 0 {
		t.Errorf("default-layout lesson plan = (%+v, %v), want nothing", got, err)
	}
}

// TestRemoveUnderRootConfinesTheRemoval proves the removal can not leave the
// root it was given: through a symlinked folder, outside every root, or at a
// root itself it refuses; a symlink that is itself the entry goes, its target
// stays; a missing path is already gone.
func TestRemoveUnderRootConfinesTheRemoval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	outside := filepath.Join(tmp, "outside")
	seedSeason(t, outside, "precious.nfo", "target/")
	if err := os.MkdirAll(filepath.Join(lib, "Show"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(lib, "Show", "Season 01")); err != nil {
		t.Fatal(err)
	}
	roots := []string{lib}

	if err := RemoveUnderRoot(roots, filepath.Join(lib, "Show", "Season 01", "precious.nfo")); err == nil {
		t.Error("removed through a symlinked season folder, want a refusal")
	}
	assertExist(t, true, filepath.Join(outside, "precious.nfo"))
	for _, p := range []string{outside, lib, filepath.Join(lib, ".."), ""} {
		if err := RemoveUnderRoot(roots, p); err == nil {
			t.Errorf("RemoveUnderRoot(%q) = nil, want a refusal", p)
		}
	}
	// A root itself is refused by name. (os.Root would refuse "." too, but only
	// as a bare "invalid argument" that names nothing.)
	if err := RemoveUnderRoot(roots, lib); err == nil || !strings.Contains(err.Error(), "is not safely inside any of") {
		t.Errorf("RemoveUnderRoot(root) = %v, want the named refusal", err)
	}
	if err := RemoveUnderRoot(nil, filepath.Join(lib, "Show")); err == nil {
		t.Error("removed with no root, want a refusal")
	}
	if err := RemoveUnderRoot(roots, filepath.Join(lib, "Show", "missing.mp4")); err != nil {
		t.Errorf("missing path = %v, want nil", err)
	}
	// The symlink entry itself is removed; what it points at stays.
	if err := RemoveUnderRoot(roots, filepath.Join(lib, "Show", "Season 01")); err != nil {
		t.Fatalf("remove the link: %v", err)
	}
	assertExist(t, false, filepath.Join(lib, "Show", "Season 01"))
	assertExist(t, true, filepath.Join(outside, "precious.nfo"), filepath.Join(outside, "target", "f.pdf"))
	// The second root is used when the first does not hold the path.
	dl := filepath.Join(tmp, "dl")
	seedSeason(t, dl, "05 - Lesson/")
	if err := RemoveUnderRoot([]string{lib, dl}, filepath.Join(dl, "05 - Lesson")); err != nil {
		t.Fatalf("remove under the second root: %v", err)
	}
	assertExist(t, false, filepath.Join(dl, "05 - Lesson"))
}
