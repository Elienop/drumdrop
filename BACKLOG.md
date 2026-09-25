# Backlog

The living list of known issues, follow-ups and open questions for drumdrop, so nobody has
to re-read the project, the lesson tree or an old investigation to know what still needs
attention. Each entry says what it is, why it matters, the evidence (a file or symbol plus a
PR or SHA, or a command to re-run), and where the detail lives.

**How to use:** work *Next up* first. *Open bugs & hardening* and *Housekeeping &
dependencies* are work anyone could pick up. *Open questions* wait on an owner decision, so
do not start one without it. Anything already decided lives under *Accepted residuals and
deliberate decisions*, so it never reads as an open task. When something ships, move its
entry to *Recently shipped* with the PR number. When something new turns up (a review
finding, an incident, a parked idea), add it here in the same commit that discovers it.

**IDs** (`D1`, `D2`, …) are stable. An entry keeps its ID when it moves between sections, and
an ID is never reused (the owner's vault cites them). A new entry takes the next number after
the highest ID on this page: the next new ID is D160 on 2026-09-25 (*moves*; re-check the
highest ID before you use it).

**Evidence commands** run from the repo root. A number marked *(moves)* was true on the day
it was written; re-run its command before you trust it.

_Last groomed: 2026-09-23, when this board was created during the vault onboarding (branch
`chore/vault-onboarding`). Every entry was re-checked against the code at `1c2dbda` (v0.7.1;
the code has not changed since) with the command in its Evidence line. Sources: the
2026-09-23 audit, the lesson tree (`musora-downloader-project.md` and its siblings), the old
standalone vault's verification findings, and a read-only SonarQube check. The same day's
review corrected D7, D9, D22 and D29, extended D45, shipped D24, and added D51–D54, each
checked against the same code. The final review sharpened D7, D52 and D54 and moved D51 and
D52 into *Next up*. The round-5 docs pass (2026-09-24, branch `fix-library-delete-and-move`)
added D96–D113 and corrected D51, D52, D58, D66, D72, D76, D82, D85, D89, D92, D93 and D95
against the code at `639e5d8`. The round-5d pass (same day and branch) added D114–D120 and
corrected D63, D64, D85, D107, D108 and D113 against its own code. The round-5e pass (same
day and branch) added D121–D125 and corrected D52, D58, D107, D113 and D114–D120 against
its own code: D120's unplugged-drive sentence was wrong, and D107 and D114–D118 credited
the round-5d reviews for round-5c findings. The round-5f pass (same day and branch) added
D126–D128 and corrected D58, D101, D113, D120, D121 and D125 against its own code, and
the round-5e security seat's probes. The round-5g pass (same day and branch) added
D129–D131 (D131 settled the same day by the refined ruling (t)) and extended D101 and
D113 against its own code and a Chromium probe. The round-5h pass (same day and branch)
added D132–D137, settled D129 by ruling (v), and corrected D58, D113, D128 and D130
against its own code and the round-5f/5g seats' probes. The round-5i pass (same day and
branch) built ruling (y), closed the round-5h security F1, and corrected D58, D101, D113,
D128, D130, D135, D136 and D137 against its own code and the round-5h seats' probes; it
added no entry. The round-5j pass (2026-09-25, same branch) narrowed ruling (y)'s check to
the lesson's own files, corrected D58, D113, D128 and D137 against its own code and the
round-5i seats' reports, and added D138 and D139. The round-5k pass (same day and
branch) reworded the left-behind refusals, corrected D58, D113, D128, D137, D138 and D139
against its own code and the round-5j seats' reports, and added D140 and D141. The
round-5m pass (same day and branch) corrected D113 and D137 against the round-5l seats'
reports, and re-anchored the line numbers round 5l's refactor moved in D58, D128, D130
and D137; it added no entry._

## Next up

*Order:* D51 and D52, which led because they left the owner's files wrong on disk, have
shipped on this branch; the entries below keep their order. This order is the 2026-09-23
onboarding session's proposal, not an owner ruling; the owner may reorder.

- **D1 · Build with a patched Go toolchain.**
  - *What:* `go.mod` pins `go 1.26.3`, and CI and the release binaries build on exactly that
    version (setup-go reads `go-version-file`). `govulncheck` finds standard-library
    vulnerabilities that drumdrop's code actually reaches, in `net/http`, `crypto/tls`,
    `crypto/x509`, `net/url`, `net/textproto`, `encoding/asn1` and `os`. All are fixed by
    go1.26.6 (9 on 2026-09-25, *moves*). The `os` one, GO-2026-4970, is an `os.Root` escape
    through a final symlink when the path ends in `/` (fixed in go1.26.5). The library
    placement guards are built on `os.Root`; drumdrop's paths never end in `/`
    (`filepath.Join` strips it), so it isn't reachable that way today, but it is the one
    closest to the guards.
  - *Why:* drumdrop is a network server (the HTTP API) and a TLS client (Musora, soundslice,
    YouTube). These are the packages it uses for both. The fix should be a version bump plus
    the normal gates, not a code change. While you're there, the Dockerfile's Go stage uses
    the floating `golang:1.26-alpine`, so the image and the release binaries can be built by
    different Go patch versions. Pin both to the same one.
  - *Evidence:* `grep '^go ' go.mod` · `~/go/bin/govulncheck ./...` · `grep -n 'golang:' Dockerfile`

- **D2 · Update react-router, which has high-severity advisories in the shipped web bundle.**
  - *What:* the lockfile has `react-router` / `react-router-dom` 7.16.0, and `npm audit` flags
    every version up to 7.18.1 as high severity. The fix stays inside the same major version
    (`npm audit fix`; `npm outdated` lists 7.18.4).
  - *Why:* the dev-tool advisories in D17 never reach users, but react-router ships to the
    browser as part of the web UI. Some of its advisories only affect server rendering, which
    drumdrop doesn't use. The open-redirect one affects `<Link>` / `useNavigate`, which the UI
    does use. The advisories were not triaged one by one; the bump is cheap enough that it
    doesn't need that.
  - *Evidence:* `cd web && npm audit --omit=dev` · `cd web && npm outdated react-router-dom`

- **D3 · Keep yt-dlp current in the published image.**
  - *What:* the Docker image downloads the newest yt-dlp when it is built, and it is only
    rebuilt when a release is cut. The v0.7.1 image was built on 2026-06-09, so its yt-dlp is
    from then, and nothing rebuilds it on a schedule. deno (`bin-2.1.4`) and ffmpeg (`7.1`)
    are pinned.
  - *Why:* song videos come from YouTube, which keeps changing the challenge yt-dlp has to
    solve. The production song failures fixed in v0.7.1 (#19) came from exactly this: a stale
    yt-dlp. Song downloads are therefore the part of drumdrop most likely to be broken right
    now. Lessons (Vimeo HLS) have never needed any of this.
  - *Decision needed (owner):* either a scheduled rebuild (for example a weekly workflow that
    rebuilds the image and re-pushes `:latest` without cutting a version), or cutting a
    release whenever songs break. Any merge to main already cuts a release (D36), so merging
    the branch that adds this board refreshes yt-dlp once.
  - *Evidence:* the yt-dlp layer and its comment in `Dockerfile` ·
    `grep -rn schedule .github/workflows` (no match) · on the server:
    `docker exec drumdrop yt-dlp --version` (see D26)
  - *Detail:* `drumdrop-song-soundslice-video.md`; vault note drumdrop-deploy-runbook.

- **D4 · The stored Musora password is written but never read.**
  - *What:* logging in (`drumdrop login`, or the web UI's `POST /api/session`) saves the
    email and password to `credentials.enc`. They are encrypted, but the key (`secret.key`)
    sits in the same config folder. Nothing reads them back: `LoadCreds` has no caller. The
    web handler's own comment says they are saved "so the daemon can refresh later", and that
    re-login was never built. Downloads don't use the login at all today (the Musora queries
    and video fetches send no cookie). The saved session only powers `whoami` and the web
    UI's "connected" check.
  - *Why:* the app keeps a recoverable copy of the account password for a feature that
    doesn't exist. Anyone who gets the config folder can decrypt it, because the key is right
    beside it. Either build the re-login or stop storing the password. That choice is D25.
  - *Evidence:* `grep -rn 'LoadCreds(' --include=*.go .` (only the definition, in
    `internal/musora/auth.go`) · `grep -rn 'SaveCreds(' --include=*.go .`
    (`cmd/drumdrop/run.go`, `internal/server/preview.go`)
  - *Detail:* vault note drumdrop-auth-posture; `musora-downloader-project.md`.

## Open bugs & hardening

D53 waits on an owner decision.

- **D58 · A legacy re-download whose folder changes leaves the old files behind.**
  - *What:* partly covered by D66, and by the owner's choice partly open again since round
    5 (ruling (e), D113). A placement removes the lesson's previous folder, when its row
    records another one (`previousFolder`), only when the download brought back every file
    in it; otherwise the folder stays where it is, no longer recorded, and the log says why
    (`⚠ … left its previous folder`). So a default-layout lesson whose title changed leaves
    `05 - Old` next to `05 - New` whenever the old folder holds a file the re-download
    didn't bring back, and a switch from the default layout to plex-tv leaves the
    `Course/NN - Lesson` folder in the same case; the owner deletes such a folder by hand.
    (Tested for a title change, a library added later, and both into plex-tv:
    `TestWorkerKeepsAPreviousFolderTheDownloadDoesNotFullyBringBack` and
    `TestWorkerRemovesAPreviousFolderTheDownloadFullyBringsBack`; the layout switch itself
    goes through the same `previousFolder` call in `moveToLibraryPlexTV`, read from the
    code.) Carrying the extra files over is D111. What remains of the legacy gap: a plex-tv
    lesson moved before the library record existed (`library_entries` NULL) that is
    re-downloaded after a switch to the default layout, or whose previous episode name is
    ambiguous (the move logs "the previous download's library files are not known"). A
    legacy lesson with no video whose title has changed is not found by the name fallback
    either. The previous folder also stays when another lesson records something in it, or
    when it is outside the downloads and library dirs (after a remount, say); since round 5
    the log says so. (When the other lessons' records can't be read, the lesson is not
    downloaded at all: it fails before the download, so that case is gone.)
  - *Every way a legacy row's season files end up claimed by nobody* (round-5d security
    review, F4; its probe re-run at round 5e): (a) plex-tv, an ambiguous episode name,
    and the move succeeds: the record holds only what it placed, and the log says only
    "…not known, so none were removed"; (b) a legacy row with no video owns its files
    through its current title alone (`legacyEpisodeBases` builds the episode name from
    it), and a sync writes the new title as soon as Musora changes it (`UpsertLesson`),
    so a rename on Musora's side un-claims them with no download at all; (c)
    `DRUMDROP_LIBRARY_DIR` unset (the setting removed after plex-tv placed the row):
    `place()` never asks
    `keptInLibrary` without a library, so a re-download is placed in downloads, records
    that folder, and the season files are claimed by nobody, with no log line; (d) a
    switch to the default layout, told in full as D119. Round 5f closed one path into
    (c) for a default-layout folder: a symlink at the lesson's downloads path that led to
    its recorded library folder made `placeLessonFolder` take that folder for its
    destination (`recordsFolder` followed the symlink through `sameDir`), then replace
    the symlink with a new folder, so the library folder was recorded by no lesson and
    the log named only the symlink, as "the lesson's earlier download" (round-5e code
    review, L1). `recordsFolder` now reads that name as the placement does (`Lstat`), and
    `previousFolder` uses it, so the library folder is the lesson's previous folder: it
    stays, and is logged, as it is without the symlink
    (`TestWorkerPlacementWithoutALibraryLeavesTheFolderASymlinkLedTo`). (e) The library
    setting pointed at a different folder with the files not moved (recorded rows too,
    not only legacy ones): every reader looks for the season folder under the new
    setting. Since round 5i (ruling (y), narrowed by round 5j), for a lesson filed in a
    season folder, a Delete and a refused placement's fallback refuse while one of the
    lesson's own files is still in its old season folder; a re-download whose placement
    succeeds still leaves the old files claimed by nobody, and a lesson kept in downloads
    whose record names season files is not covered at all (D137).
  - *A library folder spelled another way is not seen by ruling (f)'s refusal*
    (round-5f/5g security review, N3; the same at `5cb8fbe`, before round 5f). The
    refusal asks `inLibrary` first, and its first test is lexical
    (`library.Inside(lib, …)`, `internal/scheduler/worker_record.go:354`). With
    `DRUMDROP_LIBRARY_DIR` set through a symlink (or the library remounted at another
    path) and the row's folder spelled the real way, a refused library placement falls
    back to downloads, and the library folder is left where it is, logged as "inside
    neither the downloads folder nor the library" (`previousFolder`,
    `internal/scheduler/place.go:470`) and claimed by no lesson; when the refused
    target is the row's own folder under the other spelling, the same happens to it.
    A row spelled through the symlink is refused as it should be. The delete side of
    the same spelling gap is D67. *Fix (new mechanism, can wait):* decide containment
    by identity, as the repo already does in `Claims.within`
    (`internal/library/ownership.go:613`, through `sameFolder`, `:199-204`) and in
    `library.Remove`'s root lookup (`internal/library/remove.go:28-31`), both on
    `os.SameFile`. Seen in the security seat's scratch probe (S2, S2b, S3), not
    pinned by a committed test.
  - *Why:* Plex shows the old copy too, and no delete will ever remove it. For a lesson
    with a record, a folder left this way is the owner's ruling and is logged; the legacy
    rows leave theirs without a word.
  - *Evidence:* `grep -n 'previous download.s library files are not known' internal/scheduler/*.go`
    · `grep -n 'func previousFolder' -A21 internal/scheduler/place.go` (season folders are
    skipped; one another lesson records files in, or one outside every root, stays with a
    reason) · `go test -count=1 -run 'PreviousFolder' ./internal/scheduler/` ·
    `grep -n 'func legacyEpisodeBases' -A12 internal/library/ownership.go` (b) ·
    `grep -n 'lib := w.Cfg.LibraryDir' -A3 internal/scheduler/worker_record.go` (c)
- **D59 · A long title in a multibyte script can't be downloaded.**
  - *What:* `musora.Sanitize` caps a title at 150 *runes*, but file names are capped at 255
    *bytes*. The download's folder `NN - <title>` and its `<title>.mp4` hold the whole
    title, so 150 runes of CJK text (3 bytes each) is about 450 bytes and the download
    fails. The plex-tv move shortens the episode name to fit (`fitEpisodeBase`), but the
    download never gets that far.
  - *Why:* such a lesson fails every attempt.
  - *Evidence:* `grep -n 'len(r) > 150' internal/musora/download.go` ·
    `grep -n 'func lessonDir' -A3 internal/scheduler/worker.go`
