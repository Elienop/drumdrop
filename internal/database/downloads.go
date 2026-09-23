package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrDownloadAbandoned is returned by the worker's guarded writes (StartDownload,
// ConfirmDownload, FinishDownload, FailDownload, SkipDownload, CancelDownload)
// when the job or its lesson no longer exists: a delete removed them while the
// download was running. Nothing was written, and nothing of that download may
// be recorded. Whether what it wrote must be removed is what the delete wanted:
// the error also matches ErrDiscardDownload when it removes the lesson's files.
var ErrDownloadAbandoned = errors.New("download abandoned: its job or lesson was deleted")

// ErrDiscardDownload is joined to ErrDownloadAbandoned when the delete that
// abandoned the download removes the lesson's files (a lesson delete, or a
// follow removed with its files), so the worker must remove what the download
// wrote too. Without it the delete keeps files (a follow removed without its
// files), or its intent is unknown, and the worker removes nothing.
var ErrDiscardDownload = errors.New("the delete removes the lesson's files")

// ErrDownloadCanceled is returned by StartDownload and ConfirmDownload when the
// job still exists but is no longer running: it was canceled (in the database,
// before its process was registered, or by another process). Nothing was
// written; the worker must stop and record the cancel (CancelDownload).
var ErrDownloadCanceled = errors.New("download canceled")

// ErrLessonDeleting is returned when a lesson's files are being deleted right
// now: a job can not be enqueued or retried for it (EnqueueJob, RetryJob), and
// a second delete can not start (BeginLessonDelete, BeginFollowDelete,
// RemoveFollowCascade). Nothing was written.
var ErrLessonDeleting = errors.New("the lesson's files are being deleted")

// ErrLessonChanged is returned by TombstoneLesson and KeepLessonFiles when the
// lesson's recorded files are no longer the ones the caller read: something
// recorded new ones in between. Nothing was written. A delete blocks every
// download of the lesson while it runs (ErrLessonDeleting), so this is a
// defence, not an expected outcome.
var ErrLessonChanged = errors.New("lesson changed while it was being deleted")

// ErrFollowHasFiles is returned by RemoveFilelessFollowCascade when a lesson of
// the follow still has recorded files, so deleting its row would leave them
// untracked. Nothing was deleted.
var ErrFollowHasFiles = errors.New("follow still has lessons with recorded files")

// hasFilesSQL is Lesson.HasFiles as a condition on the lessons table. Keep the
// two in step: the API's has_files, a delete's "does this lesson have files to
// act on" and the store's "rows with files" must never disagree.
const hasFilesSQL = `(output_dir IS NOT NULL OR (library_entries IS NOT NULL AND library_entries <> '[]'))`

// HasFiles reports whether the row records files on disk a delete would act on:
// an output_dir, or a library record that is not empty (a damaged one counts,
// so a delete reaches it and refuses). It is the one predicate behind the
// API's has_files (see hasFilesSQL).
func (l Lesson) HasFiles() bool {
	return l.OutputDir.Valid || l.LibraryEntries.Valid && l.LibraryEntries.String != "[]"
}

// PlacedEntries decodes the lesson's library_entries record: the entries the
// plex-tv move placed in a season folder for it, each relative to the library
// folder ("<show>/Season NN/<name>"; internal/library checks and resolves
// them). recorded is false when the column is NULL (no record: a
// default-layout lesson, or one moved before the record existed). A value that
// is not a JSON array of non-empty strings is an error, never an empty record,
// so a damaged row can not pass for "owns nothing".
func (l Lesson) PlacedEntries() (paths []string, recorded bool, err error) {
	if !l.LibraryEntries.Valid {
		return nil, false, nil
	}
	if err := json.Unmarshal([]byte(l.LibraryEntries.String), &paths); err != nil {
		return nil, true, fmt.Errorf("lesson %d: library_entries is not a JSON list of paths: %w", l.RailcontentID, err)
	}
	if paths == nil {
		return nil, true, fmt.Errorf("lesson %d: library_entries is null inside the JSON", l.RailcontentID)
	}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return nil, true, fmt.Errorf("lesson %d: library_entries holds an empty path", l.RailcontentID)
		}
	}
	return paths, true, nil
}

