package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/elienop/drumdrop/internal/database"
	"github.com/elienop/drumdrop/internal/library"
)

// logOut receives the server's own diagnostics (the detail behind a fixed
// client message). A package variable so tests can capture it; the server logs
// no requests, so nothing here ever holds the API token.
var logOut io.Writer = os.Stderr

// errFilesKept is the delete outcome "some of the lesson's files could not be
// removed; the lesson keeps its record of them". Its detail is logged, never
// sent to the client (hard rule 11).
var errFilesKept = errors.New("the lesson's files could not all be removed")

// removeLessonFiles removes every file lesson l records. What it removes follows
// what was recorded, never today's DRUMDROP_LAYOUT, so a layout switch can
// never turn a one-episode delete into a season wipe:
//   - the library entries the lesson owns in plex-tv season folders
//     (library.PlanLessonEntries): exactly its record, or for a lesson moved
//     before the record existed, its name-matched episode entries. An entry
//     another lesson also claims is kept and logged, never removed. A season
//     folder itself is never removed;
//   - its own folder, when output_dir is not a season folder (the default
//     layout, or a plex-tv lesson recorded in downloads): removed whole.
//
// Every removal goes through library.RemoveUnderRoot, confined by os.Root to
// the downloads or library root it sits inside: a path outside both, a root
// itself, or a path reached through a symlinked folder that leads outside is
// refused. A missing path is already gone. The trade-off: a symlinked folder
// that an operator placed inside the library on purpose is refused too.
//
// On failure it returns an error with every detail, and remaining: the recorded
// entries still on disk, to become the lesson's record (nil when the record
// should stay as it is).
func removeLessonFiles(downloadsDir, libraryDir string, l database.Lesson, others []database.Lesson) (remaining []string, err error) {
	var roots []string
	for _, r := range []string{downloadsDir, libraryDir} {
		if r != "" {
			roots = append(roots, r)
		}
	}
	plan, err := library.PlanLessonEntries(l, others)
	if err != nil {
		return nil, err
	}
	for _, p := range plan.Kept {
		fmt.Fprintf(logOut, "drumdrop: delete lesson %d: kept %q, which another lesson also claims\n", l.RailcontentID, p)
	}
	_, recorded, _ := l.PlacedEntries() // PlanLessonEntries already refused a damaged record
	if recorded || len(plan.Remove) > 0 || len(plan.Kept) > 0 {
		remaining = []string{}
	}
	var errs []error
	for _, p := range plan.Remove {
		if rerr := library.RemoveUnderRoot(roots, p); rerr != nil {
			remaining = append(remaining, p)
			errs = append(errs, rerr)
		}
	}
	if l.OutputDir.Valid && !library.IsSeasonDir(l.OutputDir.String) {
		if rerr := library.RemoveUnderRoot(roots, l.OutputDir.String); rerr != nil {
			errs = append(errs, rerr)
		}
	}
	if len(errs) > 0 {
		return remaining, errors.Join(errs...)
	}
	return nil, nil
}

// deleteLessonFiles removes lesson l's files (as read by BeginLessonDelete or
// BeginFollowDelete, so no earlier download can record anything afterwards)
// and finishes its row:
//   - everything removed: the row is tombstoned (TombstoneLesson);
//   - something could not be removed: the row keeps its paths, its record
//     narrows to the entries still on disk (KeepLessonFiles), the detail is
//     logged, and errFilesKept is returned;
//   - a later download recorded new files meanwhile: nothing is written and
//     database.ErrLessonChanged is returned (those new files stay tracked).
//
// others is every lesson row with files, for the ownership checks.
func (s *Server) deleteLessonFiles(ctx context.Context, l database.Lesson, others []database.Lesson) error {
	remaining, err := removeLessonFiles(s.cfg.DownloadsDir, s.cfg.LibraryDir, l, others)
	if err == nil {
		return s.store.TombstoneLesson(ctx, l)
	}
	fmt.Fprintf(logOut, "drumdrop: delete lesson %d: %v\n", l.RailcontentID, err)
	if remaining != nil {
		if kerr := s.store.KeepLessonFiles(ctx, l, database.EncodeLibraryEntries(remaining)); kerr != nil {
			return kerr
		}
	}
	return errFilesKept
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
