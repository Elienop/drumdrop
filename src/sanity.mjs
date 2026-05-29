import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { BROWSER_UA } from './util.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const QUERIES_DIR = join(HERE, '..', 'queries');

// Musora's content lives in Sanity, fronted by this open-read GROQ endpoint.
// (No auth/cookie/token is sent by the web app for these reads.)
export const SANITY_BASE =
  'https://sanity.musora.com/4032r8py/apicdn/v2021-06-07/production_v2/v4';

// Queries longer than this are sent as POST (URLs have practical length limits).
const GET_MAX = 1500;

const REQ_HEADERS = {
  'User-Agent': BROWSER_UA,
  Referer: 'https://app.musora.com/',
  Accept: 'application/json',
};

const queryCache = new Map();

export async function loadQuery(name) {
  if (!queryCache.has(name)) {
    queryCache.set(name, await readFile(join(QUERIES_DIR, `${name}.groq`), 'utf8'));
  }
  return queryCache.get(name);
}

// The captured query templates target a specific lesson/course id; swap in ours.
// Only the leading `railcontent_id == <n>` filter is replaced — the nested
// projection references to `railcontent_id` (no `==`) are left intact.
export function withId(template, id) {
  return template.replace(/railcontent_id\s*==\s*\d+/, `railcontent_id == ${id}`);
}

export async function groq(query) {
  let res;
  if (query.length <= GET_MAX) {
    const url = `${SANITY_BASE}?perspective=published&query=${encodeURIComponent(query)}`;
    res = await fetch(url, { headers: REQ_HEADERS });
  } else {
    res = await fetch(`${SANITY_BASE}?perspective=published`, {
      method: 'POST',
      headers: { ...REQ_HEADERS, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query }),
    });
  }
  if (!res.ok) {
    throw new Error(`Sanity ${res.status}: ${(await res.text()).slice(0, 200)}`);
  }
  const data = await res.json();
  return data.result;
}
