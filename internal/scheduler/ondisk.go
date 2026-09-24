package scheduler

import (
	"context"
	"fmt"
	"os"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// filesOnDisk reports whether the files lesson id's row records now are on
// disk, read just before a download that recorded nothing ends (it failed,
// Musora didn't return the lesson, or it was canceled). Only then may the
// lesson keep reading 'downloaded' (owner ruling 2026-09-24 (o)): the store
// decides it in the transaction that ends the job, from this answer and the
// row as it is then (see database keepsFilesSQL). SQLite can't see the disk,
// so the check is made here, before that transaction, and no transaction
// waits on the disk. A row that can not be read is not on disk: the lesson
// then ends as a lesson without files does, and the next sync retries a
// failed one.
func (w *Worker) filesOnDisk(ctx context.Context, id int) bool {
	l, err := w.Store.GetLesson(context.WithoutCancel(ctx), id)
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ %d: its record could not be read to check its files are on disk: %v\n", id, err)
		return false
	}
	return w.recordedFilesPresent(l)
}

// recordedFilesPresent reports whether the files row l records are on disk.
// Which files count:
//   - the recorded video, when the row has one: it is the lesson, and the
//     file an interrupted re-download lost (an older release ran yt-dlp's
//     --force-overwrites in the lesson's folder, which deletes the video
//     first). Captions, a poster or resources missing beside it do not make
//     the lesson a failure;
//   - otherwise (no video: resources only, or a video-less song) every entry
//     its library record names, under the library folder; a record with no
//     library folder configured can not be found. An entry counts when it is
//     a regular file or a folder, read through a symlink as the video is: a
//     placement puts only those two there, and a recorded "<base> resources"
//     is a folder. The record names entries, not their kinds, so a folder at
//     a file's name counts too: telling them apart would mean guessing from
//     the name, which the record exists to avoid (owner ruling #66);
//   - otherwise its folder (output_dir): a default-layout lesson's own
//     folder, or a legacy plex-tv row's season folder.
//
// A path counts only when it can be read as there: anything else (missing,
// unreadable, a drive that is not mounted) counts as missing, the side that
// retries rather than claims files that may be gone.
func (w *Worker) recordedFilesPresent(l database.Lesson) bool {
	if l.VideoPath.Valid && l.VideoPath.String != "" {
		info, err := os.Stat(l.VideoPath.String)
		return err == nil && info.Mode().IsRegular()
	}
	entries, recorded, err := library.Record(l)
	if err != nil {
		return false
	}
	if recorded && len(entries) > 0 {
		if w.Cfg.LibraryDir == "" {
			return false
		}
		for _, e := range entries {
			info, err := os.Stat(library.Resolve(w.Cfg.LibraryDir, e))
			if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
				return false
			}
		}
		return true
	}
	if l.OutputDir.Valid && l.OutputDir.String != "" {
		info, err := os.Stat(l.OutputDir.String)
		return err == nil && info.IsDir()
	}
	return false
}
