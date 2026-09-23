package scheduler

// The sentences the worker records in a lesson's error (shown under the lesson,
// and sent to every client) and puts in its progress events' Err. They are
// written for the user: what happened first, then what to do. The detail
// behind each (a Go error, a path, yt-dlp's exit status) goes to the log only.
const (
	// msgDownloadFailed is a download that failed every attempt.
	msgDownloadFailed = "Couldn't download this lesson. Check the server log, fix the problem, then Download again."
	// msgAttemptFailed is one attempt that failed (another may follow).
	msgAttemptFailed = "This download attempt failed."
	// msgNotStarted is a download not started because the lessons' records,
	// which it needs to record its files, couldn't be read.
	msgNotStarted = "Didn't start this download: the lessons' records couldn't be read. Check the server log, fix the problem, then Download again."
	// msgNotResolved is a lesson Musora didn't return (locked, removed, or
	// Musora couldn't be reached): the lesson is skipped.
	msgNotResolved = "Skipped: couldn't get this lesson from Musora. It may be locked or removed. Check the server log, fix the problem, then Download again."
	// msgStopped is a download a Skip, a delete or a follow removal stopped.
	msgStopped = "Stopped: the lesson was skipped or deleted, or its follow was removed."
	// msgRequeued is a download whose job was queued again elsewhere.
	msgRequeued = "Stopped: this download was queued again elsewhere, so it starts over."
	// msgShutdown is a download stopped because drumdrop is shutting down.
	msgShutdown = "Stopped: DrumDrop is shutting down. The download starts over when it's back."
)
