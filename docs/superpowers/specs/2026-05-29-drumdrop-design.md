# drumdrop — Design Spec

**Status:** Draft for review · **Date:** 2026-05-29
**Goal:** A self-hosted app that downloads a Drumeo/Musora subscriber's lessons for personal
offline use — automated like [Youtarr](https://github.com/DialmasterOrg/Youtarr): subscribe
to courses/instructors, auto-sync new lessons on a schedule, organize with metadata for
Plex/Jellyfin, and manage everything from a web UI.

> Personal-archival tool for content the user already pays for. No DRM is circumvented
> (Musora serves plain HLS). Subject to Musora's Terms of Use; never redistribute.

---

## 1. Validated foundations (empirically confirmed 2026-05-29)

These facts are proven (live HAR capture + a working CLI), not assumed:

- **No DRM.** Lessons are plain fragmented-MP4 HLS on Vimeo's CDN — no `#EXT-X-KEY`. `yt-dlp`
  downloads them directly (240p→4K + en/es subtitles).
- **Content layer = Sanity CMS**, open-read GROQ at
  `https://sanity.musora.com/4032r8py/apicdn/v2021-06-07/production_v2/v4`. Each node is keyed
  by numeric `railcontent_id` with a uniform `parent_content_reference`/`child[]` tree.
- **Resolve = one GROQ call** returns `video.hlsManifestUrl` (a self-signed
  `player.vimeo.com/external/<id>.m3u8?s=…` URL) plus title, description, difficulty,
  instructor, `published_on`, `length_in_seconds`, genre, `parent_content_data`, `thumbnail`,
  and nullable `resources[]` / `mp3_*_url` stems / `assignments[].sheet_music_image_url` /
  `chapters[]`.
- **Auth = email/password → session cookie** (`POST api.musora.com/api/user-management-system/v1/sessions`
  → HttpOnly `musora_platform_backend_session`, ~250-day sliding; validate with `GET …/v1/me`;
  re-login on 401). The `api.musora.com` endpoints (profile, **permissions**, progress) are
  cookie-gated; the Sanity content layer is not.
- **The core engine already exists and works** (`src/{catalog,lesson,sanity,nfo,util}.mjs`,
  this repo): catalog walk → per-lesson resolve → `yt-dlp` download + resources + NFO.

**Locked product decisions** (from brainstorming): fork & adapt Youtarr · email+password login
(encrypted) · follow courses/series **and** instructors, with single-lesson granularity ·
download video **+ all attached resources** · Drumeo-first but brand-agnostic.

---

## 2. Architecture

Fork Youtarr and **replace only its source/extraction seam** with the drumdrop engine. Youtarr
is a job-queue + cron scheduler + metadata DB + web UI + media-server integration wrapped around
a single CLI (`yt-dlp`); ~70–80% of that is source-agnostic and reused as-is.

```
                Youtarr shell (REUSED)                drumdrop engine (THE NEW SEAM)
  ┌──────────────────────────────────────┐   ┌───────────────────────────────────────────┐
  │ Express API · React/Tailwind web UI   │   │ musora/auth.js     login → cookie jar       │
  │ job queue + WebSocket progress        │──▶│ musora/catalog.js  node id → leaf lessons   │
  │ node-cron scheduler                   │   │ musora/lesson.js   resolve → manifest+meta  │
  │ Sequelize/MariaDB                      │   │ musora/download.js yt-dlp + resources       │
  │ NFO generator · Plex refresh           │◀──│   emits: media file, .info-like meta,       │
  │ Apprise notifications · Docker         │   │   resources, subtitles, thumbnail           │
  └──────────────────────────────────────┘   └───────────────────────────────────────────┘
```

The engine's contract to the shell mirrors what Youtarr already consumes from yt-dlp: a media
file, sidecar metadata, a thumbnail, resource files, and parseable progress. Downstream
(NFO/Plex/queue/UI) is largely untouched.

### 2.1 Where the engine lives in the fork
Port the proven `src/*.mjs` modules into Youtarr's module layout as `server/modules/musora/`
(`auth`, `sanityClient`, `catalog`, `lessonResolver`, `downloader`, `nfo`). Keep them cohesive
and individually testable. The standalone CLI (`src/cli.mjs`) is preserved for power users and
as an integration-test harness.

---

## 3. Data model (Sequelize)

Rename Youtarr's YouTube-shaped tables to Musora concepts; keep the queue/job tables as-is.

| Youtarr | drumdrop | Notes |
| --- | --- | --- |
| `channels` | `follows` | A followed node: `railcontent_id`, `type` (course / guided-course / method-v2 / learning-path-v2 / instructor / lesson), `title`, `brand`, `enabled`, per-follow `quality`, `subfolder`, `resources_enabled`, `last_synced_at` |
| `videos` | `lessons` | `railcontent_id` (unique), `follow_id`, `title`, `brand`, `instructor`, `difficulty`, `published_on`, `duration`, `parent_title` (series), `file_path`, `downloaded_at`, `status` |
| `jobs`, `jobvideos`, `jobvideodownloads` | unchanged | source-agnostic queue/progress tracking |
| `sessions`, `apikeys` | unchanged | app auth + API-key download endpoint |
| *(new)* `musora_account` | — | one row: email, encrypted password, cached cookie jar, cached permission ids, `last_login_at` |

Drop: YouTube Data API client, Google Takeout import, `UC`-prefix/channel-id validation,
`youtube_removed*` fields, age-limit rating logic specific to YouTube.

### 3.1 Generic-node model
A follow points at **any** node. Sync = walk that node's subtree (`catalog.resolveLessonIds`)
to enumerate leaf lessons, diff against `lessons` already downloaded, and queue the new ones.
- Container node (course/method/…) → all its leaf lessons.
- Leaf node (single lesson) → just it.
- `instructor` node → resolved via an instructor-lessons query (not a child subtree); see §8 open
  questions.

---

## 4. Authentication

Email + password, performed by the app; credentials stored **encrypted at rest** (libsodium/
Node `crypto` with a key from an env var / generated secret file, never committed).

- **Login:** `POST api.musora.com/api/user-management-system/v1/sessions {email,password}` →
  persist the `musora_platform_backend_session` cookie jar.
- **Validate/keep-alive:** `GET /api/user-management-system/v1/me`; on 401, re-login. The sliding
  ~250-day cookie means a nightly job essentially never expires, with re-login as the robust
  fallback.
- **Permissions:** after login, fetch `/content/user/permissions` to learn the user's permission
  ids (e.g. `[92]`) and inject them into GROQ filters so the catalog reflects exactly what the
  account can access.

**Why login at all, given Sanity is open-read?** Content *resolution* doesn't strictly need it,
but the app does: (a) entitlement-correct catalog ("what I have access to"), (b) personalized
follows/progress, (c) resilience if Musora ever gates Sanity. Media download itself uses the
self-signed manifest (no cookie needed).

UI: replace Youtarr's cookies-upload section with an email/password login form + connection
status; reuse the same secure-storage pattern (0600 file / encrypted column).

---

## 5. Subscriptions & auto-sync

- **Follow** a course, method, learning path, instructor, or pin a single lesson (search or paste
  a Musora URL/id). Reuses Youtarr's channel-management UI, relabeled.
- **Scheduler:** `node-cron` (reused). On each run, for every enabled follow: resolve its current
  leaf-lesson set, diff against downloaded `lessons`, enqueue new ones. Honors a per-follow and
  global download archive (dedupe by `railcontent_id`).
- **Manual:** queue any course/lesson on demand (reuses the trigger endpoints + API-key
  single-item download endpoint).
- **Politeness (required):** sequential resolution, realistic UA + `Referer`, modest pacing,
  honor HTTP 429 with exponential backoff (Cloudflare + API Gateway front Musora). One persisted
  session; avoid repeated logins.

---

## 6. Resolve + download (engine)

Per lesson (drumdrop engine, already proven):
1. GROQ resolve → manifest + metadata + resource links.
2. `yt-dlp` on `hlsManifestUrl`: quality-capped best video + audio, `--write-subs --sub-langs all`,
   merge to mp4; stream progress into the job/WebSocket layer (parse yt-dlp `--progress-template`
   as Youtarr already does).
3. Fetch `resources[]`, the four `mp3_*` play-along stems, `assignments[].sheet_music_image_url`,
   and the poster — into per-lesson subfolders.
4. Write the NFO (`src/nfo.mjs`).

Quality is global + per-follow (best / 2160 / 1440 / 1080 / 720 / 480), mirroring Youtarr's
resolution config. Output layout: `<library>/<Course or Instructor>/<NN> - <Lesson>/…`.

---

## 7. Metadata & media-server integration

Reuse Youtarr's NFO generator (already mirrored in `src/nfo.mjs`), thumbnail/poster handling, and
Plex library-refresh module. NFO fields map from the Sanity payload (title, plot=description,
premiered=`published_on`, runtime, studio=brand, actor=instructors, genre, difficulty tag,
uniqueid=`railcontent_id`, set=series). Jellyfin/Kodi/Emby work via the sidecar NFO; Plex via the
existing OAuth refresh call.

---

## 8. Risks & open questions

- **Sanity-quirk fragility:** content resolve relies on open-read Sanity. If Musora gates it, the
  resolve path must move behind authenticated `api.musora.com` calls. *Mitigation:* isolate all
  Sanity access in `sanityClient`; design it to accept auth headers so the switch is one module.
- **Permission ids:** the GROQ templates embed a permission id (`[92]`). Derive it from the
  logged-in account's `/content/user/permissions` rather than hardcoding. Confirm behavior for
  content requiring permissions the account lacks (should be filtered out — correct).
- **Instructor follows:** instructors aren't a child-subtree; needs the "lessons by instructor"
  query/endpoint (capture from a live instructor page during implementation).
- **Soundslice / non-Bitmovin lessons:** `video_player ∈ {soundslice, youtube, vimeo}` lessons
  aren't standard HLS; handle/skip gracefully (notation embeds aren't downloadable video).
