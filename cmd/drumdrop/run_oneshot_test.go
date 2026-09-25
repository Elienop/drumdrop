package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// overwritingDownloader acts as yt-dlp under --force-overwrites: it deletes
// the video already at its output path, then fails, or writes a new one.
type overwritingDownloader struct {
	fail  bool
	calls int
}

func (d *overwritingDownloader) Download(_ context.Context, l *musora.Lesson, o musora.DownloadOpts) error {
	d.calls++
	base := fmt.Sprintf("%02d - %s", o.Index, musora.Sanitize(l.Title))
	dir := filepath.Join(o.Dir, base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, base+".mp4")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if d.fail {
		return errors.New("yt-dlp: HTTP Error 403: Forbidden")
	}
	return os.WriteFile(filepath.Join(dir, base+".mp4"), []byte("new"), 0o644)
}

// seedRunVideo writes an earlier download of lesson 1 "Groove" in out.
func seedRunVideo(t *testing.T, out string) string {
	t.Helper()
	p := filepath.Join(out, "Course", "01 - Groove", "01 - Groove.mp4")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runLessons() []resolvedLesson {
	return []resolvedLesson{{id: 7, lesson: &musora.Lesson{ID: 7, Title: "Groove"}, index: 1}}
}

// The one-shot "drumdrop <url>" re-downloading a lesson whose video is
// already in --out keeps that video when the download fails.
func TestDownloadResolvedFailureKeepsExistingVideo(t *testing.T) {
	out := t.TempDir()
	video := seedRunVideo(t, out)
	dl := &overwritingDownloader{fail: true}

	downloaded, failed := downloadResolved(context.Background(), dl, out, "Course", runLessons(), musora.DownloadOpts{}, console{stdout: io.Discard, stderr: io.Discard})
	if downloaded != 0 || failed != 1 || dl.calls != 1 {
		t.Fatalf("downloaded, failed, calls = %d, %d, %d; want 0, 1, 1", downloaded, failed, dl.calls)
	}
	got, err := os.ReadFile(video)
	if err != nil || string(got) != "old" {
		t.Fatalf("the earlier video after a failed re-download: %q, %v; want \"old\"", got, err)
	}
}

// A finished re-download replaces the video.
func TestDownloadResolvedSuccessReplacesVideo(t *testing.T) {
	out := t.TempDir()
	video := seedRunVideo(t, out)

	downloaded, failed := downloadResolved(context.Background(), &overwritingDownloader{}, out, "Course", runLessons(), musora.DownloadOpts{}, console{stdout: io.Discard, stderr: io.Discard})
	if downloaded != 1 || failed != 0 {
		t.Fatalf("downloaded, failed = %d, %d; want 1, 0", downloaded, failed)
	}
	got, err := os.ReadFile(video)
	if err != nil || string(got) != "new" {
		t.Fatalf("the video after a finished re-download: %q, %v; want \"new\"", got, err)
	}
}

// Once interrupted, no further lesson starts.
func TestDownloadResolvedInterruptedStartsNothing(t *testing.T) {
	out := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dl := &overwritingDownloader{}

	downloaded, failed := downloadResolved(ctx, dl, out, "Course", runLessons(), musora.DownloadOpts{}, console{stdout: io.Discard, stderr: io.Discard})
	if downloaded != 0 || failed != 1 || dl.calls != 0 {
		t.Fatalf("downloaded, failed, calls = %d, %d, %d; want 0, 1, 0", downloaded, failed, dl.calls)
	}
}
