package server

import "fmt"

// The fixed messages the API answers with (hard rule 11: a handler never sends
// err.Error(); the detail goes to the server log). The web UI shows them as
// they are, inline in the dialog whose button sent the request or as a toast's
// description, so each one is a sentence written for the user:
//   - the outcome comes first, then the cause, then what to do next;
//   - one voice, with contractions, matching the UI's "Couldn't …";
//   - at most maxMessageLen characters, so a dialog stays readable on a phone;
//   - it names the button that tries again ("Delete", "Skip", "Remove",
//     "Save", "Preview", "Add", "Connect", "Retry");
//   - it points at the log in one wording only: "Check the server log, fix
//     the problem, then <Button> again.";
//   - it says "elsewhere" for another tab or the CLI, never "another window";
//   - it never echoes what the client sent, nor a path, SQL or a Go error.
//
// TestMessagesFollowTheCopyRules checks the mechanical rules on every one.

// maxMessageLen is the longest a message may be, in characters.
const maxMessageLen = 220

// Shared answers.
const (
	// msgServerError answers a store error on a request that changes
	// something, when no route has a better sentence. Some of the change may
	// already be done by then (a write whose re-read failed, say), so it
	// promises nothing either way.
	msgServerError = "This may not have finished: something went wrong on the server. Check the server log, fix the problem, then try again."
	// msgLoadFailed answers a store error on a read (a GET), shown next to
	// the page's Retry button.
	msgLoadFailed = "Couldn't load this: something went wrong on the server. Check the server log, fix the problem, then Retry."
	// msgBadBody answers a request body that is not the JSON the route takes.
	msgBadBody = "The request couldn't be read. Reload the page, then try again."
	// msgBadQuality answers a quality outside the presets (validQuality).
	msgBadQuality = "Choose one of the listed qualities: best, 2160, 1440, 1080, 720 or 480."
	// msgBeingDeleted answers a download or retry of a lesson whose files are
	// being deleted right now.
	msgBeingDeleted = "This lesson's files are being deleted right now. Try again once that's finished."
	// msgUnauthorized answers an /api request without the API token (the
	// auth middleware, the only 401 in the API).
	msgUnauthorized = "This needs DrumDrop's API token. Enter it, then try again."
	// msgNoDaemon answers a pause, resume or sync on a server running
	// without the daemon.
	msgNoDaemon = "This server runs without the download daemon, so there's nothing to pause, resume or sync."
	// msgNoPlanner answers a dry-run sync on a server without a planner.
	msgNoPlanner = "This server runs without the sync planner, so it can't say what a sync would queue."
	// msgDryRunFailed answers a dry-run sync that failed.
	msgDryRunFailed = "Couldn't work out what a sync would queue. Check the server log, fix the problem, then try again."
	// msgNoProgressStream answers the live-progress stream on a server
	// without one.
	msgNoProgressStream = "Live progress isn't available on this server."
	// msgDatabaseDown answers the readiness probe when the database doesn't
	// answer.
	msgDatabaseDown = "The database isn't answering. Check the server log, fix the problem, then try again."
)

// msgBadPathNumber answers a path value that is not a whole number (pathInt,
// pathInt64). name is the route's own parameter name, never the client's
// input, which is not echoed.
func msgBadPathNumber(name string) string {
	return fmt.Sprintf("The %s in the address must be a whole number. Check the link, then try again.", name)
}

// msgBadQueryNumber answers a query value that is not a whole number
// (queryInt). name is the parameter's name, never the client's input.
func msgBadQueryNumber(name string) string {
	return fmt.Sprintf("The %s must be a whole number. Check the link, then try again.", name)
}

// Reads of one item (GET /api/lessons/{id}, /api/follows/{id}, /api/jobs/{id}).
const (
	msgNoSuchLesson = "There's no lesson with that id in DrumDrop."
	msgNoSuchFollow = "There's no follow with that id in DrumDrop."
	msgNoSuchJob    = "There's no job with that id in DrumDrop."
)

// Lesson download and un-skip (POST /api/lessons/{id}/download, …/unskip).
const (
	msgDownloadGone    = "This lesson is no longer in DrumDrop: its follow was removed meanwhile. There's nothing left to download."
	msgDownloadJobGone = "The download was queued, then removed meanwhile by a Skip or delete elsewhere, so it won't run."
	msgUnskipGone      = "This lesson is no longer in DrumDrop: its follow was removed meanwhile. There's nothing left to un-skip."
)

// Job cancel and retry (POST /api/jobs/{id}/cancel, …/retry).
const (
	msgCancelGone   = "This job is no longer in DrumDrop: it was removed meanwhile, elsewhere. There's nothing left to cancel."
	msgRetryGone    = "This job is no longer in DrumDrop: it was removed meanwhile, elsewhere. There's nothing left to retry."
	msgJobNotActive = "This job has already ended, so there's nothing to cancel."
	msgJobNotRetry  = "Only a failed or canceled job can be retried, and this one isn't."
)

