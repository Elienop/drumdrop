package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrAlreadyFollowing is returned (alongside the existing row) when an Add*Follow
// call targets a node or instructor that is already followed. It makes follow
// idempotent: the caller can report "already following" while still receiving
// the pre-existing Follow.
var ErrAlreadyFollowing = errors.New("already following")

// Follow is one row of the follows table: a followed Musora node (kind="node",
// keyed by railcontent_id) or a followed instructor (kind="instructor", keyed by
// slug). The nullable columns map to sql.Null* so a node's empty slug and an
// instructor's empty railcontent_id round-trip as SQL NULL rather than zero.
type Follow struct {
	ID            int64          `json:"id"`
	Kind          string         `json:"kind"`
	RailcontentID sql.NullInt64  `json:"railcontent_id"`
	Slug          sql.NullString `json:"slug"`
	Title         string         `json:"title"`
	Brand         string         `json:"brand"`
	Quality       string         `json:"quality"`
	AddedAt       time.Time      `json:"added_at"`
	LastSyncedAt  sql.NullTime   `json:"last_synced_at"`
}

// followColumns is the canonical column list for SELECTs, kept in one place so
// every scan path agrees with scanFollow's field order.
const followColumns = `id, kind, railcontent_id, slug, title, brand, quality, added_at, last_synced_at`

// scanFollow reads one follows row in followColumns order from any *sql.Row or
// *sql.Rows (both satisfy this Scan signature).
func scanFollow(row interface {
	Scan(dest ...any) error
}) (Follow, error) {
	var f Follow
	err := row.Scan(
		&f.ID, &f.Kind, &f.RailcontentID, &f.Slug,
		&f.Title, &f.Brand, &f.Quality, &f.AddedAt, &f.LastSyncedAt,
	)
	return f, err
}

// AddNodeFollow follows a Musora node by its railcontent_id. It is idempotent:
// if the node is already followed it returns the existing row together with
// ErrAlreadyFollowing rather than creating a duplicate. The insert uses
// ON CONFLICT DO NOTHING against the idx_follows_node partial-unique index, then
// reads the row back so the returned Follow always reflects what is stored.
func (s *Store) AddNodeFollow(ctx context.Context, railcontentID int, title, brand, quality string) (Follow, error) {
	return s.addFollow(ctx,
		"node", sql.NullInt64{Int64: int64(railcontentID), Valid: true}, sql.NullString{},
		title, brand, quality,
		`SELECT `+followColumns+` FROM follows WHERE kind='node' AND railcontent_id = ?`,
		int64(railcontentID),
	)
}

// AddInstructorFollow follows an instructor by slug. It is idempotent in the same
// way AddNodeFollow is, keyed on the idx_follows_instructor partial-unique index.
func (s *Store) AddInstructorFollow(ctx context.Context, slug, title, brand, quality string) (Follow, error) {
	return s.addFollow(ctx,
		"instructor", sql.NullInt64{}, sql.NullString{String: slug, Valid: true},
		title, brand, quality,
		`SELECT `+followColumns+` FROM follows WHERE kind='instructor' AND slug = ?`,
		slug,
	)
}

// addFollow is the shared idempotent-insert path for node and instructor follows.
// The mutation runs through withTx; on conflict the row is read back inside the
// same transaction and ErrAlreadyFollowing is returned with it. selectSQL and
// selectArg locate the (possibly pre-existing) row by its natural key.
func (s *Store) addFollow(
	ctx context.Context,
	kind string, railcontentID sql.NullInt64, slug sql.NullString,
	title, brand, quality string,
	selectSQL string, selectArg any,
) (Follow, error) {
	var (
		f       Follow
		existed bool
	)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO follows(kind, railcontent_id, slug, title, brand, quality)
			 VALUES(?, ?, ?, ?, ?, ?)
			 ON CONFLICT DO NOTHING`,
			kind, railcontentID, slug, title, brand, quality,
		)
		if err != nil {
			return fmt.Errorf("insert %s follow: %w", kind, err)
		}
		// rows==0 means the partial-unique index rejected the insert: the
		// target is already followed.
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			existed = true
		}

		f, err = scanFollow(tx.QueryRowContext(ctx, selectSQL, selectArg))
		if err != nil {
			return fmt.Errorf("read back %s follow: %w", kind, err)
		}
		return nil
	})
	if err != nil {
		return Follow{}, err
	}
	if existed {
		return f, ErrAlreadyFollowing
	}
	return f, nil
}

// RemoveFollow deletes the follow with the given id. It returns an error if no
// row matched so the CLI can tell the user nothing was un-followed.
func (s *Store) RemoveFollow(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM follows WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete follow %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected deleting follow %d: %w", id, err)
		}
		if n == 0 {
			return fmt.Errorf("no follow with id %d", id)
		}
		return nil
	})
}

// UpdateFollowQuality changes a follow's quality preset in place, leaving its
// identity (kind/railcontent_id/slug/brand) untouched. It is forward-only: it
// does NOT re-download or otherwise touch existing lessons — the new quality
// governs lessons enqueued from now on (the worker reads follow.Quality at
// download time). 0 rows matched → "no follow with id" error so the caller can
// map it to a 404 (mirroring RemoveFollow / TouchLastSynced; the API handler
// also reads the follow first). quality is validated by the caller against the
// allowed preset set before this is called.
func (s *Store) UpdateFollowQuality(ctx context.Context, id int64, quality string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE follows SET quality = ? WHERE id = ?`, quality, id,
		)
		if err != nil {
			return fmt.Errorf("update quality for follow %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected updating follow %d: %w", id, err)
		}
		if n == 0 {
			return fmt.Errorf("no follow with id %d", id)
		}
		return nil
	})
}

