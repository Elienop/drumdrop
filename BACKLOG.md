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
the highest ID on this page: the next new ID is D57 on 2026-09-23 (*moves*; re-check the
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
D52 into *Next up*._

## Next up

*Order:* D51 and D52, which led because they left the owner's files wrong on disk, have
shipped on this branch; the entries below keep their order. This order is the 2026-09-23
onboarding session's proposal, not an owner ruling; the owner may reorder.

- **D1 · Build with a patched Go toolchain.**
  - *What:* `go.mod` pins `go 1.26.3`, and CI and the release binaries build on exactly that
    version (setup-go reads `go-version-file`). `govulncheck` finds standard-library
    vulnerabilities that drumdrop's code actually reaches, in `net/http`, `crypto/tls`,
    `crypto/x509`, `net/url`, `net/textproto` and `encoding/asn1`. All are fixed by go1.26.6
    (8 on 2026-09-23, *moves*).
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

- **D55 · A plex-tv lesson with no video can't be deleted from the library.**
  - *What:* in a plex-tv season folder, the delete finds an episode's entries from its
    recorded video's name. A lesson recorded without one (`--resources-only`, or a song
    whose soundslice score has no recordings) has an empty `video_path`, so the delete is a
    no-op and its nfo, poster and folders stay in the library, untracked.
  - *Why:* the same leftovers D51 fixed, for the lessons that have no video. It needs the
    episode name recorded some other way (e.g. a column), which is a schema decision.
  - *Evidence:* `grep -n 'videoPath == ""' internal/server/lessonfiles.go` ·
    `grep -n 'ResourcesOnly' internal/scheduler/worker.go`
- **D56 · Deleting a lesson's files discards every error.**
  - *What:* both delete paths call `_ = removeLessonFiles(...)` (`handleDeleteLesson`, and
    `?files=true` on follow delete). A refusal or a file that can't be removed is neither
    logged nor returned, so the lesson reads as deleted while its files stay.
  - *Why:* invisible leftovers, the same symptom as D51. The server has no logger today, so
    the fix is to pick where these go (stderr like the worker, or the response).
  - *Evidence:* `grep -n '_ = removeLessonFiles' internal/server/*.go`

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
    which is a security item rather than a nit. (c) An invalid `?brand=` on the instructor
    preview comes back as a 502 instead of a 400. (d) The preview treats only the literal
    `?whole=true` as true. (e) The SQLite connection string would break on a database path
    that contains `?`.
  - *Why:* none of the remaining ones exposes anything today in a single-user, self-hosted
    tool. A review agent judged them acceptable, but there's no owner ruling, so they stay
    listed here instead of under accepted residuals.
  - *Evidence:* `grep -rn Vary internal/server` (no match) ·
    `grep -n StatusBadGateway internal/server/preview.go` · `grep -n 'Get("whole")' internal/server/preview.go` ·
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
  - *Evidence:* the plex-tv branch after `moveToLibraryPlexTV` in `internal/scheduler/worker.go`
    (it stats one video path)
  - *Detail:* `drumdrop-song-soundslice-video.md`.

