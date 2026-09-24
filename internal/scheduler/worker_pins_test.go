package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// TestWorkerPlexTvDropsARecordedEntryThatIsNotOnDisk (round-5c code L2)
// proves a recorded entry at the episode's base that is gone from the
// season folder, and that the re-download does not bring back, leaves the
// record: only an entry really there is kept as the lesson's (owner ruling
// 2026-09-24 (j)), so the record never names a file that isn't.
func TestWorkerPlexTvDropsARecordedEntryThatIsNotOnDisk(t *testing.T) {
	w, store, _, _, season := plexWorker(t)
	base := "Beginner Course - s01e05 - Lesson A"
	seedSeason(t, season, base+".mp4", base+".nfo")
	prev := recordedRow(100, season, base+".mp4", base+".nfo", base+".en.vtt")
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rec := onlyRecord(t, store); !reflect.DeepEqual(sorted(rec.entries), sorted(recordOf(season, base+".mp4", base+".nfo"))) {
		t.Errorf("entries = %v, want the placed video and nfo only", rec.entries)
	}
}

// TestWorkerPlexTvKeepsEverythingWhenTheSeasonFolderCanNotBeListed (round-5c
// code I3) proves the move that can not list the season folder places
// nothing and touches nothing there: without the listing it can not tell
// which recorded entries are this episode's to keep (owner ruling 2026-09-24
// (j)), so it would remove them. The lesson falls back to the downloads
// folder, its season-folder entries untouched and still recorded.
func TestWorkerPlexTvKeepsEverythingWhenTheSeasonFolderCanNotBeListed(t *testing.T) {
	w, store, _, _, season := plexWorker(t)
	var log bytes.Buffer
	w.Log = &log
	base := "Beginner Course - s01e05 - Lesson A"
	mine := []string{base + ".mp4", base + ".nfo", base + ".en.vtt"}
	seedSeason(t, season, mine...)
	prev := recordedRow(100, season, mine...)
	prev.Position = sql.NullInt64{Int64: 5, Valid: true}
	store.lessons[100] = prev
	store.withFiles = []database.Lesson{prev}
	orig := listSeason
	listSeason = func(*os.Root) (map[string]bool, error) { return nil, errors.New("input/output error") }
	t.Cleanup(func() { listSeason = orig })

	if _, err := w.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertContent(t, season, mine...)
	rec := onlyRecord(t, store)
	if want := filepath.Join(w.Cfg.DownloadsDir, "Beginner Course", "05 - Lesson A"); rec.outputDir != want {
		t.Errorf("recorded %q, want the downloads folder %q", rec.outputDir, want)
	}
	if !reflect.DeepEqual(sorted(rec.entries), sorted(recordOf(season, mine...))) {
		t.Errorf("entries = %v, want the season-folder entries still recorded", rec.entries)
	}
	if !strings.Contains(log.String(), fmt.Sprintf("read the season folder %q: input/output error", season)) {
		t.Errorf("log %q does not say the season folder could not be read", log.String())
	}
}

// resourcesRedownload is a resources-only re-download: the nfo and one
// resource, no video.
type resourcesRedownload struct{}

func (resourcesRedownload) Download(ctx context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	base := fmt.Sprintf("%02d - %s", o.Index, musora.Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, base+".nfo"), []byte("new nfo"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "resources", "r.pdf"), []byte("new r"), 0o644)
}

// TestWorkerRecordsTheVideoAResourcesOnlyReDownloadKeeps (round-5c code I4)
// proves a downloaded row's video_path and bytes describe the video it
// records: a resources-only re-download keeps the earlier video (merged in
// the lesson's folder, or kept at the episode's base in plex-tv, ruling (j)),
// so the row still records it and its size, in both layouts.
func TestWorkerRecordsTheVideoAResourcesOnlyReDownloadKeeps(t *testing.T) {
	for _, layout := range []string{"", LayoutPlexTV} {
		t.Run("layout="+layout, func(t *testing.T) {
			w, store, _, lib, season := plexWorker(t)
			w.Cfg.Layout = layout
			w.Cfg.ResourcesOnly = true
			w.Downloader = resourcesRedownload{}
			var prev database.Lesson
			var video string
			if layout == LayoutPlexTV {
				base := "Beginner Course - s01e05 - Lesson A"
				seedSeason(t, season, base+".mp4", base+".nfo")
				prev = recordedRow(100, season, base+".mp4", base+".nfo")
				video = filepath.Join(season, base+".mp4")
			} else {
				dir := filepath.Join(lib, "Beginner Course", "05 - Lesson A")
				writeTree(t, dir, map[string]string{"05 - Lesson A.mp4": "old mp4", "05 - Lesson A.nfo": "old nfo"})
				prev.RailcontentID, prev.Status = 100, database.StatusDownloaded
				prev.OutputDir = sql.NullString{String: dir, Valid: true}
				video = filepath.Join(dir, "05 - Lesson A.mp4")
			}
			prev.Position = sql.NullInt64{Int64: 5, Valid: true}
			prev.VideoPath = sql.NullString{String: video, Valid: true}
			store.lessons[100] = prev
			store.withFiles = []database.Lesson{prev}
			info, err := os.Stat(video)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := w.RunOnce(context.Background(), 0); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			rec := onlyRecord(t, store)
			if rec.videoPath != video || rec.bytes != info.Size() {
				t.Errorf("video %q, %d bytes; want the kept video %q, %d bytes", rec.videoPath, rec.bytes, video, info.Size())
			}
			if _, err := os.Stat(video); err != nil {
				t.Errorf("the kept video is gone: %v", err)
			}
		})
	}
}