// RemoveFollowCascade removes the follow without its files and resets the
// history it spawned: in one transaction it deletes the jobs of the follow's
// lessons, then the lessons, then the follow row itself. The explicit child
// deletes go beyond the schema's ON DELETE SET NULL so nothing is left
// orphaned with a dangling follow_id. Jobs the follow queued for another
// follow's lessons are left alone (their follow_id becomes NULL), so removing
// one follow never stops another's downloads. It refuses with
// ErrLessonDeleting while a lesson of the follow is being deleted. 0 follow
// rows → "no follow with id" error (not a wrapped sql.ErrNoRows, like
// RemoveFollow), so the caller reads the follow first for a clean 404.
//
// It never removes a file: a download it stops keeps what it wrote (see
// abandoned_jobs). It returns the ids of the jobs that may still be running,
// whose processes the caller should kill.
func (s *Store) RemoveFollowCascade(ctx context.Context, id int64) ([]int64, error) {
	var running []int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := refuseDeletingTx(ctx, tx, id); err != nil {
			return err
		}
		var err error
		running, err = removeFollowCascadeTx(ctx, tx, id, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return running, nil
}

// removeFollowCascadeTx is the body of RemoveFollowCascade, shared with
// RemoveFilelessFollowCascade. discard is what the delete wants done with what
// a stopped download wrote (see removeActiveJobsTx).
func removeFollowCascadeTx(ctx context.Context, tx *sql.Tx, id int64, discard bool) ([]int64, error) {
	running, err := removeActiveJobsTx(ctx, tx, discard, followLessonsClause, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE `+followLessonsClause, id); err != nil {
		return nil, fmt.Errorf("delete jobs for follow %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM lessons WHERE follow_id = ?`, id); err != nil {
		return nil, fmt.Errorf("delete lessons for follow %d: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM follows WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("delete follow %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected deleting follow %d: %w", id, err)
	}
	if n == 0 {
		return nil, fmt.Errorf("no follow with id %d", id)
	}
	return running, nil
}

// ListFollows returns every follow ordered by added_at (oldest first), with id
// as a stable tiebreaker so the order is deterministic when timestamps collide.
func (s *Store) ListFollows(ctx context.Context) ([]Follow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+followColumns+` FROM follows ORDER BY added_at, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list follows: %w", err)
	}
	defer rows.Close()

	var follows []Follow
	for rows.Next() {
		f, err := scanFollow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan follow: %w", err)
		}
		follows = append(follows, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate follows: %w", err)
	}
	return follows, nil
}

// GetFollow returns the follow with the given id, or sql.ErrNoRows (wrapped) if
// none exists.
func (s *Store) GetFollow(ctx context.Context, id int64) (Follow, error) {
	f, err := scanFollow(s.db.QueryRowContext(ctx,
		`SELECT `+followColumns+` FROM follows WHERE id = ?`, id,
	))
	if err != nil {
		return Follow{}, fmt.Errorf("get follow %d: %w", id, err)
	}
	return f, nil
}

// TouchLastSynced stamps last_synced_at = CURRENT_TIMESTAMP on the given follow,
// recording that sync has just processed it.
func (s *Store) TouchLastSynced(ctx context.Context, id int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE follows SET last_synced_at = CURRENT_TIMESTAMP WHERE id = ?`, id,
		)
		if err != nil {
			return fmt.Errorf("touch last_synced_at for follow %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected touching follow %d: %w", id, err)
		}
		if n == 0 {
			return fmt.Errorf("no follow with id %d", id)
		}
		return nil
	})
}
