package scheduler

import "time"

// ProgressEvent is one observable moment in the scheduler's work — a job being
// claimed, a download starting/finishing/failing, a lesson skipped, or a daemon
// cycle beginning/ending. It is emitted beside the existing human-readable log
// lines so an out-of-band consumer (the HTTP API's SSE stream) can mirror the
// worker's progress without parsing log text. Fields not relevant to a given
// Kind are left zero.
type ProgressEvent struct {
	// Kind names the moment: job_claimed, download_started, download_ok,
	// attempt_failed, lesson_skipped, cycle_started, cycle_done.
	Kind string
	// RailcontentID is the lesson's id (the Lesson PK), when the event concerns a
	// specific lesson.
	RailcontentID int
	// JobID and FollowID identify the job and its originating follow (FollowID is
	// 0 for an orphaned/manually enqueued job).
	JobID    int64
	FollowID int64
	// Title is the lesson title, when known.
	Title string
	// Attempt and MaxAttempts describe retry progress for download events.
	Attempt     int
	MaxAttempts int
	// Pct, Bytes, TotalBytes, Speed carry transfer progress when available. The
	// current scheduler emits coarse events only, so these are reserved for finer
	// download reporting and default to zero/empty.
	Pct        float64
	Bytes      int64
	TotalBytes int64
	Speed      string
	// Err is the failure reason for attempt_failed / lesson_skipped events.
	Err string
	// Planned and Processed carry the per-cycle counts on cycle_done.
	Planned   int
	Processed int
	// Time is when the event occurred.
	Time time.Time
}

// ProgressSink receives ProgressEvents. Implementations must be safe to call
// from the worker's single goroutine and must not block it (the broadcast hub
// drops on a full subscriber buffer rather than stalling the download loop).
type ProgressSink interface {
	Emit(ProgressEvent)
}

// noopSink discards every event. It is the default sink so the worker and daemon
// can emit unconditionally without a nil check and the download logic stays
// independent of any consumer.
type noopSink struct{}

func (noopSink) Emit(ProgressEvent) {}
