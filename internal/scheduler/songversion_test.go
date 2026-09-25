package scheduler

import (
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
	"github.com/elienop/drumdrop/internal/musora"
)

// Owner ruling #78 5: in plex-tv a song gets one image and one nfo per
// version video, "<base> [Label].jpg" and "<base> [Label].nfo", as Plex reads
// an episode's image and nfo only under a video's own name.

// songWorker is plexWorker with lesson 100 ("Lesson A", episode 5 of
// "Beginner Course") a song on Musora: a soundslice score and no HLS, so the
// fake downloader writes its [Original] and [Drumless] versions, one nfo and
// a resources folder, and the image when withImage is set.
func songWorker(t *testing.T, withImage bool) (w *Worker, store *fakeWorkerStore, lib, season string) {
	t.Helper()
	w, store, dl, lib, season := plexWorker(t)
	song := lesson(100, "Lesson A")
	song.Soundslice = []musora.SoundsliceRef{{Slug: "s"}}
	w.Resolver = fakeResolver{lessons: map[int]*musora.Lesson{100: song}}
	if withImage {
		dl.afterWrite = func(dir string) {
			base := filepath.Base(dir)
			if err := os.WriteFile(filepath.Join(dir, base+"-poster.jpg"), []byte("new image"), 0o644); err != nil {
				t.Error(err)
			}
		}
	}
	return w, store, lib, season
}

// versionNames is each of labels' file of the song at base with ext, in
// labels' order.
func versionNames(base, ext string, labels ...string) []string {
	var out []string
	for _, l := range labels {
		out = append(out, base+" ["+l+"]"+ext)
	}
	return out
}

// seedRecordedSong seeds names in season and records them as lesson 100's,
// its video the [Drumless] version.
func seedRecordedSong(t *testing.T, store *fakeWorkerStore, season string, names ...string) database.Lesson {
	t.Helper()
	seedSeason(t, season, names...)
	prev := recordedRow(100, season, names...)
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	prev.VideoPath = sql.NullString{String: filepath.Join(season, sameTitleBase+" [Drumless].mp4"), Valid: true}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}
	return prev
}

// assertRecordIs fails unless the only recorded download names exactly names
// in season.
func assertRecordIs(t *testing.T, store *fakeWorkerStore, season string, names ...string) {
	t.Helper()
	rec := onlyRecord(t, store)
	if got, want := sorted(rec.entries), sorted(recordOf(season, names...)); !reflect.DeepEqual(got, want) {
		t.Errorf("record = %v, want %v", got, want)
	}
}

// TestPlexTVSongReDownloadRetiresTheSharedFiles pins ruling 5c: a song
// placed before the ruling has one "<base>.nfo" and one image
// ("<base>-poster.jpg", or "<base>.jpg" from a build that had it), which Plex
// reads for no video. A re-download that places the files per version counts
// those as brought back: they go and leave the record, so the episode never
// has both shapes. One the re-download does not bring back in the new shape
// (an image it failed to fetch again) is not brought back: it stays, and
// stays recorded, so the episode never loses its only copy.
func TestPlexTVSongReDownloadRetiresTheSharedFiles(t *testing.T) {
	const base = sameTitleBase
	videos := versionNames(base, ".mp4", "Drumless", "Original")
	for _, oldImage := range []string{base + musora.PosterSuffix, base + ".jpg"} {
		for _, imageAgain := range []bool{true, false} {
			name := "image " + oldImage[len(base):] + " fetched again"
			if !imageAgain {
				name = "image " + oldImage[len(base):] + " not fetched again"
			}
			t.Run(name, func(t *testing.T) {
				w, store, _, season := songWorker(t, imageAgain)
				seedRecordedSong(t, store, season, append(videos, base+".nfo", oldImage)...)

				if _, err := w.RunOnce(context.Background(), 0); err != nil {
					t.Fatalf("RunOnce: %v", err)
				}
				nfos := versionNames(base, ".nfo", "Drumless", "Original")
				images := versionNames(base, ".jpg", "Drumless", "Original")
				want := append(append(append([]string(nil), videos...), nfos...), base+" resources")
				assertExist(t, false, filepath.Join(season, base+".nfo"))
				if imageAgain {
					want = append(want, images...)
					assertExist(t, false, filepath.Join(season, oldImage))
					for _, p := range paths(season, images...) {
						if got := readFile(p); got != "new image" {
							t.Errorf("%s = %q, want the new image", filepath.Base(p), got)
						}
					}
				} else {
					want = append(want, oldImage)
					assertContent(t, season, oldImage)
					assertExist(t, false, paths(season, images...)...)
				}
				for _, p := range paths(season, nfos...) {
					if got := readFile(p); !strings.Contains(got, "<episodedetails>") {
						t.Errorf("%s = %q, want the episode nfo", filepath.Base(p), got)
					}
				}
				assertRecordIs(t, store, season, want...)
			})
		}
	}
}

