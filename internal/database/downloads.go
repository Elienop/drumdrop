package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrDownloadAbandoned is returned by the worker's guarded writes (StartDownload,
// ConfirmDownload, FinishDownload, FailDownload, SkipDownload, CancelDownload)
// when the job or its lesson no longer exists: a delete or a skip removed them
// while the download was running. Nothing was written, and nothing of that
// download may be recorded: what it wrote is in its private folder, which goes
// whatever the stopper wanted. The error also matches ErrLessonDeleted when the
// stopper (abandoned_jobs) is deleting the lesson's own files.
var ErrDownloadAbandoned = errors.New("download abandoned: its job or lesson was removed")

// ErrLessonDeleted is joined to ErrDownloadAbandoned when the stopper is a
// delete of the lesson's own files (a lesson delete, or a follow removed with
// its files): a placement that is undone then does not put back the lesson's
// own earlier files it set aside, since the delete removes them. Without it (a
// skip, a follow removed keeping its files, or an unknown intent) the undo puts
// everything back.
var ErrLessonDeleted = errors.New("the lesson's own files are being deleted")

// The intents a stopper records for a job it removed (abandoned_jobs.intent).
const (
	intentKeep    = "keep"
	intentDiscard = "discard"
	intentDelete  = "delete"
)

// ErrDownloadCanceled is returned by StartDownload and ConfirmDownload when the
// job still exists but is no longer running: it was canceled (in the database,
// before its process was registered, or by another process). Nothing was
// written; the worker must stop and record the cancel (CancelDownload).
var ErrDownloadCanceled = errors.New("download canceled")

// ErrLessonDeleting is returned while a delete holds a lesson's files (see
// DeleteLease): a job can not be enqueued or retried for it (EnqueueJob,
// RetryJob), it can not be skipped (SkipLesson), and a second delete can not
// start (BeginLessonDelete, BeginFollowDelete, RemoveFollowCascade). Nothing
// was written.
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
// statuses. A job or lesson that is gone yields ErrDownloadAbandoned (with what
// the stopper wanted, see abandonedAnswer); a job in another status yields
// ErrDownloadCanceled. Either way nothing is written. Every write the worker
// makes about a job goes through it, and a delete or a skip removes the
// lesson's jobs first, so no step of a download that started before it lands
// after it.
func (s *Store) withLiveJob(ctx context.Context, jobID int64, id int, statuses []string, fn func(*sql.Tx) error) error {
	var abandoned error
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var status string
		err := tx.QueryRowContext(ctx,
			`SELECT status FROM jobs WHERE id = ? AND railcontent_id = ?`, jobID, id,
		).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			abandoned = abandonedAnswer(ctx, tx, jobID, id)
			return nil
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
			abandoned = abandonedAnswer(ctx, tx, jobID, id)
			return nil
		}
		if !slices.Contains(statuses, status) {
			return fmt.Errorf("job %d is %s: %w", jobID, status, ErrDownloadCanceled)
		}
		return fn(tx)
	})
	if err != nil || abandoned == nil {
		return err
	}
	return s.consumeAbandoned(ctx, jobID, abandoned)
}

// abandonedAnswer is the error for a job a stopper removed: ErrDownloadAbandoned,
// joined with ErrLessonDeleted when the stopper recorded that the lesson's own
// files are being deleted (a skip and a keep act alike, so their intents need
// no error of their own). The row must name the same job AND lesson. An
// unknown or unreadable intent keeps the files: nothing is removed without
// proof the stopper wanted it.
func abandonedAnswer(ctx context.Context, tx *sql.Tx, jobID int64, id int) error {
	var intent string
	err := tx.QueryRowContext(ctx,
		`SELECT intent FROM abandoned_jobs WHERE job_id = ? AND railcontent_id = ?`, jobID, id,
	).Scan(&intent)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("job %d, lesson %d: %w", jobID, id, ErrDownloadAbandoned)
	case err != nil:
		return fmt.Errorf("job %d, lesson %d: %w (what the stopper wanted could not be read, so its files are kept: %v)", jobID, id, ErrDownloadAbandoned, err)
	case intent == intentDelete:
		return fmt.Errorf("job %d, lesson %d: %w: %w", jobID, id, ErrDownloadAbandoned, ErrLessonDeleted)
	}
	return fmt.Errorf("job %d, lesson %d: %w", jobID, id, ErrDownloadAbandoned)
}