// EncodeLibraryEntries is the library_entries value for paths: NULL for a nil
// slice (no record), the JSON array otherwise ("[]" for an empty, non-nil one).
func EncodeLibraryEntries(paths []string) sql.NullString {
	if paths == nil {
		return sql.NullString{}
	}
	b, err := json.Marshal(paths)
	if err != nil {
		// A []string always marshals; keep the signature error-free.
		panic(fmt.Sprintf("marshal library entries: %v", err))
	}
	return sql.NullString{String: string(b), Valid: true}
}

// DownloadRecord is what FinishDownload records for a finished download.
// LibraryEntries nil leaves the lesson's library record as it is (a download
// that did not move into a season folder: whatever the lesson recorded there is
// still on disk and still its own); non-nil (even empty) records exactly those
// entries.
type DownloadRecord struct {
	Quality        string
	OutputDir      string
	VideoPath      string
	Bytes          int64
	LibraryEntries []string
}

// withLiveJob runs fn in one transaction, but only while job jobID for lesson
// id and the lesson row both still exist and the job's status is one of
// statuses. A job or lesson that is gone yields ErrDownloadAbandoned (joined
// with ErrDiscardDownload when the delete that removed it removes the lesson's
// files, see abandoned_jobs); a job in another status yields
// ErrDownloadCanceled. Either way nothing is written. Every write the worker
// makes about a job goes through it, and a delete removes the lesson's jobs
// first, so no step of a download that started before a delete lands after it.
func (s *Store) withLiveJob(ctx context.Context, jobID int64, id int, statuses []string, fn func(*sql.Tx) error) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var status string
		err := tx.QueryRowContext(ctx,
			`SELECT status FROM jobs WHERE id = ? AND railcontent_id = ?`, jobID, id,
		).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return abandonedTx(ctx, tx, jobID, id)
		}
		if err != nil {
			return fmt.Errorf("check job %d: %w", jobID, err)
		}
		var lessons int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM lessons WHERE railcontent_id = ?`, id,
		).Scan(&lessons); err != nil {
			return fmt.Errorf("check lesson %d: %w", id, err)
		}
		if lessons == 0 {
			return abandonedTx(ctx, tx, jobID, id)
		}
		if !slices.Contains(statuses, status) {
			return fmt.Errorf("job %d is %s: %w", jobID, status, ErrDownloadCanceled)
		}
		return fn(tx)
	})
}

// abandonedTx is the error for a job a delete removed: ErrDownloadAbandoned,
// joined with ErrDiscardDownload only when the delete recorded that it removes
// the lesson's files. An unknown or unreadable intent keeps the files: nothing
// is removed without proof the delete wanted it.
func abandonedTx(ctx context.Context, tx *sql.Tx, jobID int64, id int) error {
	var discard int
	err := tx.QueryRowContext(ctx,
		`SELECT discard FROM abandoned_jobs WHERE job_id = ?`, jobID,
	).Scan(&discard)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("job %d, lesson %d: %w", jobID, id, ErrDownloadAbandoned)
	case err != nil:
		return fmt.Errorf("job %d, lesson %d: %w (what the delete wanted could not be read, so its files are kept: %v)", jobID, id, ErrDownloadAbandoned, err)
	case discard == 1:
		return fmt.Errorf("job %d, lesson %d: %w: %w", jobID, id, ErrDownloadAbandoned, ErrDiscardDownload)
	}
	return fmt.Errorf("job %d, lesson %d: %w", jobID, id, ErrDownloadAbandoned)
}

// runningOnly and runningOrCanceled are the job statuses a guarded write
// accepts. A download may start or go on only while its job is running; once
// it has placed its files, a cancel that landed meanwhile is too late, and the
// download is recorded (and the job closed) rather than left untracked.
var (
	runningOnly       = []string{JobRunning}
	runningOrCanceled = []string{JobRunning, JobCanceled}
)

// StartDownload marks the lesson 'downloading' at the start of each attempt of
// job jobID. It lands only while the job is running and it and its lesson
// exist (ErrDownloadAbandoned or ErrDownloadCanceled otherwise).
func (s *Store) StartDownload(ctx context.Context, jobID int64, id int) error {
	return s.withLiveJob(ctx, jobID, id, runningOnly, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE railcontent_id = ?`,
			StatusDownloading, id,
		); err != nil {
			return fmt.Errorf("mark lesson %d downloading: %w", id, err)
		}
		return nil
	})
}

