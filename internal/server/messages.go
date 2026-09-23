package server

// The fixed messages the API answers with (hard rule 11: a handler never sends
// err.Error(); the detail goes to the server log). The web UI shows the ones
// below inline, in the dialog whose button sent the request, so each is a
// sentence that says what happened and what to do next, and names that
// dialog's button ("Delete", "Skip", "Remove", "Save", "Preview", "Add").

// Shared answers.
const (
	// msgServerError answers a store error no route has a better sentence for.
	msgServerError = "Something went wrong on the server, so this was not done. The server log says what went wrong; try again once it is fixed."
	// msgBadBody answers a request body that is not the JSON the route takes.
	msgBadBody = "The request could not be read. Reload the page, then try again."
	// msgBadQuality answers a quality outside the presets (validQuality).
	msgBadQuality = "Choose one of the listed qualities: best, 2160, 1440, 1080, 720 or 480."
	// msgBeingDeleted answers a download or retry of a lesson whose files are
	// being deleted right now.
	msgBeingDeleted = "This lesson's files are being deleted right now. Try again once that has finished."
)

// Lesson delete (DELETE /api/lessons/{id}).
const (
	msgLessonGone      = "This lesson is no longer in DrumDrop: it was removed meanwhile, with its follow or from another window. There is nothing left to delete."
	msgLessonDeleting  = "This lesson is already being deleted. Wait a moment, then Delete again if it is still listed."
	msgLessonFilesKept = "Not all of this lesson's files could be deleted. Any download of it was stopped, and it stays listed with the files that are left. The server log says what went wrong; fix that, then Delete again."
	msgLessonNoClaims  = "Nothing was deleted: DrumDrop could not read which files the other lessons use, so it could not tell which files are this lesson's alone. Any download of it was stopped. The server log says what went wrong; fix that, then Delete again."
	msgLessonChanged   = "This lesson changed while it was being deleted, so DrumDrop did not update it: some of its files may already be gone while it is still listed. Delete again to finish."
	msgLessonNotSaved  = "This lesson's files were deleted as far as possible, but DrumDrop could not save that, so it may still list them. The server log says what went wrong; Delete again to finish."
)

// Lesson skip (POST /api/lessons/{id}/skip).
const (
	msgSkipGone     = "This lesson is no longer in DrumDrop: it was removed meanwhile, with its follow or from another window. There is nothing left to skip."
	msgSkipDeleting = "This lesson's files are being deleted right now, which skips it once they are gone. Wait a moment, then Skip again only if it is still not skipped."
)

// Follow remove (DELETE /api/follows/{id}).
const (
	msgFollowGone          = "This follow is no longer in DrumDrop: it was removed meanwhile, from another window. There is nothing left to remove."
	msgFollowDeleting      = "A lesson of this follow is being deleted right now. Wait a moment, then Remove again."
	msgFollowFilesKept     = "Not every lesson's files could be deleted, so the follow was kept. The follow's downloads were stopped, lessons whose files were deleted are marked skipped, and the rest stay listed with the files that are left. The server log says what went wrong; fix that, then Remove again."
	msgFollowNoClaims      = "Nothing was deleted and the follow was kept: DrumDrop could not read which files the other lessons use, so it could not tell which files are this follow's alone. The follow's downloads were stopped. The server log says what went wrong; fix that, then Remove again."
	msgFollowLessonChanged = "A lesson of this follow changed while the follow was being removed, so the follow was kept and DrumDrop did not update that lesson: some of its files may already be gone while it is still listed. The follow's downloads were stopped, and lessons whose files were deleted are marked skipped. Remove again to delete the remaining files."
	msgFollowNewFiles      = "A lesson of this follow finished downloading while the follow was being removed, so the follow was kept with that lesson's files. The follow's other downloads were stopped, and lessons whose files were deleted are marked skipped. Remove again to delete the new files too."
	msgFollowNotSaved      = "This follow's files were deleted as far as possible, but DrumDrop could not save that, so the follow was kept and may still list them. The server log says what went wrong; Remove again to finish."
	msgFollowGoneLate      = "This follow was removed meanwhile, from another window, while its lessons' files were being deleted. There is nothing left to remove."
	msgFollowNotRemoved    = "The follow could not be removed, and nothing was changed. The server log says what went wrong; Remove again once it is fixed."
)

// Follow edit (PATCH /api/follows/{id}).
const (
	msgEditGone = "This follow is no longer in DrumDrop: it was removed meanwhile, from another window. There is nothing left to save."
)

// Follow add and preview (POST /api/follows, GET /api/preview).
const (
	msgNoContentID       = "No content id was found in “URL or id”: it needs the number of a lesson or course. Enter the id, or a link that contains it, then Preview again."
	msgPreviewNothing    = "Enter a URL or id, or an instructor's slug, then Preview again."
	msgBadKind           = "Choose Node or Instructor, then try again."
	msgSlugRequired      = "Enter the instructor's slug, then Preview again."
	msgNoInstructor      = "Musora has no instructor with that slug. Check the spelling of the slug, then Preview again."
	msgMusoraUnreachable = "Musora could not be reached, or its answer could not be read, so nothing could be looked up. Try again in a moment; the server log says what went wrong."
	msgFollowNotAdded    = "The follow could not be added, and nothing was changed. The server log says what went wrong; Add again once it is fixed."
)
