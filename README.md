# drumdrop

A personal-archival downloader for **Drumeo / Musora** lessons. Point it at a lesson
or a whole course and it saves the video (best quality + subtitles), the attached
resources (charts, play-along stems, sheet music), and a Plex/Jellyfin-ready `.nfo`.

> **Personal use only.** This is for archiving content **you already pay for**, for your
> own offline viewing. Don't share, re-upload, or redistribute downloaded material.
> Using it is subject to Musora's Terms of Use. No DRM is circumvented — Musora serves
> these lessons as plain HLS.

## Status

A pure-Go CLI and a Youtarr-style self-hosted app (web UI, job queue, scheduler, auto-sync),
both built around a validated **core engine** (the catalog → resolve → download pipeline) and
proven end-to-end on real content. Releases ship as standalone binaries and a Docker image.

## Requirements

- Go ≥ 1.26 (only to build from source; the release is a single static binary)
- [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) and `ffmpeg` on `PATH`
- For **songs** only: [`deno`](https://deno.com) on `PATH` and a **current** yt-dlp. A song's
  video is a YouTube recording, and YouTube makes yt-dlp solve a JavaScript challenge first —
  yt-dlp uses deno for that, and an outdated yt-dlp fails with HTTP 403. The Docker image
  bundles all three.

## Environment

All configuration is read from the environment at call time. Everything is optional —
the defaults give a working setup with no env at all.

| Variable | Default | Effect |
| --- | --- | --- |
| `DRUMDROP_CONFIG_DIR` | `~/.config/drumdrop` | Directory for all at-rest state: `drumdrop.db` (follow/download state), `secret.key` (credential-encryption key), `credentials.enc` (encrypted login), `session.cookie` (saved session). Override to relocate the whole config directory. |
| `DRUMDROP_DOWNLOADS_DIR` | `./downloads` | Root directory for downloads when no `--out` is given. `--out` still overrides it per run. |
| `DRUMDROP_LIBRARY_DIR` | _(none)_ | Optional Plex library. When set, each finished lesson folder is **moved** into this dir at the same path relative to the downloads root — a single copy, Sonarr-style. The downloads dir is then pure scratch for in-progress downloads; Plex watches a directory of **only** finished files and never the partials. drumdrop records the library path as the lesson's location, and Plex owns the file from there (no host-path mapping). Empty disables the move: the lesson stays in the downloads dir. For an **instant, atomic** move, downloads and library must be on **one filesystem as the process/container sees it** (see [single-parent bind mount](#run-with-docker)); across filesystems it falls back to a copy-then-delete. |
| `DRUMDROP_LAYOUT` | _(none)_ | Library destination layout (case-insensitive). Empty or `default` keeps the per-lesson-subfolder layout (`<library>/Course/NN - Lesson/…`). `plex-tv` switches the library copy to Plex's TV-Shows naming — see [Plex TV layout](#plex-tv-layout). Requires `DRUMDROP_LIBRARY_DIR`; has no effect without one. It shapes **only** the library move target, not the in-progress scratch layout. |
| `DRUMDROP_HOST_DOWNLOADS_DIR` | _(none)_ | For Docker: the host path that the downloads dir is bind-mounted from. The API rewrites lesson paths under the downloads dir to this host path, so the web UI's "Copy path" gives a path that works on the host. Only paths under the downloads dir are rewritten, so with `DRUMDROP_LIBRARY_DIR` set it no longer affects finished lessons (they live in the library). Empty keeps the container paths. |
| `DRUMDROP_LISTEN` | `127.0.0.1:8080` | Address `serve` binds. A non-loopback bind (e.g. `0.0.0.0:8080`, as in the Docker image) refuses to start without `DRUMDROP_API_TOKEN`. |
| `DRUMDROP_API_TOKEN` | _(none)_ | Bearer token for the `/api/*` data plane. Required for any non-loopback bind; the SPA shell stays unauthenticated. |
| `DRUMDROP_CORS_ORIGIN` | _(none)_ | Allowed CORS origin for the HTTP API. Empty disables cross-origin requests. |
| `DRUMDROP_INTERVAL` | `12h` | Default `--interval` for `serve`/`daemon` auto-sync (a Go duration, e.g. `6h`, `30m`). |
| `DRUMDROP_QUALITY` | _(none)_ | Default `--quality` for `serve`/`daemon`/`sync`. Empty means each follow keeps its own saved quality. |
| `DRUMDROP_AUDIO_LANG` | `en` | Preferred audio-track language (ISO 639 code, e.g. `en`, `es`, `pt`). Musora lessons increasingly ship multiple audio renditions (English plus Spanish/Portuguese dubs) and yt-dlp's default pick can land on a dub (an English intro followed by a dubbed body). drumdrop makes yt-dlp prefer the audio **tagged with this language _or_ untagged** (the original/default rendition), falling back to its normal pick only when no such track exists — so single-track lessons are unaffected. Set `any` or `all` to disable the preference entirely. |
| `DRUMDROP_PERMISSION_IDS` | `92` | Comma-separated permission ids substituted into the catalog/resolve GROQ queries; gates which content is resolvable. Malformed values fall back to the default. |
| `MUSORA_EMAIL` | _(none)_ | Login email for non-interactive `login` (skips the prompt). |
| `MUSORA_PASSWORD` | _(none)_ | Login password for non-interactive `login` (skips the prompt). |

## Usage

```bash
go run ./cmd/drumdrop <lessonOrCourseId | musoraUrl> [options]
```

Or build a binary once and run it directly:

```bash
go build -o dist/drumdrop ./cmd/drumdrop   # or: make build
drumdrop <lessonOrCourseId | musoraUrl> [options]
```

| Option | Description |
| --- | --- |
| `--out <dir>` | Output directory (default `./downloads`) |
| `--quality <q>` | `best` \| `2160` \| `1440` \| `1080` \| `720` \| `480` (default `best`) |
| `--limit <N>` | Only the first N lessons |
| `--whole-course` | From a lesson, walk up and grab the entire parent course |
| `--resources-only` | Skip video; fetch only PDFs / play-along audio / sheet music |
| `--dry-run` | List what would be downloaded, download nothing |

```bash
drumdrop 409918                 # a single lesson
drumdrop 409875                 # a whole course (all its lessons)
drumdrop 409875 --dry-run       # preview the course tree
```

### Account

```bash
drumdrop login                  # log in (prompts, or set MUSORA_EMAIL/MUSORA_PASSWORD)
drumdrop whoami                 # show the logged-in account
drumdrop logout                 # clear saved session + credentials
```

### Follows (incremental, deduped archival)

Track courses, series, single lessons, or whole instructors, then `sync` to pull
only what you have not already downloaded. State lives in a local SQLite database
(`<config-dir>/drumdrop.db`), so re-running `sync` never re-downloads a lesson.

```bash
drumdrop follow 409875                  # follow a node (course/series/lesson)
drumdrop follow https://app.musora.com/drumeo/lessons/course/409875   # URL works too
drumdrop follow @aaron-edgar            # follow an instructor by slug
drumdrop follow @aaron-edgar --brand drumeo --quality 1080
drumdrop follows                        # list everything you follow
drumdrop unfollow 3                     # stop following (id from `drumdrop follows`)
drumdrop sync                           # download every not-yet-downloaded lesson
drumdrop sync --limit 5                 # cap NEW downloads this run
drumdrop sync --dry-run                 # record what would be downloaded, download nothing
```

| `sync` option | Description |
| --- | --- |
| `--out <dir>` | Output directory (default `$DRUMDROP_DOWNLOADS_DIR`, else `./downloads`) |
| `--quality <q>` | Override each follow's saved quality |
| `--limit <N>` | Cap the number of NEW downloads this run (`0` = unlimited) |
| `--dry-run` | Expand + record in the database, download nothing |
| `--resources-only` | Skip video; fetch only resources |

### Daemon (unattended auto-sync)

`drumdrop daemon` runs the same plan + download machinery as `sync`, but on a
loop: it periodically re-checks every follow for new lessons, queues them, and
downloads them **one at a time** with **automatic retry** (3 attempts, backoff
5s → 30s → 2m). `sync` is the one-shot equivalent of a single daemon cycle. On
startup the daemon reclaims any job left `running` by a previous crash, then runs
a cycle immediately and again every `--interval`. `Ctrl-C` (SIGINT) or SIGTERM
shuts it down cleanly after the in-flight download finishes.

```bash
drumdrop daemon                         # auto-sync every 12h until stopped
drumdrop daemon --interval 6h           # check every 6 hours
drumdrop daemon --once                  # one plan+drain cycle then exit (cron-friendly)
DRUMDROP_DOWNLOADS_DIR=/media/archive drumdrop daemon
```

| `daemon` option | Description |
| --- | --- |
| `--interval <dur>` | Re-check interval as a Go duration, e.g. `6h`, `30m` (default `12h`) |
| `--once` | Run one plan+drain cycle then exit (external cron / testing) |
| `--out <dir>` | Output directory (default `$DRUMDROP_DOWNLOADS_DIR`, else `./downloads`) |
| `--quality <q>` | Override each follow's saved quality |
| `--resources-only` | Skip video; fetch only resources |

Set `DRUMDROP_DOWNLOADS_DIR` to choose the download root without passing `--out`
on every run (handy for a long-running daemon); `--out` still overrides it per run.

## Web UI

A self-hosted web UI (dashboard, follows, lessons, queue, settings) backed by the
HTTP API and live job progress over SSE. The single-page app shell is served
unauthenticated; the data plane (`/api/*`) honors the bearer token (see
`DRUMDROP_API_TOKEN`), so loading the page never requires a token but every data
call does.

### Develop

Two processes — the Go API and the Vite dev server (which proxies `/api` to the API):

```bash
make dev-api    # go run ./cmd/drumdrop serve   (API on :8080)
make dev-web    # cd web && npm run dev          (UI on :5173)
```

Then open <http://localhost:5173>.

### Release

Build the frontend, embed it into the binary (`-tags webui`), and serve everything
from one process:

```bash
make build-ui            # cd web && npm ci && npm run build, then embed via -tags webui
./dist/drumdrop serve
```

Then open <http://127.0.0.1:8080>.

> `make build` stays UI-free — the default build/test gate is green without a built
> `web/dist`. Only `make build-ui` pulls in the embedded frontend.

## Run with Docker

drumdrop ships as a `linux/amd64` image bundling yt-dlp + deno + ffmpeg with the web UI
embedded (for arm64, macOS or Windows, use a [standalone binary](#standalone-binary)). The
container binds `0.0.0.0`, so it **requires an API token** — generate one with
`openssl rand -hex 32`.

```bash
# docker-compose.yml: set DRUMDROP_API_TOKEN, then:
docker compose up -d
# → web UI + API at http://localhost:3737 (use the token in the UI's gate)
```

`docker run` equivalent:

```bash
docker run -d --name drumdrop -p 3737:8080 \
  -e DRUMDROP_API_TOKEN="$(openssl rand -hex 32)" \
  -e PUID=1000 -e PGID=1000 -e TZ=UTC \
  -v drumdrop-config:/config -v "$PWD/downloads:/downloads" \
  ghcr.io/elienop/drumdrop:latest
```

- `/config` (named volume) holds the SQLite DB + encrypted secret/cookie.
- `/downloads` (host bind) is where your archived lessons land.

The image is fully env-configured via the `DRUMDROP_*` vars in the [Environment](#environment)
table; `PUID`/`PGID`/`TZ` set the runtime user/timezone. The published port maps `3737:8080`.

#### Plex library (single-parent bind mount)

Set `DRUMDROP_LIBRARY_DIR` to **move** every finished lesson folder into a separate
directory that Plex watches — a Sonarr-style split where the file lives in **one**
place (the library) and Plex never sees the in-progress partials in the downloads dir.
The downloads dir becomes pure scratch; Plex owns the path from there.

For an **instant, atomic** move (a plain `rename`), the downloads dir and the library
dir must be one filesystem *as the container sees it*. Bind a **single parent** and
point both dirs inside it:

```yaml
services:
  drumdrop:
    volumes:
      - /mnt/pool/media:/media          # one parent → one filesystem in-container
    environment:
      - DRUMDROP_DOWNLOADS_DIR=/media/downloads/drumdrop
      - DRUMDROP_LIBRARY_DIR=/media/library
```

Point Plex at `/mnt/pool/media/library`. Two **separate** binds (e.g. `./downloads:/downloads`
and `./library:/library`) cross filesystems inside the container, so the move falls back
to a copy-then-delete (correct, just not instant). A move failure is non-fatal: the
download still succeeds and the file stays in the downloads dir.

#### Plex TV layout

Set `DRUMDROP_LAYOUT=plex-tv` (alongside `DRUMDROP_LIBRARY_DIR`) to make the **library
copy** use Plex's TV-Shows naming instead of the default `Course/NN - Lesson/…` folders.
Each course becomes one *show*, each lesson an *episode*:

```
<library>/<Show>/Season 01/
    <Show> - s01e05 - Day 4 — Workout.mp4
    <Show> - s01e05 - Day 4 — Workout.en.vtt     ← sidecars share the episode base
    <Show> - s01e05 - Day 4 — Workout.nfo        ← Kodi/Plex <episodedetails> (title/aired/instructor)
    <Show> - s01e05 - Day 4 — Workout-poster.jpg
```

- **Show** — the course for a node follow; the lesson's parent course for an instructor
  follow (falling back to the instructor name for course-less lessons); else `content-<id>`.
- **Season** is always `01`; the **episode number** is the lesson's position in the course
  (the same `NN` used in the default layout). Files are flat in the season folder.
- It shapes **only** the library move: downloads still happen in the usual scratch layout,
  and a move failure is non-fatal (the file stays in downloads). With no `DRUMDROP_LIBRARY_DIR`
  the setting does nothing.
- The `.nfo` written here is a Kodi/Plex **`<episodedetails>`** doc (not the default
  `<movie>`): it carries the episode `<title>`, `<showtitle>`, `<season>`/`<episode>`,
  `<aired>` (publish date), the instructor `<actor>`, and the lesson plot/runtime. Writing
  it is non-fatal — a write failure logs a warning and the download still succeeds.

**On the Plex side**, create a **TV Shows** library pointing at `DRUMDROP_LIBRARY_DIR` and
switch its agent to one that reads `.nfo` files — the **XBMCnfoTVImporter** plugin is what
the working setup uses — then *Refresh Metadata*. The show won't match TheTVDB, so the
episode number, title, and summary have to come from the local `<episodedetails>` nfo (and
the filenames) — which is exactly what this layout encodes. Enabling **Local Media Assets**
alone was not enough in testing: Plex kept showing generic "Episode N" until the library
used the `.nfo` agent.

### Standalone binary

Prefer no container? Download the archive for your OS/arch from the
[GitHub Releases](https://github.com/elienop/drumdrop/releases) page and extract the `drumdrop`
binary. **`yt-dlp` and `ffmpeg` must be on your `PATH`** (plus `deno` for songs — see
[Requirements](#requirements)) — the binary shells out to them. Configure
it through the `DRUMDROP_*` env vars (see the [Environment](#environment) table) or the `serve`
flags.

## Development

Git hooks live in `scripts/hooks/` (tracked) and enforce [Conventional Commits](https://www.conventionalcommits.org/). Enable them once per clone:

```bash
make hooks   # git config core.hooksPath scripts/hooks
```

- **`commit-msg`** rejects a non-conventional commit subject before the commit is created.
- **`pre-push`** checks the PR title matches the convention (the CI "Conventional title" check) before pushing.

Both use the same rule: `type(scope)!: description` with types `feat fix chore docs refactor perf test build ci style revert`. Bypass once with `git commit --no-verify` / `git push --no-verify`.

## How it works

1. **Catalog** — Musora content lives in Sanity CMS as a tree keyed by numeric
   `railcontent_id`. The engine walks the subtree under a given node to enumerate leaf
   lessons (`internal/musora`).
2. **Resolve** — one GROQ query per lesson returns the playable HLS manifest URL plus
   metadata and resource links (`internal/musora`, embedded `internal/musora/queries/`).
3. **Download** — `yt-dlp` pulls the manifest (best quality + subtitles); resources,
   play-along stems, sheet music, and a poster are fetched alongside; an `.nfo` is
   written for media servers. A **song** has no Musora video of its own: its videos are the
   YouTube recordings in its soundslice play-along score, so each one (`[Original]` and
   `[Drumless]`) is downloaded through yt-dlp as its own file (`internal/musora/soundslice.go`).