// ConfirmDownload checks, once a download has finished and before its files
// are moved anywhere, that job jobID may still record it: it is running and it
// and its lesson exist (ErrDownloadAbandoned or ErrDownloadCanceled
// otherwise). It writes nothing.
func (s *Store) ConfirmDownload(ctx context.Context, jobID int64, id int) error {
	return s.withLiveJob(ctx, jobID, id, runningOnly, func(*sql.Tx) error { return nil })
}

// FinishDownload records a successful download and closes its job, in one
// transaction: the lesson becomes 'downloaded' with rec's quality, paths, byte
// count and library entries (see DownloadRecord; error cleared, downloaded_at
// stamped), and the job becomes 'done'. A job canceled after ConfirmDownload
// passed is recorded too: its files are already in place. It lands only while
// the job and lesson still exist (ErrDownloadAbandoned otherwise).
func (s *Store) FinishDownload(ctx context.Context, jobID int64, id int, rec DownloadRecord) error {
	return s.withLiveJob(ctx, jobID, id, runningOrCanceled, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons
			    SET status = ?,
			        quality = ?,
			        output_dir = ?,
			        video_path = ?,
			        bytes = ?,
			        library_entries = CASE WHEN ? THEN library_entries ELSE ? END,
			        error = NULL,
			        downloaded_at = CURRENT_TIMESTAMP,
			        updated_at = CURRENT_TIMESTAMP
			  WHERE railcontent_id = ?`,
			StatusDownloaded, rec.Quality, rec.OutputDir, rec.VideoPath, rec.Bytes,
			rec.LibraryEntries == nil, EncodeLibraryEntries(rec.LibraryEntries), id,
		); err != nil {
			return fmt.Errorf("mark lesson %d downloaded: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ?`,
			JobDone, jobID,
		); err != nil {
			return fmt.Errorf("mark job %d done: %w", jobID, err)
		}
		return nil
	})
}

// FailDownload records a download that failed every attempt: the lesson
// becomes 'failed' with msg, and the job 'failed' with msg unless it was
// canceled meanwhile, in one transaction. It lands only while the job and
// lesson still exist (ErrDownloadAbandoned otherwise).
func (s *Store) FailDownload(ctx context.Context, jobID int64, id int, msg string) error {
	return s.finishWith(ctx, jobID, id, StatusFailed, msg, JobFailed)
}

// SkipDownload records a lesson that could not be resolved (gated or missing):
// the lesson becomes 'skipped' with reason, and the job 'failed' with reason
// unless it was canceled meanwhile, in one transaction, only while both still
// exist (ErrDownloadAbandoned otherwise).
func (s *Store) SkipDownload(ctx context.Context, jobID int64, id int, reason string) error {
	return s.finishWith(ctx, jobID, id, StatusSkipped, reason, JobFailed)
}

// finishWith is the shared body of FailDownload and SkipDownload.
func (s *Store) finishWith(ctx context.Context, jobID int64, id int, lessonStatus, msg, jobStatus string) error {
	return s.withLiveJob(ctx, jobID, id, runningOrCanceled, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE railcontent_id = ?`,
			lessonStatus, msg, id,
		); err != nil {
			return fmt.Errorf("mark lesson %d %s: %w", id, lessonStatus, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, error = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?`,
			jobStatus, msg, jobID, JobRunning,
		); err != nil {
			return fmt.Errorf("mark job %d %s: %w", jobID, jobStatus, err)
		}
		return nil
	})
}

// endDownloadSQL is what a download that ended without recording anything
// leaves on its lesson (a cancel, or a delete that removed its job): a lesson
// that still records files from an earlier download reads 'downloaded' (its
// files are there, and a 'skipped' lesson is never downloaded again), any
// other 'skipped' with error 'canceled'. Its one argument is the lesson id.
const endDownloadSQL = `UPDATE lessons
	    SET status = CASE WHEN ` + hasFilesSQL + ` THEN '` + StatusDownloaded + `' ELSE '` + StatusSkipped + `' END,
	        error = CASE WHEN ` + hasFilesSQL + ` THEN NULL ELSE 'canceled' END,
	        updated_at = CURRENT_TIMESTAMP
	  WHERE railcontent_id = ?`

