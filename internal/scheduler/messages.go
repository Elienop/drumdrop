package scheduler

// The sentences the worker records in a lesson's error (shown under the lesson,
// and sent to every client) and puts in its progress events' Err. They are
// written for the user: what happened first, then what to do. The detail
// behind each (a Go error, a path, yt-dlp's exit status) goes to the log only.
const (
	// msgAttemptFailed is one attempt that failed (another may follow).
	msgAttemptFailed = "This download attempt failed."
	// msgNotResolved is a lesson Musora answered with no match (locked for
	// the owner's account, or removed). It is stored on its job, and on the
	// lesson when it is skipped (NotReturnedDownload), so it names no button.
	msgNotResolved = "Musora didn't return this lesson. It may be locked for your account, or removed."
	// msgNotReturnedKept is the note a lesson Musora answered with no match
	// keeps when its earlier download is on disk: it stays 'downloaded' and
	// only the job fails (owner ruling 2026-09-24 (n)).
	msgNotReturnedKept = "Musora didn't return this lesson, so the earlier download was kept. The lesson may be locked for your account, or removed."
	// msgStopped is a download a Skip, a delete or a follow removal stopped.
	msgStopped = "Stopped: the lesson was skipped or deleted, or its follow was removed."
	// msgRequeued is a download whose job was queued again elsewhere.
	msgRequeued = "Stopped: this download was queued again elsewhere, so it starts over."
	// msgShutdown is a download stopped because drumdrop is shutting down.
	msgShutdown = "Stopped: DrumDrop is shutting down. The download starts over when it's back."
	// msgCanceled is a download a Cancel stopped. (A shutdown is msgShutdown:
	// the job starts over.)
	msgCanceled = "Stopped before it finished: the download was canceled."
	// msgKeptInLibrary is failKeptInLibrary's sentence under the lesson.
	msgKeptInLibrary = "Couldn't put this lesson in the library, so its copy there was kept. Check the server log, fix the problem, then Download again."
	// msgLeftBehind is failLeftBehind's sentence under the lesson.
	msgLeftBehind = "Couldn't put this lesson in the library: its files are still in the old library folder. Move them to the new one, or set the library folder back, then Download again."
)

// msgEarlierKept is the note a failed download leaves on a lesson that still
// records files from an earlier download: the lesson stays 'downloaded', so
// syncs don't retry it (owner ruling 2026-09-24 (h)). It is shown under the
// lesson, whose menu offers Download.
const msgEarlierKept = "The re-download failed, so the earlier download was kept. Check the server log, fix the problem, then Download again."

// failure is what a failed job records, twice: lesson under the lesson, whose
// menu offers Download, and job on the job, shown in the Queue beside Retry.
// Each names the button of the place it is shown in. kept, when set, replaces
// msgEarlierKept as the lesson's note when the lesson still records files
// from an earlier download (see keptNote).
type failure struct{ lesson, job, kept string }

// keptNote is the note f leaves on a lesson that still records files from an
// earlier download, which stays 'downloaded' (database.Store.FailDownload).
func (f failure) keptNote() string {
	if f.kept != "" {
		return f.kept
	}
	return msgEarlierKept
}

var (
	// failDownload is a download that failed every attempt.
	failDownload = failure{
		lesson: "Couldn't download this lesson. Check the server log, fix the problem, then Download again.",
		job:    "Couldn't download this lesson. Check the server log, fix the problem, then Retry.",
	}
	// failNotStarted is a download not started because a record it needs
	// (the lesson's, its follow's, or the other lessons', which say whose
	// files are where) couldn't be read.
	failNotStarted = failure{
		lesson: "Didn't start this download: DrumDrop's records couldn't be read. Check the server log, fix the problem, then Download again.",
		job:    "Didn't start this download: DrumDrop's records couldn't be read. Check the server log, fix the problem, then Retry.",
	}
	// failKeptInLibrary is a download whose last attempt couldn't be placed in
	// the library, where the lesson already was (errKeptInLibrary): its copy
	// there is kept, still recorded, and its sentence is the lesson's note.
	failKeptInLibrary = failure{
		lesson: msgKeptInLibrary,
		job:    "Couldn't put this lesson in the library, so its copy there was kept. Check the server log, fix the problem, then Retry.",
		kept:   msgKeptInLibrary,
	}
	// failLeftBehind is a download whose last attempt couldn't be placed in
	// the library while the lesson's files are still in a folder the library
	// setting no longer points at (errLeftBehind): they stay where they are,
	// still recorded, and its sentence, which says the fix, is the lesson's
	// note (owner ruling 2026-09-24 (y)).
	failLeftBehind = failure{
		lesson: msgLeftBehind,
		job:    "Couldn't put this lesson in the library: its files are still in the old library folder. Move them to the new one, or set the library folder back, then Retry.",
		kept:   msgLeftBehind,
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