// TestPlexTVSongRefusedRecordPutsTheSharedFilesBack proves the retired
// shared files are only set aside: when the re-download is not recorded, the
// undo puts "<base>.nfo" and "<base>-poster.jpg" back and takes out every
// per-version copy it made, so the episode is exactly as it was.
func TestPlexTVSongRefusedRecordPutsTheSharedFilesBack(t *testing.T) {
	const base = sameTitleBase
	w, store, _, season := songWorker(t, true)
	mine := append(versionNames(base, ".mp4", "Drumless", "Original"), base+".nfo", base+musora.PosterSuffix)
	seedRecordedSong(t, store, season, mine...)
	store.finishErr = errors.New("database is locked")

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertContent(t, season, mine...)
	assertExist(t, false, paths(season, append(versionNames(base, ".jpg", "Drumless", "Original"), versionNames(base, ".nfo", "Drumless", "Original")...)...)...)
}

// TestPlexTVSongReDownloadKeepsItsVersionFiles pins ruling 5d for a song with
// a record: its own "<base> [Label].nfo" must not make it disown its
// versions, as the name grammar alone would ("<base> [X].nfo" there says
// "<base> [X]" is another lesson). The download in hand and, for a lesson
// Musora says is a song, the versions its record names decide instead:
//   - an image it did not fetch again stays, and stays recorded (ruling (j));
//   - a resources-only re-download, which brings no version, keeps every
//     version video and names the new nfo per recorded version;
//   - a version the re-download no longer brings (Musora dropped it) stays,
//     its video as it was, and stays recorded, as it did before the
//     per-version nfo; its image and nfo are the song's new ones, like every
//     version's (see TestPlexTVSongVersionNotBroughtBackKeepsItsFiles);
//   - for a lesson that is NOT a song, the files of an old title
//     "<title> [Live]" placed before each version had its own image (its
//     "-poster.jpg" proves it a title, not a version) still go (ruling #66),
//     as they did before the per-version files;
//   - but one "<title> [Live]" with its own ".jpg" and ".nfo" can not be told
//     from a song's single version whose song flag changed: it is kept, and
//     stays recorded (TestAVersionIsKeptWhateverMusorasSongFlagSays).
func TestPlexTVSongReDownloadKeepsItsVersionFiles(t *testing.T) {
	const base = sameTitleBase
	videos := versionNames(base, ".mp4", "Drumless", "Original")
	nfos := versionNames(base, ".nfo", "Drumless", "Original")
	images := versionNames(base, ".jpg", "Drumless", "Original")
	perVersion := append(append(append([]string(nil), videos...), nfos...), images...)

	t.Run("image not fetched again", func(t *testing.T) {
		w, store, _, season := songWorker(t, false)
		seedRecordedSong(t, store, season, perVersion...)
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		assertContent(t, season, images...)
		assertRecordIs(t, store, season, append(append([]string(nil), perVersion...), base+" resources")...)
	})

	t.Run("resources only", func(t *testing.T) {
		w, store, _, season := songWorker(t, false)
		w.Cfg.ResourcesOnly = true
		w.Downloader = resourcesRedownload{}
		seedRecordedSong(t, store, season, perVersion...)
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		assertContent(t, season, append(append([]string(nil), videos...), images...)...)
		for _, p := range paths(season, nfos...) {
			if got := readFile(p); !strings.Contains(got, "<episodedetails>") {
				t.Errorf("%s = %q, want the new episode nfo", filepath.Base(p), got)
			}
		}
		assertExist(t, false, filepath.Join(season, base+".nfo"), filepath.Join(season, base+".jpg"))
		assertRecordIs(t, store, season, append(append([]string(nil), perVersion...), base+" resources")...)
	})

	t.Run("a version Musora no longer has", func(t *testing.T) {
		w, store, _, season := songWorker(t, true)
		gone := versionNames(base, "", "Live")[0]
		goneFiles := []string{gone + ".mp4", gone + ".nfo", gone + ".jpg"}
		seedRecordedSong(t, store, season, append(append([]string(nil), perVersion...), goneFiles...)...)
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		assertContent(t, season, goneFiles[0])
		if got := readFile(filepath.Join(season, goneFiles[2])); got != "new image" {
			t.Errorf("%s = %q, want the song's new image", goneFiles[2], got)
		}
		if got := readFile(filepath.Join(season, goneFiles[1])); !strings.Contains(got, "<episodedetails>") {
			t.Errorf("%s = %q, want the song's new episode nfo", goneFiles[1], got)
		}
		assertRecordIs(t, store, season, append(append(append([]string(nil), perVersion...), goneFiles...), base+" resources")...)
	})

	t.Run("not a song: an old title's files go", func(t *testing.T) {
		w, store, dl, _, season := plexWorker(t)
		dl.afterWrite = func(dir string) { writeExtrasExcept(t, dir, "resources") }
		oldTitle := []string{base + " [Live].mp4", base + " [Live].nfo", base + " [Live]" + musora.PosterSuffix}
		prev := seedRecordedSong(t, store, season, oldTitle...)
		prev.VideoPath = sql.NullString{String: filepath.Join(season, oldTitle[0]), Valid: true}
		store.lessons[100], store.withFiles = prev, []database.Lesson{prev}
		if _, err := w.RunOnce(context.Background(), 0); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		assertExist(t, false, paths(season, oldTitle...)...)
		assertRecordIs(t, store, season, base+".mp4", base+".nfo", base+".jpg", base+".en.vtt")
	})
}