// CancelDownload records a download killed by a cancel: the lesson as
// endDownloadSQL leaves it, and the job 'canceled' if it is still running (a
// job the API already canceled is left as it is). It lands only while the job
// and lesson still exist (ErrDownloadAbandoned otherwise).
func (s *Store) CancelDownload(ctx context.Context, jobID int64, id int) error {
	return s.withLiveJob(ctx, jobID, id, runningOrCanceled, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, endDownloadSQL, id); err != nil {
			return fmt.Errorf("mark lesson %d canceled: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?`,
			JobCanceled, jobID, JobRunning,
		); err != nil {
			return fmt.Errorf("mark job %d canceled: %w", jobID, err)
		}
		return nil
	})
}

// ListLessonsWithFiles returns every lesson row that records files on disk
// (HasFiles), whatever its status, ordered by railcontent_id. A move and a
// delete read it to learn which entries of a shared season folder other
// lessons claim.
func (s *Store) ListLessonsWithFiles(ctx context.Context) ([]Lesson, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+lessonColumns+` FROM lessons WHERE `+hasFilesSQL+` ORDER BY railcontent_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list lessons with files: %w", err)
	}
	return scanLessons(rows)
}

// BeginLessonDelete starts deleting a lesson's files. In one transaction it
// reads the lesson (a wrapped sql.ErrNoRows if unknown), refuses with
// ErrLessonDeleting if a delete of it is already running, marks it deleting (no
// job can be enqueued or retried for it until EndLessonDelete, TombstoneLesson
// or KeepLessonFiles), and removes every job of it that is queued, running or
// canceled (removeActiveJobsTx), recording that the files go. So once the
// delete answers, no step of a download already under way can record
// anything, and no new one can start. It returns the lesson as it is then,
// whose recorded files the caller removes, and the ids of the jobs whose
// processes the caller should kill.
func (s *Store) BeginLessonDelete(ctx context.Context, id int) (Lesson, []int64, error) {
	var (
		l       Lesson
		running []int64
	)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		if l, err = getLessonTx(ctx, tx, id); err != nil {
			return err
		}
		if l.Deleting {
			return fmt.Errorf("lesson %d: %w", id, ErrLessonDeleting)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE lessons SET deleting = 1 WHERE railcontent_id = ?`, id); err != nil {
			return fmt.Errorf("mark lesson %d deleting: %w", id, err)
		}
		if running, err = removeActiveJobsTx(ctx, tx, true, `railcontent_id = ?`, id); err != nil {
			return err
		}
		l, err = getLessonTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return Lesson{}, nil, err
	}
	return l, running, nil
}

// BeginFollowDelete is BeginLessonDelete for every lesson of a follow that is
// being removed with its files: in one transaction it refuses with
// ErrLessonDeleting if any of them is being deleted already, marks them all
// deleting, and removes every queued, running or canceled job of them. It
// returns the lessons as they are then and the ids of the jobs to kill. Jobs
// the follow queued for another follow's lessons are not touched.
func (s *Store) BeginFollowDelete(ctx context.Context, followID int64) ([]Lesson, []int64, error) {
	var (
		lessons []Lesson
		running []int64
	)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := refuseDeletingTx(ctx, tx, followID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE lessons SET deleting = 1 WHERE follow_id = ?`, followID); err != nil {
			return fmt.Errorf("mark lessons of follow %d deleting: %w", followID, err)
		}
		var err error
		if running, err = removeActiveJobsTx(ctx, tx, true, followLessonsClause, followID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT `+lessonColumns+` FROM lessons WHERE follow_id = ? ORDER BY railcontent_id`, followID,
		)
		if err != nil {
			return fmt.Errorf("list lessons of follow %d: %w", followID, err)
		}
		lessons, err = scanLessons(rows)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return lessons, running, nil
}

// followLessonsClause matches the jobs of a follow's own lessons (its one
// argument is the follow id).
const followLessonsClause = `railcontent_id IN (SELECT railcontent_id FROM lessons WHERE follow_id = ?)`

