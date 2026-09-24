package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

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
// destination, say) or fails is undone, and its error logged. The lesson is
// then placed in the downloads folder instead, as drumdrop always kept a
// lesson it could not move, and that is logged; unless that fallback would
// delete library files the lesson owns, or stop recording them
// (keptInLibrary): then it is not placed anywhere, the attempt fails
// (errKeptInLibrary), its library copy stays recorded and as it was, and the
// next attempt tries the library again (owner rulings 2026-09-24 (f) and
// (i), by their reason: (f) applies where the fallback would delete). Which
// lessons those are depends on what the row records, not on today's layout:
// a row can record a folder written under the other layout. An error
// returned means the download could not be placed, and nothing outside its
// private folder was changed.
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
		if video != "" {
			rec.VideoPath, rec.Bytes = filepath.Join(pl.dir, filepath.Base(video)), bytes
		} else {
			// No video came with this download (resources only, say): a video
			// the lesson's folder keeps from its earlier download (a placement
			// replaces only what the download brings back) is still the one
			// the lesson records.
			rec.VideoPath, rec.Bytes = lessonVideo(pl.dir)
		}
		return pl, nil
	}
	lib := w.Cfg.LibraryDir
	fallback := filepath.Join(w.Cfg.DownloadsDir, rel)
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
			// The video is the one placed, or else one of the episode's
			// recorded entries it keeps (owner ruling 2026-09-24 (j)): a
			// resources-only re-download keeps the earlier video, recorded.
			video := res.videoPath
			if video == "" {
				video = keptVideo(res.kept, res.episodeBase)
			}
			rec.OutputDir, rec.VideoPath = res.seasonDir, video
			if video != "" {
				if info, serr := os.Stat(video); serr == nil {
					rec.Bytes = info.Size()
				}
			}
			return res.pending, nil
		}
		if keptInLibrary(prev, lib, w.Cfg.DownloadsDir, entries, fallback) {
			return nil, fmt.Errorf("%w: %v", errKeptInLibrary, err)
		}
	} else if lib != "" {
		pl, err := placeFolder(lib)
		if err == nil {
			return pl, nil
		}
		fmt.Fprintf(w.log(), "  ⚠ move to library %d: %v\n", id, err)
		// The default layout learns nothing of the lesson's season-folder
		// entries: a record it has is left as it is (rec.LibraryEntries nil).
		if keptInLibrary(prev, lib, w.Cfg.DownloadsDir, nil, fallback) {
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
// library failed, and placing it in the downloads folder instead would delete
// or stop recording library files the lesson owns (keptInLibrary; owner
// rulings 2026-09-24 (f) and (i)).
var errKeptInLibrary = errors.New("not placed: the placement in the library failed, and the lesson's copy there is kept")

// keptInLibrary reports whether placing the lesson in its downloads folder
// fallback, once its placement in the library lib failed, would delete
// library files its row prev says it owns, or stop recording them. That
// fallback records fallback as the lesson's output_dir, sets aside the
// lesson's previous folder (previousFolder: a lesson folder, never a season
// folder) if the download brings back every file in it, and records entries
// as the lesson's library record (nil: the record is left as it is). So it
// would when:
//   - the row records the lesson's own folder in the library, and that
//     folder is not fallback: the fallback replaces it, or leaves it where
//     it is, no longer recorded (ruling (e)). This holds whatever the layout
//     is today: a plex-tv install's row may record a folder the default
//     layout placed before a layout switch;
//   - the row records a season folder in the library and no record of its
//     entries (a legacy row: it owns them only through output_dir), and
//     entries, what the plex-tv move learned it owns there, is nil: once
//     output_dir names the downloads folder, no lesson claims them.
//
// A row whose lesson folder is in the downloads folder's course folder,
// beside fallback, when the downloads folder sits inside the library, is not
// in the library (inLibrary), so it falls back: the fallback's previous
// folder is in downloads, as with a downloads folder beside the library. A
// season folder there is the library's (the fallback never writes one). A row that
// records fallback itself falls back too: the library is the downloads
// folder (or holds it under another spelling), and the lesson was kept in
// downloads before. That placement is the lesson's own folder
// (recordsFolder, as placeLessonFolder decides it; a symlink at fallback's
// name that leads to the library folder is not): it replaces only the
// lesson's own files at the names the download brings back, as any
// re-download does, and the row goes on recording that folder. A
// season-folder row with a record, or whose entries the move learned (they
// are recorded now), keeps them recorded, and
// the fallback touches nothing in the library, so it falls back (ruling
// (i)); so does a lesson whose row records nothing in the library.
func keptInLibrary(prev database.Lesson, lib, downloads string, entries []string, fallback string) bool {
	if !inLibrary(prev, lib, downloads, fallback) || recordsFolder(prev, fallback) {
		return false
	}
	if !library.IsSeasonDir(prev.OutputDir.String) {
		return true
	}
	return entries == nil && !prev.LibraryEntries.Valid
}

// recordsFolder reports whether row l records dir as its folder
// (output_dir), however each is spelled (the same path once cleaned, or the
// same folder on disk: sameDir), as a placement at dir treats it: the entry
// at dir's own name is a real folder, or is missing. A symlink or file there
// is not the recorded folder even when it leads to it: placeLessonFolder
// reads that name with Lstat and replaces such an entry with a new folder,
// so the folder the symlink led to would stay behind, recorded by no lesson.
func recordsFolder(l database.Lesson, dir string) bool {
	if !l.OutputDir.Valid {
		return false
	}
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return false
	}
	return filepath.Clean(l.OutputDir.String) == filepath.Clean(dir) || sameDir(l.OutputDir.String, dir)
}

// inLibrary reports whether keptInLibrary treats the folder the lesson's row
// records (output_dir) as the library's. It reads the spelling only (N3 in
// BACKLOG D58: a library folder spelled another way is not seen), and the
// rule is:
//   - a folder outside the library lib is not the library's;
//   - a folder inside it is, when the downloads folder downloads is beside
//     the library, is the library (a tie), or holds it;
//   - when the downloads folder sits inside the library, a folder inside it
//     is the library's too, except a lesson folder in the course folder the
//     downloads fallback goes into (beside fallback: an earlier refused move
//     kept the lesson there, under this title or an older one).
//
// This is not previousFolder's rule, which files a folder under the longest
// root holding it: with the downloads folder inside the library, a folder
// in downloads outside the fallback's course folder is the downloads
// folder's to previousFolder but the library's here, so a refusal keeps it.
// The spelling can't tell who placed such a folder: a library folder of a
// course, or of an instructor, named like the downloads folder is inside it
// too, and so is any folder the library placed there before its root moved
// up. The exception rests on what the fallback writes: only "NN - Title"
// lesson folders, which SeasonNumber never reads as a season folder, so a
// season folder in the fallback's course folder was placed by the library
// (it stays the library's). A lesson folder there is still counted as the
// downloads folder's even when the library placed it (an instructor named
// like the downloads folder, or a root moved up): BACKLOG D128.
func inLibrary(prev database.Lesson, lib, downloads, fallback string) bool {
	if !prev.OutputDir.Valid || prev.OutputDir.String == "" || !library.Inside(lib, prev.OutputDir.String) {
		return false
	}
	nested := library.Inside(lib, downloads) && !library.Inside(downloads, lib)
	return !nested || library.IsSeasonDir(prev.OutputDir.String) ||
		filepath.Dir(filepath.Clean(prev.OutputDir.String)) != filepath.Dir(filepath.Clean(fallback))
}

// keptVideo is the first, in name order, of the entries a plex-tv placement
// keeps that is a video of the episode base (isLessonVideoName), or "".
func keptVideo(kept []string, base string) string {
	for _, p := range slices.Sorted(slices.Values(kept)) {
		if isLessonVideoName(filepath.Base(p), base) {
			return p
		}
	}
	return ""
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
