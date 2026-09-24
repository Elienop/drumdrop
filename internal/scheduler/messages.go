package scheduler

// The sentences the worker records in a lesson's error (shown under the lesson,
// and sent to every client) and puts in its progress events' Err. They are
// written for the user: what happened first, then what to do. The detail
// behind each (a Go error, a path, yt-dlp's exit status) goes to the log only.
const (
	// msgAttemptFailed is one attempt that failed (another may follow).
	msgAttemptFailed = "This download attempt failed."
	// msgNotResolved is a lesson Musora answered with no match (locked for
	// the owner's account, or removed): the lesson is skipped. It is stored on
	// the lesson and on its job alike (SkipDownload), so it names no button.
	msgNotResolved = "Musora didn't return this lesson. It may be locked for your account, or removed."
	// msgStopped is a download a Skip, a delete or a follow removal stopped.
	msgStopped = "Stopped: the lesson was skipped or deleted, or its follow was removed."
	// msgRequeued is a download whose job was queued again elsewhere.
	msgRequeued = "Stopped: this download was queued again elsewhere, so it starts over."
	// msgShutdown is a download stopped because drumdrop is shutting down.
	msgShutdown = "Stopped: DrumDrop is shutting down. The download starts over when it's back."
	// msgCanceled is a download a Cancel stopped. (A shutdown is msgShutdown:
	// the job starts over.)
	msgCanceled = "Stopped before it finished: the download was canceled."
)

// failure is what a failed job records, twice: lesson under the lesson, whose
// menu offers Download, and job on the job, shown in the Queue beside Retry.
// Each names the button of the place it is shown in.
type failure struct{ lesson, job string }

var (
	// failDownload is a download that failed every attempt.
	failDownload = failure{
		lesson: "Couldn't download this lesson. Check the server log, fix the problem, then Download again.",
		job:    "Couldn't download this lesson. Check the server log, fix the problem, then Retry.",
	}
	// failNotStarted is a download not started because the lessons' records,
	// which it needs to record its files, couldn't be read.
	failNotStarted = failure{
		lesson: "Didn't start this download: the lessons' records couldn't be read. Check the server log, fix the problem, then Download again.",
		job:    "Didn't start this download: the lessons' records couldn't be read. Check the server log, fix the problem, then Retry.",
	}
	// failNoFolder is a download not started because its private folder, in
	// the downloads folder, couldn't be made.
	failNoFolder = failure{
		lesson: "Didn't start this download: DrumDrop couldn't make a folder to download into. Check the server log, fix the problem, then Download again.",
		job:    "Didn't start this download: DrumDrop couldn't make a folder to download into. Check the server log, fix the problem, then Retry.",
	}
	// failMusora is a lesson Musora couldn't be asked for (it didn't answer)
	// or whose answer couldn't be read (a query or a shape DrumDrop doesn't
	// know). Unlike a lesson Musora has no match for, it is not skipped.
	failMusora = failure{
		lesson: "Couldn't get this lesson from Musora: it didn't answer, or its answer couldn't be read. Check the server log, fix the problem, then Download again.",
		job:    "Couldn't get this lesson from Musora: it didn't answer, or its answer couldn't be read. Check the server log, fix the problem, then Retry.",
	}
)