// refuseDeletingTx returns ErrLessonDeleting if any lesson of the follow is
// being deleted right now.
func refuseDeletingTx(ctx context.Context, tx *sql.Tx, followID int64) error {
	var deleting int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM lessons WHERE follow_id = ? AND deleting = 1`, followID,
	).Scan(&deleting); err != nil {
		return fmt.Errorf("check lessons of follow %d: %w", followID, err)
	}
	if deleting > 0 {
		return fmt.Errorf("follow %d: %d lessons: %w", followID, deleting, ErrLessonDeleting)
	}
	return nil
}

// EndLessonDelete ends the deletes of lessons ids: they may be downloaded
// again. TombstoneLesson and KeepLessonFiles end it themselves; a delete calls
// this too, whatever happened, so a failure between them never leaves a lesson
// that can not be downloaded. An unknown id is not an error.
func (s *Store) EndLessonDelete(ctx context.Context, ids ...int) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx,
				`UPDATE lessons SET deleting = 0 WHERE railcontent_id = ? AND deleting = 1`, id,
			); err != nil {
				return fmt.Errorf("end delete of lesson %d: %w", id, err)
			}
		}
		return nil
	})
}

// ClearStaleDeletes ends every delete still marked on a lesson, and returns how
// many it ended. The daemon calls it once at startup, like
// RequeueStaleRunning: a delete runs inside one request of this process, so a
// mark left at startup was left by a process that died mid-delete.
func (s *Store) ClearStaleDeletes(ctx context.Context) (int, error) {
	var n int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE lessons SET deleting = 0 WHERE deleting = 1`)
		if err != nil {
			return fmt.Errorf("clear stale deletes: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected clearing stale deletes: %w", err)
		}
		n = int(affected)
		return nil
	})
	return n, err
}

// removeActiveJobsTx deletes the queued, running and canceled jobs matching
// where (a condition on the jobs table), and returns the ids of the running
// and canceled ones, whose processes may still be going. For those it records
// what the delete wants (abandoned_jobs: discard, or keep the files), which the
// worker reads when its next write finds the job gone. A canceled job is
// removed too: its worker may not have finished yet, and must not record after
// the delete. Any of their lessons still 'downloading' is left as
// endDownloadSQL says.
func removeActiveJobsTx(ctx context.Context, tx *sql.Tx, discard bool, where string, args ...any) ([]int64, error) {
	cond := `status IN ('` + JobQueued + `', '` + JobRunning + `', '` + JobCanceled + `') AND ` + where
	rows, err := tx.QueryContext(ctx, `SELECT id, railcontent_id, status FROM jobs WHERE `+cond, args...)
	if err != nil {
		return nil, fmt.Errorf("list active jobs: %w", err)
	}
	var (
		inFlight []int64
		lessons  []int
	)
	for rows.Next() {
		var (
			id     int64
			rcID   int
			status string
		)
		if err := rows.Scan(&id, &rcID, &status); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan active job: %w", err)
		}
		if status != JobQueued {
			inFlight = append(inFlight, id)
		}
		lessons = append(lessons, rcID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close active jobs: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active jobs: %w", err)
	}
	for _, id := range inFlight {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO abandoned_jobs(job_id, discard) VALUES(?, ?)`, id, discard,
		); err != nil {
			return nil, fmt.Errorf("record what the delete wants for job %d: %w", id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE `+cond, args...); err != nil {
		return nil, fmt.Errorf("delete active jobs: %w", err)
	}
	for _, rcID := range lessons {
		if _, err := tx.ExecContext(ctx, endDownloadSQL+` AND status = '`+StatusDownloading+`'`, rcID); err != nil {
			return nil, fmt.Errorf("end the download of lesson %d: %w", rcID, err)
		}
	}
	return inFlight, nil
}

// sameFilesClause matches a lesson row whose recorded files are still exactly
// before's (compare-and-swap for the delete's final write): each column is
// compared on its own, so a change to any one of them is caught.
const sameFilesClause = `railcontent_id = ? AND output_dir IS ? AND video_path IS ? AND library_entries IS ?`

