package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// TestWorkerPlexTvSameTitleReDownloadKeepsWhatItDidNotBringBack pins the
// owner's ruling 2026-09-24 (j) (round-5b security L4, code L6): in plex-tv,
// a re-download under the same title keeps the lesson's recorded entries at
// its episode base that it did not bring back (captions, a poster, a
// resources folder, each of which a re-download can fail to fetch again),
// untouched, and they stay in the lesson's record, so a later Delete with
// files removes them. What it did bring back is replaced, a folder merged.
// A recorded file at an old title's name still goes (ruling #66), even one
// whose name starts with this episode's base.
//
// The episode's image is placed as "<base>.jpg" (owner ruling #78). A
// recorded image under the name earlier versions gave it, "<base>-poster.jpg",
// counts as brought back when the re-download places "<base>.jpg": it goes,
// and the record names the new one, so the episode never has two images, and
// never none (it stays when the re-download brings no image).
func TestWorkerPlexTvSameTitleReDownloadKeepsWhatItDidNotBringBack(t *testing.T) {
	for _, recorded := range []string{sameTitleBase + "-poster.jpg", sameTitleBase + ".jpg"} {
		for _, missing := range []string{"vtt", "poster", "resources"} {
			t.Run("missing "+missing+"/recorded "+recorded, func(t *testing.T) {
				checkSameTitleReDownloadKeepsWhatItDidNotBringBack(t, missing, recorded)
			})
		}
	}
}

// sameTitleBase is lesson 100's episode base in the plex-tv tests below.
const sameTitleBase = "Beginner Course - s01e05 - Lesson A"

// Each same-title re-download writes the video and nfo, plus the scratch
// entries named in sameTitleExtras ("05 - Lesson A" is the scratch base),
// which the move places at the episode names in sameTitleEpisode.
var (
	sameTitleExtras = map[string]string{
		"vtt":       "05 - Lesson A.en.vtt",
		"poster":    "05 - Lesson A-poster.jpg",
		"resources": "resources/new.pdf",
	}
	sameTitleEpisode = map[string]string{
		"vtt":       sameTitleBase + ".en.vtt",
		"poster":    sameTitleBase + ".jpg",
		"resources": sameTitleBase + " resources/",
	}
)

// checkSameTitleReDownloadKeepsWhatItDidNotBringBack is
// TestWorkerPlexTvSameTitleReDownloadKeepsWhatItDidNotBringBack for a
// re-download that does not bring back the extra missing, of a lesson whose
// record names its image as recordedImage.
func checkSameTitleReDownloadKeepsWhatItDidNotBringBack(t *testing.T, missing, recordedImage string) {
	t.Helper()
	const base = sameTitleBase
	w, store, dl, lib, season := plexWorker(t)
	var log bytes.Buffer
	w.Log = &log
	dl.afterWrite = func(dir string) { writeExtrasExcept(t, dir, missing) }
	oldTitle := base + " (old).mp4" // "Lesson A (old)", a title the lesson had
	mine := []string{base + ".mp4", base + ".nfo", sameTitleEpisode["vtt"], recordedImage, sameTitleEpisode["resources"], oldTitle}
	seedSeason(t, season, mine...)
	prev := recordedRow(100, season, mine...)
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	prev.VideoPath = sql.NullString{String: filepath.Join(season, base+".mp4"), Valid: true}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	rec := onlyRecord(t, store)
	// The one it missed is kept as it was; the others are the new
	// download's, the resources folder merged with the earlier one.
	kept, image := sameTitleEpisode[missing], sameTitleEpisode["poster"]
	if missing == "poster" {
		kept, image = recordedImage, recordedImage
	}
	assertSeeded(t, season, kept)
	assertExtrasReplacedExcept(t, season, missing)
	assertExist(t, false, filepath.Join(season, oldTitle))
	if image != recordedImage {
		// The image the record named under its old name was replaced.
		assertExist(t, false, filepath.Join(season, recordedImage))
	}
	want := recordOf(season, base+".mp4", base+".nfo", sameTitleEpisode["vtt"], image, sameTitleEpisode["resources"])
	if got := slices.Sorted(slices.Values(rec.entries)); !reflect.DeepEqual(got, slices.Sorted(slices.Values(want))) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if strings.Contains(log.String(), "left its previous folder") {
		t.Errorf("log %q says a folder was left behind", log.String())
	}
	assertADeleteRemovesEveryEntry(t, w, lib, season, rec.entries, append(mine, image))
}

