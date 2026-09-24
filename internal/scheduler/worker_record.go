package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
)

// recordDownload finishes a successful download: it places the lesson where
// it lives (place), records it there (FinishDownload, which also closes the
// job), and only then commits the placement, which drops what it replaced.
// The record is the commit point. If it is refused, the placement is undone,
// so the lesson's earlier files are back where they were: a Skip, a delete or
// a follow removal that lands while the files are being placed stops the
// download as if it had landed before (D79).
//
// It returns the recorded byte count, and ok=false when nothing was recorded:
// a delete or a skip removed the job or lesson meanwhile, or the job was
// requeued by someone else (it runs again). Either way it reports the job's
// end itself (lesson_skipped). err is a failed attempt: the other lessons'
// claims could not be read, the download could not be placed anywhere, or
// FinishDownload could not record it. Each is retried.
//
// Before it places anything, the partial files an earlier attempt left in the
// private folder (isPartialName: yt-dlp's, and drumdrop's own temporary files)
// are removed, so none of them is placed.
func (w *Worker) recordDownload(ctx context.Context, job database.Job, lesson *musora.Lesson, follow database.Follow, prev database.Lesson, quality string, index int, outDir, privateLesson string) (bytes int64, ok bool, err error) {
	id := job.RailcontentID
	// The download is complete: a shutdown that began meanwhile does not stop
	// it being placed and recorded (both are local and short).
	claims, err := w.claims(context.WithoutCancel(ctx))
	if err != nil {
		return 0, false, fmt.Errorf("not placed: %w", err)
	}
	cleanupPartials(privateLesson)
	src, err := openScratch(w.Cfg.DownloadsDir, privateLesson)
	if err != nil {
		return 0, false, fmt.Errorf("not placed: %w", err)
	}
	defer src.close()

	rec := database.DownloadRecord{Quality: quality}
	pl, err := w.place(job, lesson, follow, prev, index, outDir, claims, src, &rec)
	if err != nil {
		return 0, false, err
	}

	// The files are in place; record them even if shutdown began meanwhile.
	finishCtx := context.WithoutCancel(ctx)
	if ferr := w.Store.FinishDownload(finishCtx, job.ID, id, rec); ferr != nil {
		// A delete of the lesson's files wants its earlier files gone, so the
		// undo does not put those back.
		stuck, uerr := pl.undo(errors.Is(ferr, database.ErrLessonDeleted))
		if uerr != nil {
			fmt.Fprintf(w.log(), "  ⚠ %d: the placement could not be fully undone (left: %q): %v\n", id, stuck, uerr)
		}
		switch {
		case errors.Is(ferr, database.ErrDownloadAbandoned):
			w.ended(job, lesson, msgStopped)
			fmt.Fprintf(w.log(), "  ⊗ %d was stopped while it was being placed; the placement was undone\n", id)
			return 0, false, nil
		case errors.Is(ferr, database.ErrDownloadCanceled):
			// The job is neither running nor canceled: another process requeued it
			// (a retry, or a startup recovery), so it runs again.
			fmt.Fprintf(w.log(), "  ⚠ record download %d: not recorded, its job was requeued meanwhile: %v\n", id, ferr)
			w.ended(job, lesson, msgRequeued)
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("the download could not be recorded: %w", ferr)
	}
	replaced, cerr := pl.commit()
	for _, e := range replaced {
		whose := "which no lesson recorded"
		if e.own {
			whose = "the lesson's earlier download"
		}
		fmt.Fprintf(w.log(), "  ↻ %d replaced %q (%s)\n", id, e.path, whose)
	}
	if cerr != nil {
		fmt.Fprintf(w.log(), "  ⚠ %d: what the placement replaced could not all be removed: %v\n", id, cerr)
	}
	for _, k := range pl.kept {
		fmt.Fprintf(w.log(), "  ⚠ %d left its previous folder %q where it was, no longer recorded: %s\n", id, k.path, k.why)
	}
	return rec.Bytes, true, nil
}

// place puts the finished download src where the lesson lives and fills rec
// with where that is, returning the placement to commit or undo:
//   - plex-tv with a library: the season folder (moveToLibraryPlexTV);
//   - otherwise the lesson's folder <root>/<Course>/NN - Title
//     (placeLessonFolder), root being the library, or the downloads folder
//     without one. No library is a library rooted at the downloads folder.
//
// A placement in the library that is refused (another lesson owns the
// destination, say) or fails is undone, and its error logged. In the default
// layout, a lesson whose row records its folder in the library is then not
// placed anywhere: the attempt fails (errKeptInLibrary), its library copy
// stays recorded and as it was, and the next attempt tries the library again
// (owner ruling 2026-09-24 (f)), since placing it in downloads would replace
// that folder. Any other lesson, and every plex-tv one, is placed in the
// downloads folder instead, as drumdrop always kept a lesson it could not
// move, and that is logged. In plex-tv this touches nothing in the library
// (a season folder is never a previous folder to replace), and the entries
// the lesson still owns there stay recorded (owner ruling 2026-09-24 (i)). An
// error returned means the download could not be placed, and nothing outside
// its private folder was changed.
func (w *Worker) place(job database.Job, lesson *musora.Lesson, follow database.Follow, prev database.Lesson, index int, outDir string, claims *library.Claims, src *scratchDir, rec *database.DownloadRecord) (*placement, error) {
	id := job.RailcontentID
	rel, err := filepath.Rel(w.Cfg.DownloadsDir, lessonDir(outDir, index, lesson.Title))
	if err != nil {
		return nil, fmt.Errorf("not placed: %w", err)
	}
	video, bytes := w.producedVideo(src.dir.Name())
	placeFolder := func(root string) (*placement, error) {
		pl, err := placeLessonFolder(root, rel, src, prev, claims, w.roots(), job.ID)
		if err != nil {
			return nil, err
		}
		rec.OutputDir = pl.dir
		rec.VideoPath, rec.Bytes = "", 0
		if video != "" {
			rec.VideoPath, rec.Bytes = filepath.Join(pl.dir, filepath.Base(video)), bytes
		}
		return pl, nil
	}
	lib := w.Cfg.LibraryDir
	if lib != "" && w.Cfg.Layout == LayoutPlexTV {
		// Plex TV layout: flatten into <library>/<Show>/Season 01/ and rename
		// every entry to the episode base. output_dir = the season folder;
		// video_path = the placed episode .mp4; library_entries = exactly what
		// the lesson owns there. The <episodedetails> nfo replaces the
		// download's <movie> one before the entries are placed, so a Plex
		// TV-Shows library (which can't match Drumeo to TheTVDB) gets the real
		// episode title/season/episode from local metadata.
		show := plexShow(follow, job, lesson)
		res, err := moveToLibraryPlexTV(lib, show, 1, index, lesson.Title, src, plexLibrary{
			self: prev, claims: claims, roots: w.roots(), jobID: job.ID,
			episodeNFO: []byte(musora.BuildEpisodeNFO(lesson, show, 1, index)),
		})
		if err != nil {
			fmt.Fprintf(w.log(), "  ⚠ move to library %d: %v\n", id, err)
		}
		// Whatever happened, the record is what the lesson has in the library
		// now: the placed entries, or the previous ones it still owns.
		entries, rerr := res.record(lib)
		if rerr != nil {
			fmt.Fprintf(w.log(), "  ⚠ move to library %d: its library record is left as it was: %v\n", id, rerr)
		}
		rec.LibraryEntries = entries
		if res.pending != nil {
			rec.OutputDir, rec.VideoPath = res.seasonDir, res.videoPath
			if !w.Cfg.ResourcesOnly && res.videoPath != "" {
				if info, serr := os.Stat(res.videoPath); serr == nil {
					rec.Bytes = info.Size()
				}
			}
			return res.pending, nil
		}
	} else if lib != "" {
		pl, err := placeFolder(lib)
		if err == nil {
			return pl, nil
		}
		fmt.Fprintf(w.log(), "  ⚠ move to library %d: %v\n", id, err)
		if inLibrary(prev, lib) {
			return nil, fmt.Errorf("%w: %v", errKeptInLibrary, err)
		}
	}
	pl, err := placeFolder(w.Cfg.DownloadsDir)
	if err != nil {
		return nil, fmt.Errorf("the download could not be placed: %w", err)
	}
	if lib != "" {
		fmt.Fprintf(w.log(), "  ⚠ %d is kept in downloads at %q\n", id, pl.dir)
	}
	return pl, nil
}

// errKeptInLibrary is a download not placed because its placement in the
// library failed while the lesson's row records its folder there, in the
// default layout: falling back to downloads would replace that copy (owner
// rulings 2026-09-24 (f) and (i)).
var errKeptInLibrary = errors.New("not placed: the placement in the library failed, and the lesson's copy there is kept")

// inLibrary reports whether the lesson's row records its folder inside the
// library lib.
func inLibrary(prev database.Lesson, lib string) bool {
	return prev.OutputDir.Valid && prev.OutputDir.String != "" && library.Inside(lib, prev.OutputDir.String)
}

// checkBeforeDownload is what a download needs before it starts, so a
// precondition that can not pass never costs a download: the other lessons'
// claims must be readable, since placing the download needs them (whose
// files are at its destination). A damaged record anywhere fails here, every
// cycle, without downloading.
func (w *Worker) checkBeforeDownload(ctx context.Context) error {
	_, err := w.claims(ctx)
	return err
}

// claims indexes what every lesson row with files claims, in the library and
// in downloads.
func (w *Worker) claims(ctx context.Context) (*library.Claims, error) {
	rows, err := w.Store.ListLessonsWithFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("the other lessons' files could not be read: %w", err)
	}
	c, err := library.NewClaims(w.Cfg.LibraryDir, rows)
	if err != nil {
		return nil, fmt.Errorf("the other lessons' files are not known: %w", err)
	}
	return c, nil
}

// roots are the folders the worker may set entries aside under.
func (w *Worker) roots() []string {
	return library.Roots(w.Cfg.LibraryDir, w.Cfg.DownloadsDir)
}