func sameFilesArgs(before Lesson) []any {
	return []any{before.RailcontentID, before.OutputDir, before.VideoPath, before.LibraryEntries}
}

// TombstoneLesson finishes a delete whose files were all removed: status
// 'skipped', error 'deleted', every recorded path cleared, and the delete
// ended. It writes only if the lesson still records exactly before's files;
// otherwise it writes nothing and returns ErrLessonChanged, so whatever it
// records now stays tracked.
func (s *Store) TombstoneLesson(ctx context.Context, before Lesson) error {
	return s.casLesson(ctx, before,
		`UPDATE lessons
		    SET status = ?, error = 'deleted', deleting = 0,
		        output_dir = NULL, video_path = NULL, bytes = NULL, library_entries = NULL,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE `+sameFilesClause,
		append([]any{StatusSkipped}, sameFilesArgs(before)...)...)
}

// KeptFiles is what a delete that could not remove everything leaves a lesson
// recording: only the files still on disk.
type KeptFiles struct {
	// OutputDir keeps output_dir (its folder, or the season folder of entries
	// still recorded, is still there).
	OutputDir bool
	// VideoPath keeps video_path and bytes (the video is still there).
	VideoPath bool
	// LibraryEntries becomes the library record; nil leaves it as it is.
	LibraryEntries []string
}

// KeepLessonFiles finishes a delete that could not remove everything: the
// lesson reads 'downloaded' (error cleared), records only what kept says is
// still on disk, and the delete ends. Like TombstoneLesson it writes only if
// the lesson still records exactly before's files (ErrLessonChanged otherwise).
func (s *Store) KeepLessonFiles(ctx context.Context, before Lesson, kept KeptFiles) error {
	return s.casLesson(ctx, before,
		`UPDATE lessons
		    SET status = ?, error = NULL, deleting = 0,
		        output_dir = CASE WHEN ? THEN output_dir END,
		        video_path = CASE WHEN ? THEN video_path END,
		        bytes = CASE WHEN ? THEN bytes END,
		        library_entries = CASE WHEN ? THEN library_entries ELSE ? END,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE `+sameFilesClause,
		append([]any{StatusDownloaded, kept.OutputDir, kept.VideoPath, kept.VideoPath,
			kept.LibraryEntries == nil, EncodeLibraryEntries(kept.LibraryEntries)},
			sameFilesArgs(before)...)...)
}

// casLesson runs a compare-and-swap UPDATE on one lesson row and turns "no row
// matched" into ErrLessonChanged (the row exists but moved on) or a wrapped
// sql.ErrNoRows (the row is gone).
func (s *Store) casLesson(ctx context.Context, before Lesson, query string, args ...any) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("update lesson %d: %w", before.RailcontentID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected updating lesson %d: %w", before.RailcontentID, err)
		}
		if n == 1 {
			return nil
		}
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM lessons WHERE railcontent_id = ?`, before.RailcontentID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check lesson %d: %w", before.RailcontentID, err)
		}
		if exists == 0 {
			return fmt.Errorf("lesson %d: %w", before.RailcontentID, sql.ErrNoRows)
		}
		return fmt.Errorf("lesson %d: %w", before.RailcontentID, ErrLessonChanged)
	})
}

// RemoveFilelessFollowCascade is RemoveFollowCascade for a delete that removed
// the follow's files: it deletes nothing, and returns ErrFollowHasFiles, if any
// lesson of the follow still records files (one whose removal failed, or one a
// download recorded in the meantime), so no file is left untracked. Jobs it
// removes that may still be running are recorded as discarding what they
// wrote; it returns their ids, to kill.
func (s *Store) RemoveFilelessFollowCascade(ctx context.Context, id int64) ([]int64, error) {
	var running []int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var withFiles int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM lessons WHERE follow_id = ? AND `+hasFilesSQL, id,
		).Scan(&withFiles); err != nil {
			return fmt.Errorf("count lessons with files for follow %d: %w", id, err)
		}
		if withFiles > 0 {
			return fmt.Errorf("follow %d: %d lessons: %w", id, withFiles, ErrFollowHasFiles)
		}
		var err error
		running, err = removeFollowCascadeTx(ctx, tx, id, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	return running, nil
}
