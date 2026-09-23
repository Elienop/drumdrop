package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// logOut receives the server's own diagnostics (the detail behind a fixed
// client message). A package variable so tests can capture it; the server logs
// no requests, so nothing here ever holds the API token.
var logOut io.Writer = os.Stderr

// errFilesKept is the delete outcome "some of the lesson's files could not be
// removed; the lesson records what is left". Its detail is logged, never sent
// to the client (hard rule 11).
var errFilesKept = errors.New("the lesson's files could not all be removed")

// errRecordNotUpdated is the delete outcome "the files were removed as far as
// possible, but the lesson's row could not be updated". Its detail is logged.
var errRecordNotUpdated = errors.New("the lesson's record could not be updated")

// The fixed messages a delete answers with (hard rule 11: never err.Error()).
// Each says what happened, what is left, and what to do next.
const (
	msgLessonFilesKept     = "Not all of this lesson's files could be deleted. Any download of it was stopped, and it stays listed with the files that are left. The server log says what went wrong; fix that, then Delete again."
	msgLessonChanged       = "This lesson changed while it was being deleted, so it was left as it is now. Delete again to remove its files."
	msgLessonDeleting      = "This lesson is already being deleted. Wait for that to finish, then refresh."
	msgLessonNotUpdated    = "This lesson's files were deleted as far as possible, but its record could not be updated. The server log says what went wrong; Delete again to finish."
	msgFollowFilesKept     = "Not every lesson's files could be deleted, so the follow was kept. Its downloads were stopped, lessons whose files were deleted are marked skipped, and the rest stay listed with the files that are left. The server log says what went wrong; fix that, then Remove again."
	msgFollowLessonChanged = "A lesson of this follow changed while the follow was being removed, so the follow was kept and that lesson was left as it is now. Its downloads were stopped, and lessons whose files were deleted are marked skipped. Remove again to delete the rest."
	msgFollowNewFiles      = "A lesson of this follow finished downloading while the follow was being removed, so the follow was kept with that lesson's files. Its other downloads were stopped, and lessons whose files were deleted are marked skipped. Remove again to delete the new files too."
	msgFollowDeleting      = "A lesson of this follow is being deleted right now. Wait for that to finish, then Remove again."
	msgFollowNotUpdated    = "This follow's files were deleted as far as possible, but a record could not be updated, so the follow was kept. The server log says what went wrong; Remove again to finish."
	msgFollowNotRemoved    = "The follow could not be removed. Nothing was changed. Try again."
)

// roots are the folders a delete may remove files under: the downloads and
// the library folders.
func (s *Server) roots() []string {
	var roots []string
	for _, r := range []string{s.cfg.DownloadsDir, s.cfg.LibraryDir} {
		if r != "" {
			roots = append(roots, r)
		}
	}
	return roots
}

// removeLessonFiles removes every file lesson l records. What it removes follows
// what was recorded, never today's DRUMDROP_LAYOUT, so a layout switch can
// never turn a one-episode delete into a season wipe:
//   - the library entries the lesson owns in plex-tv season folders
//     (library.Claims.Plan): exactly its record, or for a lesson moved before
//     the record existed, its name-matched episode entries. An entry another
//     lesson also claims is kept and logged, never removed. A season folder
//     itself is never removed;
//   - its own folder, when output_dir is not a season folder (the default
//     layout, or a plex-tv lesson recorded in downloads): removed whole, but
//     only when it is named like a lesson folder ("05 - Title") and holds
//     nothing another lesson records, so a damaged output_dir can not take
//     another lesson's files.
//
// Every removal goes through library.Remove, confined by os.Root to the
// downloads or library root it sits inside: a path outside both, a root
// itself, or a path reached through a symlinked folder that leads outside is
// refused. A missing path is already gone. The trade-off: a symlinked folder
// that an operator placed inside the library on purpose is refused too.
//
// On failure it returns an error with every detail, and kept: what the row
// must go on recording, which is only what is still on disk (see keptFiles).
func (s *Server) removeLessonFiles(c *library.Claims, l database.Lesson) (kept database.KeptFiles, err error) {
	plan, err := c.Plan(l)
	if err != nil {
		// Nothing was removed: the row keeps everything it records.
		return database.KeptFiles{OutputDir: true, VideoPath: true}, err
	}
	for _, p := range plan.Kept {
		fmt.Fprintf(logOut, "drumdrop: delete lesson %d: kept %q, which another lesson also claims\n", l.RailcontentID, p)
	}
	var left []string
	var errs []error
	for _, p := range plan.Remove {
		if rerr := library.Remove(s.roots(), p); rerr != nil {
			left = append(left, p)
			errs = append(errs, rerr)
		}
	}
	if l.OutputDir.Valid && !library.IsSeasonDir(l.OutputDir.String) {
		if rerr := s.removeLessonFolder(c, l); rerr != nil {
			errs = append(errs, rerr)
		}
	}
	if len(errs) == 0 {
		return database.KeptFiles{}, nil
	}
	kept, kerr := s.keptFiles(l, plan, left)
	return kept, errors.Join(append(errs, kerr)...)
}

