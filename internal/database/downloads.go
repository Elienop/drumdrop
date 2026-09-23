package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrDownloadAbandoned is returned by the worker's guarded writes (StartDownload,
// FinishDownload, FailDownload, SkipDownload, CancelDownload) when the job or
// its lesson no longer exists: a delete removed them while the download was
// running. Nothing was written. The worker must then remove what the download
// placed, because no row will ever track it.
var ErrDownloadAbandoned = errors.New("download abandoned: its job or lesson was deleted")

// ErrLessonChanged is returned by TombstoneLesson and KeepLessonFiles when the
// lesson's recorded files are no longer the ones the caller read: a later
// download recorded new ones in between. Nothing was written.
var ErrLessonChanged = errors.New("lesson changed while it was being deleted")

// ErrFollowHasFiles is returned by RemoveFilelessFollowCascade when a lesson of
// the follow still has recorded files, so deleting its row would leave them
// untracked. Nothing was deleted.
var ErrFollowHasFiles = errors.New("follow still has lessons with recorded files")

// PlacedEntries decodes the lesson's library_entries record: the absolute paths
// the plex-tv move placed in a season folder for it. recorded is false when the
// column is NULL (no record: a default-layout lesson, or one moved before the
// record existed). A value that is not a JSON array of non-empty strings is an
// error, never an empty record, so a damaged row can not pass for "owns nothing".
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
// LibraryEntries nil records no library entries (NULL); non-nil (even empty)
// records exactly those paths.
type DownloadRecord struct {
	Quality        string
	OutputDir      string
	VideoPath      string
	Bytes          int64
	LibraryEntries []string
}

// withLiveJob runs fn in one transaction, but only while job jobID for lesson id
// and the lesson row itself both still exist; otherwise it writes nothing and
// returns ErrDownloadAbandoned. Every write the worker makes about a job goes
// through it, and a delete removes the lesson's active jobs (or the rows) first,
// so no step of a download that started before a delete can land after it.
func (s *Store) withLiveJob(ctx context.Context, jobID int64, id int, fn func(*sql.Tx) error) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var jobs, lessons int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM jobs WHERE id = ? AND railcontent_id = ?`, jobID, id,
		).Scan(&jobs); err != nil {
			return fmt.Errorf("check job %d: %w", jobID, err)
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM lessons WHERE railcontent_id = ?`, id,
		).Scan(&lessons); err != nil {
			return fmt.Errorf("check lesson %d: %w", id, err)
		}
		if jobs == 0 || lessons == 0 {
			return fmt.Errorf("job %d, lesson %d: %w", jobID, id, ErrDownloadAbandoned)
		}
		return fn(tx)
	})
}

// StartDownload marks the lesson 'downloading' at the start of each attempt of
// job jobID, only while the job and lesson still exist (ErrDownloadAbandoned
// otherwise).
func (s *Store) StartDownload(ctx context.Context, jobID int64, id int) error {
	return s.withLiveJob(ctx, jobID, id, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE railcontent_id = ?`,
			StatusDownloading, id,
		); err != nil {
			return fmt.Errorf("mark lesson %d downloading: %w", id, err)
		}
		return nil
	})
}

// FinishDownload records a successful download and closes its job, in one
// transaction: the lesson becomes 'downloaded' with rec's quality, paths, byte
// count and library entries (error cleared, downloaded_at stamped), and the job
// becomes 'done'. It lands only while the job and lesson still exist; otherwise
// nothing is written and it returns ErrDownloadAbandoned.
func (s *Store) FinishDownload(ctx context.Context, jobID int64, id int, rec DownloadRecord) error {
	return s.withLiveJob(ctx, jobID, id, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons
			    SET status = ?,
			        quality = ?,
			        output_dir = ?,
			        video_path = ?,
			        bytes = ?,
			        library_entries = ?,
			        error = NULL,
			        downloaded_at = CURRENT_TIMESTAMP,
			        updated_at = CURRENT_TIMESTAMP
			  WHERE railcontent_id = ?`,
			StatusDownloaded, rec.Quality, rec.OutputDir, rec.VideoPath, rec.Bytes,
			EncodeLibraryEntries(rec.LibraryEntries), id,
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
// becomes 'failed' and the job 'failed', both with msg, in one transaction. It
// lands only while the job and lesson still exist (ErrDownloadAbandoned
// otherwise).
func (s *Store) FailDownload(ctx context.Context, jobID int64, id int, msg string) error {
	return s.finishWith(ctx, jobID, id, StatusFailed, msg, JobFailed)
}

// SkipDownload records a lesson that could not be resolved (gated or missing):
// the lesson becomes 'skipped' with reason and the job 'failed' with reason, in
// one transaction, only while both still exist (ErrDownloadAbandoned otherwise).
func (s *Store) SkipDownload(ctx context.Context, jobID int64, id int, reason string) error {
	return s.finishWith(ctx, jobID, id, StatusSkipped, reason, JobFailed)
}

// finishWith is the shared body of FailDownload and SkipDownload.
func (s *Store) finishWith(ctx context.Context, jobID int64, id int, lessonStatus, msg, jobStatus string) error {
	return s.withLiveJob(ctx, jobID, id, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE railcontent_id = ?`,
			lessonStatus, msg, id,
		); err != nil {
			return fmt.Errorf("mark lesson %d %s: %w", id, lessonStatus, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET status = ?, error = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ?`,
			jobStatus, msg, jobID,
		); err != nil {
			return fmt.Errorf("mark job %d %s: %w", jobID, jobStatus, err)
		}
		return nil
	})
}