- **Manifest signature lifetime:** resolve fresh per lesson, download promptly (don't cache URLs).
- **Rate-limiting / detection:** keep traffic human-paced; this is the main operational risk.
- **ToS:** personal use only; the app must not facilitate redistribution (no public sharing
  features).

---

## 9. Build sequence (phases)

1. **Fork & strip** — fork Youtarr; remove YouTube Data API, Takeout import, YouTube-specific
   validators/filters; get it booting.
2. **Engine integration** — port `src/*.mjs` into `server/modules/musora/`; wire `downloadExecutor`
   to call the Musora downloader; emit Youtarr-shaped progress/artifacts.
3. **Data model** — migrations: `channels→follows`, `videos→lessons`, add `musora_account`;
   adjust models/associations.
4. **Auth** — login flow, encrypted credential storage, permissions fetch; replace cookies UI.
5. **Catalog & follows** — follow any node; subtree enumeration; manual queue.
6. **Scheduler** — cron auto-sync with diff + download archive.
7. **Resources & metadata** — resources/stems/sheet-music/poster + NFO + Plex refresh.
8. **Web UI** — relabel channel→follow, video→lesson; login form; search/add follows.
9. **Docker & docs** — image (Node + yt-dlp + ffmpeg), compose, README/CONFIG.

Each phase is independently testable; the standalone CLI remains the engine's integration test.

## 10. Out of scope (YAGNI)
- Downloading non-video content beyond the listed resources (forum posts, comments).
- Multi-user accounts / sharing.
- Anything that redistributes or re-publishes downloaded media.
- Mobile apps; the responsive web UI (inherited from Youtarr) suffices.

## 11. Testing
- **Engine:** integration tests via the CLI against known ids (dry-run enumeration; single-lesson
  resolve shape) + unit tests for `catalog` flatten, `withId` substitution, `nfo` output.
- **Shell:** keep Youtarr's jest suites for reused modules; add tests for new migrations/auth.
- **CI:** extend `.github/workflows/ci.yml` with lint + tests once the fork lands.