// keptFiles is what lesson l goes on recording after a removal that failed
// partway: left are the planned library entries still on disk.
//   - its library record narrows to left (a lesson moved before the record
//     existed gains a record of exactly those); one that had neither a
//     record nor planned entries keeps none;
//   - output_dir stays while what it names is still there: a season folder
//     while entries of the lesson remain in it, its own folder while it exists;
//   - video_path (and its size) stays while the video exists.
//
// A path whose existence can not be read counts as still there.
func (s *Server) keptFiles(l database.Lesson, plan library.Entries, left []string) (database.KeptFiles, error) {
	var kept database.KeptFiles
	_, recorded, _ := l.PlacedEntries() // Plan already refused a damaged record
	if recorded || len(plan.Remove) > 0 || len(plan.Kept) > 0 {
		entries, err := library.EntriesFor(s.cfg.LibraryDir, left)
		if err != nil {
			// The record can not say what is left: keep everything.
			return database.KeptFiles{OutputDir: true, VideoPath: true}, err
		}
		kept.LibraryEntries = entries
	}
	if l.OutputDir.Valid {
		if library.IsSeasonDir(l.OutputDir.String) {
			kept.OutputDir = len(left) > 0
		} else {
			kept.OutputDir = stillThere(l.OutputDir.String)
		}
	}
	kept.VideoPath = l.VideoPath.Valid && l.VideoPath.String != "" && stillThere(l.VideoPath.String)
	return kept, nil
}

// stillThere reports whether path may still exist: anything but "not found".
func stillThere(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// removeLessonFolder removes lesson l's own folder (its output_dir), refusing
// one that is not named like a lesson folder or that holds a path another
// lesson records.
func (s *Server) removeLessonFolder(c *library.Claims, l database.Lesson) error {
	dir := l.OutputDir.String
	if !library.IsLessonFolder(dir) {
		return fmt.Errorf("lesson %d records the folder %q, which is not named like a lesson folder; refusing to remove it", l.RailcontentID, dir)
	}
	if ids := c.Holds(dir, l.RailcontentID); len(ids) > 0 {
		return fmt.Errorf("lesson %d records the folder %q, which holds files lessons %v record; refusing to remove it", l.RailcontentID, dir, ids)
	}
	return library.Remove(s.roots(), dir)
}

// deleteLessonFiles removes lesson l's files (as read by BeginLessonDelete or
// BeginFollowDelete: no download can record anything for it until the delete
// ends) and finishes its row:
//   - everything removed: the row is tombstoned (TombstoneLesson);
//   - something could not be removed: the row records only what is left and
//     reads 'downloaded' (KeepLessonFiles), the detail is logged, and
//     errFilesKept is returned;
//   - the row no longer records what was read (a defence: nothing should
//     change it meanwhile): nothing is written and database.ErrLessonChanged
//     is returned;
//   - the row could not be written: the detail is logged and
//     errRecordNotUpdated returned (a wrapped sql.ErrNoRows passes through).
//
// c holds what every lesson row with files claims, for the ownership checks.
func (s *Server) deleteLessonFiles(ctx context.Context, c *library.Claims, l database.Lesson) error {
	kept, err := s.removeLessonFiles(c, l)
	var werr error
	if err == nil {
		werr = s.store.TombstoneLesson(ctx, l)
	} else {
		fmt.Fprintf(logOut, "drumdrop: delete lesson %d: %v\n", l.RailcontentID, err)
		werr = s.store.KeepLessonFiles(ctx, l, kept)
	}
	switch {
	case errors.Is(werr, database.ErrLessonChanged):
		fmt.Fprintf(logOut, "drumdrop: delete lesson %d: %v\n", l.RailcontentID, werr)
		return werr
	case werr != nil && !isNotFound(werr):
		fmt.Fprintf(logOut, "drumdrop: delete lesson %d: %v\n", l.RailcontentID, werr)
		return errRecordNotUpdated
	case werr != nil:
		return werr
	case err != nil:
		return errFilesKept
	}
	return nil
}

// claims indexes what every lesson row with files claims in the library, for
// the ownership checks of one delete. A follow delete builds it once and
// forgets each lesson it tombstones. A damaged record anywhere refuses the
// delete (errFilesKept, detail logged): no ownership can be decided.
func (s *Server) claims(ctx context.Context) (*library.Claims, error) {
	others, err := s.store.ListLessonsWithFiles(ctx)
	if err != nil {
		return nil, err
	}
	c, err := library.NewClaims(s.cfg.LibraryDir, others)
	if err != nil {
		fmt.Fprintf(logOut, "drumdrop: delete: %v\n", err)
		return nil, errFilesKept
	}
	return c, nil
}

// endDelete ends the deletes of lessons ids, whatever happened (see
// database.Store.EndLessonDelete), logging a failure.
func (s *Server) endDelete(ctx context.Context, ids ...int) {
	if err := s.store.EndLessonDelete(ctx, ids...); err != nil {
		fmt.Fprintf(logOut, "drumdrop: end delete of lessons %v: %v\n", ids, err)
	}
}

// killRunning kills the processes of jobs a delete just removed, so their
// downloads stop (their writes would land nowhere anyway). nil-safe when no
// worker is attached.
func (s *Server) killRunning(jobIDs []int64) {
	if s.deps.CancelRunning == nil {
		return
	}
	for _, id := range jobIDs {
		s.deps.CancelRunning(id)
	}
}