// consumeAbandoned removes the abandoned_jobs row of jobID once its worker has
// read it (answer), so the table holds only rows a worker may still need. The
// answer stands whatever happens here: a row that could not be removed is only
// noted, and lapses after abandonedTTL.
func (s *Store) consumeAbandoned(ctx context.Context, jobID int64, answer error) error {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM abandoned_jobs WHERE job_id = ?`, jobID)
		return err
	})
	if err != nil {
		return fmt.Errorf("%w (its abandoned_jobs row stays until it lapses: %v)", answer, err)
	}
	return answer
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

// FailDownload records a download that failed: the lesson becomes 'failed'
// with lessonMsg, and the job 'failed' with jobMsg unless it was canceled
// meanwhile, in one transaction. It lands only while the job and lesson still
// exist (ErrDownloadAbandoned otherwise). The two are shown in different
// places, each beside its own button: the lesson's error under the lesson,
// whose menu offers Download, and the job's in the Queue, beside Retry. So a
// sentence that names what to press next needs one version for each.
func (s *Store) FailDownload(ctx context.Context, jobID int64, id int, lessonMsg, jobMsg string) error {
	return s.finishWith(ctx, jobID, id, StatusFailed, lessonMsg, jobMsg, JobFailed)
}

// SkipDownload records a lesson Musora answered with no match (gated or
// missing): the lesson becomes 'skipped' with reason, and the job 'failed'
// with reason unless it was canceled meanwhile, in one transaction, only while
// both still exist (ErrDownloadAbandoned otherwise). reason is shown both
// under the lesson and in the Queue, so it must name no button.
func (s *Store) SkipDownload(ctx context.Context, jobID int64, id int, reason string) error {
	return s.finishWith(ctx, jobID, id, StatusSkipped, reason, reason, JobFailed)
}

// finishWith is the shared body of FailDownload and SkipDownload.
func (s *Store) finishWith(ctx context.Context, jobID int64, id int, lessonStatus, lessonMsg, jobMsg, jobStatus string) error {
	return s.withLiveJob(ctx, jobID, id, runningOrCanceled, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE railcontent_id = ?`,
			lessonStatus, lessonMsg, id,
		); err != nil {
			return fmt.Errorf("mark lesson %d %s: %w", id, lessonStatus, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, error = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?`,
			jobStatus, jobMsg, jobID, JobRunning,
		); err != nil {
			return fmt.Errorf("mark job %d %s: %w", jobID, jobStatus, err)
		}
		return nil
	})
}

// stoppedNote is the error a download that ended without recording anything
// leaves on a lesson that records no files (see endDownloadSQL). It is shown
// under the lesson, whose menu offers Download, and nothing reads it: it is a
// sentence for the user, not a marker. Migration 005 gave the rows an older
// version left with the bare word 'canceled' this sentence.
const stoppedNote = "The download stopped before it finished. Download again to get this lesson."

// endDownloadSQL is what a download that ended without recording anything
// leaves on its lesson (a cancel, a shutdown, or a delete, skip or follow
// removal that removed its job): a lesson that still records files from an
// earlier download reads 'downloaded' (its files are there, and a 'skipped'
// lesson is never downloaded again), any other 'skipped' with stoppedNote.
// Its arguments are stoppedNote and the lesson id.
const endDownloadSQL = `UPDATE lessons
	    SET status = CASE WHEN ` + hasFilesSQL + ` THEN '` + StatusDownloaded + `' ELSE '` + StatusSkipped + `' END,
	        error = CASE WHEN ` + hasFilesSQL + ` THEN NULL ELSE ? END,
	        updated_at = CURRENT_TIMESTAMP
	  WHERE railcontent_id = ?`

// CancelDownload records a download killed by a cancel: the lesson as
// endDownloadSQL leaves it, and the job 'canceled' if it is still running (a
// job the API already canceled is left as it is). It lands only while the job
// and lesson still exist (ErrDownloadAbandoned otherwise).
func (s *Store) CancelDownload(ctx context.Context, jobID int64, id int) error {
	return s.withLiveJob(ctx, jobID, id, runningOrCanceled, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, endDownloadSQL, stoppedNote, id); err != nil {
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

// DeleteLease is how long a delete's hold on a lesson lasts unless renewed
// (lessons.deleting_until). A delete renews it while it runs (RenewLessonDelete,
// see DeleteRenewEvery) and clears it when it ends, so the lease only matters
// when the process died mid-delete: the lesson is downloadable again once it
// lapses, with no startup sweep that could clear a live delete of another
// process.
const DeleteLease = 2 * time.Minute

// DeleteRenewEvery is how often a running delete renews its lease: a quarter of
// DeleteLease, so a few missed renewals (a stalled database) never let a live
// delete's lease lapse.
const DeleteRenewEvery = DeleteLease / 4

// leaseArg is the SQLite datetime modifier that puts a lease DeleteLease ahead.
var leaseArg = fmt.Sprintf("+%d seconds", int(DeleteLease/time.Second))

// BeginLessonDelete starts deleting a lesson's files. In one transaction it
// reads the lesson (a wrapped sql.ErrNoRows if unknown), refuses with
// ErrLessonDeleting if a delete holds it already, takes the delete's lease (no
// job can be enqueued or retried for it until EndLessonDelete,
// TombstoneLesson or KeepLessonFiles, or until the lease lapses), and removes
// every job of it that is queued, running or canceled (removeActiveJobsTx),
// recording that the lesson's files go. So once the delete answers, no step of
// a download already under way can record anything, and no new one can start.
// It returns the lesson as it is then, whose recorded files the caller
// removes, and the ids of the jobs whose processes the caller should kill.
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
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET deleting_until = datetime('now', ?) WHERE railcontent_id = ?`, leaseArg, id,
		); err != nil {
			return fmt.Errorf("mark lesson %d deleting: %w", id, err)
		}
		if running, err = removeActiveJobsTx(ctx, tx, intentDelete, `railcontent_id = ?`, id); err != nil {
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
// ErrLessonDeleting if a delete holds any of them already, takes the lease on
// them all, and removes every queued, running or canceled job of them. It
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
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET deleting_until = datetime('now', ?) WHERE follow_id = ?`, leaseArg, followID,
		); err != nil {
			return fmt.Errorf("mark lessons of follow %d deleting: %w", followID, err)
		}
		var err error
		if running, err = removeActiveJobsTx(ctx, tx, intentDelete, followLessonsClause, followID); err != nil {
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

// refuseDeletingTx returns ErrLessonDeleting if a delete holds any lesson of
// the follow right now.
func refuseDeletingTx(ctx context.Context, tx *sql.Tx, followID int64) error {
	var deleting int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM lessons WHERE follow_id = ? AND `+deletingSQL, followID,
	).Scan(&deleting); err != nil {
		return fmt.Errorf("check lessons of follow %d: %w", followID, err)
	}
	if deleting > 0 {
		return fmt.Errorf("follow %d: %d lessons: %w", followID, deleting, ErrLessonDeleting)
	}
	return nil
}