// Lesson delete (DELETE /api/lessons/{id}).
const (
	msgLessonGone      = "This lesson is no longer in DrumDrop: its follow was removed meanwhile. There's nothing left to delete."
	msgLessonDeleting  = "This lesson is already being deleted. Wait a moment, then Delete again if it's still listed."
	msgLessonFilesKept = "Some of this lesson's files couldn't be deleted, so it stays listed with the ones that are left. Any download of it was stopped. Check the server log, fix the problem, then Delete again."
	msgLessonNoClaims  = "Nothing was deleted, but any download of this lesson was stopped: DrumDrop couldn't read which files other lessons use. Check the server log, fix the problem, then Delete again."
	msgLessonChanged   = "The delete didn't finish: this lesson changed while it ran, so some of its files may be gone while it's still listed. Delete again to finish."
	msgLessonNotSaved  = "This lesson's files were deleted as far as possible, but DrumDrop couldn't save that, so it may still list them. Check the server log, fix the problem, then Delete again."
)

// Lesson skip (POST /api/lessons/{id}/skip).
const (
	msgSkipGone     = "This lesson is no longer in DrumDrop: its follow was removed meanwhile. There's nothing left to skip."
	msgSkipDeleting = "This lesson's files are being deleted right now, and that skips it anyway. If it still isn't skipped in a moment, Skip again."
)

// Follow remove (DELETE /api/follows/{id}).
const (
	msgFollowGone          = "This follow is no longer in DrumDrop: it was removed meanwhile, elsewhere. There's nothing left to remove."
	msgFollowDeleting      = "A lesson of this follow is being deleted right now. Wait a moment, then Remove again."
	msgFollowFilesKept     = "The follow was kept: some of its lessons' files couldn't be deleted. Its downloads were stopped, and lessons whose files are gone are now skipped. Check the server log, fix the problem, then Remove again."
	msgFollowNoClaims      = "Nothing was deleted and the follow was kept: DrumDrop couldn't read which files other lessons use. Its downloads were stopped. Check the server log, fix the problem, then Remove again."
	msgFollowLessonChanged = "The follow was kept: one of its lessons changed during the removal, and some of its files may be gone. Its downloads were stopped, and lessons whose files are gone are now skipped. Remove again to finish."
	msgFollowNewFiles      = "The follow was kept: one of its lessons finished downloading during the removal. Its other downloads were stopped, and lessons whose files are gone are now skipped. Remove again to delete the rest."
	msgFollowNotSaved      = "The follow's files were deleted as far as possible, but DrumDrop couldn't save that, so the follow was kept and may still list them. Check the server log, fix the problem, then Remove again."
	msgFollowGoneLate      = "This follow was removed elsewhere while its lessons' files were being deleted. There's nothing left to remove."
	msgFollowNotRemoved    = "The follow wasn't removed, and nothing was changed. Check the server log, fix the problem, then Remove again."
)

// Follow edit (PATCH /api/follows/{id}).
const (
	msgEditGone = "This follow is no longer in DrumDrop: it was removed meanwhile, elsewhere. There's nothing left to save."
)

// Follow add and preview (POST /api/follows, GET /api/preview).
const (
	msgNoContentID        = "No content id was found in “URL or id”: it needs the number of a lesson or course. Enter the id, or a link that contains it, then Preview again."
	msgPreviewNothing     = "Enter a URL or id, or an instructor's name, slug or link, then Preview again."
	msgBadKind            = "Choose Node or Instructor, then try again."
	msgSlugRequired       = "Enter the instructor's name, slug or link, then Preview again."
	msgBadSlug            = "That instructor can't be looked up. Enter their name or slug in unaccented letters, digits, spaces and hyphens, like Jared Falk, or a link to their coach page. Then Preview again."
	msgBadBrand           = "That brand can't be looked up: enter drumeo, pianote, guitareo, singeo or playbass, or leave Brand empty for drumeo. Then Preview again."
	msgBrandMismatch      = "That coach page is for another brand than the one in Brand. Leave Brand empty to use the link's, or change it to match, then Preview again."
	msgNoInstructor       = "Musora has no instructor by that name, slug or link. Check its spelling, then Preview again."
	msgPreviewUnreachable = "Nothing could be looked up: Musora couldn't be reached, or its answer couldn't be read. Wait a moment, then Preview again."
	msgAddUnreachable     = "The follow wasn't added: Musora couldn't be reached, or its answer couldn't be read. Wait a moment, then Add again."
	msgFollowNotAdded     = "The follow wasn't added, and nothing was changed. Check the server log, fix the problem, then Add again."
)

// Musora login (POST /api/session). None of these is a 401: the web client
// takes any 401 for its own API token being wrong, and clears it.
const (
	msgLoginMissing     = "Enter your Musora email and password, then Connect again."
	msgLoginRejected    = "Musora didn't accept that email and password. Check them, then Connect again."
	msgLoginUnreachable = "You're not connected: Musora couldn't be reached, or its answer couldn't be read. Wait a moment, then Connect again."
	msgLoginNotSaved    = "Musora accepted the login, but DrumDrop couldn't save the session. Check the server log, fix the problem, then Connect again."
)
