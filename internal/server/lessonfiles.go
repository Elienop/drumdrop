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
// On failure it returns an error with every detail, and remaining: the recorded
// entries still on disk (relative, as recorded), to become the lesson's record
// (nil when the record should stay as it is).
func (s *Server) removeLessonFiles(c *library.Claims, l database.Lesson) (remaining []string, err error) {
	plan, err := c.Plan(l)
	if err != nil {
		return nil, err
	}
	for _, p := range plan.Kept {
		fmt.Fprintf(logOut, "drumdrop: delete lesson %d: kept %q, which another lesson also claims\n", l.RailcontentID, p)
	}
	_, recorded, _ := l.PlacedEntries() // Plan already refused a damaged record
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
		return nil, nil
	}
	if recorded || len(plan.Remove) > 0 || len(plan.Kept) > 0 {
		if remaining, err = library.EntriesFor(s.cfg.LibraryDir, left); err != nil {
			return nil, errors.Join(append(errs, err)...)
		}
	}
	return remaining, errors.Join(errs...)
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
// BeginFollowDelete, so no earlier download can record anything afterwards)
// and finishes its row:
//   - everything removed: the row is tombstoned (TombstoneLesson);
//   - something could not be removed: the row keeps its paths, its record
//     narrows to the entries still on disk (KeepLessonFiles), the detail is
//     logged, and errFilesKept is returned;
//   - a later download recorded new files meanwhile: nothing is written and
//     database.ErrLessonChanged is returned (those new files stay tracked).
//
// c holds what every lesson row with files claims, for the ownership checks.
func (s *Server) deleteLessonFiles(ctx context.Context, c *library.Claims, l database.Lesson) error {
	remaining, err := s.removeLessonFiles(c, l)
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

// claims indexes what every lesson row with files claims in the library, for
// the ownership checks of one delete. A follow delete builds it once and
// forgets each lesson it tombstones.
func (s *Server) claims(ctx context.Context) (*library.Claims, error) {
	others, err := s.store.ListLessonsWithFiles(ctx)
	if err != nil {
		return nil, err
	}
	c, err := library.NewClaims(s.cfg.LibraryDir, others)
	if err != nil {
		// A damaged record: no delete may decide anything until it is fixed.
		fmt.Fprintf(logOut, "drumdrop: delete: %v\n", err)
		return nil, errFilesKept
	}
	return c, nil
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