- **D12 · Fix the open SonarQube findings.**
  - *What:* the only analysis so far (2026-08-15, of `1c2dbda`) left 67 open issues (*moves*).
    Most are cognitive complexity (`go:S3776`, mostly in test files), read-only props
    (`typescript:S6759`), nested ternaries (`typescript:S3358`) and repeated strings
    (`go:S1192`). Two are accessibility: `ProgressRow` uses a `progressbar` role instead of
    `<progress>` (S6819), and `Dashboard.tsx` puts `tabIndex` on an element that isn't
    interactive (S6845, also the one reliability issue).
  - *Why:* the owner's standing rule is that Sonar findings get fixed: no false-positive
    marking, no rule deactivation, no custom profile (decisions #3 in the vault). The two
    accessibility findings are real problems for keyboard and screen-reader users.
  - *Evidence:* `sonar-issues --all` (read-only)
  - *Detail:* vault note drumdrop-sonarqube. The 5 findings in `web/src/test/` wait on D30.

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

## Housekeeping & dependencies

- **D16 · Move off Node 20, which reached end-of-life on 2026-04-30.**
  - *What:* CI (`node-version: 20` in `ci.yml` and `main.yml`) and the Dockerfile's web stage
    (`node:20-alpine`) still build on Node 20. `web/package.json` has no `engines` field.
    Local development already runs a much newer Node, where the tests only pass thanks to a
    `localStorage` polyfill in `web/src/test/setup.ts`.
  - *Why:* an end-of-life runtime gets no security fixes, and the build environment has
    drifted away from the development one.
  - *Evidence:* `grep -rn node-version .github/workflows` · `grep -n 'node:' Dockerfile` · `node --version`
  - *Detail:* `project_drumdrop_node26_localstorage_polyfill.md`.

- **D17 · npm: dev-tool advisories and majors behind.**
  - *What:* besides react-router (D2), `npm audit` reports advisories in the build and test
    tools, plus transitive ones (11 findings in total on 2026-09-23, *moves*). They include a
    critical one in vitest 2.x (the fix is vitest 5, a breaking upgrade) and one in vite ≤6.4.2
    (fixed inside v6). Several majors are behind: vite 6 → 8, vitest 2 → 5, TypeScript 5.9 → 7,
    lucide-react 0.x → 1.x, sonner 1 → 2, tailwind-merge 2 → 3, jsdom 25 → 30 and
    @vitejs/plugin-react 4 → 6. Nothing tracks this automatically (D31).
  - *Why:* dev-only advisories don't ship to users, but they run on the developer machine and
    in CI, and the longer the majors wait, the bigger the eventual jump. The vitest major also
    gates the coverage wiring (D18).
  - *Evidence:* `cd web && npm audit` · `cd web && npm outdated`

- **D18 · Wire test coverage into SonarQube.**
  - *What:* there's no `make coverage` target, so `sonar-scan` uploads no coverage and the scan
    reports 0%. SpenDrop is the template. It has a Makefile `coverage:` target (a Go cover
    profile and `go test -json`, plus vitest lcov and a test-execution report), the report
    paths in `sonar-project.properties`, `sonar.coverage.exclusions` for entry points that
    can't be tested, and the report files in `.gitignore`. First check whether vitest 2.x
    supports the reporter and `@vitest/coverage-v8`, or whether D17's upgrade has to come
    first.
  - *Why:* the "Sonar way" quality gate has a coverage condition on new code. At 0%, the first
    scan that includes new lines will fail it. Until then the gate passes only because it has
    nothing to check.
  - *Evidence:* `grep -n '^coverage:' Makefile` (no match) · the commented coverage lines in
    `sonar-project.properties`
  - *Detail:* vault note drumdrop-sonarqube.

- **D19 · Rescan DrumDrop in SonarQube once.**
  - *What:* the project has only ever had one analysis (2026-08-15). The server has been
    upgraded since then and the TypeScript/JS/CSS rule sets have changed, so D12's numbers
    will shift. Best done right after D18, so the new baseline includes real coverage.
  - *Why:* fixing D12 against stale numbers wastes effort.
  - *Needs:* the owner's OK, because a scan writes to the Sonar server.
  - *Evidence:* the header of `sonar-issues --all` · run `sonar-scan` from the repo root
  - *Detail:* vault note drumdrop-sonarqube.

- **D20 · `make test` is weaker than CI.**
  - *What:* `make test` is `go test ./...`, which is cached and runs without `-race`. CI runs
    `go test -race ./...`, and the race detector needs cgo. There's no Makefile target for
    the web tests, and the web "lint" is only `tsc --noEmit` (no ESLint).
  - *Why:* a green `make test` can hide a race, or a stale cached pass, that CI will then
    catch. The CI-equivalent commands are `CGO_ENABLED=1 go test -race -count=1 ./...` and
    `cd web && npx vitest run`.
  - *Evidence:* `grep -n '^test' Makefile` · `grep -n 'go test' .github/workflows/ci.yml`
  - *Detail:* `drumdrop-race-needs-cgo.md`, `project_go_test_cache_stale_pass.md`.

- **D21 · Test noise from staticcheck and React act() warnings.**
  - *What:* `staticcheck` flags two nil contexts in `internal/musora/download_test.go`
    (SA1012). A green `vitest run` prints React `act()` warnings from the SSE provider, the
    token gate and the Select component (8 on 2026-09-23, *moves*).
  - *Why:* noise teaches people to ignore warnings, and then it hides the real one when it
    arrives. staticcheck isn't a CI gate.
  - *Evidence:* `~/go/bin/staticcheck ./...` · `cd web && npx vitest run`

- **D22 · Local clean-up, in this checkout only.**
  - *What:* this checkout has:
    - a stale 16 MB `./drumdrop` binary, built 2026-05-31 with version `dev`, before 11 later
      commits to `cmd/` and `internal/`;
    - a `web/dist` built on 2026-06-01 at 18:50, while #11 was being finished. Its timestamps
      make it look older than #11, but the bundle already holds #11's UI ("Edit follow", the
      `PATCH` call in `updateFollow`), and no web change has landed since
      (`git log ad6afd6..HEAD -- web/` is empty), so it is probably current. Run
      `make build-ui` to be sure before relying on a bare `go build -tags webui`;
    - empty `downloads/` and `.claude/worktrees/` folders;
    - five `origin/*` remote-tracking refs for branches that are already deleted on GitHub
      (`git fetch --prune` drops them).
  - *Why:* stale artefacts get run or embedded by mistake. They're all gitignored or local, so
    none of them affects the repo. Read before deleting anything.
  - *Evidence:* `ls -la drumdrop downloads .claude/worktrees web/dist` · `git branch -r` compared
    with `git ls-remote --heads origin`