- **D62 · Plex can see half-copied files during a cross-filesystem move.**
  - *What:* on the copy fallback (downloads and library on different filesystems) the
    entries are copied into place under their final names, so a Plex scan during the copy
    sees partial files. Copying under a hidden staging name and renaming would close it,
    but whether Plex skips hidden entries is unverified; D93's `.plexignore` waits on the
    same answer. (Moved here from D52's *Left open*.)
  - *Why:* Plex may index a truncated file until its next scan.
  - *Evidence:* `grep -n 'func copyTree\|func copyFile' internal/scheduler/plexmove.go`
- **D92 · The edges of a shutdown during a download.**
  - *What:* since D66 a shutdown (SIGINT or SIGTERM to `daemon` or `serve`) stops the
    download in progress and leaves its job `running`, for the next `serve` or looping
    `daemon` start to requeue (a `daemon --once` start doesn't: D95); the lesson is
    neither skipped nor failed for it. Five edges remain. (a) A shutdown that
    lands just as yt-dlp returns success drops the finished download: the worker reads the
    stop before the result, so the lesson downloads again from the start. (b) A Cancel and
    a shutdown at the same moment resolve as a shutdown: the Cancel is lost, and the lesson
    downloads again at the next start. (c) The lesson reads `downloading` until its job
    runs again: `RequeueStaleRunning` moves the job only. (d) Musora's lesson lookup takes
    no context, so one that fails while drumdrop shuts down is recorded as a Musora
    failure (`failMusora`) instead of starting over; the next cycle retries it like any
    failure (a lesson whose earlier download is still on disk stays downloaded with a
    note instead, and waits for *Download*: rulings (h) and (o), D113). (e)
    `ConfirmDownload` failing because the shutdown ended its context is logged and the run
    goes on. That one is harmless, since `FinishDownload` still checks the job, and it
    predates D66.
  - *Why:* (a) and (b) cost a re-download or a Cancel; (c) and (d) show a state that isn't
    quite true for a while. None of them loses a file.
  - *Evidence:* `grep -n 'jobCtx.Err() != nil' -A8 internal/scheduler/worker.go` ·
    `grep -n 'func (s \*Store) RequeueStaleRunning' -A8 internal/database/jobs.go` ·
    `grep -n 'Resolver.Resolve' -A7 internal/scheduler/worker.go` ·
    `go test -count=1 -run 'ResolveFailureDuringShutdown' ./internal/scheduler/` (d, pinned
    as it is)
- **D93 · What `.drumdrop-in-progress` can leave behind, or show to Plex.**
  - *What:* (a) A crash or a kill between a placement setting entries aside and the
    download being recorded leaves `<root>/.drumdrop-in-progress/replaced-<job id>/` (or
    `replaced-<job id>.<k>`) in the downloads dir or the library. It may hold the only copy
    of a lesson's earlier files, and until the job runs again the lesson's record can name
    them where they no longer are. So drumdrop never removes it: every `serve` or looping
    `daemon` start logs it ("Check them, then delete the folder"), and a person has to
    check it and delete it. The requeued job never reuses it (it opens
    `replaced-<id>.<k>`). An undo that could not put an entry back keeps its folder the
    same way, logged when it happens. (b) The folder's name starts with a dot, and it
    holds a `.plexignore` that ignores everything in it, five folders deep, so a Plex
    library pointed at the downloads dir (no library) should not show a download in
    progress, and one pointed at the library should not show a set-aside file. Whether
    Plex honours either is unconfirmed: it can't be checked here. D62's hidden staging name
    waits on the same answer.
  - *Why:* (a) leaves a folder only a person can clear. (b), if Plex honours neither, shows
    partial or replaced files until they go.
  - *Related, not in `.drumdrop-in-progress`:* since round 5 (ruling (e), D113) a
    placement also leaves a lesson's previous folder where it was, no longer recorded,
    whenever the download didn't bring back every file in it (after a title change, or a
    library added later). Those are leftovers too, each logged once
    (`⚠ <id> left its previous folder "<path>" where it was, no longer recorded: <why>`),
    and they are the owner's to check and delete. Plex shows the ones in a folder it
    watches, and no delete in drumdrop removes them (D58, D111).
  - *How to check (b):* on the owner's Plex server, put a video in a library's
    `.drumdrop-in-progress/` and scan the library.
  - *Evidence:* `grep -n 'Check them, then delete the folder' internal/scheduler/private.go` ·
    `grep -n 'const plexIgnore' internal/scheduler/private.go` ·
    `go test -count=1 -run 'SweepPrivateRemovesOnlyStoppedDownloads|NeverReusesTheAreaACrashLeft' ./internal/scheduler/`
- **D95 · Only a `serve` or looping `daemon` start recovers from a crash.**
  - *What:* (a) `sync` and `daemon --once` run no startup recovery (`Daemon.Recover`).
    `SweepPrivate` skips only the jobs its own worker is running (`isRunning` reads the
    worker's in-memory map), so run from `sync` it would delete the folder a live `serve`
    is downloading into, and `RequeueStaleRunning` would queue that download again. The
    jobs table can't stand in: a canceled job still uses its folder while its worker undoes
    a placement, and a queued job can be claimed between a check and the removal. The same
    holds for a second `serve` or looping `daemon` started beside a live one (D76), and it
    is worse than a lost folder: the second process claims the requeued job and runs it in
    the same folder (`job-<id>`, named after the job), so both yt-dlp runs write the same
    names, and yt-dlp renames its finished `.part` file into place by path, so one can
    promote the other's partial file. That file is then placed and recorded as finished,
    which is what hard rule 8 exists to prevent (security round 5, L3; traced in the code,
    not run with two processes). The README says to run one per database.
    (b) A `sync` or `daemon --once` run that is killed, or stopped with `Ctrl-C` or
    SIGTERM, leaves its job `running` (a stop removes the download's folder, but requeues
    nothing), and the planner won't queue a lesson that has a running job, so a user who
    only runs `sync` or `daemon --once` never gets that lesson back until a `serve` or
    looping `daemon` start. (c) A killed one-shot `drumdrop <id | url>` leaves its
    `.drumdrop-in-progress/run-<pid>-<k>/` folder, and nothing removes it (it is hidden from
    Plex like the rest). (d) Done in `a6b54c2`: `daemon --once` now stops its download on
    `Ctrl-C` or SIGTERM (`signal.NotifyContext`, as the looping `daemon` and `sync` already
    did) instead of leaving yt-dlp running in its own process group, and the worker removes
    its folder.
  - *Why:* a CLI-only user is left with a stuck lesson after any stop, and with a stray
    folder after a crash; two live processes on one database can record a partial video as
    finished.
  - *Fix (new mechanism, needs the owner's call):* a process-liveness signal, e.g. a lock
    that `daemon`, `serve` and `sync` hold for their lifetime, so recovery runs only when no
    other process is alive, and a second process can't start beside a live one; or `sync`
    requeues only the jobs it ran itself (a worker change).
  - *Evidence:* `grep -n 'func (w \*Worker) isRunning' -A6 internal/scheduler/private.go` ·
    `grep -n 'sync runs no startup recovery' -A8 cmd/drumdrop/follow.go` ·
    `grep -n 'func (w \*Worker) startPrivate' -A17 internal/scheduler/private.go` (the
    folder is the job's, and a start removes what is in it) ·
    `grep -n 'signal.NotifyContext\|RunOnce(ctx)' cmd/drumdrop/daemon.go` (d, done) ·
    `grep -rn '\.Recover(' --include=*.go cmd internal | grep -v _test` (`serve` and the
    looping `Run` call it; `RunOnce` doesn't)
- **D105 · A file written into a previous folder after its check is deleted with it.**
  - *What:* since round 5 (ruling (e), D113) a previous folder at another place goes only
    when the download brought back every file in it. The check reads the folder
    (`previousStays`) before it is set aside by path (`setAsidePath`), and the commit
    removes whatever the set-aside copy holds by then (`aside.finish(false)`, a
    `RemoveAll`). So a file written into the folder between the check and the set-aside
    (microseconds), or through a handle held on it (a shell whose working folder it is, an
    app writing into it) until the commit, which on a copy across filesystems includes
    copying the video, is deleted with it. Only a folder the download fully brought back
    is exposed, which is strictly narrower than before round 5, when the whole folder
    always went (round-5b code review, L3, and security review, L2; both probed it).
  - *Why:* an owner file written at the wrong moment is lost, the loss ruling (e) exists
    to prevent.
  - *Fix (new mechanism, can wait; a suggestion to verify):* at commit, run the same check
    on the set-aside copy again, and put the folder back, kept and logged, if it no longer
    passes.
  - *Engine:* none. Linux has no "rename only if unchanged" (`golang.org/x/sys` offers
    `RENAME_EXCHANGE`, `RENAME_NOREPLACE` and `RENAME_WHITEOUT`), and `os.Root.RemoveAll`
    removes whatever is there.
  - *Evidence:* `grep -n 'previousStays\|setAsidePath' internal/scheduler/place.go internal/scheduler/plexmove.go`
    · `grep -n 'RemoveAll' internal/scheduler/aside.go`
- **D102 · plex-tv: a new file an undo can't take out of a merged folder isn't recorded.**
  - *What:* when a plex-tv placement fails after it merged a folder (say
    `<episode> resources`, round 5) and its undo can't take a new file back out of that
    folder, the file stays in the library. The top-level entries an undo can't take back
    join the lesson's record (`res.kept`), but the ones inside a merged folder are only
    named in the logged error: `moveToLibraryPlexTV`'s `fail` drops the merged levels'
    list (`_, uerr := undoLevels(merged)`). Nothing is lost: the old file it replaced stays
    in its `replaced-<id>` folder, which is kept (D93), and the lesson falls back to
    downloads. (Round-5 fix report; round-5f code review, I1, probed with refused renames.)
  - *Why:* the file is in the library but no lesson records it, so no delete removes it.
  - *Fix (new mechanism, can wait):* add the merged levels' stuck entries to what the
    result keeps.
  - *Evidence:* `grep -n '_, uerr := undoLevels(merged)' internal/scheduler/plexmove.go`
- **D103 · A Course, Show or Season folder a placement creates isn't flushed.**
  - *What:* since round 5 a placement flushes the folder it placed into, the folder it
    renamed out of, and the parent of a lesson folder it made (`placeSteps`,
    `placeLessonFolder`). The folders above that are made by `openLibraryParent` with
    `os.Root.MkdirAll`, which neither says which folders it made nor flushes them, so a
    new `Course/`, or a new `Show/` and `Season 01/`, is never flushed in its own parent.
    On ext4 and XFS a later folder flush commits the earlier mkdir with the journal; on
    btrfs and ZFS (the TrueNAS target) that ordering is unconfirmed. (Round-5f code
    review, I3, probed; the part of round-5 security review I2 that round 5 left open.)
  - *Why:* after a power loss the lesson could be recorded while its new folder never
    reached the disk.
  - *Fix (new mechanism, can wait):* not designed.
  - *Engine:* `os.Root.MkdirAll` (`$(go env GOROOT)/src/os/root.go`) returns only an
    error.
  - *Evidence:* `grep -n 'MkdirAll' internal/scheduler/library.go` ·
    `grep -n 'syncIn(' internal/scheduler/place.go`
- **D104 · On a case-insensitive disk an undo puts a file back under the download's
  spelling.**
  - *What:* a placement sets an entry aside under the name it is placing, so on a
    case-insensitive disk the owner's `A.PDF`, found at the download's `a.pdf`, is set
    aside as `a.pdf`, and an undo puts it back as `a.pdf`. The content is intact
    (round-5f security review, I-3).
  - *Why:* cosmetic, but it renames a file the owner named.
  - *Evidence:* `grep -n 'renameAt(' internal/scheduler/aside.go` (both renames use the
    name being placed) · `grep -n 'name := filepath.Base(st.dst)' internal/scheduler/place.go`
- **D107 · A Retry shows a downloaded lesson as pending until the retried job ends.**
  - *What:* `RetryJob` queues a failed or canceled job again and resets its lesson to
    `pending` with its note cleared, whatever the lesson's status. Since round 5 (ruling
    (h), D113) a failed re-download leaves the lesson `downloaded` with a note and its job
    failed, so *Retry* in the Queue shows a lesson that still records its files as
    pending, with no note, until the retried job ends (a success records it downloaded; a
    failure leaves it downloaded with the note again). Its files stay recorded throughout.
    (Round-5c fix report.)
  - *Why:* for the length of a download the Lessons page shows a lesson that records its
    files as pending, and its note is gone.
    The round-5c UI review saw it from the Queue (Info, V1): after a *Retry* there, a
    lesson whose earlier download is kept reads pending, with no note.
  - *Fix (can wait; a suggestion to verify):* reset only a lesson that records no files,
    with the store's own `hasFilesSQL` test, as `keepOrSQL` and `endDownloadSQL` do
    (they add the on-disk check of ruling (o), `keepsFilesSQL`).
  - *Evidence:* `grep -n 'func (s \*Store) RetryJob' -A45 internal/database/jobs.go` (the
    lesson `UPDATE` at the end)
- **D108 · A damaged record costs one Musora lookup per queued lesson per cycle.**
  - *What:* the worker asks Musora for the lesson (`Resolver.Resolve`) before it checks
    what the download needs to be recorded (`checkBeforeDownload`: the other lessons'
    claims, which one damaged record anywhere makes unreadable). So while a record is
    damaged, every queued lesson costs a Musora lookup each cycle and then fails without
    downloading (`failNotStarted`); one with no files is queued again next cycle, one with
    files on disk stays downloaded with a note (D113). The round-4 code review proposed
    moving the check ahead of the lookup, if that is a pure reorder; the round-5, 5b and
    5f code reviews found it neither done nor recorded.
  - *Why:* no download is wasted, but a damaged record turns every cycle into one Musora
    request and one failed job per queued lesson.
  - *Fix (can wait; a suggestion to verify):* read the lesson's record and run
    `checkBeforeDownload` before `Resolve`. Not quite a pure reorder: today the failure
    names the lesson by the title `Resolve` returned.
  - *Evidence:* `grep -n 'Resolver.Resolve\|checkBeforeDownload(ctx)' internal/scheduler/worker.go`
- **D114 · *Download* then *Retry* queues two jobs for one lesson.**
  - *What:* `EnqueueJob` reuses a lesson's queued or running job, but `RetryJob` requeues
    a failed or canceled job without that check. So *Download* on a lesson whose last job
    failed (a new job is queued) followed by *Retry* on the failed one in the Queue leaves
    two queued jobs for one lesson, and both download it, one after the other (round-5c
    security review, Low 3). Since ruling (m) each press also starts a sync at once.
  - *Why:* a wasted download; each job has its own private folder, so the two don't write
    into each other, and the second's placement replaces the first's.
  - *Fix (can wait; a suggestion to verify):* give `RetryJob` `EnqueueJob`'s check (answer
    with the active job, or refuse), or let the database refuse it: a partial unique index
    on `jobs(railcontent_id) WHERE status IN ('queued','running')`, as a migration (rule 5:
    its test changes too). With the index, `RetryJob`'s `UPDATE` fails with a constraint
    error when another job is active, which it would have to answer as a 409.
  - *Evidence:* `grep -n 'func (s \*Store) RetryJob' -A20 internal/database/jobs.go` ·
    `grep -n 'Atomic dedup' -A6 internal/database/jobs.go`
- **D118 · With a title over about 213 bytes, ruling (j) doesn't hold.**
  - *What:* a plex-tv episode base is shortened so that every name fits in 255 bytes, by
    the longest suffix among the entries of *this* download (`fitEpisodeBase`). A long
    title (roughly 213 bytes and up, by the round-5c code review's count) can then get a
    different base on a re-download that brings back a longer or shorter suffix, a
    resources folder say. The re-download then reads as a title change: what the lesson
    recorded at the old base that it didn't bring back goes, instead of staying as ruling
    (j) wants (round-5c code review, Info 2).
  - *Why:* rare (the title alone must be that long), but a caption or poster the
    re-download failed to fetch is deleted.
  - *Fix (can wait; a suggestion to verify):* shorten by the longest suffix drumdrop can
    ever write, not the longest in this download, so one title always has one base. That
    renames nothing already placed only if the old base is recognised too.
  - *Evidence:* `grep -n 'longest\|func fitEpisodeBase' internal/scheduler/plexmove.go`
- **D119 · A legacy plex-tv lesson re-downloaded in the default layout leaves its season
  files untracked.**
  - *What:* a row a version before the plex-tv record placed (`output_dir` a season
    folder, `library_entries` NULL) owns its season-folder files through `output_dir` and
    the name grammar alone. After a switch to the default layout, a re-download that is
    placed in the library records the new lesson folder as `output_dir` and leaves
    `library_entries` NULL (the default layout never writes a record). No lesson claims
    the season files from then on, and a delete no longer removes them; nothing is
    deleted and nothing is logged. A *refused* placement of such a row fails the attempt
    instead (D1 of round 5d, `keptInLibrary`); only the successful one loses track. Found
    while fixing that fallback; `main` does the same. It is case (d) of D58, which lists
    the other ways a legacy row's season files end up claimed by nobody.
  - *Why:* files in the library that no lesson records, and that the UI can't delete.
  - *Fix (can wait; a suggestion to verify):* on a successful default-layout placement of
    such a row, record what the name grammar gives it (as the plex-tv move already does
    for a refused move), or leave its season files named in the log.
  - *Evidence:* `grep -n 'rec.LibraryEntries == nil' internal/database/downloads.go` ·
    `grep -n 'IsSeasonDir(prev)' internal/scheduler/place.go`
- **D121 · A missing or unmounted library folder is written to, not refused.**
  - *What:* every placement creates the library root if it is missing (`os.MkdirAll` in
    `openLibraryParent`), and an empty mount point is an ordinary empty folder. So while
    the library drive is out, a lesson's download (a new one, or a re-download ruling (o)
    retries) is placed on the disk underneath, recorded there, and hidden by the drive
    once it is back (D120; round-5d security review, F1).
  - *Why:* a lesson that reads `downloaded` whose files are out of sight, space used on
    the wrong disk, and, for a re-download, a row that reads the drive's older copy.
  - *Fix (new mechanism; needs a first-run design):* refuse a library root that is
    missing, or that is an empty mount point, instead of creating it. The engine already
    refuses a missing folder: `os.OpenRoot` (`$(go env GOROOT)/src/os/root.go:82`) opens
    an existing directory only, so dropping the `MkdirAll` is the whole guard for a missing
    root. It can't simply be dropped: in the security seat's scratch copy that fails 37
    scheduler tests, whose fixtures rely on the folder being made on first use, and a
    fresh install has no library folder yet. So the folder needs creating once, at
    startup or on the first run, and an empty mount point needs a marker file drumdrop
    writes there and checks for (Sonarr refuses a missing root folder the same way).
    Go's standard library has no mount-point test. Even then, in plex-tv the refused
    placement falls back to downloads (ruling (i)), so syncs stop retrying there once a
    download succeeds (D120; round-5e security review, S2).
  - *Evidence:* `grep -n 'os.MkdirAll(root' internal/scheduler/library.go` ·
    `grep -n '^func OpenRoot' "$(go env GOROOT)/src/os/root.go"`
- **D137 · After the library setting points at another folder, a re-download can leave
  the old copy recorded by nothing.**
  - *What:* a lesson's library record lists its files relative to the library
    (`lessons.library_entries`), and a legacy season row is read at its place under
    today's library (`Claims.seasonFolder`, `internal/library/ownership.go`); every read
    joins them to today's setting (`library.Resolve`, `internal/library/record.go:77`).
    By design (ruling #66) a record survives a remount or another spelling of the same
    folder. The round-5h security seat probed the setting pointed at a *different*
    folder with the files not moved (`TestR5hRootMoves`, `TestR5hDeleteAfterRootMove`,
    scratch only; re-run by round 5i before its fix):
    - a lesson *Delete* answered 200 "deleted", and a follow delete with files 204,
      while every file stayed on disk, recorded by nothing: the delete read the season
      folder under the new setting, found nothing, and `os.RemoveAll` returns nil for a
      missing path (`$(go env GOROOT)/src/os/path.go:70-71`). That was a regression
      against `main` for every plex-tv row: `main`'s delete acted on the folder of the
      absolute video path (`removeLessonFilesPlexTV` in
      `git show main:internal/server/lessonfiles.go`), and in the same setup removed the
      episode's three files and kept the sibling episode (the seat's `probe-main`);
    - a refused library placement fell back to downloads and stopped recording the
      season files: a legacy row with a plain episode name (security F1: the move learned
      an empty record, `[]`), a recorded row whose record then names the other folder
      (M3; N3 with the downloads folder beside the library), the default layout's season
      rows (M4, M5), and a library moved down with the old folder now inside downloads
      (D1, D2);
    - nothing was ever deleted wrongly: no placement or delete reached the old folder.
  - *Now (round 5i, owner ruling (y), 2026-09-24; narrowed by round 5j):* while one of a
    season-folder row's own files (its record's entries, its video, or a legacy row's
    episode names) is still in the folder it records, and that folder isn't the one it is
    read as now (by path, then by identity through `os.SameFile`:
    `library.Claims.LeftBehind`), `DELETE
    /api/lessons/{id}` answers 409 and removes nothing; `DELETE
    /api/follows/{id}?files=true` answers 409 and removes nothing of any of its lessons,
    and the follow stays (the refusal is known before the first removal, so the follow
    is never half deleted); and a refused library placement fails with `failLeftBehind`
    instead of falling back, the lesson staying downloaded with a note that says the fix
    (move the files to the same place in the new library folder). The moved-down case
    (D1, D2) is refused too, wherever the old folder now sits. A library moved or
    remounted with its files (the old path is gone), another spelling of the same folder
    (a symlink, a bind path), nothing moved, and an old folder the lesson's files were
    moved out of (emptied, as `rsync --remove-source-files` leaves it, or holding only
    other lessons' files) work as before. Round 5j: the paths are compared before any
    stat, so an unchanged setting reads nothing on disk; `ENOTDIR` counts as gone; an
    old folder or file that can't be read still refuses (the delete's fixed 500, the
    fallback's `failKeptInLibrary`), with the error in the log; and a delete refused
    for either reason is refused before `Begin…Delete`, so it stops no download, drops
    no queued job and records no intent (security round 5i S5), with the check after
    Begin kept as a backstop. `library.Remove` still refuses anything outside today's
    roots, so the delete does not act on the old folder as `main` did.
  - *Still open:*
    - a lesson kept in downloads whose record still names season files in the library
      (a refused plex-tv placement that fell back, ruling (i)): its output_dir is not a
      season folder, so `LeftBehind` never asks, and its record is read under today's
      library; after the setting moves, a Delete answers 200 "deleted" (a follow delete
      204) and every season file stays, recorded by nothing (code round 5i, Medium 1,
      probe P1). The row holds no absolute path to the old folder, so no check can see
      it without the root recorded (the fix below);
    - a path that is gone while the files exist elsewhere (security round 5i, S6): the
      old library unmounted or renamed with the files in it, or, in Docker, the library
      moved by re-pointing the bind mount's host folder while `DRUMDROP_LIBRARY_DIR`
      stays the same (code Info 8). drumdrop sees nothing: a Delete says deleted and the
      files stay, as on `main`. README says to move the files first. A remembered root
      *path* would not see the Docker case either (the Fix below);
    - an old folder the move emptied now takes the gone-folder path through the
      fallback (code round 5j, Low 1): `LeftBehind` answers false, and the second line,
      `keptInLibrary`, never refuses a recorded folder outside today's library
      (`inLibrary` reads it as spelled, `internal/scheduler/worker_record.go:354`). So a
      legacy row whose files were moved to the same place in the new folder, and whose
      failed placement learned nothing there (a season folder it can't list, or a legacy
      name it can't settle), falls back: it records the downloads folder, and its moved
      files stay in the new season folder, recorded by nothing. Both triggers are
      probed: the unlistable folder by the round-5j code seat's scratch probe, the
      ambiguous name by the round-5l security seat's (identical in `1a03e16` and
      `7090ad0`, for an emptied old folder and for a remount: the lesson falls back, its
      row records the downloads folder with `library_entries` NULL, and the three legacy
      files in the new season folder are recorded by no row). An ambiguous name alone
      does not fail the move: the move learns nothing, succeeds, and the legacy files
      stay unrecorded, case (a) under D58. The fallback needs a second, independent
      failure (the probe refused a rename; in real use an I/O error, or an entry the
      move can't set aside).
      Rare, and nothing is deleted. A recorded row is safe: its relative record resolves
      at the new place. Before round 5j the emptied folder refused, as it still existed.
      *Later fix, the owner's call:* make `keptInLibrary` read this row's folder as it
      is read now (`Claims.seasonFolder`, `internal/library/ownership.go:225`); that
      changes the remount case ruling (y) keeps as before;
    - a copy kept in both folders (security round 5i, E2) is still refused, as its own
      files are in the old folder. Since round 5k the refusal offers switching back only
      "if the new one is still empty" (security round 5j S1: "if you moved nothing" was
      true after a copy). Switched back anyway, a Delete removes the old copy and leaves
      the new one recorded by nothing (probe P2), and a lesson drumdrop placed in the new
      folder since the change is refused in turn (P5). README says to delete the old
      copy, which finishes the move. The condition names only the old and the new
      folder, so it is exact for one change of the setting: after two (A→B→C), with
      files moved, copied or placed in B and C still empty, the sentence is true, and
      switching back to A leaves B's files recorded by nothing (code round 5k, Info 2;
      reasoned, not probed). Whether README says so is the owner's call;
    - a hung stat on an old folder that *differs* from today's has no bound (security
      round 5i, S4; restated by code round 5j Low 2 and security S3). Since round 5j the
      first stat runs in `refuseUpFront` (`internal/server/lessons.go:227`,
      `follows.go:311`), before `Begin…Delete`, so no lease is held: the lesson stays
      usable, but that Delete never answers, and each new Delete parks another request.
      The lease-renewing park now needs the mount to stall between the up-front check
      and the one after Begin (`lessons.go:252`, `follows.go:399`). The worker's
      fallback check (`refuseFallback`, `internal/scheduler/worker_record.go:235`) makes
      the same unbounded stat in the single serial worker (`daemon.go:94` →
      `Worker.RunOnce`, `worker.go:232`), so one hung stat stalls the whole queue
      (reasoned, not probed; true since round 5i, when `keptInLibrary` already stat'ed
      old paths). An unchanged setting reads nothing. Bounding `os.Stat` is a new
      mechanism (`os.Stat` takes no deadline); can wait;
    - on Windows, a not-found answer other than `ERROR_FILE_NOT_FOUND`,
      `ERROR_PATH_NOT_FOUND` and `_ERROR_BAD_NETPATH` (the three Go maps to
      `fs.ErrNotExist`, `$(go env GOROOT)/src/syscall/syscall_windows.go:201-205`), such
      as `ERROR_NOT_READY` for an empty drive, counts as "can't read", so it refuses
      (security round 5i, S3; code round 5j, Info 6; unverified, not run on Windows);
    - a re-download whose library placement *succeeds* after the change places a new
      copy under the new setting and leaves the old one where it was, recorded by
      nothing, with no log line (security round 5h, S1 and S2). *Option, the owner's
      call* (security round 5j): ask `LeftBehind` before a placement too, and fail with
      `failLeftBehind` while the files are left behind. That closes this without the
      Fix below, at the cost of refusing Download until the files move, which narrows
      ruling (y). Can wait: nothing is deleted, the old copy is a duplicate;
    - the check after `Begin…Delete` (the backstop) answers with the up-front sentence,
      which doesn't say a download was stopped, as the no-claims pair's late sentence
      does (UI round 5j, 7; deliberate, `internal/server/messages.go:107-108`). It fires
      only if the lesson's own files reappear in the old folder between the two checks;
      a late variant would be one more sentence, not worth adding now;
    - a default-layout lesson folder that the change leaves outside both dirs (the
      library pointed sideways, `/oldlib` to `/newlib`): a refused placement falls back
      to downloads and leaves that folder where it was, no longer recorded, logged
      "… inside neither the downloads folder nor the library" (round-5i scratch probe,
      in both layouts; `previousFolder`, `internal/scheduler/place.go:470`).
      `LeftBehind` reads season folders only: a lesson folder is recorded by its full
      path and never re-pointed, and the fix's message names the library folder while
      the downloads setting may be the one that moved. A delete of such a lesson is
      already refused (D67).
  - *Why:* Plex shows the old copy, and no delete will ever remove it.
  - *Fix (new mechanism, can wait):* record the library root a row was written under,
    and react when the setting changes (Sonarr refuses a changed root folder). That was
    ruling (y)'s declined option *Remember the library folder*: it fixes every reader,
    but needs its own design, so a later branch. A remembered root *path* can't see a
    Docker bind re-pointed under an unchanged container path: the recorded path and the
    setting stay equal (`internal/library/ownership.go:260`). That case needs an
    identity, such as a marker file holding an ID in the library root (security round
    5j, S2). Engine: `os.SameFile` compares two live stats only; nothing in the standard
    library or SQLite records a folder's identity across restarts, and drumdrop writes
    no such marker today.
  - *Evidence:* `grep -n 'func (c \*Claims) LeftBehind' -A24 internal/library/ownership.go`
    · `go test -count=1 -run 'LeftBehind|OwnFiles|CantRead|StopNothing|ChecksAgain' ./internal/library/ ./internal/scheduler/ ./internal/server/`
    · `grep -n 'func previousFolder' -A21 internal/scheduler/place.go` (the lesson-folder
    case)
- **D138 · Nothing checks a lesson note's length against the Lessons row's clamp.**
  - *What:* the note under a lesson is clamped to two lines from 1280px (28rem) and three
    below. The longest note the server writes is failMusora's, 147 characters; ruling
    (y)'s round-5i note was 166 and fit at 1280px with 1.3px to spare, and a 168-character
    variant was cut (round-5i UI review, 3). Only a browser measurement says whether a
    new sentence fits; the full text is in the note's `title` (`web/src/pages/Lessons.tsx`).
    Where it stands (round 5k): the left-behind note is 146 characters; the round-5j UI
    seat measured that exact sentence at two lines, 99.0px spare at 448px and 40.7px at
    424px, in a replica with the app's font and box (not the app itself). The `title`
    fallback works on mouse hover only: a `<p>` takes no keyboard focus and touch has no
    hover, so a cut note would lose its remedy for keyboard and touch users, which is
    why the fit matters (UI round 5j, 5).
  - *Fix (new mechanism, can wait):* a length cap, or a test that measures the
    scheduler's lesson sentences against the clamp. Engine: none.
  - *Evidence:* `grep -E '^\s*(const +)?(lesson:|kept:|msg[A-Za-z]+ +=)' internal/scheduler/messages.go | grep -o '"[^"]*"' | awk '{ print length($0)-2 }' | sort -rn | head -1`
    (147 on 2026-09-25, *moves*; the `(const +)?` catches a one-line `const msgX = "…"`
    such as `msgEarlierKept`, which the round-5j form missed)
- **D139 · The Queue shows a failed job's sentence on one line, cut.**
  - *What:* the job's error is cut to one line, so the left-behind sentence loses its
    remedy past "Couldn't put this lesson in the library.", like the older "…, so its
    co…"; the tooltip has the full text (round-5i UI review, 5; `web/src/pages/Queue.tsx`).
    The tooltip's trigger is a `<span>` (`web/src/pages/Queue.tsx:260-261`), which takes no
    focus, so it opens on mouse hover only: keyboard and touch users never reach the
    sentence's end. Screen readers read all of it (`truncate` is CSS only), and the same
    lesson's note on Lessons has the full sentence (UI round 5j, 6). A shorter job
    sentence can't hold both outcome and remedy in the ~45 characters the column shows
    at 1024px (the same review).
  - *Fix (can wait):* a two-line clamp, or `tabIndex={0}` on the trigger (a corrected
    line: Radix opens a tooltip when its trigger takes focus,
    `web/node_modules/@radix-ui/react-tooltip/dist/index.mjs:202-203`; the cost is one
    tab stop per failed job, and touch still has no hover). Needs a browser check.
  - *Evidence:* `grep -n 'truncate\|line-clamp' web/src/pages/Queue.tsx`
- **D140 · The web UI never shows the library folder.**
  - *What:* the left-behind refusals speak of "the old library folder" and "the new
    one", but no screen says which folder the setting points at: Settings has only the
    Musora, token and About cards (`web/src/pages/Settings.tsx:28-30`), and no API route
    returns the setting (the server holds `cfg.LibraryDir`, `internal/server/server.go:35`,
    and exposes it nowhere). The reader has to remember the change they made (UI round
    5j, 8).
  - *Fix (new mechanism, can wait):* an API field and a read-only row in Settings.
    Engine: none.
  - *Evidence:* `grep -rn 'LibraryDir' internal/server/*.go | grep -v _test` ·
    `grep -n 'Card />' web/src/pages/Settings.tsx`
- **D141 · The same refusal twice looks unchanged.**
  - *What:* a refusal that asks for a loop (move the files, then Delete again) comes
    back word for word while the move is incomplete, and the only visible change is a
    ~60ms fade; screen readers re-announce it, because `InlineError` keys the sentence
    on `error.seq` (`web/src/components/InlineError.tsx:40`). Most users close the
    dialog while they move files, and reopening it clears the old error, so the impact
    is small (UI round 5j, 9; first noted, unrecorded, in round 5i §5).
  - *Fix (new mechanism, can wait):* a visible sign that the answer is new (a count or
    a time). Needs a design decision and a browser check.
  - *Evidence:* `grep -n 'error.seq' web/src/components/InlineError.tsx`
- **D122 · A partial copy at the recorded video's name counts as on disk after a crash.**
  - *What:* across filesystems a placement sets the old video aside, then copies the new
    one straight to its final name (`copyFileInto`, `O_EXCL`). If drumdrop dies mid-copy,
    startup keeps `replaced-<job>` and requeues the job; if that retry then fails, the
    disk check (ruling (o)) finds a regular file at the recorded name, and the lesson
    stays `downloaded` with the kept note, holding a partial (3 of 7 bytes in the
    security seat's probe `TestR5dCrashMidCopyThenFailedRetry`) or zero-byte video, and
    no sync retries it. The real earlier video is in
    `.drumdrop-in-progress/replaced-<job>/`, which startup logs (round-5d security review,
    F2). Not a regression: before round 5d the record decided it, with the same result.
  - *Why:* a broken video that reads as kept. Nothing is lost, and it needs a crash and
    then a failed retry.
  - *Fix (new mechanism, can wait):* copy to a temporary name on the destination
    filesystem, then rename it into place without replacing (rename is atomic,
    `os.Rename`, `$(go env GOROOT)/src/os/file.go:437`). Prior art in the package:
    `writeScratchNFO` and `episodeTempSuffix` (`internal/scheduler/plexmove.go`); the
    aside area already lives on each root's own filesystem (`aside.go`).
  - *Evidence:* `grep -n 'func copyFileInto' -A20 internal/scheduler/plexmove.go` ·
    `grep -n 'IsRegular' internal/scheduler/ondisk.go`
- **D123 · A flood of presses runs sync cycles back to back.**
  - *What:* each press (ruling (m)) sends a kick (`Server.kick`, a non-blocking send on a
    one-slot channel), and the daemon runs a full cycle per kick it receives
    (`Daemon.Run`). The slot caps the backlog at one pending cycle, not the rate: the
    security seat's probe `TestR5dPressFlood` (5 follows, 20 ms per request, a fake
    expander) ran 18 cycles and 95 Musora requests in 2 s, against 0 idle (round-5d
    security review, I1). Every cycle plans every follow, and retries every failed lesson
    at up to 3 full downloads each, so D120's retries come per press, not per interval.
    *Download* and *Retry* have already queued their job, yet each press still costs a
    planning request per follow. POST `/api/sync` could already do this; with D53
    (tokenless loopback) a cross-site `text/plain` POST to `/api/follows` now starts
    downloading at once.
  - *Why:* load on Musora, and repeated downloads of failing lessons, from repeated
    presses.
  - *Fix (new mechanism, optional):* a drain-only kick for presses that queued a job
    themselves (`Worker.RunOnce` already drains without planning), or a minimum gap
    between planned cycles. The daemon has no rate limit today.
  - *Evidence:* `grep -n 'func (s \*Server) kick' -A6 internal/server/sync.go` ·
    `grep -n 'case <-d.Kick' -A10 internal/scheduler/daemon.go` ·
    `grep -n 's.kick()' internal/server/*.go`
- **D125 · A lesson with no video and no library record counts as on disk while its
  folder exists, even a legacy plex-tv row's shared season folder, or an empty folder.**
  - *What:* the disk check (ruling (o)) falls back to the row's folder when it records no
    video and no library entries. For a legacy plex-tv row (no record) that folder is the
    season folder, which the show's other episodes share, so it proves nothing about this
    lesson's own files: the lesson reads `downloaded` whenever any episode of the show is
    there (round-5d code review, I2). Reach: only legacy rows with no video
    (resources-only, or a video-less song) placed before the record existed. The comment
    on `recordedFilesPresent` names the choice. The same fallback counts any video-less
    lesson whose folder exists but is empty as on disk (`info.IsDir()` alone), in either
    layout (round-5e security review, I-resid: the rest of round-5d I2).
  - *Why:* a lesson whose own files are gone can keep reading `downloaded`, and no sync
    retries it.
  - *Fix (new mechanism, can wait):* check the entries the name matcher gives the row,
    `library.Claims.Plan` (`internal/library/ownership.go`), as a delete does; for a
    lesson folder, also ask whether it holds anything, which the engine answers with
    `(*os.File).ReadDir(1)` (`$(go env GOROOT)/src/os/dir.go:97`). SQLite can't see the
    disk, so nothing in the store does this.
  - *Evidence:* `grep -n 'l.OutputDir.Valid' -A3 internal/scheduler/ondisk.go` ·
    `grep -n 'func (c \*Claims) Plan' internal/library/ownership.go`
- **D128 · Where the library and the downloads folder nest, the path guesses who placed a
  folder: some lessons are refused, and a few library folders stop being recorded.**
  - *What:* since round 5f (security round 5e S3) a lesson an earlier refused move kept in
    a downloads folder that sits inside the library falls back to downloads when its
    library placement is refused again, but only when its recorded lesson folder is in
    the course folder the fallback goes into (`inLibrary`,
    `internal/scheduler/worker_record.go:353-359`). Since round 5h a season folder there
    counts as the library's, since the fallback only writes `NN - Title` folders
    (round-5f/5g security review, N1: after the library root moved up, a legacy plex-tv
    or default-layout season row fell back and its season files were claimed by no
    lesson; `TestWorkerPlexTvRefusedMoveKeepsALegacyEpisodeItCanNotName`,
    `TestWorkerDefaultLayoutRefusedPlacementOfASeasonFolderRow`). Round 5h closed that
    only for an episode the move couldn't name (a song version, `[Live] [Drumless]`,
    whose `Plan` errs, so the move learned nil); a plain lesson name took the
    known-empty branch (`[]`) and still fell back, its season files claimed by no lesson
    (round-5h security review, F1: M2, and N1 with downloads beside the library). Round 5i
    counts an empty answer as none learned (`keptInLibrary`, `len(entries) == 0`;
    `TestWorkerPlexTvRefusedMoveKeepsALegacyEpisodeTheMoveLooksForElsewhere`). The trade,
    accepted: a legacy row whose episode files are all really gone is refused too while
    its library placement keeps failing, and syncs retry it. Since ruling (y) a moved-up
    season row whose own files are still in its old folder is refused before this rule
    is asked (`failLeftBehind`, D137), so for season folders the rule is a second line,
    pinned on its own by `TestKeptInLibraryDecidesByWhatTheRowRecords`.
  - *Refused (nothing lost: the attempt fails with `failKeptInLibrary`, and the old
    folder stays whole and recorded), on every* Download *press:*
    - the course folder changed too (the follow was retitled, or an instructor
      lesson's parent course changed), and a downloads folder set through a symlink into
      the library (or spelled in another case) when the title changed (round-5f scratch
      probe `TestR5fResidualRefusals`);
    - the mirror case, the library inside the downloads folder (`dl/library`) with a
      follow titled like the library folder (`library`): a lesson kept in downloads at
      `dl/library/05 - Old Title` is inside the library as written, and the course-folder
      exception applies only when the downloads folder is the one nested, so it is
      refused whenever its title has changed (round-5f/5g code review, I5).
  - *Lost (the rule takes a library folder for a downloads one):* the fallback replaces
    that folder when the download brings back every file in it (logged "↻ … replaced …
    (the lesson's earlier download)"), or else leaves it where it is, no longer recorded
    and claimed by no lesson (logged "⚠ … left its previous folder … no longer
    recorded"). Before round 5f both cases were refused. Two setups, both seen only in
    the security seat's scratch probe:
    - an instructor whose name sanitises to the downloads folder's name (`downloads`,
      with downloads at `lib/downloads`), so the library placed the lesson at
      `lib/downloads/<Course>/05 - Old Title`; the owner unfollowed it and kept the
      files (the row's follow becomes NULL), Musora renamed the lesson, and a
      *Download* (filed under its parent course,
      `internal/scheduler/worker.go:775-779`) has its library placement fail
      (round-5f/5g security review, N2: A1, A1o, A2);
    - the library root moved up a level from a tie (library and downloads both
      `media/drumeo`, then the library at `media`), and the lesson's title changed: its
      folder, placed while the two were one, is now in the downloads course folder (B3).
  - *Why the narrow rule:* the spelling can't tell a downloads folder from a library one
    it happens to contain. With the downloads folder at `lib/downloads`, a course named
    `downloads` is placed by the library at `lib/downloads/05 - …`; the plain longest-root
    rule counted that as downloads, so a refusal fell back and left the library folder
    recorded by no lesson (ruling (f)'s declined outcome;
    `TestWorkerRefusedLibraryPlacementKeepsALibraryFolderInsideTheDownloadsFolder`).
    Instructor follows nest one level deeper, so the folder's depth can't tell either.
  - *Fix (new mechanism, can wait):* record which placement wrote a row's folder (the
    library's, or the downloads fallback), rather than reading it back from the path.
    That closes both the refusals and the losses above: a folder the fallback wrote
    falls back, and one the library placed is refused, wherever the roots sit. Neither
    SQLite nor the path records it today; the repo's root-relative pattern is the
    library record (`library.Resolve`, `internal/library/record.go:77`).
  - *Evidence:* `grep -n 'func inLibrary' -A7 internal/scheduler/worker_record.go` ·
    `go test -count=1 -run 'OfALessonKeptInDownloads|KeepsALibraryFolderInsideTheDownloadsFolder|KeepsALegacyEpisodeItCanNotName|RefusedPlacementOfASeasonFolderRow' ./internal/scheduler/`
    · `go test -count=1 -run 'KeptInLibraryDecidesByWhatTheRowRecords|TheMoveLooksForElsewhere' ./internal/scheduler/`
- **D130 · Some writes stamp a lesson's updated_at when nothing about it changed.**
  - *What:* the All tab lists lessons by updated_at, newest first (`ListLessons`), and
    ruling (r) stopped only the sync's upsert from stamping a lesson it didn't change.
    Since round 5h that holds for a lesson two follows list too (a course follow and an
    instructor follow, say): before, each wrote its own parent every sync, the course's
    id and the instructor's NULL, so the lesson was stamped every sync (round-5f/5g code
    review, L1; security review, N4). Parent is now written only by the follow the
    lesson is attributed to (`follow_id`), as `follow_id` and `position` are already
    first-follow-wins (`UpsertLesson`, `internal/database/lessons.go:144-156`;
    `TestPlanDoesNotRestampALessonTwoFollowsList`). The trade: a lesson whose follow was
    removed keeps its last parent, as it keeps its `follow_id` (NULL then), and another
    follow that lists it writes neither. So does a row that predates `follow_id`
    (migration 002, NULL since): the planner always passes a follow id
    (`internal/scheduler/planner.go:119`), so no follow matches it and its parent is
    frozen too (round-5h code review, Info 5). Nothing a user sees changes with it: the parent
    reaches the web only as `parent_railcontent_id` in the lesson DTO
    (`internal/server/dto.go:43`, `web/src/types.ts:21`), which no page reads, and a
    download with no follow files the lesson under the parent course title Musora
    returns, not the stored parent (`lessonParentTitle`,
    `internal/scheduler/worker.go:775`).
    Three other writes still stamp every time:
    - `StartDownload` sets `downloading` and stamps at the start of every attempt.
      Nothing changes the lesson between attempts, so on attempts 2 and 3 it is already
      `downloading` and only the stamp changes.
    - `SkipLesson` stamps a lesson already skipped with the same reason. Only the API
      can do that: the UI offers *Skip* on pending and failed lessons only.
    - `FailDownload` and `NotReturnedDownload` (`keepOrSQL`) stamp a lesson that already
      has the status and note they write. That happens when the job ends before
      `StartDownload`, so the lesson was never `downloading`: Musora can't be reached
      (`failMusora`), DrumDrop's records can't be read (`failNotStarted`), the private
      folder can't be made (`failNoFolder`), or Musora returns no lesson
      (`notReturned`). Syncs queue a failed lesson again every cycle
      (`ShouldSkipEnqueue`), so while Musora is down every failed lesson a follow lists
      is stamped again each cycle.
  - *What it does to All:* each stamp moves the lesson back to the top of All (page 1),
    above lessons that did change since, and the tab refreshes on each attempt's
    events. After a retry's `StartDownload` the lesson was near the top already, from
    its first attempt, so it passes only the rows that changed during the backoff.
    During an outage every failed lesson moves to the top each cycle, the reshuffle
    ruling (r) removed for the sync's upsert.
  - *Fix (can wait):* stamp only when the status or the note changes, as `UpsertLesson`
    does since (r).
  - *Evidence:* `grep -n 'func (s \*Store) StartDownload' -A5 internal/database/downloads.go`
    · `grep -n 'func keepOrSQL' -A6 internal/database/downloads.go`
    · `grep -n 'UPDATE lessons SET status = ?, error = ?, updated_at' internal/database/downloads.go`
    · `grep -n 'failBeforeDownload(ctx\|w.notReturned(ctx' internal/scheduler/worker.go`
    · `grep -n 'ORDER BY updated_at DESC' internal/database/lessons.go`
- **D80 · A Skip that lands between the planner's check and its enqueue is downloaded
  anyway.**
  - *What:* the planner asks `ShouldSkipEnqueue`, then calls `EnqueueJob`, which refuses
    only a lesson being deleted. It can't refuse skipped lessons in general, because a
    manual *Download* must work on them. So a Skip committed between the two (or a whole
    delete, whose tombstone skips the lesson and ends its lease) is followed by a full
    download, recorded as downloaded (code round 4, #5: a probe on the real store ended
    `downloaded`).
  - *Why:* it breaks the Skip dialog's promise that syncs leave a skipped lesson alone. The
    window is about as long as the Skip's transaction.
  - *Fix (new mechanism, can wait):* an enqueue only the planner uses, which re-checks the
    skip predicate in the same transaction (`INSERT … SELECT … WHERE NOT EXISTS (… status
    IN ('downloaded','skipped'))`). It changes the `scheduler.Store` interface, so all five
    implementers change with it (hard rule 6).
  - *Evidence:* `sed -n 130,170p internal/scheduler/planner.go` ·
    `grep -n 'func (s \*Store) EnqueueJob' -A5 internal/database/jobs.go`
- **D89 · One instructor can be followed on one brand only.**
  - *What:* `idx_follows_instructor` is unique on `follows(slug) WHERE kind='instructor'`
    (`001_initial_schema.sql:23`), so a second follow of the same slug on another brand is
    refused by the index. The add inserts with `ON CONFLICT DO NOTHING`, reads the existing
    row back, and answers 200 "already following" with the first follow (the CLI prints
    `• already following @<slug>`). Nothing new is tracked, and nothing says the brand was
    set aside. Musora files one person under one slug across its brands (`jared-falk` has a
    drumeo and a singeo document, D94), so this is a real case. Since the preview names
    the brand, the web UI now shows the contradiction: with `jared-falk` followed on
    drumeo, a preview on singeo reads "N lessons on Singeo", and *Add* then answers
    "Already following" with the drumeo follow's title (round-5 UI review, Info).
  - *Why:* the second brand's lessons are never synced, silently.
  - *Fix:* a migration that makes the unique key `(slug, brand)`, with `AddInstructorFollow`
    reading the row back by both (hard rule 5 applies to the migration).
  - *Evidence:* `sed -n 23p internal/database/migrations/001_initial_schema.sql` ·
    `grep -n 'func (s \*Store) AddInstructorFollow' -A7 internal/database/follows.go` ·
    `grep -n 'Already following' web/src/pages/follows/AddFollowDialog.tsx`
- **D90 · An instructor follow's lessons aren't in the order its query asks for.**
  - *What:* `instructor_lessons.groq` ends in `| order(published_on desc)`, after the
    projection `{ 'id': railcontent_id, 'type': _type, title }`. `published_on` is not in
    the projection, so the order has nothing to sort by and changes nothing (measured
    live, read-only). The lessons come back in Musora's own order.
  - *Why it was left alone:* the planner numbers each lesson by its place in the follow's
    expansion (`position`: the `NN` of its folder and its plex-tv episode number), and
    `UpsertLesson` keeps the first number a lesson got. Fixing the order changes the numbers
    new lessons get, while lessons already recorded keep theirs. Decide it with D57.
  - *Evidence:* `cat internal/musora/queries/instructor_lessons.groq` ·
    `grep -n 'position := sql.NullInt64' internal/scheduler/planner.go` ·
    `grep -n 'COALESCE(lessons.position' internal/database/lessons.go`
- **D68 · Without a token, the live-progress stream never opens.**
  - *What:* `web/src/lib/sse.tsx` returns early when there is no token (`if (!token) {
    setConnected(false); return }`), so in tokenless loopback mode (allowed, hard rule 11)
    the header chip reads "reconnecting…" forever and no live progress arrives. The server
    side works: a manual `EventSource` from the page reached `readyState` 1, and `curl` got
    `event: ready`. Unchanged against `main`. Found in the round-3 browser check (BV3).
  - *Why:* the default local setup shows no progress at all.
  - *Evidence:* `grep -n 'if (!token)' web/src/lib/sse.tsx`
- **D69 · At phone width the sidebar squeezes the page.**
  - *What:* at 375px the app shell keeps its ~220px sidebar, leaving the page a ~150px
    column with a horizontal scrollbar; there is no mobile navigation. Pre-existing layout
    (round-3 browser check, BV4).
  - *Why:* the UI is unusable on a phone.
  - *Evidence:* open the app at 375px wide.
- **D82 · The Add and Edit follow dialogs scroll as a whole, and their buttons move as they
  grow.**
  - *What:* both put `overflow-y-auto` on the whole dialog, capped at the viewport height,
    so on a very short screen the footer can scroll out of view. They are centred and
    never re-anchored, and a centred dialog moves its bottom edge, buttons included, by
    half of any growth. In *Add follow*, Preview and Add move down about 45px when the
    preview card lands (about 74px plus a 16px gap), an inline preview or save error moves
    them about 30–40px, and switching from Node to Instructor moves the tab list up about
    51px (round-5 UI review, L6). This entry used to say the buttons can't move under the
    pointer; that was false. The confirm dialogs do neither (round-4 UI review, N1 and
    BV6): only their body scrolls, their footer stays where it is while the body grows,
    and the message and the buttons stay in view while the two fit under the cap. That
    holds because the server's messages stay at 220 characters or fewer (`maxMessageLen`,
    checked on every message by a test).
  - *Why:* a button that jumps as the preview lands can take a click meant for the one
    that was there. The scrolling half is low priority: the owner doesn't use the app on a
    phone (decisions #68, *"i dont really use my phone for this app"*), and D69 already
    blocks phones.
  - *Fix (new mechanism, can wait):* reuse the confirm dialog's anchoring, or reserve the
    preview's space; Radix positions nothing itself.
  - *Evidence:* `grep -n 'overflow-y-auto' web/src/pages/follows/AddFollowDialog.tsx web/src/pages/follows/EditFollowDialog.tsx web/src/components/ConfirmDialog.tsx`
- **D70 · "Copy path" is offered for a lesson with no files.**
  - *What:* the Lessons row menu shows *Copy path* unconditionally, so a pending lesson with
    no files offers a path that does not exist (round-3 browser check, BV5; the item was at
    `web/src/pages/Lessons.tsx:367` on `main`).
  - *Why:* it copies nothing useful, and suggests the lesson has files.
  - *Evidence:* `grep -n 'Copy path' web/src/pages/Lessons.tsx`
- **D71 · Touch targets are under 44px.**
  - *What:* row buttons are 32px, *Actions* 36px, dialog buttons 36px: shadcn's default
    density (round-3 UI review, P18). A failure toast's close × is 20px (sonner's own
    stylesheet), and for a toast that stays until closed it is the only visible way to close
    it on a phone (a swipe works too, but nothing shows that).
  - *Why:* below the usual 44px minimum for touch.
  - *Evidence:* `grep -n 'h-8\|h-9\|size-8\|size-9' web/src/components/ui/button.tsx` ·
    `grep -n 'close-button' web/node_modules/sonner/dist/styles.css`
- **D91 · PlayBass's name in the UI is unconfirmed.**
  - *What:* the UI names a brand as Musora does: Drumeo, Pianote, Guitareo, Singeo (the
    add dialog's preview ends "… lessons on Pianote"). For `playbass` no spelling is
    confirmed, so the UI shows `playbass`, as the server sends it, rather than a guess.
  - *Fix:* read Musora's own spelling of the brand (never guess about Musora) and add it to
    the list.
  - *Evidence:* `grep -rn '"Singeo"' web/src --include=*.ts --include=*.tsx` · the test "a
    brand without a known name previews as the server sent it" in
    `web/src/pages/Follows.test.tsx`
- **D97 · A failure toast can't be closed while a dialog is open.**
  - *What:* a failure toast with a description stays until it is closed (`failureToast`,
    D86). While a dialog is open, Radix sets `pointer-events: none` on the page body, so
    the toast and its close button take no clicks, although sonner draws them above the
    dialog's overlay (`z-index: 999999999`). A click on the toast's × lands on the overlay
    and closes the dialog instead. Confirmed in the Orca browser: `elementFromPoint` at the
    × hits the overlay (round-5 browser pass; round-5 UI review, L10).
  - *Why:* the one way to close a sticky toast closes the dialog under it.
  - *Fix (new mechanism, can wait):* not designed. Engine: Radix's dismissable layer sets
    the body's pointer events itself.
  - *Evidence:* `grep -n 'duration: Infinity' web/src/lib/errors.ts` ·
    `grep -n 'pointerEvents = "none"' web/node_modules/@radix-ui/react-dismissable-layer/dist/index.mjs`
    · `grep -n 'z-index: 999999999' web/node_modules/sonner/dist/styles.css`
- **D98 · A confirm dialog whose body scrolls gives no sign of it.**
  - *What:* on a short screen only the confirm dialog's body scrolls (D82), and it clips
    with no fade or edge, so nothing says more text is below (round-5 UI review, P8).
  - *Why:* low: short screens only, and the owner doesn't use the app on a phone
    (decisions #68).
  - *Evidence:* `grep -n 'overflow-y-auto' web/src/components/ConfirmDialog.tsx`
- **D99 · A Retry the server refuses after a race shows a red toast that stays.**
  - *What:* the Queue's *Retry* shows a neutral note for a download removed elsewhere
    (404), but its two 409s are still failures, red and kept until closed: "Only a failed
    or canceled download can be retried, and this one isn't." (`msgJobNotRetry`: the job
    stopped being failed or canceled meanwhile) and "This lesson's files are being deleted
    right now. Try again once that's finished." (`msgBeingDeleted`). Both come from
    something done elsewhere, as the 404 and Cancel's 409 do, and those read as neutral
    notes (round-5 UI review, L1; round-5f UI review, L6).
  - *Why:* a harmless race reads as a failure the user has to close.
  - *Fix (can wait):* the owner's copy call first; then the same kind of mapping as
    Cancel's 409 (`cancelOutcome`).
  - *Evidence:* `grep -n 'const retry' -A9 web/src/pages/Queue.tsx` ·
    `grep -n 'msgJobNotRetry\|msgBeingDeleted' internal/server/messages.go internal/server/respond.go`
- **D100 · A neutral note can't carry the server's sentence.**
  - *What:* `itemOutcome` turns the API's 404 into "already-gone" and drops the server's
    sentence, so each neutral note has to be true on its own: Download's "Won't download:
    skipped or removed elsewhere" covers both `msgDownloadGone` and `msgDownloadJobGone`.
    Every action on one lesson, job or follow goes through it, so none of the server's
    `msg…Gone` sentences reaches the screen (round-5f code review, L1; round-5f UI
    review, L1).
  - *Why:* the server says exactly what happened; the note can only say what fits every
    case.
  - *Fix (new mechanism, can wait):* `itemOutcome` would have to keep the sentence, for
    the note's description.
  - *Evidence:* `grep -n 'export function itemOutcome' -A11 web/src/lib/errors.ts` ·
    `grep -n "Won't download" web/src/pages/Lessons.tsx`
- **D101 · Queue's Retry and Cancel, Settings' Connect, and a pressed row that leaves
  its list, or the screen, drop keyboard focus.**
  - *What:* Retry and Cancel in a Queue row are disabled while their request runs, and stay
    disabled once the row's status changes, so a keyboard user's focus falls to the page
    body on every press (round-5b UI review, Low 3). Connect keeps focus while it signs in
    (a `PendingButton` since round 5), but a successful sign-in clears the password, which
    disables Connect, and focus falls to the body then (round-5c fix report). Since round
    5e the Lessons page and the Queue refresh when a download starts, so a pressed row
    leaves a status tab (Failed, Downloaded for a row with a kept note, the Queue's
    Queued tab), or a later page of All, seconds after the press rather than when the
    download ends, and its keyboard focus falls to the body with it (round-5e UI review,
    Low B: the Failed tab's row was gone at +1.68s). The pointer has its own version on
    the Queue's Queued tab: it lists queued jobs oldest first, and the worker claims the
    oldest, so when a download starts (`download_started` refreshes the jobs) the top
    row leaves the list and the rows below move up. A pointer resting on that row's
    *Cancel* is then over the next job's *Cancel*, and a click meant for the job that
    just started cancels another one. Since ruling (w), a row that moves to the top of
    All leaves the screen with its ⋯ still focused (round-5h UI review, finding 2): on
    All, mid-list, ⋯ → *Download*, and about 1.5s later the row moved to the top while
    the view held at 2270px; the focused ⋯ sat at y=−1905, Enter opened its menu out of
    view (y=−1865) with *Copy path* highlighted, and one ArrowDown reached *Cancel
    download* unseen. Radix focuses the menu with `preventScroll: true`
    (`web/node_modules/@radix-ui/react-menu/dist/index.mjs:258-260`). Before (w) the
    view followed the row (2270→0). A pointer user sees the row leave; the cues are the
    "Queued" toast and "downloading 1" in the top bar.
  - *Why:* after the press a keyboard user starts again from the top of the page; a
    pointer user can cancel a job they didn't mean to.
  - *Fix (new mechanism, can wait):* each needs somewhere to send focus. React Router and
    Radix don't manage focus when a table row changes (could not confirm any library
    facility for it). The app's own `rowFocusTargets` (`web/src/lib/focus.ts:25`)
    already picks a neighbour row for the dialogs, and could be reused for a row that
    leaves its list.
  - *Evidence:* `grep -n 'disabled={!canRetry\|disabled={!canCancel' web/src/pages/Queue.tsx`
    · `grep -n 'setPassword("")\|disabled={email' web/src/pages/Settings.tsx`
    · `grep -n 'case "download_started"' web/src/lib/sse-reducer.ts`
    · `grep -n 'func (s \*Store) ListJobsByStatus' -A3 internal/database/jobs.go`
- **D109 · The token prompt's Save button spans the whole dialog.**
  - *What:* the token prompt (`TokenGate`) has no dialog footer: its one Save button sits
    in the form's column and stretches to the dialog's full width, unlike every other
    dialog's buttons. It predates this branch, and the 12px gap of rulings (b) and (k)
    doesn't apply to a single button (round-5f UI review, Polish).
  - *Why:* the one dialog that looks unlike the rest; cosmetic.
  - *Evidence:* `grep -n 'type="submit"' web/src/components/TokenGate.tsx`
- **D110 · On a card, a filled control's ring gap shows the page's colour.**
  - *What:* since ruling (g) every filled control sets its focus ring 2px off its fill,
    and the strip between them is the page background (`ring-offset-background` in
    `FILLED_RING_OFFSET`). On a card (the Settings cards, say) that strip is darker than
    the card around it, a faint moat (1.06–1.09:1 against the card; the ring itself still
    measures 3.6–3.9:1). Matching each surface would need an offset colour per surface
    (round-5b UI review, Info).
  - *Why:* cosmetic.
  - *Fix (new mechanism, can wait):* an offset colour per surface; could not confirm that
    Tailwind can inherit the surface's colour for it.
  - *Evidence:* `cat web/src/lib/ring.ts`
- **D115 · On a 503, Run sync and Dry-run drop keyboard focus.**
  - *What:* when the server answers 503 (no daemon, or no planner), the Dashboard swaps the
    pressed button for a disabled copy wrapped in a tooltip, so focus falls to the page
    body (round-5c UI review, Low 3).
  - *Why:* a keyboard user starts again from the top of the page.
  - *Fix (can wait):* keep the same button and mark it disabled with `aria-disabled`, or
    send focus to the copy.
  - *Evidence:* `grep -n 'runBlocked\|dryRunBlocked' web/src/pages/Dashboard.tsx`
- **D116 · A downloaded lesson with a note is hard to find.**
  - *What:* since rulings (h) and (n) a lesson whose re-download failed, or that Musora no
    longer returns, stays `downloaded` with a note. The Lessons page has no filter or count
    for those, so the owner finds one only by scrolling (round-5c UI review, Info, V1).
  - *Why:* a kept lesson needs the owner's *Download* to be tried again, and nothing says
    how many there are.
  - *Fix (can wait; new mechanism):* a filter or a count for downloaded lessons with a
    note; the API's lesson already carries `error`.
  - *Evidence:* `grep -n 'TabsTrigger' web/src/pages/Lessons.tsx` (one tab per status,
    none for a note) · `grep -n 'json:"error"' internal/server/dto.go`
- **D117 · The Queue's failed-job sentence doesn't say the earlier files were kept.**
  - *What:* a failed re-download of a lesson whose files are on disk leaves the lesson
    `downloaded` with a note that says so (`msgEarlierKept`, `msgNotReturnedKept`), but its
    job, shown in red in the Queue, carries the plain failure sentence (`failDownload.job`,
    or `msgNotResolved`), which reads as if the lesson were lost (round-5c UI review,
    Info, V1).
  - *Why:* a red row for a lesson that is still there.
  - *Fix (can wait; a suggestion to verify):* a kept version of each job sentence, chosen
    in the same transaction (`keepOrSQL`) as the lesson's note.
  - *Evidence:* `grep -n 'job:' internal/scheduler/messages.go` ·
    `grep -n 'func keepOrSQL' -A8 internal/database/downloads.go`
- **D124 · With syncs paused, a press still toasts "Queued" and nothing starts.**
  - *What:* since ruling (m) a press starts a sync at once, so the owner expects the
    download to begin. While syncs are paused the daemon drops the kick, the toast still
    reads "Queued" with the lesson's name, and only the header's "paused" chip says why
    nothing starts (round-5d UI review, Info 1).
  - *Why:* the press looks like it did nothing.
  - *Fix (new mechanism, can wait):* a toast description that reads the pause flag.
    TopBar already reads `summary.paused`; the download response doesn't carry it.
  - *Evidence:* `grep -n 'summary.data?.paused' web/src/components/app-shell/TopBar.tsx` ·
    `grep -n 'toast.success("Queued"' web/src/pages/Lessons.tsx` ·
    `grep -n 'if d.IsPaused()' internal/scheduler/daemon.go`
- **D126 · During Musora's lookup a 0% bar sits under the old "failed" badge.**
  - *What:* when the worker claims a job it emits `job_claimed`, which starts the row's
    progress bar at 0%, but the lesson list isn't refreshed until `download_started`,
    after Musora's lookup. Until then a retried lesson shows its bar under the red "failed"
    badge and the old failure note (round-5e UI review, Info). Older than round 5e.
  - *Why:* for a second or two the row says both "failed" and "downloading".
  - *Fix (can wait):* the owner's call on what the row should say while the lookup runs;
    then either no bar before `download_started`, or a refresh on `job_claimed`.
  - *Evidence:* `grep -n 'case "job_claimed"' web/src/lib/sse-reducer.ts` ·
    `grep -n 'Kind:          "job_claimed"' internal/scheduler/worker.go`
- **D72 · On Windows the library move renames by path.**
  - *What:* on Linux and macOS each rename of the move acts on the folders it holds open
    (`renameat2`/`renameatx_np`), so a folder swapped for a symlink between the move's
    checks and its rename can't redirect it, and an entry already at the destination makes
    the rename, and so the move, refuse (except on a filesystem without the no-replace
    flag, where it retries without it). On Windows `renameat_other.go` still renames by
    path, with `os.Rename`, which is `MoveFileEx(..., MOVEFILE_REPLACE_EXISTING)`: there,
    that swap still redirects the placement, and an entry that appeared at the destination
    after the checks is replaced. The call is in Go's reach: `os.Root.Rename` renames
    relative to folder handles on Windows (`$(go env GOROOT)/src/internal/syscall/windows/at_windows.go`,
    `NtSetInformationFile` with a root handle), but it replaces an existing entry, and
    `golang.org/x/sys` v0.42.0 exports what a no-replace version across two folders needs
    (`NtCreateFile`, `NtSetInformationFile`, `OBJECT_ATTRIBUTES.RootDirectory`,
    `FileRenameInformation`). A move across volumes falls back to the copy on
    `ERROR_NOT_SAME_DEVICE` (`crossdevice_windows.go`), which creates every entry afresh.
    Needs write access to the library and a race. Setting an entry aside and putting it
    back (D66) use the same rename, so they go by path on Windows too, and that is worse
    than a misplaced file: a folder a placement sets an entry aside from, swapped for a
    junction between the check and the rename, moves an entry from OUTSIDE the library
    into the set-aside area, and once the download is recorded the commit deletes that
    area, and the outside entry with it (security round 5, L2, traced in the code). The
    swap points are the lesson folder (the season folder in plex-tv) and, since round 5,
    every subfolder a placement merges entry by entry (`mergeFolder`), at any depth
    (round-5f security review, I-4). No test runs there (CI runs on `ubuntu-latest`), so
    that path is only compiled and vetted.
  - *Why:* the one platform where "nothing lands outside the library, even under a race"
    does not hold, and where a race can delete a file outside it.
  - *Evidence:* `cat internal/scheduler/renameat_other.go internal/scheduler/crossdevice_windows.go` ·
    `grep -n 'func Renameat' -A25 "$(go env GOROOT)/src/internal/syscall/windows/at_windows.go"` ·
    `grep -n 'renameAt(\|RemoveAll' internal/scheduler/aside.go` ·
    `grep -n 'func mergeFolder' internal/scheduler/place.go` · `grep -n runs-on .github/workflows/ci.yml`
- **D73 · yt-dlp writes the video into downloads by path.**
  - *What:* every file drumdrop writes itself goes through `os.Root` on the job's private
    folder (`musora.DownloadOpts.Root`, `writeInRoot`; D66), but yt-dlp is given an output
    path in that folder and follows whatever symlink is on it. The folder is made afresh
    for each job through the downloads folder held open, and a symlinked
    `.drumdrop-in-progress` is refused, so the symlink has to be planted (or a folder on
    the path swapped for one) while the download runs.
  - *Why:* the downloads folder must be trusted; the README says so.
  - *Evidence:* `grep -n 'YtDlpArgs(' internal/musora/download.go` ·
    `grep -n 'func (w \*Worker) startPrivate' -A20 internal/scheduler/private.go`
- **D74 · A follow delete does not hold other follows' lessons.**
  - *What:* a follow delete holds only its own lessons. In the microseconds between reading
    the claims and removing a legacy lesson's name-matched entries, a neighbour's move
    could place a song version the name match picks up (round-3 security review, I7).
  - *Why:* negligible window, legacy rows only; filed so it is not rediscovered.
  - *Evidence:* `grep -n 'func (s \*Store) BeginFollowDelete' -A20 internal/database/downloads.go`
- **D76 · One daemon per database is assumed, not enforced.**
  - *What:* `Daemon.Recover` requeues every `running` job at startup, and removes the
    private download folder of every job its own worker isn't running (`SweepPrivate`, D66),
    which is only right when no other process is downloading: beside a live one it would
    queue that download again and delete its folder, and both would then run it in the same
    folder, where one can promote the other's partial video (D95). The README says to run
    one `daemon` or `serve` per database. Separately, a delete holds its lessons by a
    two-minute lease it renews every 30 seconds. The lease race needed neither a second
    process nor a failing database: one follow delete plus one lesson delete in the same
    process was enough (security round 4, LOW-1). The follow delete's tombstone of lesson 1
    ended lesson 1's lease, a lesson delete took lesson 1, and the follow delete's final end
    cleared that new lease. Fixed minimally on `fix-library-delete-and-move` (`36a0b0d`): a
    delete's hold drops each lesson its own tombstone or keep finished, and renews and ends
    only what it still holds. What remains: a lease that lapses while its holder is alive
    (renewals failing for two minutes, a host suspend, or a wall-clock step, since SQLite's
    `datetime('now')` follows the wall clock) still lets a second delete begin, and the
    first delete's renew or end then acts on the second one's lease. A renewal already in
    flight when a lesson is released can extend a newer delete's lease once, which is
    harmless: that delete ends it itself.
  - *Also:* a follow delete's last step (`RemoveFilelessFollowCascade`) removes the
    follow's lesson rows without looking at their lease, so it can remove the row of a
    lesson a later lesson delete holds (one begun after the follow delete tombstoned it).
    Harmless today: the cascade refuses while any of the follow's lessons records files, so
    that lesson has none, and its delete only finds the row gone and answers 404.
  - *Why:* the process-lock half needs a second process; the lease half needs a lease to
    lapse while its delete is alive. A process lock on the database, and a holder token on
    the lease (D81), would close them.
  - *Evidence:* `grep -n 'func (d \*Daemon) Recover' -A15 internal/scheduler/daemon.go` ·
    `grep -n 'w.isRunning(id)' internal/scheduler/private.go` ·
    `grep -n 'DeleteLease\|DeleteRenewEvery' internal/database/downloads.go` ·
    `grep -n 'func (h \*deleteHold) released' -A5 internal/server/lessonfiles.go` ·
    `grep -n 'func (s \*Store) RemoveFilelessFollowCascade' -A15 internal/database/downloads.go`
- **D81 · The delete lease has no holder.**
  - *What:* `RenewLessonDelete` and `EndLessonDelete` match a lesson by its id and
    `deleting_until IS NOT NULL`, and `TombstoneLesson` and `KeepLessonFiles` clear the
    lease the same way: nothing records which delete holds it. The full fix for D76's lease
    half (security round 4, LOW-1; code round 4, #4): a `deleting_by` holder token on
    `lessons`, set by `BeginLessonDelete` and `BeginFollowDelete` and checked by
    `RenewLessonDelete`, `EndLessonDelete`, `TombstoneLesson` and `KeepLessonFiles`. A
    renewal that matches no rows means the hold was lost, so the delete stops before
    removing anything more.
  - *Why:* after a lapse the tombstone's compare-and-swap is the only backstop, and the
    worker never reads the lease: a job queued while it has lapsed runs, and can undo the
    delete.
  - *Decision needed (owner):* migration 004 (`004_lessons_library_entries.sql`) has not
    been released (it is not on `main`), so the column can go into it for free until then;
    afterwards it needs a migration of its own. Every merge to `main` is a release (D36),
    so decide before this branch merges.
  - *Engine:* SQLite (and `modernc.org/sqlite`) has no lease or owner feature, so this is
    plain SQL.
  - *Evidence:* `grep -n deleting_until internal/database/downloads.go` ·
    `git ls-tree --name-only main internal/database/migrations/` (no 004)
- **D77 · Test fixtures are copied into three packages.**
  - *What:* `seedSeason`, `recordedRow`/`recordOf`, `sorted` and `assertExist` exist, each
    slightly different, in `internal/library`, `internal/scheduler` and `internal/server`
    tests. A small `internal/library/librarytest` package would hold one copy. Not done in
    round 4 of this branch: moving them touches some thirty test files mid-review, and the
    copies differ in signature (the server's `recordOf` builds entries from a season path
    only).
  - *Why:* a fix to one copy does not reach the others.
  - *Evidence:* `grep -n 'func seedSeason\|func recordOf\|func assertExist' internal/*/*_test.go`

- **D54 · Every API route accepts the token in the URL, not only the live-progress stream.**
  - *What:* `requestToken` in `internal/server/auth.go` reads the `Authorization: Bearer`
    header and, when there isn't one, falls back to `?access_token=` in the URL. It does that
    for every `/api/*` request, including the ones that change or delete data (POST, PATCH,
    DELETE). Only the live-progress stream, `GET /api/events`, needs the URL form, because
    the browser's EventSource can't send headers; `web/src/lib/sse.tsx` is the only code that
    uses it. `internal/server/auth_test.go` locks the broad behaviour in, on
    `GET /api/follows?access_token=…`. This used to be D9(b), which described it as the
    stream only and so understated it.
  - *Why:* a URL gets written down in places a header doesn't: a reverse proxy's access log,
    browser history, shell history, a copied link. The token is the one key to the whole API.
    Allowing it in the URL for the one read-only stream that needs it is a narrow, known
    trade-off; allowing it everywhere widens the ways it can leak for no benefit. The fix
    below does not take the stream's own URL out of proxy logs: `sse.tsx` still sends
    `?access_token` on `/api/events`, the only place drumdrop's own code puts the token in a
    URL. What the fix buys is defence in depth: no other route accepts a URL token, so
    scripts and copied links can't come to rely on it. Closing the stream's residual would
    need the stream to stop carrying the long-lived token (for example a short-lived,
    single-use stream ticket); whether that gets its own entry is the owner's call.
  - *Fix:* honour the URL token only on `GET /api/events`. Change the test so it proves other
    routes reject a URL token, and that the stream still accepts it.
  - *Evidence:* `grep -n access_token internal/server/auth.go internal/server/auth_test.go web/src/lib/sse.tsx`
  - *Detail:* vault note drumdrop-auth-posture; `drumdrop-http-api-sse-security.md`.

- **D5 · The CORS preflight doesn't allow PATCH.**
  - *What:* when `DRUMDROP_CORS_ORIGIN` is set, the API answers the browser's preflight with
    `Access-Control-Allow-Methods: GET, POST, DELETE, OPTIONS`. But editing a follow's quality
    is `PATCH /api/follows/{id}` (added in #11), and the web UI calls it.
  - *Why:* a page on the allowed origin can't edit a follow, because the browser blocks the
    request before it is sent. It fails closed, so this is a broken feature rather than a
    security hole. It only shows up with CORS turned on, which is why nothing caught it. The
    web UI on its own origin is unaffected.
  - *Evidence:* `grep -n Allow-Methods internal/server/auth.go` · `grep -n PATCH internal/server/server.go`
  - *Detail:* vault note drumdrop-auth-posture.

- **D6 · Quality values from the CLI and the environment aren't validated.**
  - *What:* the web API rejects an unknown quality (`validQuality`). Every CLI `--quality`
    flag, and `DRUMDROP_QUALITY`, is passed through unchecked. A value that isn't a number,
    like `1080p`, silently falls back to "best" in `FormatSelector`.
  - *Why:* a typo downloads at full resolution (bigger files) with no warning. This was
    deferred once as "separate scope" and is still true.
  - *Evidence:* `grep -rn 'validQuality(' --include=*.go .` (only `internal/server/follows.go`) ·
    `FormatSelector` in `internal/musora/download.go`
  - *Detail:* `musora-downloader-project.md`.

- **D7 · `sync --dry-run` ignores `--limit`.**
  - *What:* in `runSync`, the dry-run branch calls `PlanDryRun`, which takes no limit, and
    returns before the limited path.
  - *Why:* a dry run is supposed to preview the real run. It prints counts, one line per
    follow plus a total (`[dry-run] N new lesson(s) would be queued`), not a list of lessons,
    and with `--limit 5` every one of those counts is uncapped instead of reflecting the 5 the
    real run would queue. Low impact.
  - *Evidence:* `grep -n 'dryRun\|PlanDryRun' cmd/drumdrop/follow.go` ·
    `grep -n 'func (p \*Planner) PlanDryRun' internal/scheduler/planner.go` ·
    `grep -n 'would be queued' internal/scheduler/planner.go cmd/drumdrop/follow.go`
  - *Detail:* `drumdrop-sync-kick-channel.md`, `musora-downloader-project.md`.

- **D8 · The `?status` / `?state` list filters accept any value.**
  - *What:* `GET /api/lessons?status=`, `GET /api/jobs?state=` and the `?status=` on a
    follow's lesson list all pass the value straight to the store.
  - *Why:* a typo returns an empty list instead of a 400, which looks like "there's nothing
    here". Low impact.
  - *Evidence:* `grep -n 'Get("status")\|Get("state")' internal/server/*.go`

- **D9 · Small API hardening nits.**
  - *What:* (a) CORS responses carry no `Vary: Origin`. (b) Moved to its own entry, D54: the
    token in the URL is accepted on every `/api/*` route, not only the live-progress stream,
    which is a security item rather than a nit. (c) Fixed on `fix-library-delete-and-move`:
    an invalid `?brand=` on the instructor preview is now a 400 (`msgBadBrand`) before any
    Musora call, and `POST /api/follows` refuses one too. Before, the add stored it, and
    every sync of that instructor follow failed. (d) The preview treats only the literal
    `?whole=true` as true. (e) The SQLite connection string would break on a database path
    that contains `?`.
  - *Why:* none of the remaining ones exposes anything today in a single-user, self-hosted
    tool. A review agent judged them acceptable, but there's no owner ruling, so they stay
    listed here instead of under accepted residuals.
  - *Evidence:* `grep -rn Vary internal/server` (no match) ·
    `grep -n ValidateBrand internal/server/preview.go internal/server/follows.go` (c, fixed) ·
    `grep -n 'Get("whole")' internal/server/preview.go` ·
    `grep -n 'dsn :=' internal/database/db.go`
  - *Detail:* `drumdrop-http-api-sse-security.md`; agent memory
    `~/.claude/agent-memory/code-reviewer/project_drumdrop_preview_session_handler.md` and
    `project_drumdrop_write_mutex_wal_pool.md`.

- **D10 · Orphaned lessons from old databases aren't re-adopted when you follow again.**
  - *What:* the `ON CONFLICT … DO UPDATE` in `UpsertLesson` never sets `follow_id`. A lesson
    row left with `follow_id = NULL` (from remove/re-add churn on builds before v0.2.0)
    therefore stays orphaned when its course is followed again, and a later
    unfollow-with-delete misses it. A one-line fix was offered and not built:
    `follow_id = COALESCE(lessons.follow_id, excluded.follow_id)`, which keeps "first follow
    wins".
  - *Why:* low today. Since v0.2.1 a delete removes rows instead of orphaning them, and the
    production database was wiped on 2026-06-01, so new orphans shouldn't appear. It only
    matters for an old database, and the same fix would re-adopt any leftovers there.
  - *Evidence:* the `ON CONFLICT` clause in `internal/database/lessons.go`
  - *Detail:* `musora-downloader-project.md` (the 2026-06-01 TrueNAS entry).

- **D11 · Two-version songs in the plex-tv layout record only the first file's size.**
  - *What:* a song downloads two videos (`[Original]` and `[Drumless]`). In the default
    layout the recorded size is the sum of both. In the plex-tv layout only the first
    version's size is stored.
  - *Why:* cosmetic, because the UI shows about half the real total. It was accepted during
    review, with no owner ruling, so it stays listed.
  - *Evidence:* the plex-tv branch after `moveToLibraryPlexTV` in `place`,
    `internal/scheduler/worker_record.go` (it stats one video path)
  - *Detail:* `drumdrop-song-soundslice-video.md`.

- **D12 · Fix the open SonarQube findings.**
  - *What:* the latest analysis of `main` (2026-09-25, `344ee1f` = v0.8.0) has 43 open issues
    (*moves*), down from 67 at `1c2dbda`. Most are cognitive complexity (`go:S3776` ×12) and
    read-only props (`typescript:S6759` ×12), then nested ternaries (`typescript:S3358` ×4).
    One is accessibility: `ProgressRow` uses a `progressbar` role instead of `<progress>`
    (S6819). Branch `fix/sonar-gate-and-coverage` fixes the five that `main`'s gate counts as
    new code: three `go:S3776`, `StatusBadge`'s props, and `Dashboard.tsx`'s focusable span
    (S6845, which was the one reliability issue), and the 4 in `web/src/test/` (D30). Its
    branch scan at `96eed52` has 34 open (*moves*), so 34 should remain after its release.
  - *Why:* the owner's standing rule is that Sonar findings get fixed: no false-positive
    marking, no rule deactivation, no custom profile (decisions #3 in the vault). The
    accessibility finding is a real problem for screen-reader users. Leaving the rest open
    also costs the gate. An older issue on a line a PR changes counts as new code, which is how
    v0.8.0's scan got `new_violations` 5.
  - *Evidence:* `sonar-issues --all` (read-only)
  - *Detail:* vault note drumdrop-sonarqube.

- **D13 · Status colours bypass the design tokens.**
  - *What:* `web/src/index.css` defines the shadcn base tokens (including `--destructive`)
    but no semantic status tokens such as `--success` or `--warning`. Status tones are raw
    Tailwind colours (emerald for success, red for error), now gathered in `StatusBadge.tsx`.
    The error red doesn't use `--destructive`.
  - *Why:* this is design-system drift. A palette change means hunting down literals, and
    "error" can drift away from the destructive colour that buttons and dialogs use.
    Should fix, not urgent.
  - *Evidence:* `grep -n -- '--success\|--destructive' web/src/index.css` ·
    `grep -rn 'emerald-\|red-[0-9]' web/src --include=*.tsx`
  - *Detail:* `~/.claude/agent-memory/design-enforcer/project_drumdrop_status_color_inconsistency.md`.

- **D14 · Dead web code.**
  - *What:* only its own test uses `formatDuration` in `web/src/lib/format.ts`. The SSE
    reducer keeps `recent` and `lastCycle` state up to date, but only the reducer's tests
    read it.
  - *Why:* code nothing uses still has to be read, kept compiling and kept tested. Delete it
    along with its tests, or put it to use in the UI.
  - *Evidence:* `grep -rn formatDuration web/src` · `grep -rn lastCycle web/src`

- **D15 · The HTTP API has no reference doc.**
  - *What:* the README documents the CLI and the web UI but not the `/api/*` endpoints. One
    example of a contract that exists only in code: `POST /api/lessons/{id}/download` returns
    202 when it creates a job and 200 when one already exists.
  - *Why:* anyone scripting against the API, or reviewing it, has to read the handlers. Low
    priority for a single-user tool.
  - *Evidence:* `grep -n StatusAccepted internal/server/lessons.go` · the route list:
    `grep -n HandleFunc internal/server/server.go`
  - *Detail:* `musora-downloader-project.md`.

- **D53 · Without a token on loopback, any web page in the browser can drive the API.**
  - *What:* with no `DRUMDROP_API_TOKEN`, a server listening on loopback allows every
    `/api/*` request (`authorized` in `internal/server/auth.go`). That is the local case:
    `drumdrop serve` or `make dev-api` on a desktop, which listen on `127.0.0.1:8080` by
    default. Nothing checks the `Origin` or `Host` header, and the handlers decode a JSON body
    whatever `Content-Type` it arrives with. A browser sends a "simple" cross-site request (a
    GET, or a POST with a plain-text body) without asking the server first. So any page open
    in the owner's browser can make drumdrop act, and through DNS rebinding (the page's own
    hostname re-pointed at 127.0.0.1) it can also read the follow, lesson and job lists.
    Checked live on a scratch build on 2026-09-23: a cross-origin `text/plain`
    `POST /api/pause` returned 200 and really paused it, and a foreign `Host` header was
    accepted. Deletes and edits are safe: DELETE and PATCH need a preflight, which gets a 404.
  - *Scope:* the TrueNAS container is not affected. It listens on `0.0.0.0`, so it always has
    a token, and a foreign page can't attach it. Severity: low.
  - *Decision needed (owner):* (A) always require a token, even on loopback. (B) keep the
    tokenless loopback mode, but accept only a loopback `Host` (`localhost`, `127.0.0.1`,
    `[::1]`) and reject a request whose `Origin` isn't drumdrop's own; MusicDrop's host and
    origin guards are the worked example. (C) accept it as a residual. The note recommends B:
    it closes both holes and keeps the zero-setup local run. A is just as sound if drumdrop
    never runs outside Docker.
  - *Evidence:* `grep -rn 'Header.Get("Origin")\|r\.Host' --include=*.go internal/server` (no
    match) · `authorized` and `isLoopbackAddr` in `internal/server/auth.go`
  - *Detail:* vault note drumdrop-auth-posture, §3.

- **D142 · A sync button that gets blocked drops keyboard focus.**
  - *What:* on the Dashboard, when Run sync or Dry-run gets a 503 from the server, the pressed
    `PendingButton` is replaced by the blocked control. The two are different components at
    the same place, so React mounts a new `<button>`, and keyboard focus falls to `<body>`. No
    ring shows, a second Enter does nothing, and a screen reader loses its place. Measured in
    Orca's Chromium (2026-09-25): the next Tab lands on the new blocked button itself, because
    the browser keeps a starting point where the removed node was. Firefox and Safari were not
    measured. It predates branch `fix/sonar-gate-and-coverage`, which made the blocked control
    focusable (S6845).
  - *Fix, sketched by that branch's UI review seat:* one component path in both states. The
    wrapper, `Tooltip` and `TooltipTrigger asChild` are always rendered around a
    `PendingButton`, with the tooltip held shut while unblocked (Radix's controlled
    `open`/`onOpenChange`). `PendingButton` then needs a blocked state it can't express today:
    it sets `aria-disabled` after the caller's props, and its `aria-disabled:cursor-progress`
    would give a blocked button a busy cursor. That also folds the Dashboard's local
    blocked-button recipe (`BLOCKED_RING`, the per-variant `BLOCKED` map, the click guards)
    into the shared component instead of forking the button system. Accepted side effect:
    after the 503 the focused button doesn't open its tooltip, since no new focus event fires;
    the toast carries the reason.
  - *Why:* losing focus breaks WCAG 2.4.3 (focus order).
  - *Evidence:* `SyncButton`'s two return branches in `web/src/pages/Dashboard.tsx` · the
    comment "The pressed node was replaced, so focus starts from the page." in
    `web/src/pages/Dashboard.test.tsx` (it encodes jsdom, which restarts from the top)

- **D143 · A blocked sync button says why only after a press, and stays blocked.**
  - *What:* when Run sync or Dry-run is blocked (the server has no daemon or planner
    attached):
    - its reason shows in the press's toast, which stays until closed (since
      `fix/sonar-gate-and-coverage`, on the owner's choice: *"Use the server's sentence"*),
      and once that is closed, only in a tooltip. Escape hides that, touch can't open it by
      hovering (whether a tap focuses the button, which would open it, is unmeasured), and a
      screen reader hears it only while it is open (Radix sets `aria-describedby` only then);
    - the user learns about the block only by pressing the button;
    - the block lasts until the page remounts, even if the server restarts with a daemon.

    The summary DTO already carries `paused` (`web/src/types.ts:62`); a daemon or planner
    presence field would let the buttons show blocked from the start. TopBar's Pause meets the
    same 503 differently: it stays live and toasts on every press. Since these toasts stay
    until closed, repeats pile up: leaving the Dashboard and coming back makes the buttons
    live again, and each new press, and each Pause, adds another. sonner already updates a
    toast in place when given an id it has (`toast(…, { id })`), so a fixed id per sentence
    would stop it; that is a new parameter on `failureToast`, so it waits here.
  - *Why:* rare in practice, since `drumdrop serve` always attaches its daemon. The owner chose
    to record it for later (2026-09-25, *"Record it for later"*), rather than add a visible
    line beside the buttons on `fix/sonar-gate-and-coverage`.
  - *Also:* "only DrumDrop's own 503 blocks" holds by the body's shape, not its origin:
    `fromServer` is set for any JSON error body with a string `error`
    (`web/src/lib/api.ts`), so a gateway answering 503 in that shape would block the button
    and show its sentence, as text. Most proxies send HTML, plain text or nothing. A
    DrumDrop-only marker would be a new mechanism, so it waits here (security seat of
    `fix/sonar-gate-and-coverage`).
  - *Evidence:* `nothingAttached` and `SyncButton` in `web/src/pages/Dashboard.tsx` · the
    `toggle` mutation's `onError` in `web/src/components/app-shell/TopBar.tsx`

- **D144 · The Queue's job-error tooltip can't be reached by keyboard.**
  - *What:* the full error of a failed job shows in a tooltip whose trigger is a truncated
    `<span>` that can't take focus (`web/src/pages/Queue.tsx:258-262`). A keyboard user sees
    only the truncated text.
  - *Why:* keyboard and screen-reader users lose the error text. The fix must not be a
    `tabIndex` on a span, which is SonarQube's S6845.
  - *Evidence:* the trigger in `Queue.tsx`; found by the UI review seat of
    `fix/sonar-gate-and-coverage`

- **D145 · No focus indicator in forced-colours mode.**
  - *What:* Tailwind 4's `outline-none` sets `outline-style: none`, and the app draws focus with
    `box-shadow` rings, which forced-colours mode (Windows High Contrast) removes. So buttons,
    inputs, checkboxes, select triggers and the blocked sync buttons' wrapper ring show no
    focus there. Two kinds do: menu and select items use `outline-hidden`, which Tailwind 4
    turns into a transparent outline that forced colours repaint, and tab triggers draw a
    real 1px outline.
  - *Why:* minor for this owner, but a real accessibility gap. The usual fix is an
    `outline` that stays transparent until forced colours are on.
  - *Evidence:* `grep -rn 'outline-none\|outline-hidden\|outline-1' web/src/components/ui`

## Housekeeping & dependencies

- **D146 · The release job installs the web dependencies while holding a write token.**
  - *What:* goreleaser's before-hook runs `npm --prefix web ci` (`.goreleaser.yaml:8`) inside
    a step whose environment holds `GITHUB_TOKEN`, with contents and packages write
    (`.github/workflows/main.yml`). Any npm install script then runs beside that token. Today
    only esbuild, msw and fsevents have one: msw's does nothing here, and fsevents is
    macOS-only.
    The same hook list then runs the build (`npm --prefix web run build`,
    `.goreleaser.yaml:9`), which executes dependency code (tsc, vite, rollup, esbuild,
    Tailwind's native module, every Vite plugin) beside the same token. And the token is
    already on disk before the hooks run: `actions/checkout` keeps its credentials by default
    (`persist-credentials: true`), and the registry login lasts the job.
  - *Options:* npm's own `--ignore-scripts` on the hook narrows this but doesn't close it
    (the build and the token on disk remain); the security seat measured `npm ci
    --ignore-scripts` then `npm run build` to give byte-identical JS and CSS locally (Node 26;
    one CI run would confirm). Only building `web/dist` in a separate read-only job, handed
    over as an artifact, closes all three routes.
  - *Why:* supply-chain hardening. No package added so far has an install script, and the
    security seat of `fix/sonar-gate-and-coverage` found this, not introduced by it.
  - *Evidence:* `grep -n 'npm' .goreleaser.yaml` · `grep -n -A3 'permissions' .github/workflows/main.yml`

- **D147 · `make coverage` can leave the previous run's reports behind.**
  - *What:* when the Go tests fail, the target stops before the web half, so the earlier
    `web/coverage/lcov.info` stays on disk. The `sonar-scan` wrapper aborts on the failure, but
    its own hint, `SONAR_SKIP_COVERAGE=1`, would then upload this run's Go profile with the
    previous run's web coverage. vitest empties `web/coverage/` only when the web half runs.
  - *Fix:* start the target by removing the report files. That is a new step, so it waits
    here (the vault's BEHAVIOR §12).
  - *Evidence:* the `coverage:` target in `Makefile`

- **D151 · A failed resource download logs its full URL.**
  - *What:* when a lesson's PDF, play-along or sheet-music fetch fails, the download writes
    the whole URL to stderr (`internal/musora/download.go`, the loop over
    `fetchAuxArtifacts`). Whether Musora's resource URLs carry a signature or token in the
    query string, which would make a logged URL usable by whoever reads the log, has not been
    checked.
  - *To do:* check a real resource URL's shape (from `internal/musora/`, never the `*.har`
    files). If it carries one, log the URL without its query.
  - *Why:* container logs outlive the request, and the image runs on the owner's NAS.
  - *Evidence:* `grep -n 'failed to fetch' internal/musora/download.go`; found by the security
    seat of `fix/sonar-gate-and-coverage` (it predates the branch)

- **D152 · Commands without flags ignore `-h`, and `logout --help` logs out.**
  - *What:* `login`, `whoami`, `logout` and `follows` never parse their arguments, and
    `unfollow` reads only its first. So `drumdrop logout --help` removes the saved session
    and credentials, `drumdrop login -h` asks for an email, and `drumdrop whoami -h` fails
    when logged out. Commands with flags answer `-h` with their usage and exit 0 (fixed on
    `fix/sonar-gate-and-coverage`).
  - *Fix:* Go's flag package already does this: an empty `flag.NewFlagSet("drumdrop logout",
    flag.ContinueOnError)` parsed before each such command prints its usage for `-h` and
    rejects an unknown flag. That last part changes behaviour (today any extra argument is
    ignored), so it waits for the owner.
  - *Evidence:* `cmdLogin`, `cmdWhoami`, `cmdLogout`, `cmdFollows`, `cmdUnfollow` in
    `cmd/drumdrop/`; found by the fix round of that branch

- **D157 · An empty "Africa" show in the owner's library.**
  - *What:* the owner's plex-tv library holds a show "Africa" with an nfo and a poster but
    no video, from June. How it got there has not been investigated: which lesson row (if
    any) records those files, and whether a download placed them without a video.
  - *To do:* find the row(s) filed there (`ListLessonsWithFiles`, by `output_dir` or a
    record entry under `Africa/`), and what their last download placed. Never list or read
    the owner's share without asking first.
  - *Evidence:* reported by the owner on 2026-09-25 while testing show artwork
- **D158 · Two lessons of one course land in two one-lesson shows.**
  - *What:* "Welcome To 30-Day Jazz!" and "The Swing Pattern" each became a show of one
    episode, instead of two episodes of one show. A likely cause, not yet checked: each was
    followed on its own, and a node follow's show is the followed node's title
    (`plexShow`: `folderTitle(f)` for any follow that is not an instructor's), so a
    followed lesson is a show named after itself.
  - *To do:* check the two lessons' follows. If that is the cause, decide whether a
    followed single lesson should file under its parent course's show instead (an owner
    call: it moves where new downloads go).
  - *Evidence:* `plexShow` in `internal/scheduler/worker.go`; reported by the owner on
    2026-09-25
- **D159 · A resource is saved without its file extension.**
  - *What:* a lesson resource named "E-Book PDF" was saved as `E-Book PDF`, with no `.pdf`,
    so a file manager or Plex can't tell what it is. The name comes from the resource's
    title when it has one (`Sanitize(firstNonEmpty(r.Name, urlBasename(r.URL)))`), and a
    title carries no extension; only the URL's basename does.
  - *To do:* keep the title, and add the URL's extension when the title has none.
  - *Evidence:* `resourceFetches` in `internal/musora/download.go`; reported by the owner
    on 2026-09-25

- **D16 · Move off Node 20, which reached end-of-life on 2026-04-30.**
  - *What:* CI (`node-version: 20` in `ci.yml` and `main.yml`) and the Dockerfile's web stage
    (`node:20-alpine`) still build on Node 20. `web/package.json` has no `engines` field.
    Local development already runs a much newer Node. The tests run the same on both, because
    `web/src/test/jsdom-shims.ts` installs its in-memory `localStorage` unconditionally.
  - *Why:* an end-of-life runtime gets no security fixes, and the build environment has
    drifted away from the development one.
  - *Evidence:* `grep -rn node-version .github/workflows` · `grep -n 'node:' Dockerfile` · `node --version`
  - *Detail:* `project_drumdrop_node26_localstorage_polyfill.md`.

- **D17 · npm: dev-tool advisories and majors behind.**
  - *What:* besides react-router (D2), `npm audit` reports advisories in the build and test
    tools, plus transitive ones (12 findings in total on 2026-09-25, *moves*). They include a
    critical one in vitest 2.x (the fix is vitest 5, a breaking upgrade) and one in vite ≤6.4.2
    (fixed inside v6). Several majors are behind: vite 6 → 8, vitest 2 → 5, TypeScript 5.9 → 7,
    lucide-react 0.x → 1.x, sonner 1 → 2, tailwind-merge 2 → 3, jsdom 25 → 30 and
    @vitejs/plugin-react 4 → 6. Nothing tracks this automatically (D31).
  - *Why:* dev-only advisories don't ship to users, but they run on the developer machine and
    in CI, and the longer the majors wait, the bigger the eventual jump. Coverage (D18) runs on
    vitest 2.1: `@vitest/coverage-v8` is pinned to 2.1.9, which peers exactly vitest 2.1.9, so a
    vitest upgrade moves both together. `npm audit` also lists `@vitest/coverage-v8` as
    critical, through vitest's own advisory. That coverage also reports line 1 of a module that
    imports anything as never run (46 of 51 files on 2026-09-25, *moves*), so a new file's
    first line counts against new-code coverage. Whether a newer `@vitest/coverage-v8` fixes
    it is unchecked.
  - *Evidence:* `cd web && npm audit` · `cd web && npm outdated` · after `make coverage`,
    `awk '/^SF:/{f=$0} /^DA:1,0$/{print f}' web/coverage/lcov.info | wc -l`

- **D20 · `make test` is weaker than CI.**
  - *What:* `make test` is `go test ./...`, which is cached and runs without `-race`. CI runs
    `go test -race ./...`, and the race detector needs cgo. There's no Makefile target for
    the web tests, and the web "lint" is only `tsc --noEmit` (no ESLint).
  - *Why:* a green `make test` can hide a race, or a stale cached pass, that CI will then
    catch. The CI-equivalent commands are `CGO_ENABLED=1 go test -race -count=1 ./...` and
    `cd web && npx vitest run`.
  - *Evidence:* `grep -n '^test' Makefile` · `grep -n 'go test' .github/workflows/ci.yml`
  - *Detail:* `drumdrop-race-needs-cgo.md`, `project_go_test_cache_stale_pass.md`.

- **D21 · Test noise from React act() warnings.**
  - *What:* a green `vitest run` prints React `act()` warnings from the SSE provider, the
    token gate and the Select component (8 on 2026-09-23, *moves*). (The staticcheck half,
    two nil contexts in `internal/musora/download_test.go`, is fixed on
    `fix-library-delete-and-move`: `~/go/bin/staticcheck ./...` prints nothing.)
  - *Why:* noise teaches people to ignore warnings, and then it hides the real one when it
    arrives. staticcheck isn't a CI gate, so nothing stops new findings.
  - *Evidence:* `cd web && npx vitest run`

- **D22 · Local clean-up, in this checkout only.**
  - *What:* this checkout has a stale 16 MB `./drumdrop` binary, built 2026-05-31 with version
    `dev`, long before v0.8.0, and an empty `downloads/` folder. The rest was cleared on
    2026-09-25, after v0.8.0 merged:
    - the 42 agent worktrees under `.claude/worktrees/` and their 42 branches, each checked
      first for uncommitted work and for commits missing from the merged code;
    - the stale `origin/*` refs (`git fetch --prune`).

    `web/dist` is rebuilt only by `npm run build` or `make build-ui`; the gate (the project
    rules' hard rule 1, and `ci.yml`) doesn't build it. So run `make build-ui` before a bare
    `go build -tags webui`. New agent worktrees keep appearing
    under `.claude/worktrees/` while a branch is being built.
  - *Why:* stale artefacts get run or embedded by mistake. They're all gitignored or local, so
    none of them affects the repo. Read before deleting anything.
  - *Evidence:* `ls -la drumdrop downloads .claude/worktrees web/dist` · `git branch -r` compared
    with `git ls-remote --heads origin`

- **D23 · Sonar display name.**
  - *What:* `sonar.projectName=drumdrop`, while the sibling projects use `MusicDrop` and
    `SpenDrop`. Cosmetic: renaming changes only the label on the Sonar server, not the key.
  - *Evidence:* `grep -n projectName sonar-project.properties`

- **D112 · The casefold tests probably skip in GitHub CI.**
  - *What:* `casefold_linux_test.go` re-runs a test in a user and mount namespace, mounts
    a case-insensitive tmpfs and marks a folder casefold, and skips when any of that is
    refused. That needs unprivileged user namespaces and a tmpfs with casefold (Linux 6.13
    or later, per the round-5b code review); whether GitHub's `ubuntu-latest` has both is
    unconfirmed. Two portable tests pin the same identity guards through a symlinked
    folder, on any disk (`TestPlacedAtIsByIdentity`,
    `TestWorkerPlexTvMergeOfARecordSpelledAnotherWay`); only the casefold tests make the
    real case, a title that changes only in letter case on a case-insensitive disk.
  - *Why:* a green CI doesn't say the casefold tests ran.
  - *How to check:* CI runs `go test -race ./...` without `-v`, so its log doesn't show
    skips; one run with `-v -run 'Casefold|CaseInsensitiveDisk' ./internal/scheduler/`
    would.
  - *Evidence:* `grep -n 't.Skipf' internal/scheduler/casefold_linux_test.go` ·
    `grep -n 'go test' .github/workflows/ci.yml`

## Open questions (owner decisions)

Three choices described in their own entries are also waiting on the owner: D3's yt-dlp
rebuild, D53's fix for the tokenless loopback mode (options A, B or C), and whether D81's
lease holder token goes into the unreleased migration 004 (before this branch merges).

- **D154 · A show's own files stay after its last lesson is deleted.**
  - *Context:* ruling #78 (3) makes `tvshow.nfo`, `poster.jpg` and `fanart.jpg` write-once
    and unrecorded, like the season folder: drumdrop creates a missing one and never
    removes one. So deleting a show's last lesson leaves the show folder with those three
    files (and the empty season folder) behind, and Plex keeps listing an empty show.
  - *The question:* whether a delete that removes a show's last recorded lesson should also
    remove the show files drumdrop wrote (it can't tell them from an owner's own files at
    the same names without recording them), or leave them.
  - *Evidence:* the top comment of `internal/scheduler/showfiles.go` ("never replaces or
    removes one"); `EnsureShowFiles` writes no record
- **D155 · Show art is never refreshed when Musora changes it.**
  - *Context:* a show's files are written once; `tvshow.nfo` marks the show done, and from
    then on the show costs no request (ruling #78 (3)). A new course header, coach card or
    description on Musora never reaches the show folder. The only way today is by hand:
    remove `tvshow.nfo` and the images to replace, and the next cycle writes them again.
  - *The question:* whether to refresh them (on a schedule, or from an action in the UI),
    given that a refresh must not replace a file the owner put there.
  - *Evidence:* `EnsureShowFiles` and `showFilesFor` in `internal/scheduler/showfiles.go`
- **D156 · A re-download replaces a hand-placed `<episode>.jpg`.**
  - *Context:* in plex-tv the episode image is `<episode>.jpg`, the name Plex documents
    for an episode's image, so an owner may put one there by hand. A re-download places its
    image at that name and, like every other name it places, replaces an entry there that
    no lesson records (owner ruling #66: a leftover drumdrop no longer tracks), logging
    "replaced … which no lesson recorded". The one-time rename does not: it leaves a name
    already taken alone. Legacy name matching never claims `<episode>.jpg`, so a delete
    never removes an unrecorded one.
  - *The question:* whether `<episode>.jpg` (and a song version's `.jpg` and `.nfo`) should
    be protected from that replacement, unlike the other episode names.
  - *Evidence:* `clearNames` in `internal/scheduler/place.go` ·
    `TestPlexTVMoveReplacesAnEntryNoLessonClaims`

- **D132 · A long title makes the Lessons table scroll sideways, and then every ⋯ is out
  of view.**
  - *Context:* ruling (t) made the table fit a 1024px window with short titles (D131).
    Measured by the round-5f/5g UI seat in headless Chromium (Noto Sans, overlay
    scrollbars; finding 3): at 1024px the table fits only while no title on the page is
    longer than about 53 characters (55 overflow by 2px, 60 by 28px, 70 by 94px); at
    1280px the limit is about 75. With a 91-character title, 1279px fits but 1280px
    overflows by 112px, because Brand and Quality come back at 1280; 1366px fits again.
    Whenever the table overflows (at 900px even with short titles, D131), every row's ⋯
    is off-screen, and the only horizontal scrollbar sits under the 50-row table.
  - *The question:* whether to let titles wrap below the `xl` breakpoint (the seat's
    option, a design call), or leave it.
  - *Evidence:* `grep -n 'WIDE_ONLY\|min-w-88' web/src/pages/Lessons.tsx` · on a page of
    All, compare `scrollWidth` with `clientWidth` on the table's parent at 1024, 1279
    and 1280px, with a title over 53 characters on the page.
- **D133 · In a three-line row the other cells sit lower than the title.**
  - *Context:* the badge, Size, Updated and ⋯ are centred on the row, so in a row whose
    note wraps to three lines they sit 26px below the title's midline (18px in a
    two-line row); they line up with the note's first line. It still reads fine at
    1024px (round-5f/5g UI review, finding 4, polish).
  - *The question:* align the row's cells to the top (the seat's option), or keep them
    centred.
  - *Evidence:* the Lessons table row in `web/src/pages/Lessons.tsx` (the seat cited
    `:407-428` at `4eedce9`).
- **D134 · Below 1280px a lesson's downloaded quality isn't shown anywhere.**
  - *Context:* ruling (t) hides the Quality column below 1280px (D131). Follows shows the
    quality a follow asks for, not the one each lesson got, so at those widths nothing
    says what a lesson was downloaded at (round-5f/5g UI review, finding 6). Nothing else
    is lost: headers and cells hide together, and no cell spans columns.
  - *The question:* whether the quality needs a place below 1280px, and where.
  - *Evidence:* `grep -n 'WIDE_ONLY' web/src/pages/Lessons.tsx`
- **D135 · An open row menu can still change or move under the reader in cases ruling
  (v) doesn't cover.**
  - *Context:* (v) closes a lesson's open ⋯ menu when the lesson starts or stops
    downloading (D129). Found by the round-5h web fix, in headless Chromium against a
    mock API (1280px, All):
    - (a) Other changes to the lesson still change the open menu's items: another
      client skips it (*Un-skip* appears first) or starts deleting it.
    - (b) Before the start, the row already grows when its job is claimed (the progress
      bar appears), and the open menu moves down 31px. In that run the resting pointer
      ended between items, but it could land on *Skip*. The items haven't changed then,
      so (v) doesn't close the menu.
    - (c) If the row leaves the list while its menu is open (the Pending or Downloading
      tab, or page 2 of All), the menu goes with it and focus drops to the page body,
      not to a ⋯. Same fix as D101: `rowFocusTargets` (`web/src/lib/focus.ts:25`).
    - (d) After (v) closes the menu, a click lands on whatever is underneath: a table
      cell (harmless), or another row's ⋯, which opens that row's menu. Other rows' ⋯
      buttons cover about 16% of an open menu (a 138×115px menu, 36px buttons, all in
      its right strip; round-5h UI review, finding 6).
    - (e) A screen reader gets no reason for the close: focus lands on "Actions for
      <title>", and the only announcement is the top bar's polite "downloading 1" at
      the claim (`web/src/components/app-shell/GlobalProgress.tsx:19`), which doesn't
      name the lesson (round-5h UI review, finding 5). Announcing it is a new mechanism
      and can wait; sonner's toaster is already a polite live region.
    - An open menu also slides with the rows when a row above the view grows (D136).
    The round-5h UI seat rated none of (a)–(d) more than can-wait.
  - *The question:* widen (v) to any change in a lesson's items or position, or leave
    these as they are.
  - *Evidence:* `grep -n 'function RowMenu' web/src/pages/Lessons.tsx` · the fixer's
    report, `docs/superpowers/checkpoints/2026-09-23-library-fixes/round5h-fix-web.md`
    (local only)
- **D136 · With scroll anchoring off, a row that grows above the view moves the rows in
  view.**
  - *Context:* ruling (w) turned scroll anchoring off on the Lessons page
    (`[overflow-anchor:none]` on its root, `b040729`), so a started download that moves
    to the top of All no longer drags the reader's view (18 jumps in 15s before, 0
    after). The cost: when content above the view grows, the browser no longer
    compensates, so the rows in view shift down. A progress bar appearing at a claim
    shifted them by about 62px (round-5h web fix, headless Chromium). Firefox and Safari
    anchor differently and were not checked. The Queue page keeps anchoring: there, a
    started job leaving the Queued tab above the view shifted the scroll by one row while
    the same row stayed at the top, which is anchoring holding the view still.
  - *Measured since* (round-5h UI review, finding 3; headless Chromium at 1280×900, a
    15s sync of 9 lessons against a mock, one unchanging row watched): a reader whose
    top visible row is next to start sees one 46px shift (anchoring on jumped 1132px);
    a lesson mid-view that starts moves nothing (anchoring on jumped 2270→0); pending
    lessons above the view give one 46px shift at the first claim (0 with anchoring
    on). With pending lessons *below* the view the rows drift +99/−46px per lesson,
    477px in all, and **the same with anchoring on**: React keeps the started row's
    element and re-inserts every row above it, Chrome's anchor included, so Chrome loses
    its anchor. A row leaving above the view (the Pending tab, say) shifts the rows in
    view too (round-5h code review, Info 5). An open ⋯ menu slides with the rows: 46px
    at the first claim, with the menu open on a lesson and pending lessons above the
    view (a pointer on *Copy path* ended above the menu, one on *Delete* on *Copy path*;
    0px with anchoring on). That widens D135 (b). One download runs at a time, so
    *Cancel download* can't slide under the pointer this way. Not measured: the real
    worker's gap between a download's end and the next claim (the mock sends both in
    one frame, which may hide a −46/+46 bounce per lesson).
  - *Ruling (x), 2026-09-24, "Follow the lesson":* with a lesson's ⋯ menu open, when the
    lesson starts or stops downloading and moves to the top of All, (v)'s focus return
    wins over (w). Radix hands focus back to ⋯ with a plain `focus()`
    (`web/node_modules/@radix-ui/react-dropdown-menu/dist/index.mjs:113-114`), which
    scrolls the page to it: measured in Orca 431→145px (the lesson third in All, at
    1280×420), and by the UI seat 2270→0 from mid-list. The reader was acting on that
    lesson, so the page follows it and shows its ⋯ ringed, which also says why the menu
    closed. Without an open menu, (w) holds. Declined: *Keep the view still* (focus ⋯
    without scrolling), since the menu and the lesson would both vanish with nothing
    showing where.
  - *The question:* accept the shift as (w)'s cost, or look for a way to keep both. The
    UI seat found no cheap one: only a steady order for All would help (the owner's
    call; D113, D130).
  - *Evidence:* `grep -n 'overflow-anchor' web/src/pages/Lessons.tsx`
- **D127 · A skipped lesson's menu offers *Un-skip* and *Download*, which now do almost
  the same thing.**
  - *Context:* since ruling (m) both start a sync at once. *Un-skip* sets the lesson
    back to pending (`UnskipLesson`) and lets that sync queue it, if its follow still
    lists it; *Download* queues its job itself (`EnqueueJob`), whatever the follow
    lists, then kicks the same sync. So on a skipped lesson both usually end with it
    downloading. Raised for the owner's eye by the round-5d UI review (Info 4); nothing
    is broken.
  - *The question:* keep both items, or show only one of them on a skipped lesson.
  - *Evidence:* `grep -n 'unskip.mutate\|download.mutate' web/src/pages/Lessons.tsx` ·
    `grep -n 'func (s \*Server) handleDownloadLesson\|func (s \*Server) handleUnskipLesson' internal/server/lessons.go`

- **D96 · A lesson Musora returns with no video is recorded downloaded. Is that right?**
  - *Context:* `DownloadLesson` downloads a video only when the lesson has an HLS manifest,
    or a soundslice slug (a song). A lesson with neither gets its resources, poster and
    `.nfo`, and nothing else, and the download succeeds: the worker records it
    `downloaded`, and syncs never try it again. `main` does the same. Found in the round-5
    browser pass, where a test stub answered every lesson query with the wrong document,
    so each lesson resolved with no video and was recorded with only a `.nfo`; real
    Musora filters by id, so that was the stub's fault.
  - *Why it matters:* if Musora ever renames or reshapes the video field (hard rule 10),
    every lesson would be recorded as downloaded with no video, silently, and never tried
    again.
  - *Decision needed (owner):* are lessons with no video legitimate? D55 implies some are
    (`--resources-only`, or a song whose score has no recordings). If they are, should the
    lesson's row say it has no video (a note, say), so a silent change on Musora's side
    shows up?
  - *Evidence:* `grep -n 'if !o.ResourcesOnly' -A42 internal/musora/download.go` ·
    `git show main:internal/musora/download.go | grep -n 'SoundsliceSlug(); slug'`
- **D106 · After a title change, a plex-tv re-download still deletes old-title files it
  didn't bring back. Keep that?**
  - *Context:* round 5 keeps what a re-download didn't bring back in two cases: a previous
    folder at another place (ruling (e)) and a plex-tv lesson's recorded entries at the
    same episode name (ruling (j)). A plex-tv lesson's recorded files at an old title's
    names are still its own by record (ruling #66), and still go, whether or not the
    re-download brought each back. So after a title change: (1) old-title captions or a
    poster the re-download failed to fetch are deleted, and nothing replaces them; (2) a
    song version it didn't get (an old-title `[Drumless].mp4`) is deleted; (3) a file the
    owner edited at an old name (a hand-fixed `.nfo` or poster) is deleted; (4) with
    `--resources-only`, a title change deletes the old-title video, and nothing new
    replaces it. The default layout keeps its old-title folder unless the download brought
    back every file in it (D58). (Round-5c fix report; round-5b code review, I5.)
  - *Options:* (a) keep ruling #66 for files. (b) keep an old-title file, still recorded,
    when the re-download brought nothing back in its place (a new mechanism).
  - *Why it's the owner's:* it changes what ruling #66 says a re-download removes.
  - *Evidence:* `sed -n 12,23p internal/scheduler/previous.go` ·
    `grep -n 'fileIsOwn' internal/scheduler/previous.go internal/scheduler/plexmove.go`
- **D57 · Two lessons can share an episode number in one show. Keep the numbering?**
  - *Context:* a lesson's episode number is its position in the expansion of the follow
    that first found it (`UpsertLesson` keeps the first write). The plex-tv show is the
    course, so lessons that reached one course through different follows (a node follow of
    the course, and an instructor follow, say) can get the same `s01eNN`, and a lesson with
    no position is numbered 1. Since this branch drumdrop tells their files apart by record,
    so a delete or a re-download never touches the other lesson's files; how Plex shows two
    files with one episode number in one season is unverified.
  - *Options:* (a) keep it: ownership is by record, and the names stay as they are. (b)
    Number episodes by the lesson's place in its course. (c) Give a colliding lesson the
    next free number. (b) and (c) rename files in the owner's library, which is why this is
    the owner's call.
  - *Evidence:* `grep -n 'COALESCE(lessons.position' internal/database/lessons.go` ·
    `grep -n 'position := sql.NullInt64' internal/scheduler/planner.go`

- **D25 · The stored password: build automatic re-login, or stop storing it?**
  - *Context:* see D4. The original design (2026-05-29) chose "auth via stored email/password
    (encrypted)" so drumdrop could log in again when the session cookie expires (it slides,
    roughly 250 days). The re-login was never built, and downloads turned out not to need a
    session at all.
  - *Options:* (a) build the re-login, which gives the stored password a purpose, although a
    recoverable password with its key beside it remains the design. (b) Stop storing the
    password: drop the `credentials.enc` handling, keep the cookie, and run `drumdrop login`
    again when it expires. (c) Ask whether drumdrop needs a Musora login at all, since
    downloads are open-read.
  - *To weigh:* (b) or (c) removes the risk rather than managing it, because a password that
    is never stored can't leak. The web UI's Musora card says the server signs in with the
    email and password and "keeps a copy in its config folder"; (b) or (c) changes that
    sentence too (`grep -n 'keeps a copy' web/src/pages/Settings.tsx`).
  - *Detail:* vault note drumdrop-auth-posture.

- **D83 · Which 4xx does Musora send for a wrong password?**
  - *What:* `POST /api/session` answers 422 for credentials Musora refused, 502 when Musora
    can't be reached or its answer can't be read, and 500 when the session couldn't be
    saved; never 401, since the web client clears its API token on any 401 (D88).
    "Refused" is any 4xx but 408 and 429 (`rejectsCredentials` in `internal/musora/auth.go`).
    Which status Musora really sends for a wrong password is unconfirmed: the HAR captures
    that would show it hold live session credentials and are off-limits. A 403 from a web
    application firewall, a block rather than a verdict on the password, would read as
    "Musora didn't accept that email and password".
  - *How:* one login with a wrong password, reading only the status Musora answers
    (`POST` to `AuthBase + "/sessions"`, as `Login` sends it).
  - *Why it's the owner's:* it signs in to the owner's Musora account with a wrong
    password.
  - *Evidence:* `grep -n 'func rejectsCredentials' -A3 internal/musora/auth.go` ·
    `grep -n 'StatusUnprocessableEntity\|StatusBadGateway' internal/server/preview.go`

- **D26 · Is v0.7.1 actually running on TrueNAS, and do songs work there?**
  - *What:* the v0.7.1 fixes for the two production song failures, Kryptonite (a hash-style
    soundslice slug) and Even Flow (a YouTube 403), were only verified in a locally built
    image. No production confirmation is recorded. Neither is the v0.6.1 song re-download
    that was left to the owner.
  - *How:* `curl http://<truenas-host>:3737/healthz` (it returns the version) ·
    `docker exec drumdrop yt-dlp --version` · retry one song.
  - *Why it's the owner's:* the host and its compose file can't be reached from the repo or
    the vault.
  - *Detail:* `drumdrop-song-soundslice-video.md`, `drumdrop-groq-go-schema-mismatch.md`;
    vault note drumdrop-deploy-runbook.

- **D27 · github-tag-action has a v7. Does decisions #20 still hold here?**
  - *What:* the release job runs `mathieudutour/github-tag-action@v6.2` under
    `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24`, and the workflow's own comment says to drop that
    flag once the action ships a Node 24 release. v7, which runs on Node 24, was published on
    2026-09-23 with empty release notes. decisions #20 (in the owner's vault) parks swapping
    the action and says not to re-raise it. But it was written when the action looked
    abandoned (last release v6.2, from 2024), and it describes SpenDrop, which pins the action
    by SHA. drumdrop pins it by the `v6.2` tag, which can be moved.
  - *Why it's the owner's:* #20 is explicit that only a failed release or the owner re-opens
    it. Nothing is broken today. When GitHub drops the Node 20 shim, the release will fail
    loudly rather than number a version wrong.
  - *Evidence:* `grep -n 'github-tag-action\|FORCE_JAVASCRIPT' .github/workflows/main.yml` ·
    `gh release list -R mathieudutour/github-tag-action --limit 3`

- **D29 · Keep `web/src/components/ui/**` out of SonarQube?**
  - *What:* the 14 vendored shadcn primitives (plus `sonner.test.tsx`) are excluded from
    analysis. SpenDrop's test is that an edited primitive has become our code and should be
    analysed. SpenDrop analyses its copy for that reason (more than half of its primitives
    had been edited); MusicDrop excludes its folder. Here, 9 of the 14 now carry a
    `LOCAL EDIT` comment: alert-dialog, badge, button, checkbox, dialog, input, select,
    sonner and tabs (*moves*). The first was `sonner.tsx`: in PR #6, commit `5f1541c` removed
    its theme lookup (next-themes) and hardcoded `theme="dark"`, because the app is dark-only.
    When this entry was first written, that was the only edit.
  - *How to check, and how not to:* `git log` on main can't show an edit like that. PRs are
    squash-merged, so main has one "added" line per file and hides whatever happened on the
    PR branch. That is how this board and `sonar-project.properties` first said "none has
    been edited". Check a PR's own commits instead (below), or diff each file against the
    shadcn registry. The registry diff is the stronger check, because it also catches an edit
    made before a file's first commit; it hasn't been run.
  - *Options:* (a) keep excluding the whole folder, accepting that one edited file goes
    unanalysed. (b) exclude only the unedited files by name (5 today) and let Sonar analyse
    the edited ones. (c) analyse the whole folder, SpenDrop's choice.
  - *To weigh:* (b) applies SpenDrop's test exactly, but the list then has to be updated
    by hand whenever a primitive is edited or added. (c) needs no list, but under decisions #3
    every finding in vendored code then has to be fixed, not marked.
  - *Why it's the owner's:* what Sonar sees is the owner's call under decisions #3. The reason
    and the check are written in `sonar-project.properties`. The owner's 2026-09-25 ruling on
    coverage (decisions #75, *"no work around sonarqube find fixes instead"*) may bear on it;
    whether it also covers what Sonar analyses is the owner's to say.
  - *Evidence:* `grep -l "LOCAL EDIT" web/src/components/ui/*` ·
    `gh api repos/elienop/drumdrop/pulls/6/commits --jq '.[].sha'`, then
    `gh api repos/elienop/drumdrop/commits/<sha> --jq '.files[].filename'` for each; the
    folder came in through #6, #8 and #11 ·
    `gh api repos/elienop/drumdrop/commits/5f1541c --jq '.files[] | select(.filename|endswith("sonner.tsx")) | .patch'` ·
    not `git log -- web/src/components/ui` on main, which shows only `A` lines whatever
    happened on the PR branches

- **D31 · Turn on Dependabot and vulnerability alerts?**
  - *What:* there's no `.github/dependabot.yml`, and the repo's vulnerability alerts are off
    (the API returns 404). So nothing flags D1, D2 or D17 unless someone runs an audit by hand.
  - *Why it's the owner's:* repo settings belong to the owner. The vault's decisions log has
    Dependabot history from the sibling repos (#1, #22) that is worth reading first.
  - *Evidence:* `ls .github/dependabot.yml` · `gh api repos/elienop/drumdrop/vulnerability-alerts`
    (404 = off)

- **D32 · Delete branches on merge?**
  - *What:* the repo setting `delete_branch_on_merge` is off, which is why five merged PR
    branches lingered. They're gone from GitHub now; only local tracking refs remain (D22).
  - *Why it's the owner's:* it's a repo setting.
  - *Evidence:* `gh api repos/elienop/drumdrop --jq .delete_branch_on_merge`

- **D33 · Scrub the design spec committed in `9436cc0` from history?**
  - *What:* a design spec (`docs/superpowers/specs/2026-05-29-drumdrop-design.md`) was
    committed in `9436cc0` and pushed, then untracked in `9596f24`. It's still in main's
    history. It holds design text only, no secrets.
  - *Options:* leave it (no harm has been found), or rewrite history and force-push main.
    The rewrite is disruptive: every later commit gets a new SHA and every tag would have to
    move.
  - *Evidence:* `git merge-base --is-ancestor 9436cc0 main && echo still-in-history`
  - *Detail:* `git-no-push-without-ask.md`.

- **D34 · `DRUMDROP_HOST_DOWNLOADS_DIR`: keep or retire?**
  - *What:* this setting (added in v0.0.4) rewrites paths under the downloads folder to their
    host path, so the web UI's "Copy path" works on the host. Since v0.3.0, a finished lesson
    is moved into `DRUMDROP_LIBRARY_DIR` when one is set, and library paths are never
    rewritten ("Plex owns the path", D38). With a library, the setting no longer affects
    finished lessons. Without one, it still works. The README now documents this.
  - *Options:* keep it for setups without a library, or retire it.
  - *Evidence:* `hostPath` in `internal/server/dto.go` · `grep -rn HOST_DOWNLOADS --include=*.go --include=*.yml .`
  - *Detail:* `musora-downloader-project.md`.

- **D149 · The status badges' tone borders differ in strength.**
  - *What:* since D148, each status badge draws its tone's border at the same alpha
    (`web/src/components/StatusBadge.tsx`), which reads unevenly against the page: amber
    (downloading, running, deleting) about 2.6:1, emerald (done) 1.9:1, red (failed) and zinc
    (queued) about 1.5:1. In-progress pills look outlined, failed ones soft.
  - *Why it's the owner's:* a visual tuning call. All four beat the grey rim they had before
    (1.0 to 1.2:1 against their own fill).
  - *Evidence:* the tone classes in `StatusBadge.tsx`; measured by the UI review seat of
    `fix/sonar-gate-and-coverage`

- **D150 · The border token is faint where it is a control's only edge.**
  - *What:* `--border` and `--input` (`oklch(0.3 0 0)`) are 1.38:1 against the background and
    1.30:1 against cards. For an unchecked checkbox or an empty input, that border is the
    control's only visible edge, and WCAG 1.4.11 asks 3:1 for the parts that identify a
    control.
  - *Why it's the owner's:* it is a theme token; raising it changes every card, table and
    dialog border too, unless the controls get their own token.
  - *Evidence:* the tokens in `web/src/index.css`; found by the UI review seat of
    `fix/sonar-gate-and-coverage` (it predates the branch)

## Accepted residuals and deliberate decisions (not work)

- **D120 · With the library drive unplugged, a failed re-download is retried until one
  succeeds, and that one lands on the disk underneath (owner ruling (o), 2026-09-24).** A
  lesson stays `downloaded` after an attempt that recorded nothing only if the files it
  records are on disk at that moment (the recorded video; without one, every entry of its
  plex-tv record, a regular file or a folder, or else its folder; anything that can't be
  read counts as missing). The owner picked *"Check the disk at the end"* accepting the
  cost as this entry then put it: every sync retries until the drive is back. **That was
  wrong** (round-5d security review, F1; corrected in round 5e; the owner was told
  in the round-5e report and kept (o), vault decisions #72 (o)). What happens while the library drive is not mounted:
  - a failed re-download marks the lesson failed, and the next sync downloads it again;
  - the placement creates a missing library folder (`os.MkdirAll` in
    `openLibraryParent`), or writes into the empty folder the drive is mounted on, so the
    first retry whose download succeeds is placed on the disk underneath, recorded there,
    and syncs stop retrying. Once the drive is back it hides that copy: the recorded paths
    read the drive's older copy again, and the retry's copy takes space on the other disk
    under the mount point (the seat's probe `TestR5dUnpluggedRetryWritesUnderTheMountPoint`,
    both with the folder gone and as an empty mount point). A lesson downloaded for the
    first time while the drive is out is placed there the same way (that predates (o)).
    In plex-tv that retry's record lists only what it placed (the video and its `.nfo`
    in the probe), so once the drive is back the drive's other files for the episode
    (captions, a poster) are claimed by no lesson, and a *Delete* leaves them (round-5e
    security review, S2: `TestR5eUnpluggedDriveREADME`, the `.en.vtt` "claimed by []");
  - while the library path answers with an error (EIO, a failing mount; chmod 000 in the
    probe) the placement fails. In the default layout every sync then retries until the
    drive is back, as the ruling assumed. In plex-tv a lesson whose library files are
    recorded falls back to downloads, as any failed move does (ruling (i)): the retry is
    recorded in `dl/<Course>/NN - Title`, the library record is kept, and the next sync
    queues 0; nothing is lost. Only when the downloads folder sits on the same failing
    mount (the compose file's single-parent bind) does the fallback fail too, and syncs
    retry;
  - a re-download Musora doesn't return, or one that is canceled, ends `skipped`, since
    its files read as missing (`keepOrSQL(StatusSkipped)` in `NotReturnedDownload`,
    `endDownloadSQL` in `CancelDownload`), and it stays skipped when the drive is back:
    syncs never queue a skipped lesson, so *Download* or *Un-skip* is needed (round-5d
    code review, I1). The row keeps its paths, so the Lessons page still offers both,
    and *Delete*.

  D121's guard would make "retries until the drive is back" true in the default layout
  only. In plex-tv a refused placement falls back to downloads with the library record
  kept (ruling (i)), with D121 in place too, so there syncs stop retrying once one
  download succeeds.
  *Evidence:* `grep -n 'func (w \*Worker) recordedFilesPresent' -A30 internal/scheduler/ondisk.go`
  · `grep -n 'os.MkdirAll(root' internal/scheduler/library.go`
  · `grep -n 'keepOrSQL(StatusSkipped)\|const endDownloadSQL' -A2 internal/database/downloads.go`
  · `go test -count=1 -run 'WithTheLibraryUnplugged' ./internal/scheduler/`

- **D65 · No startup backfill of legacy library rows (decided 2026-09-23, this branch).** A
  review suggested turning every plex-tv row moved before the library record existed
  (`library_entries` NULL) into a record once, at startup, so the runtime could be
  record-only. Not done, for three reasons. The name fallback cannot *prove* ownership, so
  a backfill would turn every legacy guess into a record at once, for lessons nobody
  touched, and every later delete and move would trust it as proof (owner ruling #66 says
  files are known by record). Today a guess becomes a record only when an action on that
  lesson already treats it as the lesson's: a re-download whose move was refused keeps
  the previous entries it matched, and a delete that failed partway keeps the matched
  entries it could not remove. Ambiguous rows would still need the name fallback at
  runtime, so no code would go. And a backfill run while the library is not mounted (a
  TrueNAS share still coming up) would record nothing, or the wrong thing, for good.
  Revisit if the legacy fallback ever has to grow again.

- **D35 · The Docker image is linux/amd64 only.** Decided on 2026-06-01 (`82baf22`, v0.2.1)
  after building arm64 under emulation hung a release for about an hour. The image step went
  from about 15 minutes to about 90 seconds. The release binaries still cover five targets
  (linux amd64/arm64, darwin amd64/arm64, windows amd64). One side effect: the v0.2.0 tag has
  binaries but no image.
  *Evidence:* `grep -n platforms .github/workflows/*.yml` · `.goreleaser.yaml` ·
  *Detail:* `musora-downloader-project.md`.

- **D36 · Every merge to main cuts a release, even a docs or chore merge.** This is deliberate,
  following SpenDrop's pattern. `main.yml` tags every push to main, and `default_bump: patch`
  means any commit type that isn't feat or fix still bumps the patch version. Each release
  rebuilds the image, which also refreshes yt-dlp (D3). Worth remembering: merging a
  docs-only PR ships a new version.
  *Evidence:* `grep -n default_bump .github/workflows/main.yml` · `git tag` (v0.2.1 and v0.6.2
  came from `ci:` commits) · *Detail:* `project_drumdrop_release_pipeline_flow.md`.

- **D37 · CI cross-compiles every release target on every PR.** The `goreleaser-check` job
  runs a full snapshot release (all five targets, with the embedded UI) on each PR. It's
  slow, but it's the only check that catches a break on another OS before a release does.
  *Evidence:* `grep -n goreleaser .github/workflows/ci.yml` ·
  *Detail:* `project_drumdrop_release_pipeline_flow.md`, `cross-compile-release-targets.md`.

- **D38 · The library is a move, not a hardlink, and Plex owns the path.** This is the owner's
  ruling of 2026-06-01 ("Plex owns the path not drumdrop"). A finished lesson is moved into
  the library, so it exists in exactly one place. v0.3.0 (#12) replaced v0.1.0's hardlink mirror,
  which made the files look duplicated. drumdrop deliberately doesn't map host library paths.
  Don't re-propose hardlinks.
  *Detail:* vault note drumdrop-plex-library; `musora-downloader-project.md`.

- **D39 · Songs download both mixes, as two versions of one episode.** This was an owner call
  (#18). Each song saves an `[Original]` and a `[Drumless]` file. In the plex-tv layout they
  share one episode name, so Plex shows a single episode with two versions.
  *Detail:* `drumdrop-song-soundslice-video.md`; vault note drumdrop-plex-library.

- **D40 · Audio language is one global setting.** This was an owner call (#15). drumdrop
  prefers a single English track (or the untagged original), controlled only by
  `DRUMDROP_AUDIO_LANG`. There's no per-follow setting, database field or UI control, the
  same shape as `DRUMDROP_LAYOUT`.
  *Detail:* `musora-downloader-project.md`.

- **D41 · No "in library" badge in the UI.** This was an owner call (2026-06-01). The lesson
  status model stays as it is (downloaded, skipped, …), and the UI doesn't mark which lessons
  are in the library. It's kept simple on purpose.
  *Detail:* `musora-downloader-project.md`.

## Deferred ideas

- **D42 · Re-download at a higher quality after editing a follow.** Editing a follow's
  quality only affects future downloads, so lessons already downloaded keep their resolution.
  This was out of scope in #11.
- **D43 · Edit a follow's identity.** A follow's kind, id and slug can't be changed. Unfollow
  and follow again instead. (Out of scope in #11.)
- **D44 · Library extras.** Trigger a Plex scan through the Plex API (today drumdrop relies on
  Plex noticing the change on its own), and add a `--library` CLI flag. The library is
  configured only by environment variable today.
- **D45 · Migrate an existing library to the plex-tv layout.** The layout (#13) and the
  `<episodedetails>` NFOs (#14) apply only to new downloads. Files already in the library
  keep the old folders and `<movie>` NFOs. A migration should also account for a historical
  gap: from v0.4.0 until v0.7.0, the plex-tv move skipped each lesson's `resources/` and
  `play-along/` folders and then deleted them along with the scratch folder, so plex-tv
  lessons downloaded under v0.4.0–v0.6.x reached the library without them. #18 fixed the
  move. Confirmed in the code at `9528ad9` (#13) and `6f00851` (#14):
  `git show 6f00851:internal/scheduler/library.go | grep -n 'ignore any nested dir'`. Whether
  any lesson in the owner's library is affected is unknown; re-downloading a lesson restores
  its folders.
- **D46 · `daemon --dry-run`.** Only `sync` has a dry run.
- **D47 · Read the permission ids from Musora.** Derive them from
  `GET /content/user/permissions` instead of the `DRUMDROP_PERMISSION_IDS` default of `92`.
  Nobody has captured that endpoint's response shape yet.
- **D48 · YouTube cookies.** If a song log ever shows "Sign in to confirm you're not a bot",
  that's YouTube's IP bot-detection. It's a different thing from the challenge in D3, and it
  needs a yt-dlp `--cookies` file. It hasn't been seen from the home IP, and it isn't
  supported yet.

*Detail for D42–D48:* `musora-downloader-project.md`; D48 also
`drumdrop-song-soundslice-video.md`.

- **D111 · Carry an old-title folder's extra files into the new one.** Since round 5 a
  previous folder the re-download didn't fully bring back stays where it was, untracked,
  for the owner to delete by hand (ruling (e), D113). Moving its extra files into the new
  folder automatically needs the default layout to record each file first; today it
  records only the folder (`output_dir`). The ruling names this as "later". Related: D58,
  D93, D106.

## Recently shipped

- **D153 · Plex shows and episodes get artwork Plex reads.** Branch `feat/plex-show-artwork`
  (owner rulings #78, 1–5).
  - *Was:* in plex-tv, drumdrop wrote nothing at show level, and named each episode's image
    `<episode>-poster.jpg`, which Plex does not read. A song's one `<episode>.nfo` and image
    matched neither version video, so Plex showed a song with no date and no cover.
  - *Now:* each show folder gets `tvshow.nfo`, `poster.jpg` and (courses) `fanart.jpg`,
    created only when missing, never replaced, never recorded; a new show gets them before
    its first video. Each episode's image is `<episode>.jpg`, and a song has one `.jpg` and
    one `.nfo` per version video; all are placed before the video. Files placed by earlier
    versions are renamed once, at the end of a cycle, by copy, record, then removal, so a
    crash or an unmounted library never loses a file; a lesson moved before the record
    existed is recorded by it (D65's exception). The README's *Plex TV layout* has the Plex
    setup that works (*Plex TV Series* scanner, *Plex NFO Series* agent, local assets and
    local metadata on) and the one-time "Plex Dance" for shows already in Plex.
  - *Residuals:* a show's own files stay after its last lesson is deleted (D154) and are
    never refreshed (D155); a hand-placed `<episode>.jpg` is replaced by a re-download
    (D156). A `tvshow.nfo` the owner wrote by hand marks the show done, so drumdrop adds no
    poster or background to it. A song retitled from "X [Y]" to "X" at the same episode
    number keeps its old title's per-version files, recorded (a later delete removes them).
    A song's subtitles keep one unsuffixed name, which may match neither version in Plex.
  - *Evidence:* `internal/scheduler/showfiles.go`, `internal/scheduler/episodefiles.go`,
    `episodeNames` in `internal/scheduler/plexmove.go`, `library.VersionEntry`

- **D18 · Test coverage reaches SonarQube.** Branch `fix/sonar-gate-and-coverage`.
  - *Was:* there was no `make coverage` target, so every scan reported 0%. v0.8.0's scan of
    `main`, the first with new code since v0.7.1, failed the gate's new-code coverage condition
    (0 against 80), and would have failed it on every release.
  - *Now:* `make coverage` writes three reports: the Go cover profile (`coverage.out`), the
    `go test -json` stream (`go-test-report.json`) and vitest's v8 lcov
    (`web/coverage/lcov.info`). `sonar-scan` runs it before each upload, and a failing test
    fails it. `sonar-project.properties` names the three files and excludes nothing from
    coverage: the owner's ruling on 2026-09-25 (decisions #75) was *"no work around sonarqube
    find fixes instead"*. So the code that was hard to test was made testable:
    - `main` hands the command line to `run(args, stdout, stderr)`, which the tests drive;
      only `main()`'s own `os.Exit` line is left unrun;
    - the Musora test fake (`internal/musora/musoratest`) has tests of its own;
    - the vitest setup moved into `web/src/test/jsdom-shims.ts` and `msw.ts`, which coverage
      sees (it leaves setup files out), and installs the same stand-ins on every Node (D30).

    `web/src/main.tsx` and `web/vite.config.ts` have no tests and stay uncovered. lcov
    doesn't list `vite.config.ts` (coverage takes `src/**`); Sonar counted it at 0 of 2 lines
    in a branch scan on 2026-09-25. It runs on vitest 2.1 (see D17 for the pin).
  - *Measured:* at `9efb083`, the source lines this branch changed are 171/173 lines and 32/32
    branch conditions covered (99.0%), not counting `web/vite.config.ts` and
    `web/src/test/setup.ts`, which lcov does not list. The gate's own figure comes from the
    next release's scan of `main` (D19).
  - *Evidence:* `make coverage` · `grep -n 'reportPaths\|exclusions' sonar-project.properties`
    (no `coverage.exclusions`)
- **D30 · The findings in `web/src/test/` are fixed, not excluded.** Branch
  `fix/sonar-gate-and-coverage`. The owner's ruling on 2026-09-25 (decisions #75): *"no work
  around sonarqube find fixes instead"*.
  - *Was:* `msw.ts` and `setup.ts` carried open findings (4 at v0.8.0), and the recommended
    option was to add `web/src/test/**` to `sonar.coverage.exclusions`.
  - *Now:* the findings are fixed, and the folder is analysed and covered like any other
    source. `setup.ts` only imports: jest-dom's matchers, then `jsdom-shims.ts` and `msw.ts`,
    each of which has its own test file. Every stub installs unconditionally, so the tests (and the coverage) are
    the same on CI's Node 20 and on a newer local Node.
  - *Evidence:* a branch scan lists nothing in `web/src/test/`; `sonar-issues --all` reports
    `main`, so it lists the 4 until the release's scan · `ls web/src/test`
- **D148 · Border colour classes paint.** Branch `fix/sonar-gate-and-coverage`, found by its
  browser pass on 2026-09-25.
  - *Was:* `web/src/index.css` set `* { border-color: var(--color-border) }` outside any
    cascade layer, since web/src's first commit (PR #6), so no release ever painted these. A
    rule outside a layer outranks every layered Tailwind utility whatever its specificity, so
    no `border-*` colour class painted: a focused control's amber
    border (the outline one restored on this branch included), a checked checkbox's primary, a
    status badge's tone, a `border-transparent` badge. Class tests passed throughout, since
    jsdom builds no CSS.
  - *Now:* the rule sits in `@layer base`, as shadcn's Tailwind v4 setup has it. Measured in
    the browser: a focused button, input or checkbox and a checked checkbox draw the ring
    colour; StatusBadge's border takes its tone (emerald at 30% for done); the Lessons filter
    badge's border is transparent. Tab strips lose the grey frame every inactive tab had, so
    only the active tab is framed (Lessons, Queue, Add follow). A focused tab then kept a grey
    border inside its amber outline: Radix Tabs activate on focus, and the active tab's
    `dark:data-[state=active]:border-input` beat the focus border. `tabs.tsx` now takes
    `dark:focus-visible:border-ring` as important, because Tailwind emits the plain class
    before the active one (the plain class is what fixed the outline button). `aria-invalid:border-destructive`
    now paints on inputs, selects and checkboxes; an outline button in the dark theme still
    loses it to `dark:border-input` (same specificity, later in the CSS), the collision the
    focus border had. No page sets `aria-invalid`, so nothing shows it today.
  - *Evidence:* the `the default border colour` tests in `web/src/design-tokens.test.ts` ·
    in a browser, `getComputedStyle(el).borderTopColor` on a focused outline button
- **D19 · `main` rescanned at v0.8.0.** No PR, since a scan writes only to the Sonar server. The
  owner's OK on 2026-09-25: *"Yes, right after the merge"*.
  - *Was:* one analysis, of `1c2dbda` (v0.7.1) on 2026-08-15: 67 open, under older rule sets.
  - *Now:* a second analysis, of `344ee1f` = v0.8.0: 43 open, gate ERROR. New-code coverage was
    0 (D18), and five older issues sit on lines PR #21 changed (D12). The next release's scan
    is the check that `fix/sonar-gate-and-coverage` turns it green.
  - *Evidence:* the header of `sonar-issues --all` · vault note drumdrop-sonarqube
- **D28 · The tracked git hooks are back on.** Local git config only, so there is no PR. The
  owner asked on 2026-09-23: *"drumdrop: turn its Git hooks back on."*
  - *Was:* `core.hooksPath` pointed at `.git/hooks`, which holds only the `*.sample` files, so
    neither tracked hook ran.
  - *Now:* `make hooks` set `core.hooksPath` to `scripts/hooks`. `commit-msg` rejects a
    subject that isn't a conventional commit, and `pre-push` checks the PR title. This is
    per clone: a fresh clone needs `make hooks` again. MusicDrop and SpenDrop still point
    `core.hooksPath` at `.git/hooks`; their own sessions decide that.
  - *Evidence:* `git config --show-origin --get core.hooksPath` (`file:.git/config
    scripts/hooks`) · `printf 'bad subject\n' > /tmp/m && git hook run commit-msg -- /tmp/m`
    (exits 1 with the bypass hint).
- **D113 · Round 5: what the review seats found, and the owner's rulings (a)–(y).** This
  branch (`fix-library-delete-and-move`), PR #21.
  - *Was:* at `8ee019d` the round-5 seats (code, security, UI), and the seats on each fix
    round after it, found:
    - a re-download set a lesson's subfolders (`resources/`, `play-along/`,
      `sheet-music/`) aside whole, so a PDF whose re-fetch failed, or a file the owner had
      put there, was deleted (`main` kept them when no library is set);
    - a previous folder at another place (a title change, a library added later) was
      deleted whole, owner files included (`main` never removed a folder under an old
      title);
    - when the library placement failed, the fallback to downloads replaced the lesson's
      library folder whole;
    - a failed re-download marked the lesson failed, and the planner queues a failed
      lesson again every cycle, so a library placement refused for good cost 3 full
      downloads per cycle, indefinitely;
    - a plex-tv re-download at the same title deleted the recorded captions or poster it
      didn't bring back (as `main` did), and dropped a recorded resources folder it didn't
      bring back from the record;
    - `daemon --once` had no `Ctrl-C` handling (D95 (d)); a Cancel during a retry's
      backoff waited for the backoff to end; a follow that couldn't be read was taken for
      a missing one, so the download was named, and placed, by defaults;
    - `openRealDir` could open a folder swapped in between its check and its open, and a
      placement by rename flushed nothing before the download was recorded;
    - an instructor typed as the preview shows it (`@jared-falk`), or a brand as the UI
      shows it (`Pianote`), was refused, and a node follow refused a capitalised brand;
    - in the web UI: the toast's × was sonner's corner badge, and the toast's focus rings
      lost to sonner's own CSS; paired buttons were 8px apart (Follows' Edit and Remove,
      0px); Add follow's filled button was the disabled Add (UI N6, P7); Pause, Resume,
      Run sync, Dry-run and Connect dropped keyboard focus while they ran; a job or lesson
      removed or ended elsewhere read as a failure; a count of one said "1 lessons"; and
      Queue's toasts said "job" where Lessons said "download".
  - *Now:* the owner's rulings of 2026-09-24 (vault decisions #72, (a)–(y), in four
    addenda) and the seats' corrected lines, each fix round reviewed by the seats again:
    - (a) a re-download merges the lesson's subfolders file by file, at every depth, by
      the top level's rule (#66): a file at a path the download produced is replaced and
      logged, and every other file stays. A file where the download places a folder, or
      the reverse, is replaced whole.
    - (e) a previous folder at another place goes only when the download brought back
      every file in it; otherwise it stays, no longer recorded, and the log says why
      (`⚠ … left its previous folder …`), including the two cases that used to stay
      without a word (another lesson records files in it; it is outside both roots). A
      plex-tv lesson's recorded files at an old title's names still go (#66; D106).
    - (f) and (i), read by their reason since round 5d: a lesson whose library placement
      fails falls back to downloads only when that neither deletes nor stops recording a
      library file the lesson owns (`keptInLibrary`). One whose row records its own
      folder in the library (in either layout: a plex-tv install can still record a
      folder the default layout placed), or a legacy season folder whose entries the move
      couldn't name, is not placed in downloads: the attempt fails (`errKeptInLibrary`),
      and its library copy stays recorded and as it was. Any other lesson falls back; in
      plex-tv its recorded library entries stay recorded. (Round 5 had keyed this on
      today's layout, which let a plex-tv fallback stop recording a default-layout folder
      or a legacy row's season files.) Since round 5e a lesson whose recorded folder is the
      downloads folder it would fall back to (the library is the downloads dir, or holds
      it) falls back: that replaces only its own files at the names the download brings
      back (`recordsFolder`, the own-folder test `placeLessonFolder` uses; since round 5f
      a symlink at that name doesn't count). Since round 5f, with the downloads dir inside
      the library, a folder in the downloads course folder the fallback uses isn't in the
      library at all (`inLibrary`), so a lesson kept there falls back after a title change
      too.
    - (h) a failed download of a lesson that still records files leaves it `downloaded`
      with a note (`FailDownload`), and only the job fails; syncs don't retry it, and the
      Lessons page shows the note and offers *Download*. A lesson without files is still
      marked failed and retried next cycle. Rows an older build left `failed` with files
      are queued once more at the next cycle, then settle: a success clears the note, and
      a failure leaves them `downloaded` with it. No migration. Since round 5d only while
      the files are on disk, (o) below.
    - (m) *Download*, *Retry* and *Un-skip*, and adding a follow, start a sync at once
      through the daemon's existing kick channel (`Server.kick`, a non-blocking send):
      a press never waits, presses during a sync share one more cycle, a missing daemon
      makes it a no-op, and a paused daemon drops it (*Resume* kicks). *Download* kicks
      even when the lesson is already queued. Un-skip of a lesson that wasn't skipped,
      adding a follow that exists, and a refused press start none.
    - (n) a lesson Musora doesn't return (locked or removed) whose earlier download is
      on disk stays `downloaded` with its own note (`msgNotReturnedKept`); only the job
      fails, reported as a failed attempt, and syncs leave the lesson alone
      (`NotReturnedDownload`, which replaces `SkipDownload`; all five `scheduler.Store`
      implementers changed). A lesson without files is still skipped.
    - (o) an attempt that recorded nothing (failed, not returned, canceled) leaves a
      lesson `downloaded` only if the files it records are on disk at that moment: the
      worker checks the recorded video, or else every plex-tv record entry, or else the
      folder, from a fresh read of the row (`filesOnDisk`), and the store decides in the
      transaction that ends the job (`keepsFilesSQL`). Anything that can't be read counts
      as missing, so the lesson fails and is retried; a canceled one is skipped with the
      stopped note. The cost with an unplugged drive is D120. A delete, skip or follow
      removal still leaves a lesson with files `downloaded`.
    - (j) a plex-tv re-download at the same title keeps, still recorded, the captions,
      poster or resources folder it didn't bring back, so a later delete removes them.
    - (b) and (k): paired buttons stand 12px apart, in dialog footers and page rows (an
      offset ring reaches 5px out). (c) Add follow fills the next step's button: Preview
      until a preview shows, then Add (this settles UI N6 and P7). (d) The toast's × sits
      inside its top-right corner, level with the title, with the amber ring (Tailwind's
      `!` suffix beats sonner's unlayered CSS). (g) Every filled control sets its focus
      ring 2px off its fill: amber, red and grey buttons, the active sidebar link and a
      checked checkbox (the grey buttons and the checkbox are the implementer's reading of
      "every filled control", not yet confirmed by the owner). (l) Pause and Resume stay
      compact: the spinner takes the icon's place and the label stays; Run sync and
      Dry-run do the same.
    - (q) while a lesson downloads, its row menu puts *Copy path* first and *Cancel
      download* after it, with *Delete* still last, so a menu that changes under the
      cursor when the download starts lands on a harmless item (round-5e UI review, Low
      A: Enter on a highlighted *Download* became *Cancel download*, and the lesson ended
      skipped). (r) A sync stamps a lesson's updated_at only when something about it
      changed (`UpsertLesson` updates only when the title, the parent or a missing
      position changes), so the All tab, ordered by updated_at, no longer reshuffles
      every lesson a sync lists (round-5e UI review, Low C); D130 records the writes
      that still stamp without a change. (s) The Add follow dialog
      says that adding starts the lessons downloading right away (round-5d UI review,
      Info 2). (t) Below 1280px the Lessons table hides its Brand and Quality columns,
      header and cells, and a row note keeps 22rem and wraps to three lines (refined
      the same day, after 28rem measured 85px too wide); from 1280px the note keeps
      28rem and two lines, and the table is as it was. So at 1024px, with short titles,
      the table fits with no note cut and no sideways scroll; a long title still scrolls
      it (D131 has the measurements). (u) A
      pointer resting on a skipped lesson's *Download* that clicks just as the download
      starts presses *Cancel download*: recorded, not fixed (D129). Superseded by (v).
    - (v) When a lesson starts or stops downloading while its ⋯ menu is open, the menu
      closes, so nothing can be clicked or chosen on a menu whose items just changed:
      the pointer and the keyboard, at the start and when an attempt fails or ends
      (D129). (w) Scroll anchoring is off on the lessons list: when a started download
      moves to the top of All, the reader's view stays still, and the rows below it
      shift by one row, with no jump.
    - (x) With a lesson's ⋯ menu open, (v)'s focus return wins over (w): when the lesson
      starts or stops downloading and moves to the top of All, the page scrolls to it and
      shows its ⋯ ringed (D136). (y) When a lesson's files stayed behind in the old
      library folder after the library setting was pointed at another folder, a *Delete*
      (of the lesson, or of a follow with its files) and the fallback to downloads refuse,
      with the fix in the message: move the files to the new library folder, or set the
      setting back (D137).
    - Corrected lines: `daemon --once` stops its download on `Ctrl-C` or SIGTERM (D95
      (d)); a Cancel during a backoff lands at once; a follow that can't be read fails the
      job before downloading (`failNotStarted`); `openRealDir` refuses a folder swapped
      while it opened it (`os.SameFile`); a placement flushes what it placed and renamed,
      and the parent of a lesson folder it made, before the record; an instructor takes
      one leading `@`, and a brand in any case, for node follows too (`musora.NodeBrand`);
      Pause, Resume, Run sync, Dry-run and Connect keep keyboard focus while they run
      (`PendingButton`); a job or lesson removed or ended elsewhere reads as a neutral
      note that goes by itself; a count of one says "lesson"; Queue's toasts speak of
      downloads and name the lesson; the server's round-5 sentences put the outcome first
      (`263b65a`); the dead `ErrDiscardDownload` is gone; the CLI sync tests write into
      their own temporary folder. The README says what changed.
    - Round 5d's corrected lines: a downloaded row records the video a resources-only
      re-download keeps, in both layouts (`keptVideo`, `lessonVideo`); the casefold
      tests read the child's exit status before its output, and take only a skip of
      the test itself for a skip (`judgeChild`); the bad-brand answer names the brands
      as the UI does. New pins: a recorded entry missing from disk, a season folder that
      can't be listed (`listSeason`), the kept note a not-started download leaves, the
      previous-folder log under plex-tv, and (j) for a legacy row. `msgStopped` keeps its
      "Stopped:" prefix, shared with `msgRequeued` and `msgShutdown`.
    - Round 5e's corrected lines: a refused library placement whose downloads fallback is
      the lesson's own recorded folder falls back again (security F3, a regression from
      5d); the disk check counts a recorded entry only as a regular file or a folder, read
      through a symlink as the video is, so a dangling symlink at a recorded name is gone
      (security I2); *Un-skip* answers 409 with the being-deleted sentence while a delete
      holds the lesson, and starts no sync (`UnskipLesson`, security I5); the (n) note
      says "The lesson may be locked…", so "It" no longer reads as the kept download (UI
      Low 1); a lesson and its job read as downloading as soon as the download starts, not
      when it ends: `download_started` refreshes lessons, jobs and the summary (UI Medium
      1, `5cb8fbe`); Follows' actions cell shows no hand cursor where a click does nothing
      (UI Low 2, `b6f3455`); `plexEpisodeBase`'s comment says what reads the move's base
      now. New pins: `keptVideo`'s filter and its order, an empty record, and a plex-tv
      row that records a lesson folder and a season-folder record (code L1, L2). D120 is
      corrected (security F1), and D121–D125 are recorded. (The UI findings are the
      round-5d UI review's.)
    - Round 5f's corrected lines (from the round-5e reviews): the own-folder test reads the
      fallback's name as the placement does (`recordsFolder`, `Lstat`), so a symlink in
      downloads that leads to the lesson's library folder no longer lets a refused
      placement fall back and leave that folder recorded by no lesson (security S1, code
      L1, a regression from 5e); `previousFolder` uses the same test, so without a
      library the folder such a symlink led to is kept and logged as a previous folder
      (D58 (c)); with the downloads folder inside the library, a lesson an earlier refused
      move kept in the downloads course folder falls back when its title changed, while
      any other folder there, and every folder when the library is the downloads folder,
      still counts as the library's (`inLibrary`, security S3; D128 records what it still
      refuses); a sync stamps updated_at only for a lesson it changed (ruling (r)); the
      README's unplugged-drive paragraph and D120 say what plex-tv does (security S2), and
      the README says a lesson kept in the library-equals-downloads folder always falls
      back there, not that it is always placed (code I1). New pins: a symlinked downloads
      root and a deleted recorded folder still fall back (security S4, code L2), a tie
      counts as the library for a legacy season row. The web half: UI Low A is fixed by
      (q)'s reorder together with a key on every menu item (`08df44c`): the reorder alone
      didn't stop React reusing the highlighted *Download* node as *Cancel download*.
      UI Low D is fixed by the note's 28rem minimum width, so a page of short titles no
      longer cuts it (`dbe6ace`), and then by (t): below 1280px the table hides Brand
      and Quality (header and cells), and the note keeps 22rem on three lines instead,
      so the table fits a 1024px window (`7a387e3` and the refinement after it; D131).
      Round-5d Info 2 is fixed by the Add
      follow line (`2566e5f`), whose wording goes beyond (s) to stay true in every
      state. Round 5h reworded it (`909f7fb`, UI finding 5), because the first version
      assumed a sync was running: "Its lessons start downloading right away, or after
      any sync already running.", and while syncing is paused, "Syncing is paused: its lessons start
      downloading when you Resume." UI Low C is settled by (r) during planning: a lesson
      whose download starts still changes (its status), so it still moves to the top of
      All then; whether the page still jumps at that moment is for the browser pass (it
      does: round 5h, below). D125
      takes the security seat's empty-folder residual; D101 takes UI Low B; D126–D128 are
      recorded, and D129–D131 after the web half (D131 since settled).
    - Round 5h's corrected lines (from the round-5f/5g reviews): a season folder in the
      course folder the downloads fallback goes into counts as the library's, since the
      fallback only writes `NN - Title` folders (`inLibrary`,
      `internal/scheduler/worker_record.go:358`; security N1, a regression from 5f:
      after the library root moved up from the downloads folder, a legacy season row fell
      back and its season files were claimed by no lesson); `inLibrary`'s comment states
      its rule and where it differs from `previousFolder`'s longest root (code I4); a
      lesson two follows list is no longer stamped by every sync: only the follow it is
      attributed to writes its parent (`UpsertLesson`, code L1, security N4; D130). New
      pins: the downloads-inside-library rule at the plex-tv call site of `keptInLibrary`
      (code L2, mutant MF), and a regular file at the recorded folder's name, which is not
      the lesson's folder (code I3, mutant MC). D128 now lists the losses the path rule
      still allows (security N2, B3) and the mirror refusal (code I5); D58 records that
      ruling (f)'s refusal doesn't see a library folder spelled another way (security
      N3). The web half builds rulings (v) and (w): (v) settles D129 and code I1 (the
      reverse flip, *Cancel download* to *Download* on a failed attempt, where Enter
      pressed *Download*); (w) settles UI finding 2, the page jump when a download
      starts: with lessons that share a stamp, listed by id, a reader holding the view
      at 1500px was moved 18 times in 15s, once per start, whenever the started lesson
      was the row Chrome anchored to (the top visible row). That explanation was too
      narrow: with anchoring on, a lesson mid-view that started also pulled the view to
      it (2270→0; round-5h UI review, finding 4), and (w) fixes that too. UI findings 3,
      4 and 6 are recorded for the owner as D132–D134.
    - Round 5i's corrected lines (from the round-5h reviews). The Go half:
      `keptInLibrary` counts an empty answer from the move as none learned, so a legacy
      episode with a plain name is kept after the library moved up, not only a song
      version (security F1, `d27a892`; D128 has the trade). Ruling (y) is built
      (security F2, `56ab315`): `library.Claims.LeftBehind` reports a season-folder row
      whose recorded folder is still there and is not the folder it is read as now (by
      path or `os.SameFile`); then a lesson delete and a follow delete with files answer
      409 and remove nothing, and a refused placement fails with `failLeftBehind`
      instead of falling back, moved-down libraries included (D137). The file-gate test
      says it checks the default layout by the failure kind alone (code Info 3), and
      `UpsertLesson` says "one value per sync" and that a row predating `follow_id` has
      a frozen parent (code Info 4, Info 5; `e5156ac`). New pins: `TestLeftBehind`,
      `TestWorkerRefusedPlacementKeepsFilesLeftBehindByALibraryMove` (the seat's M, N,
      D, C and R cases, plus a setting spelled through a symlink),
      `TestDeleteLessonRefusesFilesLeftBehindByALibraryMove`,
      `TestDeleteFollowRefusesFilesLeftBehindByALibraryMove`,
      `TestWorkerPlexTvRefusedMoveKeepsALegacyEpisodeTheMoveLooksForElsewhere` and
      `TestKeptInLibraryDecidesByWhatTheRowRecords` (which also keeps round 5h's
      course-folder rule pinned, now that a moved library is refused first). The web
      half (`6e2d66e`, `083a3d6`): the no-fade test now pins *when* the class arrives
      (code Low 1): the menu must be gone in the commit that closes it, after a start and
      after an end, because Radix Presence reads the animation once, in that render
      (`web/node_modules/@radix-ui/react-presence/dist/index.mjs:59-66`); with the flag set
      from an effect the menu stayed on screen and clickable, and a click cancelled. The
      RowMenu comment says what the render-time reset buys, and the `overflow-anchor`
      comment is widened (a lesson starting mid-view pulled the view too, 2270→0) and
      records (x); the (w) and (t) tests each carry their own comment again (code Info 2).
    - Round 5j's corrected lines (from the round-5i reviews; `05f5b92`, `1af417b`, `f505a41`
      and the docs commit after them). `Claims.LeftBehind` asks for the
      lesson's own files: an old season folder the files were moved out of (emptied, or
      holding only a sibling's episode) no longer refuses, and a partial move or a copy
      in both still does (UI 1b, security S1, code Low 4); it compares paths before any
      stat (security S4), takes `ENOTDIR` for gone (S3b), and puts the error of a folder
      it can't read in the log (S3a), which the delete answers with the fixed 500 and
      the fallback with `failKeptInLibrary`. A delete refused for that, or because which
      files are whose can't be read, is refused before `Begin…Delete`: no download
      stopped, no queued job dropped, no intent written (security S5; UI 6); the check
      after Begin stays, pinned by making the change while the delete stops the
      download. The refusal says "to the same place in the new one" (UI 1a) and puts the
      cause before the fix (UI 2); the delete dialogs offer "set it back if you moved
      nothing", and the note and the job sentence offer only the move, which fits the
      row (D138). The no-claims sentences now say "which files belong to which lesson",
      true for a damaged record, a store error and an unreadable old folder alike. The
      fail-closed arm is pinned (security S2, code Low 3), the follow test seeds the
      left-behind lesson last (code Low 2), `UnskipLesson`'s comment no longer names the
      removed `updateStatus` (code Info 6), and README scopes the promises to lessons
      filed in a season folder and names what drumdrop can't see (code Medium 1, Info 8,
      security S6). G1 stays as a second line (code Info 5). Still open: D137, D138,
      D139.
    - Round 5k's corrected lines (from the round-5j reviews; `9a220bf`, `e9204c7`,
      `b6b0382`, `d083db6`, `0ecc1a8`, `01721cf`, `f50d64a` and the docs commit after
      them). The delete dialogs
      offer switching back only "if the new one is still empty": "if you moved nothing"
      was true after a copy kept in both folders, and switching back then left the new
      copy recorded by nothing (security S1, UI 1 and 3). The note and the job sentence
      state the failed placement and the old folder as two facts, since the placement
      failed for a reason only the log has and the old folder is why it didn't fall back
      (UI 2). `ownFilesIn` is split (`videoIn`, `legacyOwnFileIn`, `anyBaseClaims`,
      `anyExists`; gocognit 20 → 7, code Low 5), and `legacyClaims` shares its
      any-base-claims loop. Two fail-safe arms are pinned: a recorded season folder that
      is now a regular file reads as gone, and the video counts only inside the old
      folder (code Info 7, security S4). `refuseFallback`'s comment says its second line
      never refuses outside today's library (code Low 1), the follow test's comment names
      the test that pins "before the first removal" (code Low 4), and `errNoClaims`' text
      covers everything it stands for (code Info 8). README: the setting's table row
      points at the warning, "the same place" has an example, switching back and a copy
      are spelled out, a Docker bind re-pointed on the host has its own bullet, and the
      safe order is stated (UI 4, security S1). BACKLOG: D137 gains the emptied-folder
      fallback (code Low 1), the hung stat as it is now (code Low 2, security S3), the
      Windows code (code Info 6), the identity a remembered root needs (security S2),
      the placement-time option and the backstop's sentence (UI 7); D58 and D128's line
      numbers are re-derived (code Low 3); D138 and D139 say where they stand (UI 5, 6);
      D140 and D141 are new (UI 8, 9). After the round-5k code seat: an unsettled
      legacy episode name counts a file under every candidate name, pinned where
      `LeftBehind` and `Claimants` read it (`anyBaseClaims`; mutants trying only the
      first or only the last name had survived since before round 5k; code 5k Low 1).
    - Round 5l cleared the SonarQube findings new on the branch against `main` (68 new
      → 0 new; the branch scan at `7090ad0` has 43 open, every one also on `main`), in
      14 commits: `370ac34`, `528fa23`, `6f4b796`, `8f5d1d7`, `9d4282e`, `4f7361e`,
      `e37130b`, `7a39778`, `0df2d70`, `61cd192`, `9781401`, `a77c6b9`, `7f178ff` and
      `7090ad0`. It split long functions and tests into single-purpose steps and
      named checks, and `8f5d1d7` pins a nested root, an alias of a root and a folder
      named like an nfo. Both seats found the behaviour preserved: the code seat by
      reading each split against the base and 22 mutants, the security seat by 15,386
      worker and 380 delete scenarios giving identical results in `1a03e16` and
      `7090ad0` (JSON timestamps aside), and by running each side's tests over the other side's code. Their
      findings were test gaps that predate the round, each a line the code already
      gets right, pinned in round 5m (`142e87f`, `b4de5b2`, `e4cd069` and the docs
      commit after them): a placement that fails while clearing names takes back a
      folder it merged, in both layouts (code W1); an empty record `[]` owns nothing
      (W2); `library.Remove` goes by the root a path is written inside before any
      identity lookup, so a symlink out of downloads into the library is refused (W3);
      a recorded row claims only what it records, legacy matching or not (S1); and
      `TestPlacementReleasesItsFolders` counts only the descriptors open in its own
      temp folder, so another failed test's leak can't disturb it (security I2).
      D137's ambiguous-name trigger is measured now, not reasoned (security I1), and
      the line numbers the refactor moved are re-anchored in D58, D128, D130 and D137.
  - *Evidence:* `go test -count=1 -run 'MergesTheSubfolders|StopDuringAMerge|FailsAfterAMerge|PreviousFolder|LibraryPlacementFailure|RefusedLibraryPlacement|FailedReDownloadLeaves|FailedFirstDownloadFails|SameTitleReDownloadKeeps|PlexTvRefusedMoveKeepsThePreviousRecord|SpelledAnotherWay|LastAttemptsFailure|NewFolderFlushFails|ReleasesItsFolders|CancelDuringABackoff|OpenRealDir|NodeBrand|FollowNodeFoldsItsBrand|CreateNodeFollowFoldsTheBrand|InstructorInputIsNormalisedAlike|AFailedReDownloadKeepsTheLessonDownloaded' ./internal/scheduler/ ./internal/database/ ./internal/musora/ ./internal/server/ ./cmd/drumdrop/`
    · `cd web && npx vitest run src/button-rows.test.tsx src/components/ui/sonner.test.tsx src/design-tokens.test.ts src/pages/Lessons.test.tsx`
    · round 5d: `go test -count=1 -run 'RefusedMoveKeeps|RefusedMoveOfALegacyRow|RefusedPlacementOfASeasonFolderRow|APress|OnDisk|WhoseVideoIsGone|LibraryUnplugged|LessonMusoraDoesNotReturn|RecordedFilesPresent|NotOnDisk|CanNotBeListed|ResourcesOnlyReDownload|OfALegacyRow|JudgeCasefoldChild|BadBrandNames' ./internal/scheduler/ ./internal/server/`
    · round 5e: `go test -count=1 -run 'FallsBackIntoTheLessonsOwnFolder|RecordedFilesPresent|RecordsTheKeptVideo|KeepsAFolderTheDefaultLayoutPlaced|UnskipLessonRefusesWhileADeleteHoldsIt|WhileADeleteHoldsTheLesson' ./internal/scheduler/ ./internal/database/ ./internal/server/`
    and `cd web && npx vitest run src/lib/sse-reducer.test.ts src/pages/Queue.test.tsx src/pages/Follows.test.tsx src/pages/Lessons.test.tsx`
    · round 5f: `go test -count=1 -run 'IgnoresASymlinkToTheLibraryFolder|WithoutALibraryLeavesTheFolderASymlinkLedTo|OfALessonKeptInDownloads|KeepsALibraryFolderInsideTheDownloadsFolder|FallsBackIntoTheLessonsOwnFolder|KeepsALegacyEpisodeItCanNotName|StampsUpdatedAtOnlyOnAChange' ./internal/scheduler/ ./internal/database/`
    and `cd web && npx vitest run src/pages/Lessons.test.tsx src/pages/Follows.test.tsx -t 'start downloading|keeps its width|hides Brand and Quality'`
    (round 5h replaced the (q) flip test, "…starts downloading highlights Copy path…",
    with (v)'s tests below)
    · round 5h: `go test -count=1 -run 'KeepsALegacyEpisodeItCanNotName|RefusedPlacementOfASeasonFolderRow|OfALessonKeptInDownloads|DoesNotTakeAFileForTheLessonsFolder|StampsUpdatedAtOnlyOnAChange|DoesNotRestampALessonTwoFollowsList' ./internal/scheduler/ ./internal/database/`
    and `cd web && npx vitest run src/pages/Lessons.test.tsx src/pages/Follows.test.tsx -t 'under its open menu|stays closed when that attempt fails|no exit fade|stays open, its highlight|out of scroll anchoring|start downloading|reads as not paused'`
    (web commits `3e04844`, `e4a71a2`, `909f7fb`, `b040729`, `6bb675c`)
    · round 5i: `go test -count=1 -run 'LeftBehind|TheMoveLooksForElsewhere|KeptInLibraryDecidesByWhatTheRowRecords|KeepsALegacyEpisodeItCanNotName|RefusedPlacementOfASeasonFolderRow|DoesNotTakeAFileForTheLessonsFolder|MessagesFollowTheCopyRules' ./internal/library/ ./internal/scheduler/ ./internal/server/`
    and `cd web && npx vitest run src/pages/Lessons.test.tsx -t 'gone in the same commit|takes itself out of scroll anchoring'`
    · round 5j: `go test -count=1 -run 'LeftBehind|OwnFiles|OwnFileAlone|CantRead|StopNothing|ChecksAgain|WhileARecordIsDamaged|MessagesFollowTheCopyRules' ./internal/library/ ./internal/scheduler/ ./internal/server/`
    · round 5k: `go test -count=1 -v -run 'ReadsAPathThroughAFileAsGone|CountsTheVideoOnlyInTheOldFolder|LeftBehind|MessagesFollowTheCopyRules|TriesEveryNameOfAnUnsettledLegacyEpisode' ./internal/library/ ./internal/server/ ./internal/scheduler/ | grep -c -- '--- PASS'`
    and `~/go/bin/gocognit ./internal/library/ | grep -E 'ownFilesIn|legacyOwnFileIn|legacyClaims'`
    · round 5l: `go test -count=1 -v -run 'WhileClearingNamesTakesTheMergeBack|ByAnEmptyRecordOwnsNothing|GoesByTheRootAPathIsWrittenIn|ReadsARecordedRowByItsRecordAlone|ReleasesItsFolders|RefusesARootInsideAnotherRoot' ./internal/library/ ./internal/scheduler/ | grep -c -- '--- PASS'`
    (12) and `git -C /mnt/data/projects/Musora/drumdrop rev-list --count 1a03e16..7090ad0` (14)
  - *Left open:* D96–D112, found or recorded in round 5; D114–D119, recorded in round 5d
    (D114–D118 from the round-5c reviews, D119 found in the round-5d fix); D121–D125, from
    the round-5d reviews; D126–D128, from the round-5e reviews; D130, from the round-5f
    web half and its rulings; D132–D134, from the round-5f/5g UI review; D135–D137,
    from the round-5h fixes (D137 narrowed by rounds 5i and 5j to what ruling (y) leaves
    open); D138 and D139, from the round-5i UI review; D140 and D141, from the round-5j
    UI review;
    D95 (two processes on one database, and `--once`), D72 (merged subfolders are Windows
    swap points too), D82 (the follow dialogs' buttons move), D89 (the preview names a
    brand Add won't follow), D93 (the previous folders ruling (e) keeps).
- **D131 · At 1024px the Lessons table scrolled sideways.** This branch
  (`fix-library-delete-and-move`), PR #21. Settled by ruling (t), refined
  by the owner on 2026-09-24.
  - *Was:* ruling (t) hides Brand and Quality below 1280px so that at 1024px the table
    fits, nothing cut and no sideways scroll, with Title and note, Status, Size, Updated
    and ⋯ shown. With the note's 28rem minimum it didn't. Measured in Chromium, short
    titles only, the page's 15px scrollbar counted: those five columns needed 772px
    (Title 464, the note's 448 plus padding; Status 105; Size 77; Updated 74; ⋯ 52),
    and a 1024px window leaves the table 687px. It scrolled sideways by 85px: *Updated*
    was cut and ⋯ was out of view until you scrolled (70px over without a scrollbar
    gutter). It stopped scrolling from a 1109px window; before (t), from 1243px. The
    options measured at 1024px: hiding Size as well left 8px over, Updated as well 11px
    over, both fit; a 22rem note on three lines fit; 16px card padding instead of 24px
    didn't (772px in 703px); a collapsing sidebar wasn't measured.
  - *Now:* the owner chose the 22rem note. Below 1280px a row note keeps 22rem and
    wraps to three lines; from 1280px it keeps 28rem and two lines. Measured the same
    way at 1440, 1280, 1279, 1100, 1024 and 900px with short, mixed and one long title:
    at 1440 and 1280 every header's position and width, and the first rows' heights,
    match the build before (7a387e3, itself identical there to dbe6ace). With short
    titles the table fits from a 1013px window: at 1024px the note is 363px wide, on
    two or three lines, and the table takes its 687px exactly, every ⋯ in view. At
    900px it scrolls sideways by 113px, the note at its 352px minimum. No note is
    clamped at any width measured; the longest sentence the server writes takes three
    lines at 352px. One long title (91 characters) still scrolls the table, as before:
    935px of table at 1100, 1024 and 900px, 1070px in 943px at 1280px; it fits at
    1279px, where Brand and Quality go, and at 1440px.
  - *Evidence:* `grep -n 'WIDE_ONLY\|min-w-88' web/src/pages/Lessons.tsx` ·
    `cd web && npx vitest run src/pages/Lessons.test.tsx -t 'keeps its width|hides Brand and Quality'`
- **D129 · A click or a key meant for a lesson's row menu could press *Cancel download*
  or *Download* as the lesson started or stopped downloading.** This branch
  (`fix-library-delete-and-move`), PR #21. Settled by the owner's ruling
  (v), 2026-09-24, which supersedes (u); built by the web half of round 5h.
  - *Was:* the row menu is built from the live row, so its items change when a download
    starts or stops. Ruling (u) recorded one case, not fixed: a pointer resting on a
    skipped lesson's *Download* (*Un-skip*, *Download*, *Copy path*) that clicks just as
    the download starts presses *Cancel download*, now second. The round-5f/5g UI seat
    measured the pointer case wider (finding 1). The open menu moves with its row:
    Radix Popper re-anchors it on every layout change
    (`web/node_modules/@radix-ui/react-popper/dist/index.mjs:95-99`), and a click
    selects whatever item is under the pointer
    (`web/node_modules/@radix-ui/react-menu/dist/index.mjs:393`, `:398-400`). At 1280px
    on All, a failed lesson with a two-line note, the pointer resting on *Download*, the
    first item: at the claim the row grew from 71 to 133px and the menu slid from top
    444 to 475; at the start the row moved to the top of All and shrank to 99px, the
    menu went up to 405, and the resting pointer was over *Cancel download*. The click
    sent the cancel ("Download canceled"). So the suggested fix, *Cancel download* after
    a separator, would not have helped: the whole menu moves under the pointer. The
    keyboard had the reverse flip (round-5f/5g code review, I1): a downloading lesson's
    menu open from the keyboard, *Cancel download* highlighted, and the attempt fails;
    the menu becomes *Download*, *Skip*, *Copy path*, Radix moves focus to the first
    item (`web/node_modules/@radix-ui/react-roving-focus/dist/index.mjs:102-109`), and
    Enter queued a download. Ruling (q)'s order protected only the forward flip.
  - *Now:* ruling (v): when a lesson starts or stops downloading while its ⋯ menu is
    open, the menu closes, so nothing can be clicked or chosen on a menu whose items just
    changed. That covers the pointer and the keyboard, in both directions: the start,
    and an attempt that fails or ends. Built by the web half of round 5h: each row's
    menu is a small controlled `RowMenu` that remembers whether its lesson was
    downloading when it opened, and closes in the same render that brings the new
    items, so the changed menu is never shown open; focus goes back to ⋯, as Escape
    sends it (`3e04844`, `e4a71a2`). That close has no exit fade (`6bb675c`): Radix
    keeps a closing menu on screen for its fade, already showing the new items, and it
    still takes clicks. In headless Chromium, with only the first commit, a click on
    *Download*'s old spot during the fade still sent the cancel 3 times out of 3; with
    the no-fade close, 0 of 5. A close the reader makes keeps its fade. The class has to
    be on the menu in the render that closes it: set from an effect, the menu stayed on
    screen and clickable (round-5h code seat); round 5i's test pins that timing, after a
    start and after an end (`6e2d66e`). Cases (v) doesn't cover are D135.
  - *Evidence:* `grep -n 'function RowMenu' web/src/pages/Lessons.tsx` ·
    `cd web && npx vitest run src/pages/Lessons.test.tsx -t 'under its open menu|stays closed when that attempt fails|no exit fade|stays open, its highlight'`
- **D78 · A Skip was undone by a queued or running download.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* Skip only set the lesson's status, so its queued job was claimed and downloaded
    anyway, and a download already running recorded itself as downloaded: the lesson ended
    `downloaded` although the dialog promised "Syncs leave a skipped lesson alone".
  - *Now:* `SkipLesson` marks the lesson skipped and removes its queued, running and
    canceled jobs in one transaction, recording `discard` for the ones a worker holds; the
    API kills the running download. That download records nothing, and nothing outside its
    private folder is touched, so the lesson's earlier files stay (D66); a Skip that lands
    while the download is being placed undoes the placement (D79). Skip cancels rather than
    refusing while a download runs: the user asked for the
    lesson not to be downloaded, and a refusal would only send them to *Cancel* first. It
    answers 409 only while a delete holds the lesson, which skips it anyway once the files
    are gone.
  - *Evidence:* `go test -count=1 -run 'SkipSticks|SkipStops|SkipWhileDeleting|SkipLesson' ./internal/database/ ./internal/scheduler/ ./internal/server/`
  - *Left open:* D80 (a Skip that races the planner's enqueue). D79 (a Skip during the
    library move) has since shipped on this branch.
- **D85 · A lesson Musora couldn't be reached for was skipped for good.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* the worker treated any error from Musora's lesson lookup, a network failure or
    an answer it couldn't read included, as "gated or missing", and skipped the lesson.
    Syncs never queue a skipped lesson again, so a short outage during a sync skipped every
    lesson looked up during it, until each was un-skipped by hand. `main` does the same.
  - *Now:* a lesson Musora couldn't be reached for, or whose answer couldn't be read, fails
    and is tried again; only a lesson Musora answers with no match (gated or missing) is
    skipped. (Since round 5 a lesson that still records files from an earlier download
    stays downloaded with a note instead of failing, and waits for *Download*; since
    round 5d only while those files are on disk, and a lesson Musora has no match for is
    kept the same way, with only its job failed, instead of skipped: D113.)
  - *Evidence:* `grep -n 'Resolver.Resolve' -A12 internal/scheduler/worker.go` ·
    `go test -count=1 -run 'SkipsOnlyALessonMusoraHasNoMatchFor' ./internal/scheduler/`
- **D86 · A failure's text was raw, wrong, or gone too fast.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* the lesson's note (shown under the lesson, and in the Queue next to *Retry*) and
    the live events carried the worker's raw error (a Go error, yt-dlp's exit status), and
    a canceled download showed the bare word "canceled". The API answered with fragments
    ("lesson not found", "job is not active", "login failed", "invalid id: <input>"), some
    echoing the input. Failure toasts closed after about 4 seconds while carrying the
    server's full sentence, and the Queue replaced a 409's reason with "Job is not
    retryable".
  - *Now:* the lesson's note and the events carry a fixed sentence for each kind of
    failure, true in both places it shows, and the error behind it goes to the server log
    only (security round 4, Info 5); a canceled download no longer shows the bare word
    "canceled", and migration 005 gives the rows an older version left with it the same
    sentence as today's ("The download stopped before it finished. Download again to get
    this lesson."). Every message the API sends is a sentence under the copy rules at the
    top of `internal/server/messages.go` (outcome first, at most 220 characters, no echoed
    input), checked on every message by a test. A failure toast with a description stays
    until it is closed, and the Queue shows the server's sentence (round-4 UI review, N2,
    N3, N4).
  - *Evidence:* `go test -count=1 -run 'RecordsSentencesNotErrors|MessagesFitTheDialog|MessagesFollowTheCopyRules|MigrationGivesOldCanceledNotesASentence' ./internal/scheduler/ ./internal/server/ ./internal/database/`
    · `grep -rn 'failureToast(' web/src --include=*.tsx`
- **D87 · Partial files could reach the library.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* the move took the lesson folder as it was, so partial files a run left when it
    died (yt-dlp's `.part`, `.ytdl`, `.f<number>.<ext>` and `.temp.<ext>`, drumdrop's own
    `.drumdrop-part` and `.drumdrop-episode`) moved into the library with the lesson.
  - *Now:* a download is placed from its private folder (D66), and before it is placed,
    drumdrop removes them from that lesson folder and its subfolders, so none of them
    reaches the lesson's folder, in the library or in downloads (security round 4, Info
    2). The README says so.
  - *Evidence:* `go test -count=1 -run 'MovesNoPartialFileIntoTheLibrary|CleanupPartialsRemovesOnlyPartials' ./internal/scheduler/`
- **D88 · A wrong Musora password logged the user out of DrumDrop.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* `POST /api/session` answered 401 when Musora refused the login, and also when
    Musora couldn't be reached. The web client takes any 401 for its own API token being
    refused, so it cleared the stored token and opened the token prompt: a mistyped Musora
    password locked the user out of DrumDrop until they pasted the token again, and in the
    tokenless local mode the prompt asked for a token that doesn't exist. On `main` since
    `43fb9b1` (#5; round-4 UI review, X1).
  - *Now:* credentials Musora refused (any 4xx but 408 or 429) are a 422, "Musora didn't
    accept that email and password. Check them, then Connect again."; Musora unreachable,
    or an answer that can't be read, is a 502; a session that couldn't be saved is a 500.
    Only the auth middleware may answer 401, and a test reads the server package's source
    to keep it that way. The Settings test mounts the real token prompt.
  - *Evidence:* `go test -count=1 -run 'LoginNeverAnswers401|OnlyTheAuthMiddlewareAnswers401|LoginSaysWhether' ./internal/server/ ./internal/musora/`
    · `cd web && npx vitest run src/pages/Settings.test.tsx`
  - *Left open:* D83 (which 4xx Musora sends for a wrong password is unconfirmed).
- **D66 · A stopped download could cost the lesson its video, and what it wrote was known
  only by its folder.** This branch (`fix-library-delete-and-move`), PR #21.
  - *Was:* a download wrote into the lesson's own folder (`<downloads>/<Course>/NN -
    Title`), shared with whatever was already there: an earlier download, a copy kept in
    downloads when a library move failed, or a folder kept when a follow was removed
    without its files. Without a library that folder was the lesson's permanent home.
    drumdrop could not tell which files a download wrote, so a stopped or failed download
    went by the folder: one it created was removed whole, one already there lost only its
    partial files, and the download's own new files stayed there, untracked. And yt-dlp
    runs with `--force-overwrites`, so it deletes an existing `<base>.mp4` (and its
    subtitles) before it downloads (`existing_file` in yt-dlp's `YoutubeDL.py`): in the
    default setup a re-download that then failed, or was skipped or canceled, had already
    lost the earlier video. `main` passes the same flag.
  - *Now:* every download writes into a private folder,
    `<downloads>/.drumdrop-in-progress/job-<id>/` (emptied first; the parent holds a
    `.plexignore`), and is placed where the lesson lives only once it has finished. A
    failure, a Skip, a delete, a Cancel or a shutdown touches nothing outside that folder,
    which goes whole when the job ends. "No library" is placed as a library rooted at
    downloads, so every setup takes the same path. A placement replaces only the lesson's
    recorded entries and entries no lesson records at exactly the names being placed. It
    sets them aside first, in a `replaced-<id>` folder under the same root, removes them
    only once the download is recorded (the ordering that closes D79 and D75), and logs
    each (`↻ <lesson id> replaced "<path>" (the lesson's earlier download | which no lesson
    recorded)`). The default layout merges into an existing lesson folder, so unrelated
    files stay (since round 5 its subfolders too, file by file), and handles the lesson's
    previous folder when its row records another one (D58, in part): since round 5 that
    folder goes only when the download brought back every file in it, and otherwise
    stays, no longer recorded, and logged (ruling (e), D113). The old "library ==
    downloads is a no-op" guard became "a placement refuses its own source", since that is
    now an ordinary placement. The poster, resource, play-along and sheet-music fetches
    stop when the download is canceled, and a canceled download writes no nfo. A shutdown
    no longer skips the lesson: the job is left `running`, and the next `serve` or looping
    `daemon` start requeues it and removes every stopped job's folder, in downloads and in
    the library (`Daemon.Recover`), keeping and logging any `replaced-<id>` folder. A
    download confirmed just before a shutdown is still placed and recorded. `sync` (and,
    since round 5, `daemon --once`) stops cleanly on `Ctrl-C` (yt-dlp is killed and its
    folder removed) but runs no startup recovery (D95), and the one-shot
    `drumdrop <lessonOrCourseId | musoraUrl>` no longer loses a file already in its lesson
    folder when its download fails. The README says so.
  - *Evidence:* `go test -count=1 -run 'StoppedReDownload|ReplacesOnlyTheLessons|ReplacesThePreviousFolder|ClaimedAgain|SweepPrivate|RecoverSweeps|Shutdown|RefusesItsOwnSource|AliasedToItsSource|NeverReusesTheArea|CanceledDuringTheFetches|NeverTouchesAFolder' ./internal/scheduler/ ./internal/musora/`
    · `grep -n 'def existing_file' -A8 "$(python3 -c 'import os, yt_dlp; print(os.path.dirname(yt_dlp.__file__))')/YoutubeDL.py"`
  - *Left open:* D92 (the edges of a shutdown), D93 (a `replaced-<id>` folder a crash
    leaves; whether Plex skips `.drumdrop-in-progress`), D72 (on Windows, setting aside
    goes by path and is untested), D58 (legacy plex-tv rows), D62 (Plex and the copy
    fallback).
- **D79 · A Skip during a library move could lose the lesson's earlier files.** This
  branch (`fix-library-delete-and-move`), PR #21.
  - *Was:* the move ran after the download was confirmed and before `FinishDownload` read
    the Skip, and a Skip's kill stopped only yt-dlp, so a Skip that landed during the move
    was seen only once the move was done. The move's irreversible step came first: in the
    plex-tv layout it removed the lesson's previous recorded entries before placing the new
    ones, and when the title or episode number had changed the Skip's discard then removed
    the new ones too. The season folder ended empty while the row still recorded the old
    names (security round 4, LOW-3; code round 4, #3). The default layout had the same
    shape when the move replaced a library folder no lesson recorded.
  - *Now:* closed by ordering, with D66. `FinishDownload` is the commit point: a placement
    only sets aside what it replaces, and removes it once the download is recorded. A
    Skip, a delete or a follow removal that lands during the placement makes the record
    refuse, and the placement is undone: the placed entries go back into the private
    folder, and the set-aside ones go back where they were. The one exception is a delete
    of the lesson's files, which leaves the lesson's own earlier files out, as it removes
    them anyway. An entry that can't be put back stays in its `replaced-<id>` folder, and
    the log names it (D93).
  - *Evidence:* `go test -count=1 -run 'StopDuringThePlacement|UndoPutsEverythingBack|PlexDiscardLeavesTheSeasonFolderQuietly|SetsNothingAsideUnlessItCanSetAll' ./internal/scheduler/`
    · `grep -n 'pl.undo' internal/scheduler/worker_record.go`
- **D75 · A moved download whose record failed was untracked until the next download.**
  This branch (`fix-library-delete-and-move`), PR #21.
  - *Was:* the move ran before `FinishDownload`. When that write failed (a database error),
    the attempt failed and nothing reported success, but the files the move had placed in
    the library stayed, recorded by no row, until the lesson's next download replaced them.
  - *Now:* closed by the same ordering as D79: a record that fails undoes the placement, so
    the new files go back into the private folder and the replaced ones go back where they
    were. The attempt fails and is retried.
  - *Evidence:* `go test -count=1 -run 'StopDuringThePlacement|UnrecordedDownloadIsNotReportedDone' ./internal/scheduler/`
    (the `unrecorded` cases)
    · `grep -n 'the download could not be recorded' internal/scheduler/worker_record.go`
- **D84 · An instructor could be followed only by Musora's slug.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* an instructor follow took only the slug (`jared-falk`). A name, another letter
    case or a pasted coach-page link was refused with a 400 before any lookup
    (`msgBadSlug`). D84 was the owner question whether to accept more.
  - *Now:* decided (owner ruling #70) and built. A name, a slug or a coach-page link all
    work, on preview, on add and in the CLI (`@jared-falk`, `@Jared-Falk`, `@'Jared Falk'`,
    `@<link>`, or `--instructor`), through one normaliser (`musora.NormalizeInstructor`),
    so a preview shows what an add stores. A name is lower-cased (ASCII only), and each run
    of spaces becomes one hyphen. A link is read by its path, `/<brand>/coaches/<slug>`
    with an optional number (the shape of the instructor documents' `web_url_path`, read
    live), and its brand applies when Brand is empty; a different Brand is refused
    (`msgBrandMismatch`). The preview answers the normalised `slug` and the `brand`, and
    the add dialog shows both ("@jared-falk", "… lessons on Pianote"). A coach-page link
    given as a node is refused (`msgCoachLinkAsNode`): its number is the instructor's.
    Anything else (non-ASCII, underscores, dots, a link that isn't a coach page) is still
    refused before any lookup. Brands read the same across the UI.
  - *Evidence:* `go test -count=1 -run 'NormalizeInstructor|IsCoachLink|InstructorInputIsNormalisedAlike|ALinkForAnotherBrand|ACoachLinkIsNotANode|FollowAtName|FollowRefusesACoachLink' ./internal/musora/ ./internal/server/ ./cmd/drumdrop/`
    · `cd web && npx vitest run src/pages/Follows.test.tsx`
  - *Left open:* D89 (one instructor, one brand), D91 (PlayBass's name).
- **D94 · An instructor follow could sync none of its brand's lessons.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* Musora keeps several instructor documents under one slug, one per brand and
    sometimes two in one brand (`jared-falk` has a drumeo and a singeo one). The lessons
    query referenced only the first document for the slug (`[0]`), so a follow in a brand
    whose lessons reference another document found none: a drumeo follow of `jared-falk`
    synced 0 lessons. The follow's display name came from that first document too. On
    `main` since the instructor follow was built (#3).
  - *Now:* the lessons query references every document for the slug, and the lesson's own
    brand scopes the result (`instructor_lessons.groq`). The name comes from the brand's
    own document, else the first (`pickInstructorDoc`). Measured live with read-only
    queries: `jared-falk` on drumeo went from 0 to 529 lessons, and `stephane-chamberland`
    from 0 to 18. A follow stores only the slug and brand, so an existing one finds its
    lessons at its next sync, without being added again.
  - *Evidence:* `go test -count=1 -run 'InstructorLessonsFindsTheBrandsLessons|ResolveInstructorIDPicksTheBrandsDocument|PreviewAndAddUseTheBrandsInstructor|FollowInstructorStoresTheBrandsName' ./internal/musora/ ./internal/server/ ./cmd/drumdrop/`
    · `cat internal/musora/queries/instructor_lessons.groq`
  - *Left open:* D89 (one instructor, one brand), D90 (the query's order does nothing).
- **D67 · A default-layout delete after the library moved answered 200 and lost track of
  the files.** This branch (`fix-library-delete-and-move`), PR #21.
  - *Was:* a default-layout lesson records its folder by absolute path. After the library
    (or downloads) was mounted at another path, a delete found nothing at the old path,
    called it already gone, tombstoned the row and logged nothing, while the files stayed
    at the new path, untracked; with `?files=true` on a follow the rows went too.
  - *Now:* `library.Remove` refuses any path outside every root, missing or not, so the
    delete answers the fixed 500, the lesson stays downloaded and keeps naming its folder
    and video, and the log says why.
  - *Evidence:* `go test -count=1 -run 'OutsideEveryRoot|AnotherSpelling' ./internal/server/ ./internal/library/`
  - *Left open:* recording `output_dir` relative to its root, as the library record is,
    would let such a delete find the files; that is a migration of every row.
- **D52 · A library move that failed part-way left an untracked copy.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* on the copy fallback (two filesystems) nothing was undone when a step failed. A
    default-layout copy that failed part-way left a partial folder in the library; a plex-tv
    one left the lesson split between the season folder and scratch, recorded with no video.
    When the copy finished but downloads couldn't be cleared, the worker dropped the library
    folder the move returned and recorded downloads, so Plex showed an untracked copy.
  - *Now:* whatever fails, one complete copy is left and that is what gets recorded. A copy
    that fails part-way is taken back out of the library (already-moved entries are renamed
    back into the download's private folder; a partly copied entry is removed, and only
    what is really left is reported), and the lesson is placed and recorded in downloads
    instead, unless that would delete or stop recording a library file the lesson owns
    (since round 5d, `keptInLibrary`, in either layout: a lesson whose row records its own
    folder in the library, or a legacy season folder whose entries the move couldn't
    name, keeps its library copy, still recorded, and the attempt fails; since round 5e
    one whose recorded folder is that downloads folder itself is placed there; rulings
    (f) and (i), D113). A finished copy whose private folder can't be removed is recorded
    in the library, in both layouts; the folder is left behind and removed at the next
    `serve` or looping `daemon` start (D66). Copied files, and the folders the copy
    creates, are flushed to disk before the download's own copy is removed, and the copy
    refuses a symlinked lesson folder instead of following it.
    Every write into the library goes through `os.Root` opened on the destination folder
    (which must resolve inside the library), a copied entry is created with `O_EXCL`
    (files) or `Mkdir` (folders), and each rename acts on the folders the move holds open
    and refuses an entry already at its name (`renameAt`: `renameat2(RENAME_NOREPLACE)` on
    Linux, `renameatx_np(RENAME_EXCL)` on macOS; a filesystem without the flag gets a retry
    without it, which replaces), so no write follows a symlink planted in the library, even
    one swapped in after the checks. A rename that refuses makes the move refuse (the
    lesson is placed in downloads instead, with the same exception); the copy runs
    only across filesystems (`EXDEV`,
    `ERROR_NOT_SAME_DEVICE` on Windows), and a copy that fails removes only what it created
    (round 4, `9928719`). On Windows the rename is still by path (D72). The download's side
    is opened the same way: its lesson folder must be a real folder inside downloads, the
    episode nfo is written there through it, and the copy reads only through it.
    Library == downloads is decided by identity, not spelling: every command refuses to
    start with a library that is the downloads folder under another path (a symlink, or
    one folder bound twice; `engine.Config`), and a placement refuses a destination that
    is the downloaded folder itself (D66 replaced the old no-op guard). A plex-tv
    re-download first sets aside what the lesson's previous download recorded (D51), and
    removes it only once the new download is recorded; if it can't be set aside, nothing
    is placed in the library. (Since round 5 it keeps, still recorded, what it recorded at
    the same episode name that the download didn't bring back, and a recorded folder under
    an old name goes only when the download brought back every file in it: rulings (j) and
    (e), D113.) Anything that can't be cleaned up is logged with its path
    (`⚠ move to library`). The move stays non-fatal, except, since round 5, for a
    lesson whose library copy the fallback would delete or stop recording (above).
  - *Evidence:* `go test -count=1 -run 'CopyFails|NotRemovable|UndoRenamesBack|Flushed|Aliased|SymlinkedLessonFolder|DiscardPartialCopy|CannotBeCleared|ConfigRefuses|UnderARace|NeverReplaces|PlantedSymlink|KeepAnEntryPlanted|CopyOnlyAcrossFilesystems'
    ./internal/scheduler/ ./internal/engine/` (the permission-based ones skip as root).
  - *Left open:* if the OS refuses both the move and its undo (a renamed entry can't be
    renamed back), the lesson stays split and the log names every path; an entry that
    can't be put back stays in its `replaced-<id>` folder (D93). Plex seeing half-copied
    files is D62.
- **D51 · A plex-tv delete left most of a song, and every lesson's folders, behind.** This
  branch (`fix-library-delete-and-move`), PR #21.
  - *Was:* the delete matched the recorded video's name minus `.mp4` followed by `.` or `-`.
    A song records its `[Drumless]` version, so its `[Original]` video, `.nfo`, poster and
    folders stayed; any lesson's `<episode> resources` / `play-along` folders stayed too.
    Separately, the delete chose its method from today's `DRUMDROP_LAYOUT`, so switching the
    layout back to default and deleting one plex-tv lesson would have removed its whole
    shared season folder.
  - *Now:* files are known by record (owner ruling #66). The plex-tv move records the exact
    season-folder entries it placed with the lesson (`lessons.library_entries`, migration
    004), relative to the library (`<Show>/Season NN/<entry>`, so a record survives the
    library being mounted or spelled differently), and a delete or a re-download acts on
    exactly those, whatever the title is now (since round 5 a re-download keeps those it
    didn't bring back at the same episode name, and a folder under an old name it didn't
    fully bring back: D113): both versions of a song, its nfo, poster and folders go, and
    nothing another lesson
    recorded, even at the same episode number or under a title that extends this one
    (`Five`, `Five [Live]`, `Five-Part Fill`, `Five.5`). A lesson moved before the record
    existed falls back to name matching (`library.Claims.Plan`), read under the library as it
    is configured today: only the names the move produces, skipping (and logging) anything
    another lesson claims, and refusing rather than guessing when its episode name is
    ambiguous. "Claims" is decided by the real file (`os.SameFile`), so a hard link or
    another spelling of a claimed entry is claimed too. The move never overwrites or removes
    an entry another lesson claims (it refuses, writing nothing); an entry at one of its
    names that no lesson claims is replaced and logged (owner ruling, 2026-09-23). It
    accepts any title, brackets included. The delete picks its method from where the lesson
    was recorded (`library.IsSeasonDir`), not from the layout setting, and removes through
    `os.Root` under the longest matching root, so it stays inside the downloads and library
    dirs. A damaged record (not `<Show>/Season NN/<entry>` paths) refuses every delete and
    plex-tv move rather than guessing; the README gives the one-line repair.
  - *Evidence:* `go test -count=1 -run 'RemoveLessonFiles|DeleteLesson|DeleteFollow|Plan|Record|Remove|LegacyEpisode|MoveToLibraryPlexTV|WorkerPlexTv|Migration004'
    ./internal/server/ ./internal/scheduler/ ./internal/database/ ./internal/library/`
  - *Left open:* D57 (two lessons can share an episode number), D58 (a legacy lesson's
    previous files can be left behind when its folder changes).
- **D55 · A plex-tv lesson with no video couldn't be deleted from the library.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* the delete found an episode's entries from its recorded video's name, so a
    lesson recorded without one (`--resources-only`, or a song whose score has no
    recordings) was a no-op, and its nfo, poster and folders stayed, untracked.
  - *Now:* the lesson's record names its entries, video or not. A no-video lesson moved
    before the record existed falls back to the episode name built from its title and
    position (if the title has changed since, see D58).
  - *Evidence:* `go test -count=1 -run 'NoVideoLessonByRecord|SeasonFolderIsNeverWiped' ./internal/server/`
- **D56 · Deleting a lesson's files discarded every error.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* both delete paths called `_ = removeLessonFiles(...)`, so a refusal or a file that
    couldn't be removed was neither logged nor returned, and the lesson read as deleted
    while its files stayed.
  - *Now:* a lesson whose files could not all be removed records only the files still
    there (its record narrowed, `video_path` and `output_dir` cleared once what they name
    is gone) and reads downloaded, and the caller is told: a fixed 500 message saying what
    happened, what is left and to "Delete again", with the detail on the server's stderr
    (never in the response). Deleting a follow with its files keeps the follow and every
    lesson row in that case, so no file loses its row. A delete first marks the lesson as
    being deleted and removes its queued, running and canceled jobs, then kills the
    running download. Every write the worker makes is guarded by its job, so once a delete
    answers, no step of an earlier download records anything; and until it answers no
    download of the lesson can be enqueued or retried, nor the lesson skipped (the API
    answers 409, the planner skips it), so the delete never removes files a newer download
    wrote. The hold is a lease (`lessons.deleting_until`, two minutes) the delete renews
    while it runs, so a delete the process died in releases the lesson by itself, with no
    startup sweep that could clear a live delete; the API's lesson carries it as
    `deleting`. Its last write is still a compare-and-swap on each file column (409 if the
    row changed, which nothing in drumdrop can do now). The delete runs to the end if the
    client goes away, and a second delete of the same lesson answers 409. A folder outside
    both roots is refused even when it is missing, and the row keeps naming it (was D67).
  - *Evidence:* `go test -count=1 -run 'CannotBeRemoved|ReportsWhatRemains|KeepsOnlyWhatIsThere|StopsItsDownload|Meanwhile|BlocksNewDownloads|ClientLeaves|Damaged|Abandoned|StopsWhenDeleted|TombstoneAndKeep|KeepLessonFiles|BeginLessonDelete|BeginFollowDelete|Lease|RenewsItsHold|OutsideEveryRoot|DTODeleting'
    ./internal/server/ ./internal/scheduler/ ./internal/database/`
  - *Left open:* nothing of its own. D66, which it left open, has since shipped on this
    branch.
- **D60 · A download a delete overtook could leave or lose files.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* the worker could not tell which kind of delete had removed its job. Without a
    library the lesson folder was left alone, so files written after a lesson delete
    stayed untracked; with a library (or with the library the same path as downloads) the
    worker removed what it wrote even when a follow was removed *without* its files, so a
    copy the owner chose to keep was deleted, and a kept copy in downloads went with it.
    Removing one follow also stopped (and so discarded) a download another follow's lesson
    had queued under it.
  - *Now:* a stopper records what it wants for each job it removes that a worker holds
    (`abandoned_jobs`: `keep`, `discard` for a Skip, `delete` for a delete of the files),
    matched on the job and its lesson, consumed by the first write that reads it, and
    dropped after seven days unread. Since D66 a stopped download touches nothing outside
    its private folder, whatever the intent, and that folder goes. The intent matters only
    when the stop lands while the download is being placed: the placement is undone, and
    what it set aside goes back, except the lesson's own files when the stopper is a delete
    of those files. So `keep` and `discard` now act alike, and the keep intent means: a
    follow removed without its files keeps the lesson's recorded files; a download not yet
    recorded never became the lesson's, so none of it is kept. (Before D66 the intent chose
    what a stopped download removed from the lesson folder; round 4, `1c7f50f`.) A
    follow's delete stops only its own lessons' downloads. A job
    canceled in the database while its worker held it (before its process was registered)
    stops at its next step instead of running to the end, and a cancel that lands once the
    files are placed is recorded rather than leaving them untracked.
  - *Evidence:* `go test -count=1 -run 'Abandoned|HonoursADatabaseCancel|HonourACancel|StopsOnlyItsOwn|WorkerWritesAreAbandoned|AbandonedJobsAreBounded|StoppedByADelete|StoppedReDownload|StopDuringThePlacement'
    ./internal/scheduler/ ./internal/database/ ./internal/server/`
  - *Left open:* nothing of its own. D66, which it left open, has since shipped on this
    branch.
- **D61 · A job canceled between two attempts was briefly re-marked running.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* `MarkJobRunning` re-stamped a job by id whatever its status, overwriting a
    cancel with `running` and counting one more attempt.
  - *Now:* it re-stamps only a running job; a canceled one is left as it is, and the
    worker's next step (`StartDownload`) stops the download and records the cancel.
  - *Evidence:* `go test -count=1 -run 'MarkJobRunningLeavesACanceledJob|HonoursADatabaseCancel' ./internal/database/ ./internal/scheduler/`
- **D63 · A lesson whose re-download was canceled or deleted kept its files as "skipped".**
  This branch (`fix-library-delete-and-move`), PR #21.
  - *Was:* canceling a re-download, or a delete that stopped one and then could not remove
    every file, left the row `skipped` while it still recorded its earlier files, and the
    Lessons page offered *Delete* only for `downloaded` lessons.
  - *Now:* a download that ends without recording anything (a cancel, or a delete that
    removed its job) leaves a lesson that still records files `downloaded`, and a delete
    that could not remove everything leaves it `downloaded` too. The API's lesson carries
    `has_files`, true exactly when a delete would have files to act on (the store's own
    predicate, whatever the status); the UI half (offering Delete for any lesson with files)
    shipped in `ce12d81` and moves onto `has_files`. (Since round 5d a cancel keeps it
    `downloaded` only while those files are on disk, ruling (o); otherwise it is
    `skipped` with the stopped note. A delete, skip or follow removal that removed the job
    still leaves it `downloaded`: whatever stopped the download settles the lesson. D113.)
  - *Evidence:* `go test -count=1 -run 'GuardedFailSkipCancel|DownloadingLessonWithFiles|KeepLessonFiles|HasFiles' ./internal/database/ ./internal/server/`
- **D64 · The store exported download writers no job guarded.** This branch
  (`fix-library-delete-and-move`), PR #21.
  - *Was:* `MarkDownloaded`, `MarkDownloading`, `MarkFailed`, `MarkJobDone`,
    `MarkJobFailed`, `MarkJobCanceled` and `UpdateLessonDeleted` wrote a lesson or a job by
    id, whatever had happened to it since. No production code called them any more, but a
    new caller would have let an earlier download record after a delete.
  - *Now:* they are gone. The worker records only through the job-guarded writers
    (`StartDownload`, `ConfirmDownload`, `FinishDownload`, `FailDownload`, `SkipDownload`
    (since round 5d `NotReturnedDownload`),
    `CancelDownload`) and a delete through `BeginLessonDelete` / `TombstoneLesson` /
    `KeepLessonFiles`. Tests seed through those same writers, or, for a test about jobs
    alone, through a raw test-only `endJob`. The API's *Skip* is `SkipLesson`, which stops
    the lesson's downloads in the same transaction (D78).
  - *Evidence:* `grep -rnE '\.(MarkDownloaded|MarkDownloading|MarkFailed|MarkJobDone|MarkJobFailed|MarkJobCanceled|UpdateLessonDeleted)\(' --include=*.go .`
    (prints nothing).
- **D49 · Board and repo hygiene.** PR #20 (branch `chore/vault-onboarding`) adds this
  BACKLOG.md. It gitignores `.claude/`, which holds the CLAUDE.md symlink into the owner's
  vault, and `.mcp.json` (per-machine Claude Code config). It also tracks `sonar-project.properties` with its
  configuration unchanged and a written reason for each exclusion.
- **D50 · README corrected.** Also in PR #20. The
  README now says the image is linux/amd64 only, and the Status section no longer calls the
  shipped web UI, queue and scheduler a roadmap. It now covers songs (soundslice → YouTube)
  and their deno and current-yt-dlp requirement, and documents `DRUMDROP_HOST_DOWNLOADS_DIR`.
  The Plex agent advice now matches the owner's live test, in `docker-compose.yml` as well.
- **D24 · `docker-compose.dev.yml` repeated the old Plex advice.** Also in PR #20. Its
  comment said a TV Shows library needs "the Local Media
  Assets agent", and that episode titles come from the filenames. The owner's live test on
  2026-06-02 found that the `.nfo`-reading agent (XBMCnfoTVImporter) is what makes episode
  titles show up, and the titles come from the `<episodedetails>` nfo. The dev compose file
  now carries the same wording as `docker-compose.yml`.
  *Evidence:* `grep -n 'Local Media Assets' docker-compose.dev.yml` (only the "not enough"
  line) · *Detail:* vault note drumdrop-plex-library; `musora-downloader-project.md` (the
  v0.5.0 entry).
- **Before this board:** releases v0.0.1 (2026-05-31) to v0.7.1 (2026-06-09), PRs #1–#19.
  Re-derive them with `git tag --sort=creatordate` or `gh release list`. The owner's vault
  note drumdrop-log keeps the release history from before the vault. No changelog is copied
  here.