// RenewLessonDelete extends the lease of a delete still holding lessons ids by
// another DeleteLease. A lesson whose delete already ended (no lease) is left
// alone, as is an unknown id.
func (s *Store) RenewLessonDelete(ctx context.Context, ids ...int) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx,
				`UPDATE lessons SET deleting_until = datetime('now', ?) WHERE railcontent_id = ? AND deleting_until IS NOT NULL`,
				leaseArg, id,
			); err != nil {
				return fmt.Errorf("renew the delete of lesson %d: %w", id, err)
			}
		}
		return nil
	})
}

// EndLessonDelete ends the deletes of lessons ids: they may be downloaded
// again. TombstoneLesson and KeepLessonFiles end it themselves; a delete calls
// this too, whatever happened, so a failure between them never leaves a lesson
// that can not be downloaded until its lease lapses. An unknown id is not an
// error.
func (s *Store) EndLessonDelete(ctx context.Context, ids ...int) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx,
				`UPDATE lessons SET deleting_until = NULL WHERE railcontent_id = ? AND deleting_until IS NOT NULL`, id,
			); err != nil {
				return fmt.Errorf("end delete of lesson %d: %w", id, err)
			}
		}
		return nil
	})
}

// SkipLesson is the API's Skip: in one transaction it marks lesson id skipped
// with reason (recorded in its error column), and stops every download of it
// for good: its queued jobs are removed, and so are its running and canceled
// ones, recording that what those downloads wrote goes, except what any lesson
// row records (the lesson's own earlier files stay). So once it answers no
// download of the lesson, queued or running, can record anything, and syncs
// leave it alone (ShouldSkipEnqueue). It refuses with ErrLessonDeleting while a
// delete holds the lesson, and returns a wrapped sql.ErrNoRows for an unknown
// id. It returns the ids of the jobs whose processes the caller should kill.
func (s *Store) SkipLesson(ctx context.Context, id int, reason string) ([]int64, error) {
	var running []int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		l, err := getLessonTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if l.Deleting {
			return fmt.Errorf("lesson %d: %w", id, ErrLessonDeleting)
		}
		if running, err = removeActiveJobsTx(ctx, tx, intentDiscard, `railcontent_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE railcontent_id = ?`,
			StatusSkipped, reason, id,
		); err != nil {
			return fmt.Errorf("skip lesson %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return running, nil
}

// abandonedTTL is how long an abandoned_jobs row nobody read is kept: longer
// than any worker holds one job. A row still needed after it (a download in
// another process that ran for a week) is gone, and a missing row keeps the
// files: the safe side.
const abandonedTTL = "-7 days"

// removeActiveJobsTx deletes the queued, running and canceled jobs matching
// where (a condition on the jobs table), and returns the ids of the ones whose
// worker may still be going: every running job, and every canceled job that a
// worker had claimed (started_at set; one canceled while still queued never
// had a worker). For those it records what the stopper wants (abandoned_jobs:
// intent), which the worker reads, and consumes, when its next write finds
// the job gone. A canceled job is removed too: its worker may not have
// finished yet, and must not record after the stopper. Any of their lessons
// still 'downloading' is left as endDownloadSQL says. Rows older than
// abandonedTTL are dropped first, so the table can not grow without bound.
func removeActiveJobsTx(ctx context.Context, tx *sql.Tx, intent, where string, args ...any) ([]int64, error) {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM abandoned_jobs WHERE recorded_at < datetime('now', ?)`, abandonedTTL,
	); err != nil {
		return nil, fmt.Errorf("drop lapsed abandoned jobs: %w", err)
	}
	cond := `status IN ('` + JobQueued + `', '` + JobRunning + `', '` + JobCanceled + `') AND ` + where
	rows, err := tx.QueryContext(ctx, `SELECT id, railcontent_id, status, started_at IS NOT NULL FROM jobs WHERE `+cond, args...)
	if err != nil {
		return nil, fmt.Errorf("list active jobs: %w", err)
	}
	type inFlightJob struct {
		id   int64
		rcID int
	}
	var (
		inFlight []inFlightJob
		lessons  []int
	)
	for rows.Next() {
		var (
			id      int64
			rcID    int
			status  string
			started bool
		)
		if err := rows.Scan(&id, &rcID, &status, &started); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan active job: %w", err)
		}
		if status == JobRunning || status == JobCanceled && started {
			inFlight = append(inFlight, inFlightJob{id: id, rcID: rcID})
		}
		lessons = append(lessons, rcID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close active jobs: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active jobs: %w", err)
	}
	ids := make([]int64, 0, len(inFlight))
	for _, j := range inFlight {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO abandoned_jobs(job_id, railcontent_id, intent) VALUES(?, ?, ?)`, j.id, j.rcID, intent,
		); err != nil {
			return nil, fmt.Errorf("record what the stopper wants for job %d: %w", j.id, err)
		}
		ids = append(ids, j.id)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE `+cond, args...); err != nil {
		return nil, fmt.Errorf("delete active jobs: %w", err)
	}
	for _, rcID := range lessons {
		if _, err := tx.ExecContext(ctx, endDownloadSQL+` AND status = '`+StatusDownloading+`'`, stoppedNote, rcID); err != nil {
			return nil, fmt.Errorf("end the download of lesson %d: %w", rcID, err)
		}
	}
	return ids, nil
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
		    SET status = ?, error = 'deleted', deleting_until = NULL,
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
		    SET status = ?, error = NULL, deleting_until = NULL,
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
		running, err = removeFollowCascadeTx(ctx, tx, id, intentDelete)
		return err
	})
	if err != nil {
		return nil, err
	}
	return running, nil
}