// CancelDownload records a download killed by a cancel: the lesson becomes
// 'skipped' with error 'canceled', and the job 'canceled' if it is still
// running (a job the API already canceled is left as it is). It lands only
// while the job and lesson still exist (ErrDownloadAbandoned otherwise).
func (s *Store) CancelDownload(ctx context.Context, jobID int64, id int) error {
	return s.withLiveJob(ctx, jobID, id, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET status = ?, error = 'canceled', updated_at = CURRENT_TIMESTAMP WHERE railcontent_id = ?`,
			StatusSkipped, id,
		); err != nil {
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

// ListLessonsWithFiles returns every lesson row that records files on disk (an
// output_dir or library entries), whatever its status, ordered by
// railcontent_id. A move and a delete read it to learn which entries of a shared
// season folder other lessons claim.
func (s *Store) ListLessonsWithFiles(ctx context.Context) ([]Lesson, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+lessonColumns+` FROM lessons
		  WHERE output_dir IS NOT NULL OR library_entries IS NOT NULL
		  ORDER BY railcontent_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list lessons with files: %w", err)
	}
	return scanLessons(rows)
}

// BeginLessonDelete starts deleting a lesson's files. In one transaction it
// reads the lesson (a wrapped sql.ErrNoRows if unknown) and removes every
// queued or running job for it, so no step of a download already under way can
// record anything afterwards (see withLiveJob). A lesson left 'downloading' by
// such a job becomes 'skipped' with error 'canceled', as a cancel would leave
// it. It returns the lesson as it was read, whose recorded files the caller
// then removes, and the ids of the jobs that were running, whose processes the
// caller should kill.
func (s *Store) BeginLessonDelete(ctx context.Context, id int) (Lesson, []int64, error) {
	var (
		l       Lesson
		running []int64
	)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		l, err = scanLesson(tx.QueryRowContext(ctx,
			`SELECT `+lessonColumns+` FROM lessons WHERE railcontent_id = ?`, id,
		))
		if err != nil {
			return fmt.Errorf("get lesson %d: %w", id, err)
		}
		running, err = removeActiveJobsTx(ctx, tx, `railcontent_id = ?`, id)
		return err
	})
	if err != nil {
		return Lesson{}, nil, err
	}
	return l, running, nil
}

