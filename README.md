# drumdrop

A personal-archival downloader for **Drumeo / Musora** lessons. Point it at a lesson
or a whole course and it saves the video (best quality + subtitles), the attached
resources (charts, play-along stems, sheet music), and a Plex/Jellyfin-ready `.nfo`.

> **Personal use only.** This is for archiving content **you already pay for**, for your
> own offline viewing. Don't share, re-upload, or redistribute downloaded material.
> Using it is subject to Musora's Terms of Use. No DRM is circumvented — Musora serves
> these lessons as plain HLS.

## Status

A pure-Go CLI built around a validated **core engine** (the catalog → resolve → download
pipeline), proven end-to-end on real content. The roadmap is a Youtarr-style self-hosted
app (web UI, job queue, scheduler, auto-sync) built around this engine.

## Requirements

- Go ≥ 1.26 (only to build from source; the release is a single static binary)
- [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) and `ffmpeg` on `PATH`

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
| `--out <dir>` | Output directory (default `./downloads`) |
| `--quality <q>` | Override each follow's saved quality |
| `--limit <N>` | Cap the number of NEW downloads this run (`0` = unlimited) |
| `--dry-run` | Expand + record in the database, download nothing |
| `--resources-only` | Skip video; fetch only resources |

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
   written for media servers.
