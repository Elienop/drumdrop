package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"io"
)

// Planner discovers newly available lessons across every follow and enqueues a
// download job for each, deduped against already-downloaded lessons and lessons
// that already have an active (queued or running) job. It performs no downloads
// itself — that is the Worker's job — so a Plan call is cheap and safe to run on
// a schedule.
type Planner struct {
	// Store persists lessons and jobs and lists follows.
	Store Store
	// Expander turns a follow into the railcontent ids of its lessons.
	Expander Expander
	// PermIDs is the comma-separated permission id string passed to the catalog
	// queries so gated content is expanded with the owner's entitlements.
	PermIDs string
	// Log receives human-readable per-follow progress. It defaults to
	// io.Discard; the planning logic never depends on it.
	Log io.Writer
}

// log returns the configured writer or io.Discard so callers can write without
// a nil check and logic stays independent of logging.
func (p *Planner) log() io.Writer {
	if p.Log == nil {
		return io.Discard
	}
	return p.Log
}

// Plan lists every follow and, for each, expands it to its lessons, records each
// lesson (UpsertLesson), and enqueues a job for any lesson that is neither
// already downloaded nor already covered by an active job. It returns the number
// of jobs enqueued.
//
// Behavior contract:
//   - A follow whose expansion fails is logged and skipped; the others are still
//     processed and the failed follow is NOT touched (TouchLastSynced), so it
//     retries next cycle.
//   - limit > 0 caps the total number of jobs enqueued this run; once reached,
//     no further jobs are enqueued, but follows already reached are still touched
//     and their lessons still upserted.
//   - A follow that was reached (expansion succeeded) is touched exactly once
//     after its lessons are processed.
//
// Plan returns an error only on a fatal store failure (UpsertLesson,
// IsDownloaded, ActiveJobExists, EnqueueJob); per-follow expansion errors are
// non-fatal.
func (p *Planner) Plan(ctx context.Context, limit int) (enqueued int, err error) {
	return p.plan(ctx, limit, false)
}

// PlanDryRun is the report-only sibling of Plan: it lists follows, expands each,
// and records every lesson (UpsertLesson) exactly as Plan does, but it NEVER
// enqueues a job — a queued job would otherwise be drained later by a daemon,
// which is surprising for a "dry run". It returns the number of lessons that
// WOULD be enqueued (not downloaded, not already covered by an active job). It
// also does not stamp last_synced_at, since nothing was actually synced.
//
// Dedupe and skip-already-downloaded behavior is identical to Plan, so the count
// it reports matches what a real Plan would queue.
func (p *Planner) PlanDryRun(ctx context.Context) (wouldEnqueue int, err error) {
	return p.plan(ctx, 0, true)
}

// plan is the shared core of Plan and PlanDryRun. In dry-run mode it counts the
// lessons that would be enqueued but performs no EnqueueJob and no
// TouchLastSynced; otherwise it enqueues (deduped) up to limit lessons and
// stamps each reached follow.
func (p *Planner) plan(ctx context.Context, limit int, dryRun bool) (enqueued int, err error) {
	follows, err := p.Store.ListFollows(ctx)
	if err != nil {
		return enqueued, fmt.Errorf("list follows: %w", err)
	}

	for _, f := range follows {
		ids, err := p.Expander.Expand(f, p.PermIDs)
		if err != nil {
			// Expansion failure is isolated: log, skip, and leave last_synced_at
			// untouched so this follow is retried next cycle.
			fmt.Fprintf(p.log(), "follow #%d %s: expand failed: %v\n", f.ID, f.Kind, err)
			continue
		}

		// A node follow links its lessons to itself via railcontent_id; an
		// instructor follow has no node parent, so parent stays NULL.
		var parent sql.NullInt64
		if f.Kind == "node" && f.RailcontentID.Valid {
			parent = f.RailcontentID
		}

		fNew := 0
		for _, id := range ids {
			// Record the lesson regardless of whether we enqueue it.
			if err := p.Store.UpsertLesson(ctx, id, "", parent, f.Brand); err != nil {
				return enqueued, fmt.Errorf("upsert lesson %d: %w", id, err)
			}

			done, err := p.Store.IsDownloaded(ctx, id)
			if err != nil {
				return enqueued, fmt.Errorf("check lesson %d downloaded: %w", id, err)
			}
			if done {
				continue
			}

			active, err := p.Store.ActiveJobExists(ctx, id)
			if err != nil {
				return enqueued, fmt.Errorf("check lesson %d active job: %w", id, err)
			}
			if active {
				// Dedupe: a job for this lesson is already queued or running.
				continue
			}

			// Respect the run-wide enqueue limit. Once reached, stop enqueuing
			// further lessons (this follow and any remaining follows) but keep
			// recording follows as touched.
			if limit > 0 && enqueued >= limit {
				break
			}

			if !dryRun {
				if _, err := p.Store.EnqueueJob(ctx, sql.NullInt64{Int64: f.ID, Valid: true}, id); err != nil {
					return enqueued, fmt.Errorf("enqueue lesson %d: %w", id, err)
				}
			}
			enqueued++
			fNew++
		}

		if dryRun {
			fmt.Fprintf(p.log(), "follow #%d %s: %d new lesson(s) would be queued\n", f.ID, f.Kind, fNew)
			continue
		}

		// The follow was reached and processed (even if the limit truncated its
		// lessons), so stamp last_synced_at.
		if err := p.Store.TouchLastSynced(ctx, f.ID); err != nil {
			fmt.Fprintf(p.log(), "follow #%d: touch last_synced failed: %v\n", f.ID, err)
		}
		fmt.Fprintf(p.log(), "follow #%d %s: enqueued %d new lesson(s)\n", f.ID, f.Kind, fNew)

		// If the limit is reached, no further follows can enqueue anything;
		// stop early so we don't expand/upsert work that can produce no jobs.
		if limit > 0 && enqueued >= limit {
			break
		}
	}

	return enqueued, nil
}