// BeginFollowDelete is BeginLessonDelete for every lesson of a follow: in one
// transaction it reads the follow's lessons and removes every queued or running
// job for them or for the follow. It returns the lessons as read and the ids of
// the jobs that were running.
func (s *Store) BeginFollowDelete(ctx context.Context, followID int64) ([]Lesson, []int64, error) {
	var (
		lessons []Lesson
		running []int64
	)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT `+lessonColumns+` FROM lessons WHERE follow_id = ? ORDER BY railcontent_id`, followID,
		)
		if err != nil {
			return fmt.Errorf("list lessons of follow %d: %w", followID, err)
		}
		if lessons, err = scanLessons(rows); err != nil {
			return err
		}
		running, err = removeActiveJobsTx(ctx, tx,
			`(follow_id = ? OR railcontent_id IN (SELECT railcontent_id FROM lessons WHERE follow_id = ?))`,
			followID, followID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return lessons, running, nil
}

// removeActiveJobsTx deletes the queued and running jobs matching where (a
// condition on the jobs table), sets any of their lessons still 'downloading'
// to 'skipped' with error 'canceled', and returns the ids of the running ones.
func removeActiveJobsTx(ctx context.Context, tx *sql.Tx, where string, args ...any) ([]int64, error) {
	cond := `status IN ('` + JobQueued + `', '` + JobRunning + `') AND ` + where
	rows, err := tx.QueryContext(ctx, `SELECT id, railcontent_id, status FROM jobs WHERE `+cond, args...)
	if err != nil {
		return nil, fmt.Errorf("list active jobs: %w", err)
	}
	var (
		running []int64
		lessons []int
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
		if status == JobRunning {
			running = append(running, id)
		}
		lessons = append(lessons, rcID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close active jobs: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active jobs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE `+cond, args...); err != nil {
		return nil, fmt.Errorf("delete active jobs: %w", err)
	}
	for _, rcID := range lessons {
		if _, err := tx.ExecContext(ctx,
			`UPDATE lessons SET status = ?, error = 'canceled', updated_at = CURRENT_TIMESTAMP
			  WHERE railcontent_id = ? AND status = ?`,
			StatusSkipped, rcID, StatusDownloading,
		); err != nil {
			return nil, fmt.Errorf("cancel lesson %d: %w", rcID, err)
		}
	}
	return running, nil
}

// sameFilesClause matches a lesson row whose recorded files are still exactly
// before's (compare-and-swap for the delete's final write).
const sameFilesClause = `railcontent_id = ? AND output_dir IS ? AND video_path IS ? AND library_entries IS ?`

func sameFilesArgs(before Lesson) []any {
	return []any{before.RailcontentID, before.OutputDir, before.VideoPath, before.LibraryEntries}
}

// TombstoneLesson finishes a delete whose files were all removed: status
// 'skipped', error 'deleted', and every recorded path cleared (see
// UpdateLessonDeleted). It writes only if the lesson still records exactly
// before's files; if a later download recorded new ones, it writes nothing and
// returns ErrLessonChanged, so those new files stay tracked.
func (s *Store) TombstoneLesson(ctx context.Context, before Lesson) error {
	return s.casLesson(ctx, before,
		`UPDATE lessons
		    SET status = ?, error = 'deleted',
		        output_dir = NULL, video_path = NULL, bytes = NULL, library_entries = NULL,
		        updated_at = CURRENT_TIMESTAMP
		  WHERE `+sameFilesClause,
		append([]any{StatusSkipped}, sameFilesArgs(before)...)...)
}

// KeepLessonFiles finishes a delete that could not remove everything: the
// lesson keeps its status and paths, and its library entries become entries
// (the ones still on disk). Like TombstoneLesson it writes only if the lesson
// still records exactly before's files (ErrLessonChanged otherwise).
func (s *Store) KeepLessonFiles(ctx context.Context, before Lesson, entries sql.NullString) error {
	return s.casLesson(ctx, before,
		`UPDATE lessons SET library_entries = ?, updated_at = CURRENT_TIMESTAMP WHERE `+sameFilesClause,
		append([]any{entries}, sameFilesArgs(before)...)...)
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
// download recorded in the meantime), so no file is left untracked.
func (s *Store) RemoveFilelessFollowCascade(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var withFiles int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM lessons
			  WHERE follow_id = ? AND (output_dir IS NOT NULL OR library_entries IS NOT NULL)`, id,
		).Scan(&withFiles); err != nil {
			return fmt.Errorf("count lessons with files for follow %d: %w", id, err)
		}
		if withFiles > 0 {
			return fmt.Errorf("follow %d: %d lessons: %w", id, withFiles, ErrFollowHasFiles)
		}
		return removeFollowCascadeTx(ctx, tx, id)
	})
}
