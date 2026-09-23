package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sync"
	"time"

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

// errNoClaims is the delete outcome "what the other lessons claim could not
// be read (a store error, or a damaged record)": nothing was removed. Its
// detail is logged.
var errNoClaims = errors.New("the other lessons' files could not be read")

// roots are the folders a delete may remove files under: the downloads and
// the library folders.
func (s *Server) roots() []string {
	return library.Roots(s.cfg.DownloadsDir, s.cfg.LibraryDir)
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
// refused. A missing path inside a root is already gone; one outside every
// root is refused too, since it may have moved with a folder mounted elsewhere
// (the row then keeps naming it, see keptFiles). The trade-off: a symlinked
// folder that an operator placed inside the library on purpose is refused too.
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
	folderKept := false
	if l.OutputDir.Valid && !library.IsSeasonDir(l.OutputDir.String) {
		if rerr := c.RemoveLessonFolder(s.roots(), l.OutputDir.String, l.RailcontentID); rerr != nil {
			folderKept = true
			errs = append(errs, fmt.Errorf("lesson %d: %w", l.RailcontentID, rerr))
		}
	}
	if len(errs) == 0 {
		return database.KeptFiles{}, nil
	}
	kept, kerr := s.keptFiles(l, plan, left, folderKept)
	return kept, errors.Join(append(errs, kerr)...)
}

// keptFiles is what lesson l goes on recording after a removal that failed
// partway: left are the planned library entries still on disk, and folderKept
// says its own folder was refused or could not be removed.
//   - its library record narrows to left (a lesson moved before the record
//     existed gains a record of exactly those); one that had neither a
//     record nor planned entries keeps none;
//   - output_dir stays while what it names is still there: a season folder
//     while entries of the lesson remain in it; its own folder while it
//     exists, and also when its removal was refused while it is not where the
//     row says (a folder outside every root, as after the library moved: the
//     record is how the files can still be found);
//   - video_path (and its size) stays while the video exists, and in that
//     same refused-and-unseen case.
//
// A path whose existence can not be read counts as still there.
func (s *Server) keptFiles(l database.Lesson, plan library.Entries, left []string, folderKept bool) (database.KeptFiles, error) {
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
	// unseen: the folder's removal was refused and it is not where the row
	// says (a library moved since, say): the row is how its files can still
	// be found, so it keeps naming them.
	unseen := false
	if l.OutputDir.Valid {
		if library.IsSeasonDir(l.OutputDir.String) {
			kept.OutputDir = len(left) > 0
		} else {
			there := stillThere(l.OutputDir.String)
			unseen = folderKept && !there
			kept.OutputDir = there || unseen
		}
	}
	kept.VideoPath = l.VideoPath.Valid && l.VideoPath.String != "" && (unseen || stillThere(l.VideoPath.String))
	return kept, nil
}

// stillThere reports whether path may still exist: anything but "not found".
func stillThere(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
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
//     errRecordNotUpdated returned (a wrapped sql.ErrNoRows passes through:
//     the row is gone).
//
// A tombstone or keep that was written also ended the lesson's lease, so it
// is dropped from hold: another delete may take the lesson from then on, and
// this one must not end that delete's lease (security LOW-1).
//
// c holds what every lesson row with files claims, for the ownership checks.
func (s *Server) deleteLessonFiles(ctx context.Context, c *library.Claims, hold *deleteHold, l database.Lesson) error {
	kept, err := s.removeLessonFiles(c, l)
	var werr error
	if err == nil {
		werr = s.store.TombstoneLesson(ctx, l)
	} else {
		fmt.Fprintf(logOut, "drumdrop: delete lesson %d: %v\n", l.RailcontentID, err)
		werr = s.store.KeepLessonFiles(ctx, l, kept)
	}
	if werr == nil {
		hold.released(l.RailcontentID)
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
// forgets each lesson it tombstones. When the rows can not be read (a store
// error) or a record anywhere is damaged, no ownership can be decided: the
// delete removes nothing and answers errNoClaims (detail logged). A read that
// failed is never taken for "the other lessons own nothing".
func (s *Server) claims(ctx context.Context) (*library.Claims, error) {
	others, err := s.store.ListLessonsWithFiles(ctx)
	if err != nil {
		fmt.Fprintf(logOut, "drumdrop: delete: the other lessons' files could not be read: %v\n", err)
		return nil, errNoClaims
	}
	c, err := library.NewClaims(s.cfg.LibraryDir, others)
	if err != nil {
		fmt.Fprintf(logOut, "drumdrop: delete: %v\n", err)
		return nil, errNoClaims
	}
	return c, nil
}

// deleteRenewEvery is how often a running delete renews its lease on its
// lessons (database.DeleteRenewEvery); a variable so a test can shorten it.
var deleteRenewEvery = database.DeleteRenewEvery

// deleteHold is a running delete's hold on the lessons whose lease it still
// holds (holdDelete).
type deleteHold struct {
	mu   sync.Mutex
	ids  []int
	stop chan struct{}
	done chan struct{}
}

// released drops lesson id from the hold: the delete's own tombstone or keep
// ended its lease, so the lesson is free and a later delete may take it. The
// hold neither renews nor ends that lesson's lease from then on. A renewal
// already under way may still extend a new delete's lease once, which only
// lengthens a hold that delete keeps and ends itself anyway.
func (h *deleteHold) released(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ids = slices.DeleteFunc(h.ids, func(held int) bool { return held == id })
}

// held returns the lessons whose lease the hold still holds.
func (h *deleteHold) held() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.ids)
}

// holdDelete keeps the delete of lessons ids (begun by BeginLessonDelete or
// BeginFollowDelete) holding them for as long as it runs: it renews their
// lease every deleteRenewEvery, so however long the removal takes, the
// lessons stay refused to downloads and to a second delete, while a delete
// the process died in lapses by itself (database.DeleteLease). A lesson the
// delete finished (deleteLessonFiles) leaves the hold. endDelete ends it.
// Failures are logged.
func (s *Server) holdDelete(ctx context.Context, ids ...int) *deleteHold {
	h := &deleteHold{ids: slices.Clone(ids), stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(h.done)
		t := time.NewTicker(deleteRenewEvery)
		defer t.Stop()
		for {
			select {
			case <-h.stop:
				return
			case <-t.C:
				held := h.held()
				if len(held) == 0 {
					continue
				}
				if err := s.store.RenewLessonDelete(ctx, held...); err != nil {
					fmt.Fprintf(logOut, "drumdrop: renew the delete of lessons %v: %v\n", held, err)
				}
			}
		}
	}()
	return h
}

// endDelete ends hold h, whatever happened: it stops the renewals, waits for
// one in flight, and ends the deletes of the lessons h still holds
// (EndLessonDelete). Failures are logged.
func (s *Server) endDelete(ctx context.Context, h *deleteHold) {
	close(h.stop)
	<-h.done
	held := h.held()
	if len(held) == 0 {
		return
	}
	if err := s.store.EndLessonDelete(ctx, held...); err != nil {
		fmt.Fprintf(logOut, "drumdrop: end delete of lessons %v: %v\n", held, err)
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
