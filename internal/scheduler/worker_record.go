package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
	"github.com/elienop/drumdrop/internal/musora"
)

// recordDownload finishes a successful download: it moves the lesson into the
// library when one is configured, then records where its one complete copy is
// (FinishDownload, which also closes the job). The move is non-fatal in either
// layout: a move error is logged, and the lesson is recorded where the move says
// it is — the library, or else the scratch downloads folder.
//
// It returns the recorded byte count, and ok=false when nothing was recorded:
// a delete or a skip removed the job or lesson meanwhile (what this download
// wrote then goes as the stopper wants, see discardAbandoned), or the job was
// requeued by someone else (it runs again; nothing is reported). err is a
// failed attempt: the move needs every other lesson's claims, and when they
// can not be read it does not move, nor record the lesson in scratch (a lesson
// moved before the record existed would lose track of its library copy); and a
// download FinishDownload could not record is not reported as done. Either is
// retried.
func (w *Worker) recordDownload(ctx context.Context, job database.Job, lesson *musora.Lesson, follow database.Follow, prev database.Lesson, quality string, index int, dir string) (bytes int64, ok bool, err error) {
	id := job.RailcontentID
	// A download that does not move into a season folder keeps the lesson's
	// library record as it is (LibraryEntries nil): those entries are still on
	// disk and still the lesson's.
	rec := database.DownloadRecord{Quality: quality}
	var placed []string
	recorded := false
	var claims *library.Claims
	if w.Cfg.LibraryDir != "" {
		if claims, err = w.claims(ctx); err != nil {
			return 0, false, fmt.Errorf("not moved to the library: %w", err)
		}
	}
	switch {
	case w.Cfg.Layout == LayoutPlexTV && w.Cfg.LibraryDir != "":
		// Plex TV layout: flatten into <library>/<Show>/Season 01/ and rename
		// every entry to the episode base. output_dir = the season folder;
		// video_path = the moved episode .mp4; library_entries = exactly what the
		// lesson owns there.
		show := plexShow(follow, job, lesson)
		// The <episodedetails> nfo replaces the download's <movie> one before
		// the move places it, so a Plex TV-Shows library (which can't match
		// Drumeo to TheTVDB) gets the real episode title/season/episode from
		// local metadata, and nothing is written in the season folder after.
		res, err := moveToLibraryPlexTV(w.Cfg.LibraryDir, show, 1, index, lesson.Title, dir, plexLibrary{
			downloads: w.Cfg.DownloadsDir, self: prev, claims: claims, roots: w.roots(),
			episodeNFO: []byte(musora.BuildEpisodeNFO(lesson, show, 1, index)),
		})
		if err != nil {
			fmt.Fprintf(w.log(), "  ⚠ move to library %d: %v\n", id, err)
		}
		// Whatever happened, the record is what the lesson has in the library
		// now: the placed entries, and any previous ones it still owns.
		entries, rerr := res.record(w.Cfg.LibraryDir)
		if rerr != nil {
			fmt.Fprintf(w.log(), "  ⚠ move to library %d: its library record is left as it was: %v\n", id, rerr)
		}
		rec.LibraryEntries = entries
		placed = res.placed
		if res.seasonDir == "" {
			break // the lesson is whole in the scratch folder: record it there
		}
		dir = res.seasonDir
		rec.VideoPath = res.videoPath
		if !w.Cfg.ResourcesOnly && res.videoPath != "" {
			if info, serr := os.Stat(res.videoPath); serr == nil {
				rec.Bytes = info.Size()
			}
		}
		recorded = true
	case w.Cfg.LibraryDir != "":
		// Default layout: move the whole "NN - title" leaf into the library at
		// the same path relative to DownloadsDir. A non-empty newDir holds the
		// whole lesson even when an error came with it (the downloads copy could
		// not be fully removed, or a note), so record it either way.
		newDir, err := moveToLibrary(w.Cfg.DownloadsDir, w.Cfg.LibraryDir, dir, claims, id)
		if err != nil {
			fmt.Fprintf(w.log(), "  ⚠ move to library %d: %v\n", id, err)
		}
		if newDir != "" {
			dir = newDir
		}
	}
	// For the default layout (and for a plex-tv move that did not happen,
	// leaving the files in the scratch dir) derive the video from the dir's
	// "<base>.mp4".
	if !recorded {
		rec.VideoPath, rec.Bytes = w.producedVideo(dir)
	}
	rec.OutputDir = dir

	// The files are in place; record them even if shutdown began meanwhile.
	finishCtx := context.WithoutCancel(ctx)
	ferr := w.Store.FinishDownload(finishCtx, job.ID, id, rec)
	switch {
	case errors.Is(ferr, database.ErrDownloadAbandoned):
		w.discardAbandoned(finishCtx, id, dir, placed, ferr)
		return 0, false, nil
	case errors.Is(ferr, database.ErrDownloadCanceled):
		// The job is neither running nor canceled: another process requeued it
		// (a retry, or a startup recovery), so it runs again and replaces this.
		fmt.Fprintf(w.log(), "  ⚠ record download %d: not recorded, its job was requeued meanwhile: %v\n", id, ferr)
		return 0, false, nil
	case ferr != nil:
		return 0, false, fmt.Errorf("the download could not be recorded: %w", ferr)
	}
	return rec.Bytes, true, nil
}