- **D23 · Sonar display name.**
  - *What:* `sonar.projectName=drumdrop`, while the sibling projects use `MusicDrop` and
    `SpenDrop`. Cosmetic: renaming changes only the label on the Sonar server, not the key.
  - *Evidence:* `grep -n projectName sonar-project.properties`

## Open questions (owner decisions)

Two choices described in their own entries are also waiting on the owner: D3's yt-dlp rebuild,
and D53's fix for the tokenless loopback mode (options A, B or C).

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
    is never stored can't leak.
  - *Detail:* vault note drumdrop-auth-posture.

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

- **D28 · Re-enable the tracked git hooks?**
  - *What:* `scripts/hooks/` holds a `commit-msg` hook (conventional commit subject) and a
    `pre-push` hook (PR title), which `make hooks` turns on. In this clone, `core.hooksPath`
    points at `.git/hooks`, which holds only the `*.sample` files, so the hooks are off.
    MusicDrop and SpenDrop have the same setting. `.git/config` last changed on 2026-09-11,
    and the cause is unknown.
  - *Why it's the owner's:* it changes local git config, and the identical setting in three
    repos suggests something set it on purpose.
  - *Evidence:* `git config --show-origin --get core.hooksPath` · `ls .git/hooks scripts/hooks`

- **D29 · Keep `web/src/components/ui/**` out of SonarQube?**
  - *What:* the 13 vendored shadcn primitives are excluded from analysis. SpenDrop's test is
    that an edited primitive has become our code and should be analysed. SpenDrop analyses
    its copy for that reason (more than half of its primitives had been edited); MusicDrop
    excludes its folder. Here, one file is edited: `sonner.tsx`. In PR #6, commit `5f1541c`
    removed its theme lookup (next-themes) and hardcoded `theme="dark"`, because the app is
    dark-only (the comment at the top of the file). In their PRs' own commits the other 12
    were added once and never changed.
  - *How to check, and how not to:* `git log` on main can't show an edit like that. PRs are
    squash-merged, so main has one "added" line per file and hides whatever happened on the
    PR branch. That is how this board and `sonar-project.properties` first said "none has
    been edited". Check a PR's own commits instead (below), or diff each file against the
    shadcn registry. The registry diff is the stronger check, because it also catches an edit
    made before a file's first commit; it hasn't been run.
  - *Options:* (a) keep excluding the whole folder, accepting that one edited file goes
    unanalysed. (b) exclude only the 12 unedited files by name and let Sonar analyse
    `sonner.tsx`. (c) analyse the whole folder, SpenDrop's choice.
  - *To weigh:* (b) applies SpenDrop's test exactly, but the list of 12 then has to be updated
    by hand whenever a primitive is edited or added. (c) needs no list, but under decisions #3
    every finding in vendored code then has to be fixed, not marked.
  - *Why it's the owner's:* what Sonar sees is the owner's call under decisions #3. The reason
    and the check are written in `sonar-project.properties`.
  - *Evidence:* `gh api repos/elienop/drumdrop/pulls/6/commits --jq '.[].sha'`, then
    `gh api repos/elienop/drumdrop/commits/<sha> --jq '.files[].filename'` for each; the
    folder came in through #6, #8 and #11 ·
    `gh api repos/elienop/drumdrop/commits/5f1541c --jq '.files[] | select(.filename|endswith("sonner.tsx")) | .patch'` ·
    not `git log -- web/src/components/ui` on main, which shows only `A` lines whatever
    happened on the PR branches