// TestPlexTVSongImageAndNfoArePlacedBeforeEitherVersion is invariant 1 for a
// song placed with its files per version: when either version video lands,
// every version's image and nfo are already in the season folder, and they
// hold the download's image and the episode nfo.
func TestPlexTVSongImageAndNfoArePlacedBeforeEitherVersion(t *testing.T) {
	const base = sameTitleBase
	w, store, _, season := songWorker(t, true)
	before := append(versionNames(base, ".jpg", "Drumless", "Original"), versionNames(base, ".nfo", "Drumless", "Original")...)
	var videos int
	stubRename(t, func(oldpath, newpath string) error {
		if strings.HasSuffix(newpath, ".mp4") {
			videos++
			assertExist(t, true, paths(season, before...)...)
		}
		return renameNoReplace(oldpath, newpath)
	})
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if videos != 2 {
		t.Fatalf("%d videos placed, want 2", videos)
	}
	rec := onlyRecord(t, store)
	for _, n := range before {
		if !slices.Contains(rec.entries, "Beginner Course/Season 01/"+n) {
			t.Errorf("record %v does not name %s", rec.entries, n)
		}
	}
}

// TestAVersionIsKeptWhateverMusorasSongFlagSays pins that a re-download never
// deletes a recorded song version's video because Musora's song flag changed
// (an HLS video added, the soundslice slug gone), as the per-version nfo
// would otherwise have made the name grammar read the versions as another
// title's files: two or more version videos are versions whatever the flag
// says (one earlier title is one video, never two), and a single one in the
// per-version shape can not be told from an earlier title, so it is kept
// too, never deleted on a guess (owner ruling #72 (j)). Each version keeps
// its video, image and nfo, and stays recorded beside the new download.
func TestAVersionIsKeptWhateverMusorasSongFlagSays(t *testing.T) {
	const base = sameTitleBase
	for _, labels := range [][]string{{"Drumless", "Original"}, {"Drumless"}} {
		t.Run(strings.Join(labels, "+"), func(t *testing.T) {
			w, store, dl, _, season := plexWorker(t) // lesson 100 is not a song now
			withPoster(dl)
			var versions []string
			for _, ext := range []string{".mp4", ".nfo", ".jpg"} {
				versions = append(versions, versionNames(base, ext, labels...)...)
			}
			seedRecordedSong(t, store, season, versions...)
			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			assertContent(t, season, versions...)
			assertExist(t, true, paths(season, base+".mp4", base+".nfo", base+".jpg")...)
			assertRecordIs(t, store, season, append(append([]string(nil), versions...), base+".mp4", base+".nfo", base+".jpg", base+".en.vtt")...)
		})
	}
}

// TestPlexTVSongVersionNotBroughtBackKeepsItsFiles pins that a song
// re-download that does not bring back a version its record names (Musora
// renamed a recording) still gives that version its own image and nfo
// before the one shared "<base>.nfo" and image are retired: the version
// stays, with its video, image and nfo, never without them.
func TestPlexTVSongVersionNotBroughtBackKeepsItsFiles(t *testing.T) {
	const base = sameTitleBase
	w, store, dl, _, season := plexWorker(t)
	song := lesson(100, "Lesson A")
	song.Soundslice = []musora.SoundsliceRef{{Slug: "s"}}
	w.Resolver = fakeResolver{lessons: map[int]*musora.Lesson{100: song}}
	dl.afterWrite = func(dir string) {
		b := filepath.Base(dir)
		if err := os.Remove(filepath.Join(dir, b+" [Original].mp4")); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(filepath.Join(dir, b+"-poster.jpg"), []byte("new image"), 0o644); err != nil {
			t.Error(err)
		}
	}
	videos := versionNames(base, ".mp4", "Drumless", "Original")
	seedRecordedSong(t, store, season, append(append([]string(nil), videos...), base+".nfo", base+musora.PosterSuffix)...)
	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	nfos := versionNames(base, ".nfo", "Drumless", "Original")
	images := versionNames(base, ".jpg", "Drumless", "Original")
	assertContent(t, season, videos[1]) // [Original], not brought back
	for _, p := range paths(season, images...) {
		if got := readFile(p); got != "new image" {
			t.Errorf("%s = %q, want the song's image", filepath.Base(p), got)
		}
	}
	for _, p := range paths(season, nfos...) {
		if got := readFile(p); !strings.Contains(got, "<episodedetails>") {
			t.Errorf("%s = %q, want the episode nfo", filepath.Base(p), got)
		}
	}
	assertExist(t, false, filepath.Join(season, base+".nfo"), filepath.Join(season, base+musora.PosterSuffix))
	assertRecordIs(t, store, season, append(append(append(append([]string(nil), videos...), nfos...), images...), base+" resources")...)
}