// checkBeforeDownload is what a download needs before it starts, so a
// precondition that can not pass never costs a download: with a library, the
// other lessons' claims must be readable (the move needs them). A damaged
// record anywhere fails here, every cycle, without downloading.
func (w *Worker) checkBeforeDownload(ctx context.Context) error {
	if w.Cfg.LibraryDir == "" {
		return nil
	}
	_, err := w.claims(ctx)
	return err
}

// claims indexes what every lesson row with files claims in the library.
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

// roots are the folders the worker may remove entries under.
func (w *Worker) roots() []string {
	return library.Roots(w.Cfg.LibraryDir, w.Cfg.DownloadsDir)
}

// discardAbandoned is what happens to what a download wrote once a delete or a
// skip removed its job or lesson (cause, from the guarded write, says what the
// stopper wanted):
//   - a stopper that keeps the files (a follow removed without its files), or
//     one whose intent is unknown: nothing the download finished is removed,
//     only yt-dlp's partial files in its lesson folder;
//   - one that discards them (a skip): the library entries this download
//     placed, and its lesson folder dir (scratch, or the folder it moved into),
//     are removed, except anything a lesson row records now (read fresh),
//     which is kept and logged;
//   - a delete of the lesson's files: the same, except that the lesson's own
//     row does not count (its files are being deleted, so its record protects
//     nothing this download wrote).
//
// A season folder is never removed, nor a folder not named like a lesson
// folder. If the rows can not be read, nothing is removed.
func (w *Worker) discardAbandoned(ctx context.Context, id int, dir string, placed []string, cause error) {
	if !errors.Is(cause, database.ErrDiscardDownload) {
		if !library.IsSeasonDir(dir) {
			cleanupPartials(dir)
		}
		fmt.Fprintf(w.log(), "  ⊗ %d was removed while downloading; its files were kept, as asked\n", id)
		return
	}
	c, err := w.claims(ctx)
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ %d was stopped while downloading; nothing it wrote was removed: %v\n", id, err)
		return
	}
	self := 0
	if errors.Is(cause, database.ErrLessonDeleted) {
		self = id
	}
	var errs []error
	for _, p := range placed {
		if ids, err := c.Claimants(p, self, true); err != nil || len(ids) > 0 {
			errs = append(errs, fmt.Errorf("kept %q, which lessons %v record (%v)", p, ids, err))
			continue
		}
		errs = append(errs, library.Remove(w.roots(), p))
	}
	if !library.IsSeasonDir(dir) {
		if err := c.RemoveLessonFolder(w.roots(), dir, self); err != nil {
			cleanupPartials(dir)
			errs = append(errs, fmt.Errorf("kept the lesson folder: %w", err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintf(w.log(), "  ⚠ %d was stopped while downloading; not everything it wrote was removed: %v\n", id, err)
		return
	}
	fmt.Fprintf(w.log(), "  ⊗ %d was stopped while downloading; removed what it had written\n", id)
}

// dropFailedDownload removes what a download that failed every attempt left in
// its scratch lesson folder dir, so no copy stays in downloads that no lesson
// records: the whole folder, unless a lesson row (this lesson's included)
// records something in it, as when its earlier download is recorded there;
// then only yt-dlp's partial files go. If the rows can not be read, only the
// partial files go.
func (w *Worker) dropFailedDownload(ctx context.Context, id int, dir string) {
	c, err := w.claims(ctx)
	if err != nil {
		cleanupPartials(dir)
		fmt.Fprintf(w.log(), "  ⚠ %d failed; its partial files were removed, the rest of %q was kept: %v\n", id, dir, err)
		return
	}
	if err := c.RemoveLessonFolder(w.roots(), dir, 0); err != nil {
		cleanupPartials(dir)
		if len(c.Holds(dir, 0)) == 0 {
			fmt.Fprintf(w.log(), "  ⚠ %d failed; %q could not be removed: %v\n", id, dir, err)
		}
	}
}
