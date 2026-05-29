import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { groq, loadQuery, withId } from './sanity.mjs';
import { buildNfo } from './nfo.mjs';
import {
  BROWSER_UA,
  ensureDir,
  fetchToFile,
  pad2,
  runYtDlp,
  sanitizeName,
  warn,
} from './util.mjs';

// Resolve a single lesson's full payload (video manifest + metadata + resources).
export async function resolveLesson(id) {
  const result = await groq(withId(await loadQuery('resolve_lesson'), id));
  return result?.[0] ?? null;
}

function formatSelector(quality) {
  if (!quality || quality === 'best') return 'bv*+ba/b';
  const h = parseInt(quality, 10);
  if (!Number.isFinite(h)) return 'bv*+ba/b';
  return `bv*[height<=${h}]+ba/b[height<=${h}]/bv*+ba/b`;
}

// The 2x2 matrix of play-along stems (drums on/off x click on/off).
const MP3_FIELDS = {
  mp3_no_drums_no_click_url: 'play-along (no drums, no click).mp3',
  mp3_no_drums_yes_click_url: 'play-along (no drums, click).mp3',
  mp3_yes_drums_no_click_url: 'play-along (drums, no click).mp3',
  mp3_yes_drums_yes_click_url: 'play-along (drums, click).mp3',
};

// Download everything for one lesson into "<dir>/<NN> - <title>/".
export async function downloadLesson(
  lesson,
  { dir, index, quality = 'best', resourcesOnly = false } = {}
) {
  const base = `${pad2(index)} - ${sanitizeName(lesson.title)}`;
  const lessonDir = join(dir, base);
  await ensureDir(lessonDir);
  const summary = { id: lesson.id, video: false, resources: 0, mp3: 0, sheets: 0, nfo: false };

  // 1) Video (best quality + all subtitles), via yt-dlp on the HLS manifest.
  const hls = lesson.video?.hlsManifestUrl;
  if (!resourcesOnly && hls) {
    await runYtDlp([
      '--user-agent', BROWSER_UA,
      '--referer', 'https://player.vimeo.com/',
      '-f', formatSelector(quality),
      '--merge-output-format', 'mp4',
      '--write-subs', '--sub-langs', 'all',
      '--no-warnings', '--newline',
      '-o', join(lessonDir, `${base}.%(ext)s`),
      hls,
    ]);
    summary.video = true;
  } else if (!resourcesOnly && !hls) {
    warn(`no video manifest for "${lesson.title}" (player: ${lesson.video?.video_player ?? 'n/a'})`);
  }

  // 2) Poster / thumbnail.
  const thumb = lesson.thumbnail || lesson.video?.video_poster_image_url;
  if (thumb) {
    await fetchToFile(thumb, join(lessonDir, `${base}-poster.jpg`)).catch((e) =>
      warn('poster:', e.message)
    );
  }

  // 3) Downloadable resources (PDF charts, ZIP packs).
  for (const r of lesson.resources || []) {
    if (!r?.resource_url) continue;
    const name = sanitizeName(r.resource_name || r.resource_url.split('/').pop());
    try {
      await fetchToFile(r.resource_url, join(lessonDir, 'resources', name));
      summary.resources++;
    } catch (e) {
      warn('resource:', e.message);
    }
  }

  // 4) Play-along stems.
  for (const [field, fname] of Object.entries(MP3_FIELDS)) {
    const url = lesson[field];
    if (!url) continue;
    try {
      await fetchToFile(url, join(lessonDir, 'play-along', fname));
      summary.mp3++;
    } catch (e) {
      warn('play-along:', e.message);
    }
  }

  // 5) Sheet music (per assignment).
  let sheetNo = 0;
  for (const a of lesson.assignments || []) {
    if (!a?.sheet_music_image_url) continue;
    sheetNo++;
    const ext = (a.sheet_music_image_url.split('?')[0].split('.').pop() || 'png').slice(0, 4);
    const name = `${pad2(sheetNo)} - ${sanitizeName(a.title || 'assignment')}.${ext}`;
    try {
      await fetchToFile(a.sheet_music_image_url, join(lessonDir, 'sheet-music', name));
      summary.sheets++;
    } catch (e) {
      warn('sheet-music:', e.message);
    }
  }

  // 6) NFO metadata sidecar.
  try {
    await writeFile(join(lessonDir, `${base}.nfo`), buildNfo(lesson));
    summary.nfo = true;
  } catch (e) {
    warn('nfo:', e.message);
  }

  return summary;
}