// writeExtrasExcept writes, into the downloaded lesson folder dir, every
// sameTitleExtras entry but missing, each holding "new <kind>".
func writeExtrasExcept(t *testing.T, dir, missing string) {
	t.Helper()
	for k, rel := range sameTitleExtras {
		if k == missing {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("new "+k), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// assertExtrasReplacedExcept fails unless every extra but missing is the new
// download's in season: a file replaced, the resources folder merged with
// the earlier one.
func assertExtrasReplacedExcept(t *testing.T, season, missing string) {
	t.Helper()
	for k := range sameTitleExtras {
		if k == missing {
			continue
		}
		if k == "resources" {
			assertTree(t, filepath.Join(season, sameTitleBase+" resources"), map[string]string{"f.pdf": sameTitleBase + " resources", "new.pdf": "new resources"})
			continue
		}
		if got, err := os.ReadFile(filepath.Join(season, sameTitleEpisode[k])); err != nil || string(got) != "new "+k {
			t.Errorf("%s = %q, %v; want the new download's", sameTitleEpisode[k], got, err)
		}
	}
}

// assertADeleteRemovesEveryEntry fails unless a Delete with files, planned
// from the lesson's record entries, removes every entry of mine in season:
// the kept one included.
func assertADeleteRemovesEveryEntry(t *testing.T, w *Worker, lib, season string, entries, mine []string) {
	t.Helper()
	row := recordedRow(100, season)
	row.LibraryEntries = database.EncodeLibraryEntries(entries)
	c, err := library.NewClaims(lib, []database.Lesson{row})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.Plan(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan.Remove {
		if err := library.Remove(library.Roots(w.Cfg.DownloadsDir, lib), p); err != nil {
			t.Errorf("remove %q: %v", p, err)
		}
	}
	assertExist(t, false, paths(season, mine...)...)
}

// TestWorkerPlexTvSameTitleReDownloadOfALegacyRow (round-5c security I2)
// pins ruling (j) for a legacy row (no record: it owns its season-folder
// entries by the name grammar alone). A same-title re-download keeps what the
// grammar gives the lesson and it did not bring back, and records it: here
// the captions, and an unclaimed "<base> [Live].mp4", which the grammar reads
// as a version of this episode (the name can't say whether it is a song
// version or a leftover of a lesson whose row is gone). An entry another
// lesson claims, by record or by its own legacy match, is never the lesson's:
// it stays untouched and out of its record.
func TestWorkerPlexTvSameTitleReDownloadOfALegacyRow(t *testing.T) {
	const base = "Beginner Course - s01e05 - Lesson A"
	live := base + " [Live].mp4"
	for _, who := range []string{"no one", "lesson 200's record", "lesson 200's legacy match"} {
		t.Run("[Live] claimed by "+who, func(t *testing.T) {
			w, store, _, _, season := plexWorker(t)
			seedSeason(t, season, base+".mp4", base+".nfo", base+".en.vtt", live)
			prev := legacyRow(100, "Lesson A", 5, season, base+".mp4")
			store.lessons[100] = prev
			store.withFiles = []database.Lesson{prev}
			switch who {
			case "lesson 200's record":
				store.withFiles = append(store.withFiles, recordedRow(200, season, live))
			case "lesson 200's legacy match":
				store.withFiles = append(store.withFiles, legacyRow(200, "Lesson A [Live]", 5, season, live))
			}

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			rec := onlyRecord(t, store)
			assertContent(t, season, base+".en.vtt", live)
			want := recordOf(season, base+".mp4", base+".nfo", base+".en.vtt")
			if who == "no one" {
				want = append(want, recordOf(season, live)...)
			}
			if got := slices.Sorted(slices.Values(rec.entries)); !reflect.DeepEqual(got, slices.Sorted(slices.Values(want))) {
				t.Errorf("entries = %v, want %v", got, want)
			}
		})
	}
}

// TestWorkerPlexTvRefusedRecordKeepsTheOldImageName proves the image a
// re-download retires under its old name ("<base>-poster.jpg", replaced by
// "<base>.jpg") is only set aside: when the download is not recorded, the
// undo puts it back where it was and takes the new one out, so the episode
// is exactly as it was.
func TestWorkerPlexTvRefusedRecordKeepsTheOldImageName(t *testing.T) {
	const base = sameTitleBase
	w, store, dl, _, season := plexWorker(t)
	dl.afterWrite = func(dir string) { writeExtrasExcept(t, dir, "") }
	mine := []string{base + ".mp4", base + ".nfo", base + "-poster.jpg"}
	seedSeason(t, season, mine...)
	prev := recordedRow(100, season, mine...)
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}
	store.finishErr = errors.New("database is locked")

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertContent(t, season, mine...)
	assertExist(t, false, filepath.Join(season, base+".jpg"))
}