- **D30 · The 5 SonarQube findings in `web/src/test/`: fix them, or exclude the folder?**
  - *What:* `msw.ts` and `setup.ts` are test infrastructure, but Sonar analyses them as source,
    and they carry 5 open findings.
  - *Options:* (a) fix the 5 and add `web/src/test/**` only to `sonar.coverage.exclusions`
    (MusicDrop's pattern). This is the recommended one. (b) Add the folder to
    `sonar.exclusions` (SpenDrop's pattern). That makes the 5 disappear without fixing them,
    which decisions #3 treats as hiding code from the rules.
  - *Evidence:* `sonar-issues --all | grep web/src/test`

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

## Accepted residuals and deliberate decisions (not work)

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

## Recently shipped

- **D52 · A library move that failed part-way left an untracked copy.** This branch
  (`fix-library-delete-and-move`), PR number to follow.
  - *Was:* on the copy fallback (two filesystems) nothing was undone when a step failed. A
    default-layout copy that failed part-way left a partial folder in the library; a plex-tv
    one left the lesson split between the season folder and scratch, recorded with no video.
    When the copy finished but downloads couldn't be cleared, the worker dropped the library
    folder the move returned and recorded downloads, so Plex showed an untracked copy.
  - *Now:* whatever fails, one complete copy is left and that is what gets recorded. A copy
    that fails part-way is taken back out of the library (plex-tv renames already-moved
    entries back into scratch), and the lesson is recorded whole in downloads. A finished
    copy whose downloads folder can't be removed is recorded in the library, in both
    layouts. Anything that can't be cleaned up is logged with its path (`⚠ move to
    library`). A plex-tv re-download clears the episode's previous entries first, like the
    default layout clears its folder. The move stays non-fatal; library == downloads is
    still a no-op.
  - *Evidence:* `go test -count=1 -run 'CopyFails|NotRemovable|UndoRenamesBack|ReplacesAPrevious'
    ./internal/scheduler/` (the permission-based ones skip as root).
  - *Left open:* if the OS refuses both the move and its undo (a renamed entry can't be
    renamed back), the lesson stays split and the log names every path. While a
    cross-filesystem copy is running, Plex can see the half-copied files (it happened before
    too); copying to a hidden staging name first would close that, but whether Plex skips
    hidden folders is unverified.
- **D51 · A plex-tv delete left most of a song, and every lesson's folders, behind.** This
  branch (`fix-library-delete-and-move`), PR number to follow.
  - *Was:* the delete matched the recorded video's name minus `.mp4` followed by `.` or `-`.
    A song records its `[Drumless]` version, so its `[Original]` video, `.nfo`, poster and
    folders stayed; any lesson's `<episode> resources` / `play-along` folders stayed too.
    Separately, the delete chose its method from today's `DRUMDROP_LAYOUT`, so switching the
    layout back to default and deleting one plex-tv lesson would have removed its whole
    shared season folder.
  - *Now:* the delete removes every entry the move placed for the episode, through
    `scheduler.PlexEpisodeMatcher`, and the move refuses (writing nothing) to place any name
    that matcher would not recognise, so the two cannot drift apart again. The delete picks
    its method from where the lesson was recorded (`scheduler.IsPlexSeasonDir`), not from the
    layout setting: a season folder loses one episode's entries, any other folder is removed
    whole. Episode 5 still never matches episode 50, nor a title that extends its own.
  - *Evidence:* `go test -count=1 -run 'RemoveLessonFiles|PlexEpisode|PlexSeason|RefusesAName'
    ./internal/server/ ./internal/scheduler/`
  - *Left open:* D55 (a plex-tv lesson with no video can't be deleted), D56 (delete errors
    are discarded).
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
