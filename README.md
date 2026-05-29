# drumdrop

A personal-archival downloader for **Drumeo / Musora** lessons. Point it at a lesson
or a whole course and it saves the video (best quality + subtitles), the attached
resources (charts, play-along stems, sheet music), and a Plex/Jellyfin-ready `.nfo`.

> **Personal use only.** This is for archiving content **you already pay for**, for your
> own offline viewing. Don't share, re-upload, or redistribute downloaded material.
> Using it is subject to Musora's Terms of Use. No DRM is circumvented — Musora serves
> these lessons as plain HLS.

## Status

Proof-of-concept **core engine** (the catalog → resolve → download pipeline), validated
end-to-end on real content. The roadmap is a Youtarr-style self-hosted app (web UI, job
queue, scheduler, auto-sync) built around this engine.

## Requirements

- Node.js ≥ 20 (uses built-in `fetch`)
- [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) and `ffmpeg` on `PATH`

## Usage

```bash
node src/cli.mjs <lessonOrCourseId | musoraUrl> [options]
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
node src/cli.mjs 409918                 # a single lesson
node src/cli.mjs 409875                 # a whole course (all its lessons)
node src/cli.mjs 409875 --dry-run       # preview the course tree
```

## How it works

1. **Catalog** — Musora content lives in Sanity CMS as a tree keyed by numeric
   `railcontent_id`. The engine walks the subtree under a given node to enumerate leaf
   lessons (`src/catalog.mjs`).
2. **Resolve** — one GROQ query per lesson returns the playable HLS manifest URL plus
   metadata and resource links (`src/lesson.mjs`, `queries/`).
3. **Download** — `yt-dlp` pulls the manifest (best quality + subtitles); resources,
   play-along stems, sheet music, and a poster are fetched alongside; an `.nfo` is
   written for media servers.
