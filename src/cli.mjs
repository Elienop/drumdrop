#!/usr/bin/env node
import { join } from 'node:path';
import { resolveLessonIds } from './catalog.mjs';
import { resolveLesson, downloadLesson } from './lesson.mjs';
import { ensureDir, err, extractId, log, pad2, sanitizeName, warn } from './util.mjs';
import { login, me, loadCookie, saveCreds } from './auth.mjs';
import { rmSync, existsSync } from 'node:fs';
import { credsPath, cookiePath } from './config.mjs';
import { createInterface } from 'node:readline/promises';

const HELP = `drumdrop — download a Drumeo/Musora lesson or whole course (personal archival)

Usage:
  node src/cli.mjs <lessonOrCourseId | musoraUrl> [options]

Options:
  --out <dir>          output directory (default ./downloads)
  --quality <q>        best | 2160 | 1440 | 1080 | 720 | 480   (default best)
  --limit <N>          only the first N lessons
  --whole-course       from a lesson, walk up and grab the entire parent course
  --resources-only     skip video; fetch only PDFs / play-along audio / sheet music
  --dry-run            list what would be downloaded, download nothing
  -h, --help           show this help

Account:
  node src/cli.mjs login       log in (prompts, or set MUSORA_EMAIL/MUSORA_PASSWORD)
  node src/cli.mjs whoami      show the logged-in account
  node src/cli.mjs logout      clear saved session + credentials

Examples:
  node src/cli.mjs 409918                 # one lesson
  node src/cli.mjs 409875                 # a whole course (all its lessons)
  node src/cli.mjs https://app.musora.com/drumeo/lessons/course/409875/409918 --whole-course
`;

function parseArgs(argv) {
  const args = {
    _: [],
    out: './downloads',
    quality: 'best',
    limit: 0,
    dryRun: false,
    whole: false,
    resourcesOnly: false,
    help: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--out') args.out = argv[++i];
    else if (a === '--quality') args.quality = argv[++i];
    else if (a === '--limit') args.limit = parseInt(argv[++i], 10) || 0;
    else if (a === '--dry-run') args.dryRun = true;
    else if (a === '--whole-course') args.whole = true;
    else if (a === '--resources-only') args.resourcesOnly = true;
    else if (a === '-h' || a === '--help') args.help = true;
    else args._.push(a);
  }
  return args;
}

// For non-interactive/headless use, set MUSORA_EMAIL / MUSORA_PASSWORD instead of typing.
async function prompt(question) {
  const rl = createInterface({ input: process.stdin, output: process.stdout });
  const answer = await rl.question(question);
  rl.close();
  return answer.trim();
}

async function cmdLogin() {
  const email = process.env.MUSORA_EMAIL || (await prompt('Musora email: '));
  const password = process.env.MUSORA_PASSWORD || (await prompt('Musora password: '));
  const { user } = await login(email, password);
  saveCreds(email, password);
  log(`✓ Logged in${user?.email ? ` as ${user.email}` : ''}. Session + credentials saved.`);
}

async function cmdWhoami() {
  const cookie = loadCookie();
  if (!cookie) { err('Not logged in. Run: drumdrop login'); process.exit(1); }
  const user = await me(cookie);
  if (!user) { err('Session expired. Run: drumdrop login'); process.exit(1); }
  log(`Logged in as ${user.email || user.display_name || user.id || '(unknown)'}`);
}

async function cmdLogout() {
  for (const p of [cookiePath(), credsPath()]) if (existsSync(p)) rmSync(p);
  log('✓ Logged out (cleared saved session + credentials).');
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const cmd = args._[0];
  if (cmd === 'login') return cmdLogin();
  if (cmd === 'whoami') return cmdWhoami();
  if (cmd === 'logout') return cmdLogout();
  if (args.help || args._.length === 0) {
    log(HELP);
    process.exit(args.help ? 0 : 1);
  }

  const targetId = extractId(args._[0]);
  if (!targetId) {
    err(`Could not parse a content id from: ${args._[0]}`);
    process.exit(1);
  }

  log(`Resolving ${args.whole ? 'whole course for' : 'target'} id ${targetId} …`);
  const { rootId, lessonIds } = await resolveLessonIds(targetId, { whole: args.whole });
  if (lessonIds.length === 0) {
    err('No lessons found (content gated, or unknown id).');
    process.exit(1);
  }
  let ids = lessonIds;
  if (args.limit > 0) ids = ids.slice(0, args.limit);
  log(`Found ${lessonIds.length} lesson(s) under root ${rootId}${args.limit ? `, taking first ${ids.length}` : ''}.`);

  // Resolve metadata first (gives titles + the course/series name for foldering).
  const resolved = [];
  let courseTitle = null;
  for (let i = 0; i < ids.length; i++) {
    const id = ids[i];
    let lesson = null;
    try {
      lesson = await resolveLesson(id);
    } catch (e) {
      warn(`resolve ${id} failed: ${e.message}`);
    }
    if (!lesson) {
      resolved.push({ id, failed: true });
      continue;
    }
    if (!courseTitle) courseTitle = lesson.parent_content_data?.[0]?.title || `content-${rootId}`;
    log(`  [${pad2(i + 1)}/${ids.length}] ${id}  ${lesson.title}${lesson.video?.hlsManifestUrl ? '' : '  (no video)'}`);
    resolved.push({ id, lesson, index: i + 1 });
  }

  const outDir = join(args.out, sanitizeName(courseTitle || `content-${rootId}`));
  if (args.dryRun) {
    log(`\n[dry-run] would download ${resolved.filter((r) => !r.failed).length} lesson(s) into: ${outDir}`);
    return;
  }
  await ensureDir(outDir);

  const tally = { downloaded: 0, failed: 0 };
  for (const r of resolved) {
    if (r.failed) {
      tally.failed++;
      continue;
    }
    try {
      log(`\n▼ [${pad2(r.index)}] ${r.lesson.title}`);
      const s = await downloadLesson(r.lesson, {
        dir: outDir,
        index: r.index,
        quality: args.quality,
        resourcesOnly: args.resourcesOnly,
      });
      tally.downloaded++;
      log(`  ✓ video:${s.video} resources:${s.resources} play-along:${s.mp3} sheets:${s.sheets} nfo:${s.nfo}`);
    } catch (e) {
      tally.failed++;
      err(`  lesson ${r.id} failed: ${e.message}`);
    }
  }

  log(`\nDone → ${outDir}\n  downloaded: ${tally.downloaded}  failed: ${tally.failed}`);
}

main().catch((e) => {
  err(e?.stack || e?.message || String(e));
  process.exit(1);
});
