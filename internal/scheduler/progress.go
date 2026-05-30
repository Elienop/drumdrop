package scheduler

import "time"

// ProgressEvent is one observable moment in the scheduler's work — a job being
// claimed, a download starting/finishing/failing, a lesson skipped, or a daemon
// cycle beginning/ending. It is emitted beside the existing human-readable log
// lines so an out-of-band consumer (the HTTP API's SSE stream) can mirror the
// worker's progress without parsing log text. Fields not relevant to a given
// Kind are left zero.
// The json tags are snake_case so SSE data: frames match the snake_case REST
// DTOs the UI consumes — a single wire vocabulary across both transports.
type ProgressEvent struct {
	// Kind names the moment: job_claimed, download_started, download_progress,
	// download_ok, attempt_failed, lesson_skipped, cycle_started, cycle_done.
	Kind string `json:"kind"`
	// RailcontentID is the lesson's id (the Lesson PK), when the event concerns a
	// specific lesson.
	RailcontentID int `json:"railcontent_id"`
	// JobID and FollowID identify the job and its originating follow (FollowID is
	// 0 for an orphaned/manually enqueued job).
	JobID    int64 `json:"job_id"`
	FollowID int64 `json:"follow_id"`
	// Title is the lesson title, when known.
	Title string `json:"title"`
	// Attempt and MaxAttempts describe retry progress for download events.
	Attempt     int `json:"attempt"`
	MaxAttempts int `json:"max_attempts"`
	// Pct, Bytes, TotalBytes, Speed carry transfer progress, populated on
	// download_progress (and Bytes on download_ok). Coarse events leave them zero.
	Pct        float64 `json:"pct"`
	Bytes      int64   `json:"bytes"`
	TotalBytes int64   `json:"total_bytes"`
	Speed      string  `json:"speed"`
	// Err is the failure reason for attempt_failed / lesson_skipped events. The
	// JSON tag is "error" (not "err") so SSE frames and REST error DTOs share one
	// error vocabulary on the wire; the Go field name stays Err.
	Err string `json:"error"`
	// Planned and Processed carry the per-cycle counts on cycle_done.
	Planned   int `json:"planned"`
	Processed int `json:"processed"`
	// Time is when the event occurred.
	Time time.Time `json:"time"`
}

// ProgressSink receives ProgressEvents. Implementations must be safe to call
// from the worker's single goroutine and must not block it (the broadcast hub
// drops on a full subscriber buffer rather than stalling the download loop).
//
// In the serve entrypoint events originate from BOTH the daemon goroutine and
// HTTP-handler goroutines (any path that drives the worker), so an Emit may be
// called concurrently from more than one goroutine; implementations must be
// safe for concurrent callers. Database writes those callers make are serialized
// by Store.mu, but note a lock-free read followed by a withTx write is NOT
// atomic (see the Store doc comment).
type ProgressSink interface {
	Emit(ProgressEvent)
}

// noopSink discards every event. It is the default sink so the worker and daemon
// can emit unconditionally without a nil check and the download logic stays
// independent of any consumer.
type noopSink struct{}

func (noopSink) Emit(ProgressEvent) {}
