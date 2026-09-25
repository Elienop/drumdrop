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
  yt-dlp uses deno for that, and an outdated yt-dlp fails with HTTP 403. Use the official
  release binary from [yt-dlp's releases page](https://github.com/yt-dlp/yt-dlp/releases)
  (it's what the Docker image installs) and keep it updated. The Docker image bundles all
  three, but its yt-dlp is only as fresh as the image: it is downloaded when the image is
  built, at each drumdrop release, so an image that hasn't been rebuilt in a while can fail
  on songs (tracked as D3 in [BACKLOG.md](BACKLOG.md)).

## Environment

All configuration is read from the environment at call time. Everything is optional —
the defaults give a working setup with no env at all.

| Variable | Default | Effect |
| --- | --- | --- |
| `DRUMDROP_CONFIG_DIR` | `~/.config/drumdrop` | Directory for all at-rest state: `drumdrop.db` (follow/download state), `secret.key` (credential-encryption key), `credentials.enc` (encrypted login), `session.cookie` (saved session). Override to relocate the whole config directory. |
| `DRUMDROP_DOWNLOADS_DIR` | `./downloads` | Root directory for downloads when no `--out` is given. `--out` still overrides it per run. Downloads in progress are written in its `.drumdrop-in-progress/` folder, and a finished one is placed from there (see [Plex library](#plex-library-single-parent-bind-mount)). |
| `DRUMDROP_LIBRARY_DIR` | _(none)_ | Optional Plex library. When set, each finished lesson folder is **moved** into this dir at the same path relative to the downloads root — a single copy, Sonarr-style. The downloads dir is then pure scratch for in-progress downloads; Plex watches a directory of **only** finished files and never the partials. drumdrop records the library path as the lesson's location, and Plex owns the file from there (no host-path mapping). Empty disables the move: finished lessons are placed in the downloads dir. For an **instant, atomic** move, downloads and library must be on **one filesystem as the process/container sees it** (see [single-parent bind mount](#run-with-docker)); across filesystems it falls back to a copy-then-delete. Changing it later? Move the files with it: see [Plex library](#plex-library-single-parent-bind-mount). |
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
drumdrop follow @jared-falk             # follow an instructor by slug,
drumdrop follow @'Jared Falk'           # by name,
drumdrop follow @https://app.musora.com/drumeo/coaches/jared-falk/31880   # or by coach page
drumdrop follow @jared-falk --brand singeo --quality 1080
drumdrop follows                        # list everything you follow
drumdrop unfollow 3                     # stop following (id from `drumdrop follows`)
drumdrop sync                           # download every not-yet-downloaded lesson
drumdrop sync --limit 5                 # cap NEW downloads this run
drumdrop sync --dry-run                 # record what would be downloaded, download nothing
```

An instructor is named by slug, by name, or by a link to their coach page, with `@` or
with `--instructor`. One leading `@` is dropped on every path, so
`--instructor @jared-falk`, and `@jared-falk` typed into the web UI's Instructor tab (as
its preview shows an instructor), work too; `@@jared-falk` doesn't. A name is lower-cased
and each run of spaces becomes a hyphen, so `@'Jared Falk'` and `@Jared-Falk` both follow
`jared-falk`; only unaccented letters, digits, spaces and hyphens are taken. A coach-page
link is read by its path, `/<brand>/coaches/<slug>`, with or without the number after it;
its host isn't checked. `--brand` must be `drumeo`, `pianote`, `guitareo`, `singeo` or
`playbass`, in any letter case (`--brand Pianote`), for a node follow as for an
instructor. It defaults to `drumeo`, or, for an instructor, to the brand in a coach-page
link, and a `--brand` that differs from the link's is refused. A coach-page link given as
a lesson or course is refused too, since its number is the instructor's. The web UI's
*Add follow* dialog takes the same three forms, and its preview shows the slug and brand
that will be followed. For now one instructor can be followed on one brand only (BACKLOG
D89).

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
downloads them **one at a time** with **automatic retry** (3 attempts, waiting 5s
before the second and 30s before the third). `sync` is the one-shot equivalent of a
single daemon cycle. A lesson whose download fails every attempt is marked failed and
tried again next cycle, unless it was downloaded before and the files it records are
still on disk when the attempt ends: then it stays downloaded, with a note saying the
re-download failed and the earlier download was kept. Syncs leave that lesson alone;
*Download* on the web UI's Lessons page (or *Retry* in the Queue) tries again, and a
download that succeeds clears the note. The files checked are the lesson's recorded
video; for a lesson without one, every file or folder its Plex TV record names, or else
its folder. One that can't be read counts as gone.

So with the library drive unplugged, a failed re-download marks the lesson failed, and
the next sync downloads it again. That retry doesn't wait for the drive. drumdrop
creates a missing library folder, and writes into the empty folder a drive is mounted
on, so the first retry whose download succeeds is placed on the disk underneath and
recorded there, and syncs stop retrying. Once the drive is back, it hides that copy: the
lesson's recorded paths read the drive's older copy again, and the retry's copy takes
space on the other disk, out of sight under the mount point. In the plex-tv layout that
retry's record lists only what it placed (say, the video and its `.nfo`), so once the
drive is back, the drive's other files for the episode (captions, a poster) are recorded
by no lesson, and a *Delete* leaves them. A lesson downloaded for the first time while
the drive is out is placed there the same way, and once the drive is back its recorded
files aren't there. While the library path answers with an error instead (a failing
mount, say), the placement fails: in the default layout the lesson fails and every sync
retries it until the drive is back. In the plex-tv layout the failed move of a lesson
whose library files are recorded falls back to the downloads dir, as any refused move
does (see [Plex library](#plex-library-single-parent-bind-mount)), so the retry is placed and recorded there, the library files
stay recorded, and syncs stop retrying; nothing is lost. Only when the downloads dir sits
on the same failing mount does that fail too, and syncs retry. A
re-download that Musora doesn't return, or that you cancel, while the drive is out ends
skipped instead, and stays skipped when the drive is back; *Download* or *Un-skip* brings
it back (BACKLOG D120, D121).

A lesson Musora doesn't return (locked for your account, or removed) is skipped, and
syncs leave it alone until it is un-skipped. One whose earlier download is still on
disk stays downloaded instead, with a note saying Musora didn't return it and the
earlier download was kept; only its job fails, and syncs leave it alone too.

In the web UI, *Download*, *Retry* and *Un-skip*, and adding a follow, start a sync at
once instead of waiting for the next `--interval`. *Download* starts one even when the
lesson is already queued. Un-skip on a lesson that wasn't skipped, adding a follow that
exists, and a press that is refused (a lesson whose files are being deleted, say) start
none. A press never waits on the daemon: presses made while a sync runs share one more
sync after it, and while the daemon is paused a press starts none; *Resume* starts it.

On startup the daemon reclaims any job left `running` by a previous crash or shutdown,
and removes the folders stopped downloads left in `.drumdrop-in-progress`, then runs
a cycle immediately and again every `--interval`. `Ctrl-C` (SIGINT) or SIGTERM
stops it: a download in progress is stopped and its folder removed, and it starts over
at the next `serve` or looping `daemon` start; the lesson is neither skipped nor failed
for it. `daemon --once` stops the same way on `Ctrl-C` or SIGTERM, but runs no startup
step, so nothing requeues its stopped download until a `serve` or looping `daemon`
starts: until then the job stays `running`, and no sync queues that lesson again
(BACKLOG D95). Run **one** `daemon` or `serve` per database: that startup step takes
every `running` job for a crashed one, so a second process started beside a live one
would queue its download again and run it in the same folder,
`.drumdrop-in-progress/job-<id>/`, removing what the first had written there. Both
yt-dlp runs then write the same names, and yt-dlp renames its finished `.part` file
into place by path, so one can promote the other's partial file, which is then placed
and recorded as finished (traced in the code, not run; BACKLOG D95).

```bash
drumdrop daemon                         # auto-sync every 12h until stopped
drumdrop daemon --interval 6h           # check every 6 hours
drumdrop daemon --once                  # one plan+drain cycle then exit (cron-friendly)
DRUMDROP_DOWNLOADS_DIR=/media/archive drumdrop daemon
```

| `daemon` option | Description |
| --- | --- |
| `--interval <dur>` | Re-check interval as a Go duration, e.g. `6h`, `30m` (default `12h`) |
| `--once` | Run one plan+drain cycle then exit (external cron / testing). It runs no startup recovery: a job a crash or a `Ctrl-C` left `running` waits for `serve` or a looping `daemon` |
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

**Upgrading.** Back up `/config` first, then `docker compose pull && docker compose up -d`. A
new version may update the database once, when it first starts. To go back to an older
version afterwards, restore that backup: an older version still starts, but it doesn't keep
what the new one added up to date.

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
to a copy-then-delete (correct, just not instant). A copied file is flushed to disk before
the download's own copy is removed. A move that is refused or fails is undone (a copy that
fails part-way is taken back out of the library) and logged. The lesson is then placed
in the downloads dir instead and recorded there, and the download still succeeds, unless
that would delete a library file the lesson owns, or stop recording one. That is so for a
lesson whose own folder is in the library, whatever the layout is today (a plex-tv
install can still record a folder the default layout placed before the layout was
switched), and for a lesson a version before the plex-tv record placed in a season
folder, whose files drumdrop can't name (the default layout never names them). Such a
lesson is not placed in downloads, so its library copy stays as it was and stays the one
recorded: the attempt fails instead. Each attempt downloads the lesson again, and when
the last one fails this way the lesson stays downloaded, if its files are on disk (see
[Daemon](#daemon-unattended-auto-sync)), with the note "Couldn't put this lesson in the
library, so its copy there was kept. Check the server log, fix the problem, then Download
again." When the downloads dir sits inside the library, a lesson whose recorded lesson
folder is in the course folder of the downloads dir it would be placed in (an earlier
refused move kept it there, under this title or an older one) isn't in the library: it
is placed in downloads, and its old folder, when its title has changed since, is handled
as any previous folder is (below). A season folder there is still the library's, since
drumdrop never places one in downloads. Any other folder inside the library counts as
the library's, even one inside the downloads dir as written (a course named like the
downloads dir), and so does every folder when the library is the downloads dir. The
exception is a lesson whose recorded folder
is the very folder it would be placed in within downloads (the library is the downloads
dir, and an earlier refused move kept the lesson there): placing it there replaces only
its own files at the names the download brings back, as any re-download does, so it is
placed and recorded. A symlink or a file at that path doesn't count, even a symlink that
leads to the lesson's library folder: the placement would replace it with a new folder
and leave the library folder recorded by no lesson (or the file gone), so the attempt
fails instead. drumdrop decides all this from the paths alone, so it can guess wrong: a
library lesson folder that sits in the downloads course folder counts as downloads (an
instructor named like the downloads dir, or a folder placed before you moved the
library up a level), and a folder recorded under another spelling of the library's path
(`DRUMDROP_LIBRARY_DIR` set through a symlink, or a remount) isn't recognised as the
library's. Either way a refused placement falls back to downloads, which may replace that
folder, or leaves it where it is, recorded by no lesson (BACKLOG D128 and D58). Any other
lesson, a plex-tv one whose library files are recorded or one with
none, is placed in downloads, and the library files it already had stay recorded (see
[Plex TV layout](#plex-tv-layout)). If the copy finished but the download's own folder
can't be removed afterwards, the library copy is kept and recorded, and the folder left
behind is removed at the next `serve` or looping `daemon` start. Anything drumdrop could
not clean up is logged with its path.

**Pointing `DRUMDROP_LIBRARY_DIR` at another folder? Move the files with it.** A lesson
filed in a plex-tv season folder is always looked for under the library setting as it is
now (its record is relative to it, see [Plex TV layout](#plex-tv-layout)). So if you point
the setting at a different folder and leave the files where they were (say you move it up
a level, from `/media/drumeo` to `/media`), drumdrop looks for them in the wrong place.
While any of a lesson's own files is still in its old season folder, and that folder isn't
the same folder as its place under the new setting, drumdrop refuses rather than lose track
of them: a refused placement doesn't fall back to downloads (the attempt fails, and the
lesson stays downloaded with the note "Couldn't put this lesson in the library. Its files
are in the old library folder: move them to the same place in the new one, then Download
again."), and a *Delete* removes nothing (below). That holds wherever the old folder is,
the downloads dir included (a library moved down from it). An old folder it can't read
counts as holding them; the server log says why. The safe order: stop drumdrop, move the
files to the same place, change the setting, then start it again.

- **Move the files to the same place** in the new folder, `<Show>/Season 01/` and not
  loose in it: `/media/drumeo/Beginner Course/Season 01/` goes to
  `/media/Beginner Course/Season 01/`
  (drumdrop looks for each file exactly where it recorded it, and takes one it doesn't find
  there for gone). If you copied them, delete the old copy: that finishes the move. An old
  season folder left empty, or holding only other lessons' files, doesn't block anything.
- **Point the setting back at the old folder only while the new one is still empty.** A
  lesson whose files you moved is then looked for in the old folder, and a *Delete* of it
  says deleted while its files stay; one you copied loses its old copy and keeps the new
  one, recorded by nothing; and a lesson drumdrop placed in the new folder since the change
  is refused in turn.
- **In Docker, moving the library on the host** (re-pointing the bind mount's host folder,
  with `DRUMDROP_LIBRARY_DIR` unchanged): stop the container, move the files to the same
  place, re-point the bind, then start it. drumdrop can't see this change, since the path it
  reads stays the same, so after it a *Delete* of a lesson whose files you left behind says
  deleted and the files stay (BACKLOG D137).
- A library moved or remounted with its files (the old path is gone), and the same folder
  under another spelling (a symlink, a bind path), work as before.
- **Not covered yet** (BACKLOG D137): drumdrop can't see an old folder whose path is gone
  while its files still exist elsewhere, such as an unmounted drive, nor a bind mount
  re-pointed on the host (above). A *Delete* then says deleted and the files stay, as on
  earlier versions: move the files first. The same goes for a lesson kept in downloads
  whose record still names season files in the library. A re-download whose library
  placement *succeeds* after such a change places a new copy under the new setting and
  leaves the old one where it was, recorded by nothing, and so does a refused placement of
  a default-layout lesson whose folder is outside both dirs now.

Every download, with a library or without one, writes into a folder of its own, named after
its job, `<downloads>/.drumdrop-in-progress/job-<id>/`, and only a finished one is placed
where the lesson lives: its folder in the library, or in the downloads dir when there is no
library. Until then nothing outside that folder is touched. So a download that fails, is
skipped, canceled or deleted, or is stopped by a shutdown, leaves the lesson's earlier
files and everything else as they were, and its folder goes when it ends. yt-dlp is the
reason: drumdrop runs it with `--force-overwrites`, so a truncated file is never kept as
finished, and that makes yt-dlp delete an existing video (and its subtitles) at its output
path before it downloads a new one. The `.drumdrop-in-progress` name starts with a dot, and
the folder holds a `.plexignore`, so a Plex library pointed at the downloads dir should not
show a download in progress; whether Plex honours either is unconfirmed (BACKLOG D93). The
one-shot `drumdrop <lessonOrCourseId | musoraUrl>` has no queue, but a download of it that
fails doesn't cost a file already in its lesson folder either.

A download that is retried starts again in the same folder, which can still hold partial
files from the attempt before: yt-dlp's `.part`, `.ytdl`, `.f<number>.<ext>` and
`.temp.<ext>` files, and drumdrop's own `.drumdrop-part` and `.drumdrop-episode`. Before a
download is placed, drumdrop removes them from its folder and its subfolders, so none of
them reaches the lesson's folder or the library.

Placing a download replaces only what it has to: an entry at a path the download produced,
which is the lesson's own earlier file or one no lesson records (say, a file kept when a
follow was removed without its files). Everything else stays. In the default layout the
new files are merged into the lesson's folder, and a subfolder such as `resources/` is
merged the same way, file by file at every depth, so a file in it that the download didn't
bring back (a PDF whose fetch failed this time, or one you put there) stays. A
re-download that brings no video (resources only) leaves the earlier video in the
folder, and it stays the lesson's recorded video. An entry of
another kind than the one the download places at its name (a file where it places a
folder, or a folder where it places a file) is replaced whole.

The lesson's previous folder, when its record names another one (its title changed, the
library was added after it was downloaded, or it was kept in downloads when a move was
refused), goes only when the download brought back every file in it, each at the matching
path (a file named after the old folder matches the one named after the new). Otherwise it
stays where it is, with everything in it, its old video included, and is no longer
recorded: it is yours to check and delete. The log says so, and why:
`⚠ <lesson id> left its previous folder "<path>" where it was, no longer recorded: <why>`,
where the reason is that the new download didn't bring back one of its files, that some of
it could not be read, that another lesson records files in it, that it isn't a real folder,
or that it is inside neither the downloads dir nor the library. A folder another lesson
records anything in is never placed into. Each replaced entry is logged
(`↻ <lesson id> replaced "<path>"`, saying whether it was the lesson's earlier download or
one no lesson recorded). Until the download is recorded, a replaced entry is only set
aside, in a `.drumdrop-in-progress/replaced-<id>/` folder of the same dir (downloads or
library), and it is removed once the download is recorded. A Skip, a delete or a follow
removal that lands while the download is being placed undoes the placement: the new files
go, and the replaced ones go back where they were, except the lesson's own files when a
delete is removing them. A record that fails (a database error) undoes it the same way,
and the download is tried again.

If drumdrop dies during a download (a crash, or the process killed), the download's folder
stays; the next `serve` or looping `daemon` start removes it and queues the download
again. `sync` and `daemon --once` don't: `sync` can't tell a crashed download from one a
running `serve` is doing, so it leaves both, and `--once` runs no startup step either
(BACKLOG D95). `Ctrl-C` during `sync` or `daemon --once` stops the download and removes
its folder. If drumdrop dies while a download is being placed, a `replaced-<id>` folder
can stay as well. It may hold the only copy of a lesson's earlier files, so drumdrop never
removes one: every `serve` or looping `daemon` start logs it (`kept "…": files a placement
set aside when DrumDrop stopped. Check them, then delete the folder`), and it is yours to
check and delete (BACKLOG D93).

drumdrop refuses to start when `DRUMDROP_LIBRARY_DIR` is the downloads dir reached by
another path (a symlink, or one host folder bound twice): every recorded path would then
have two spellings, which drumdrop isn't built for. The same path written the same way is
allowed. In the default layout a finished download is then placed in its lesson folder
there, as with no library; in the plex-tv layout lessons are still filed into
`<Show>/Season 01/` folders inside that one dir. When a move there is refused, the rule
above says whether the lesson is placed in its lesson folder in that dir instead. One an
earlier refused move already kept there always falls back there, but that placement can
fail too: in the default layout it is the very placement that just failed, so it fails
the same way.

Deleting a lesson (or a follow with its files) only ever removes files inside the downloads
and library dirs. A path that reaches outside them through a symlinked folder is refused,
and so is the root of either dir. A path outside both dirs is refused too, even when
nothing is there any more: in the default layout a lesson records its folder by its full
path, so after the library is mounted at a new path its files are elsewhere, and drumdrop
will not call them deleted. The trade-off: a symlink you placed inside the library on
purpose, pointing at another disk, is refused too, so lessons behind it can't be deleted
from drumdrop. If a file can't be removed, the lesson is kept (it reads downloaded, and
records the files still there, or, for a folder outside both dirs, the path it had), the
delete answers with an error, and the detail goes to the server log. Deleting a follow with
its files keeps the follow and all its lessons in that case, so no file is left that
drumdrop no longer tracks. That also depends on the library setting pointing where the
files are: a lesson filed in a plex-tv season folder whose files stayed in the old folder
after `DRUMDROP_LIBRARY_DIR` was pointed at another one can't be deleted until you move them
to the same place in the new library folder (or point the setting back at the old folder,
if the new one is still empty). The delete answers so before it stops any download, and
removes nothing; for a follow with its files, nothing of any of its lessons, and the
follow stays (see above).

A delete stops the lesson's downloads first. While it runs, the lesson can't be downloaded,
retried, skipped or deleted again (each is refused until it finishes), so a delete never
removes files a newer download just wrote; the API's lesson says so (`deleting`). A delete
holds the lesson for two minutes at a time and renews that while it runs, so if drumdrop
stops in the middle of one, the lesson is released by itself two minutes later. A download
a delete stops leaves nothing, as above; one it stops while the download is being placed
is undone, and the lesson's own earlier files are not put back, since the delete removes
them. Removing a follow *without* its files keeps every file its lessons recorded: a
download it stops leaves nothing, not even a finished one that was being placed. It never
stops another follow's download, even one it queued. Canceling a re-download leaves the
lesson's earlier files as they were, and the lesson downloaded if those files are still
on disk (checked as under [Daemon](#daemon-unattended-auto-sync)); otherwise it is
skipped with the note "The download stopped before it finished. Download again to get
this lesson.", as a canceled first download is. A Cancel that lands once
the download is being placed is too late: the download is recorded.

Skipping a lesson stops its queued or running download for good: that download records
nothing and leaves nothing, and the lesson's earlier files stay. A Skip that lands while
the download is being placed undoes the placement, as above. Syncs then leave the lesson
alone until it is un-skipped.

A download that fails every attempt leaves nothing either: the lesson's earlier files, and
everything else, stay as they were. A download is not started at all when Musora doesn't
answer for the lesson (or its answer can't be read), when a record it needs can't be read
(the lesson's own, its follow's, or the other lessons', which say whose files are where),
or when its own folder can't be made. Either way, a lesson whose earlier download is still
on disk stays downloaded, with a note, and syncs leave it alone until you press
*Download* (or *Retry* in the Queue); any other lesson, one whose earlier files are gone
or whose own record can't be read included, is marked failed and tried again next cycle,
as described under [Daemon](#daemon-unattended-auto-sync). A lesson Musora doesn't
return (locked or removed) is not a failure: it is skipped, or kept, as described there
too.

drumdrop writes the files it fetches itself (the poster, resources, play-along audio,
sheet music and the `.nfo`) through the download's own folder, which it holds open, never
through a symlink out of it. yt-dlp, a separate program, writes the video by path, so the
downloads dir must not be writable by anyone you don't trust.

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
  (the same `NN` used in the default layout). Files are flat in the season folder; a
  lesson's `resources/`, `play-along/` and `sheet-music/` folders move in as
  `<episode> resources` and so on.
- drumdrop **records the exact files and folders** each move places in the season folder,
  and acts on that record. Two lessons can share an episode number in one show, and one
  title can extend another (`Five` and `Five [Live]`), so a name alone can't say whose a file
  is. The record is kept relative to `DRUMDROP_LIBRARY_DIR` (`<Show>/Season 01/<entry>`), so
  it stays true if the library is mounted at another path or the setting is spelled another
  way. Point the setting at a different folder without moving the files, and drumdrop
  refuses to delete a lesson filed in a season folder, or to fall back to downloads for it,
  until they're moved (see
  [Plex library](#plex-library-single-parent-bind-mount)). Whether a file is claimed is decided by the file itself, not its spelling: the same
  file reached under another name (a hard link, or another letter case on a
  case-insensitive disk, in the file's name or in any of its folders') counts as claimed
  too.
- If a lesson's record is ever **damaged** (not a list of `<Show>/Season NN/<entry>` paths,
  say after editing the database by hand), drumdrop can't tell what that lesson owns, so it
  refuses every delete and every plex-tv move until the record is fixed, rather than guess;
  the server log names the lesson. To fix it, clear that lesson's record and it falls back
  to name matching: `sqlite3 <config-dir>/drumdrop.db "UPDATE lessons SET library_entries =
  NULL WHERE railcontent_id = <id>"`.
- **Deleting** a lesson removes exactly what it recorded, even if its title has changed
  since; the season folder and every other lesson's files stay. A lesson moved by a version
  before the record existed is matched by name instead: `<episode>.mp4`, `.nfo`,
  `-poster.jpg`, subtitles, song versions `<episode> [Label].mp4`, and the `resources`,
  `play-along` and `sheet-music` folders. Anything another lesson also claims is kept and
  logged, and if drumdrop can't tell which episode name is the lesson's, the delete is
  refused rather than guessed.
- A **re-download** replaces what the lesson's previous download recorded at the names it
  places, and merges a recorded folder such as `<episode> resources` with the one it
  places, file by file, as in the default layout. What the lesson recorded at the same
  episode name that the re-download didn't bring back (captions or a poster it failed to
  fetch this time, a resources folder) stays where it is and stays recorded, so a later
  delete removes it; a re-download that brings no video (resources only) keeps the earlier
  one as the lesson's video. A lesson moved by a version before the record existed keeps,
  the same way, what the name matching above gives it at its episode name, and from then
  on it is recorded. After a title change, the lesson's recorded files at the old episode
  name go, whether or not the re-download brought each back; a recorded folder there goes
  only when the download brought back every file in it, and otherwise stays, no longer
  recorded, and logged as above (one more reason applies here: the new download has no
  folder in its place). A re-download never overwrites or removes another lesson's file:
  if one of its names is taken by another lesson, the move is refused and the lesson is
  placed in downloads instead (logged), and the library files it already had stay
  recorded; when they can't be (a lesson whose files drumdrop can't name, or a folder the
  default layout placed that isn't the lesson's folder in downloads itself), the attempt
  fails instead, as described under [Plex
  library](#plex-library-single-parent-bind-mount). Every write and removal in the library
  goes through the library folder itself, so a symlink planted in it can't send one
  outside: the season folder must resolve inside the library, each copied file is created
  afresh rather than written through whatever is at its name, and on Linux and macOS each
  rename acts on the folders drumdrop holds open, so a folder swapped for a symlink after
  the checks can't redirect it either. A rename there also refuses an entry already at its
  name (`RENAME_NOREPLACE`, `RENAME_EXCL`), except on a filesystem without that flag (some
  network filesystems), where it retries without it and replaces. When a rename refuses,
  the move refuses, as above, and the entry in the way is neither removed nor copied into.
  drumdrop copies instead of renaming only when downloads and the library are on different
  filesystems (volumes on Windows), and a copy that fails removes only what it created. On
  Windows the rename goes by path and replaces an existing entry, so neither guarantee
  holds there. The default layout's move works the same way, and never places into a
  library folder another lesson records anything in. When its move is refused or fails,
  the same rule says whether the lesson is placed in downloads instead (logged): one whose
  folder is already in the library keeps its library copy, and the attempt fails, unless
  that folder is the lesson's folder in downloads itself (see
  [Plex library](#plex-library-single-parent-bind-mount)). A file at one of its names that
  no lesson claims (say, one kept when a follow was deleted without its files) is
  replaced, and that is logged. A name too long for the filesystem (255 bytes) has its
  title shortened; if even that can't fit, the move is refused.
- It shapes **only** the library move: downloads still happen in their own folder, and a
  move failure never costs a file: a half-done move is undone, and the lesson is placed in
  downloads, with the library files it already had still recorded, or else the attempt
  fails and its library copy stays the one recorded. With no
  `DRUMDROP_LIBRARY_DIR` the setting does nothing.
- The `.nfo` written here is a Kodi/Plex **`<episodedetails>`** doc (not the default
  `<movie>`): it carries the episode `<title>`, `<showtitle>`, `<season>`/`<episode>`,
  `<aired>` (publish date), the instructor `<actor>`, and the lesson plot/runtime. It
  replaces the download's own `.nfo` before the move, so it is placed and recorded like
  every other entry. Writing it is non-fatal: a failure logs a warning, the download still
  succeeds, and the episode keeps the download's `<movie>` nfo.

**On the Plex side**, create a **TV Shows** library pointing at `DRUMDROP_LIBRARY_DIR` and
switch its agent to one that reads `.nfo` files — the
[**XBMCnfoTVImporter**](https://github.com/gboudreau/XBMCnfoTVImporter.bundle) plugin is what
the working setup uses — then *Refresh Metadata*. XBMCnfoTVImporter is a third-party plugin,
not part of Plex, and its last commit is from 2019: it only appears in the agent list once
it has been installed into Plex Media Server's `Plug-ins` folder and Plex has been restarted
(its README has the steps). The show won't match TheTVDB, so the
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
   YouTube recordings in its soundslice play-along score. drumdrop finds them in the score
   (`internal/musora/soundslice.go`), then downloads each one (`[Original]` and `[Drumless]`)
   through yt-dlp as its own file (`DownloadLesson` in `internal/musora/download.go`).
