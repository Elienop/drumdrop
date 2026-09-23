package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/musora"
)

// recordDownload finishes a successful download: it moves the lesson into the
// library when one is configured, then records where its one complete copy is
// (FinishDownload, which also closes the job). The move is non-fatal in either
// layout: a move error is logged, and the lesson is recorded where the move says
// it is — the library, or else the scratch downloads folder.
//
// It returns the recorded byte count, and ok=false when a delete removed the
// job or lesson meanwhile: then nothing is recorded and what this download
// wrote is removed (discardAbandoned), so no file is left untracked.
func (w *Worker) recordDownload(ctx context.Context, job database.Job, lesson *musora.Lesson, follow database.Follow, prev database.Lesson, quality string, index int, dir string) (bytes int64, ok bool) {
	id := job.RailcontentID
	rec := database.DownloadRecord{Quality: quality, LibraryEntries: carriedEntries(w.log(), prev)}
	var placed []string
	recorded := false
	switch {
	case w.Cfg.Layout == LayoutPlexTV && w.Cfg.LibraryDir != "":
		// Plex TV layout: flatten into <library>/<Show>/Season 01/ and rename
		// every entry to the episode base. output_dir = the season folder;
		// video_path = the moved episode .mp4; library_entries = exactly what the
		// lesson owns there. The move needs every other lesson's claims to know
		// what it may not touch; without them it does not move at all.
		others, err := w.Store.ListLessonsWithFiles(ctx)
		if err != nil {
			fmt.Fprintf(w.log(), "  ⚠ move to library %d: not moved, the other lessons' files could not be read: %v\n", id, err)
			break
		}
		show := plexShow(follow, job, lesson)
		res, err := moveToLibraryPlexTV(w.Cfg.LibraryDir, show, 1, index, lesson.Title, dir,
			plexLibrary{self: prev, others: others, roots: w.roots()})
		if err != nil {
			fmt.Fprintf(w.log(), "  ⚠ move to library %d: %v\n", id, err)
		}
		// Whatever happened, owned() is what the lesson has in the library now:
		// the placed entries, and any previous ones it still owns.
		rec.LibraryEntries = res.owned()
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
		// Overwrite the moved <movie> nfo with an <episodedetails> nfo so a Plex
		// TV-Shows library (which can't match Drumeo to TheTVDB) gets the real
		// episode title/season/episode from local metadata. Only at a path the
		// move placed (and so recorded). Non-fatal, like the aux-artifact fetches.
		nfoPath := filepath.Join(res.seasonDir, res.episodeBase+".nfo")
		if !slices.Contains(res.placed, nfoPath) {
			fmt.Fprintf(w.log(), "  ⚠ episode nfo %d: the move placed no %q, so none was written\n", id, nfoPath)
		} else if err := os.WriteFile(nfoPath, []byte(musora.BuildEpisodeNFO(lesson, show, 1, index)), 0o644); err != nil {
			fmt.Fprintf(w.log(), "  ⚠ episode nfo %d: %v\n", id, err)
		}
	case w.Cfg.LibraryDir != "":
		// Default layout: move the whole "NN - title" leaf into the library at
		// the same path relative to DownloadsDir. A non-empty newDir holds the
		// whole lesson even when an error came with it (the downloads copy could
		// not be fully removed), so record it either way.
		newDir, err := moveToLibrary(w.Cfg.DownloadsDir, w.Cfg.LibraryDir, dir)
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
	err := w.Store.FinishDownload(context.WithoutCancel(ctx), job.ID, id, rec)
	if errors.Is(err, database.ErrDownloadAbandoned) {
		w.discardAbandoned(id, dir, placed)
		return 0, false
	}
	if err != nil {
		fmt.Fprintf(w.log(), "  ⚠ record download %d: %v\n", id, err)
	}
	return rec.Bytes, true
}

// carriedEntries is the library record a download keeps from the lesson's
// previous one when it does not replace it (the default layout, or a plex-tv
// move that never ran): those entries are still on disk and still the lesson's.
// nil when there is no record; a damaged record is logged and dropped.
func carriedEntries(log io.Writer, prev database.Lesson) []string {
	paths, recorded, err := prev.PlacedEntries()
	if err != nil {
		fmt.Fprintf(log, "  ⚠ lesson %d: %v\n", prev.RailcontentID, err)
		return nil
	}
	if !recorded {
		return nil
	}
	return paths
}

// roots are the folders the worker may remove entries under.
func (w *Worker) roots() []string {
	var roots []string
	for _, r := range []string{w.Cfg.LibraryDir, w.Cfg.DownloadsDir} {
		if r != "" {
			roots = append(roots, r)
		}
	}
	return roots
}

// discardAbandoned removes what a download wrote after a delete removed its job
// or lesson, since no row will ever track it: the library entries it placed and
// its lesson folder (the scratch folder, or the library folder it moved into),
// never a shared season folder. Without a library the lesson folder is also the
// lesson's permanent home (the delete removes it itself), so it is left alone.
func (w *Worker) discardAbandoned(id int, dir string, placed []string) {
	if w.Cfg.LibraryDir == "" {
		fmt.Fprintf(w.log(), "  ⊗ %d was deleted while downloading; nothing was recorded\n", id)
		return
	}
	var errs []error
	for _, p := range placed {
		errs = append(errs, RemoveUnderRoot(w.roots(), p))
	}
	if !IsPlexSeasonDir(dir) {
		errs = append(errs, RemoveUnderRoot(w.roots(), dir))
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintf(w.log(), "  ⚠ %d was deleted while downloading; what it wrote could not all be removed: %v\n", id, err)
		return
	}
	fmt.Fprintf(w.log(), "  ⊗ %d was deleted while downloading; removed what it had written\n", id)
}

// logAbandoned logs the outcome of a terminal store write that records
// nothing on disk (a skip or a failure).
func (w *Worker) logAbandoned(id int, err error) {
	switch {
	case errors.Is(err, database.ErrDownloadAbandoned):
		fmt.Fprintf(w.log(), "  ⊗ %d was deleted meanwhile; nothing was recorded\n", id)
	case err != nil:
		fmt.Fprintf(w.log(), "  ⚠ record %d: %v\n", id, err)
	}
}
