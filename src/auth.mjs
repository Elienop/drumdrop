import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { configDir, cookiePath, credsPath } from './config.mjs';
import { encrypt, decrypt } from './secrets.mjs';
import { BROWSER_UA } from './util.mjs';

const API = 'https://api.musora.com/api/user-management-system/v1';
const SESSION_NAME = 'musora_platform_backend_session';

function headers(extra = {}) {
  return { 'User-Agent': BROWSER_UA, Accept: 'application/json', ...extra };
}

function writeFile0600(path, data) {
  mkdirSync(configDir(), { recursive: true });
  writeFileSync(path, data, { mode: 0o600 });
}

// Pull the session cookie out of a Set-Cookie list -> "name=value" (no attributes).
function pickSessionCookie(setCookieList) {
  for (const c of setCookieList || []) {
    if (c.startsWith(`${SESSION_NAME}=`)) return c.split(';')[0].trim();
  }
  return null;
}

export function loadCookie() {
  return existsSync(cookiePath()) ? readFileSync(cookiePath(), 'utf8').trim() : null;
}

export function saveCreds(email, password) {
  writeFile0600(credsPath(), encrypt(JSON.stringify({ email, password })));
}

export function loadCreds() {
  if (!existsSync(credsPath())) return null;
  return JSON.parse(decrypt(readFileSync(credsPath(), 'utf8')));
}

export async function login(email, password) {
  const res = await fetch(`${API}/sessions`, {
    method: 'POST',
    headers: headers({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ email, password }),
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok || !body?.user) {
    throw new Error(`Login failed: ${body?.message || res.status}`);
  }
  const cookie = pickSessionCookie(res.headers.getSetCookie());
  if (!cookie) throw new Error('Login succeeded but no session cookie was returned');
  writeFile0600(cookiePath(), cookie);
  return { user: body.user, cookie };
}

export async function me(cookie) {
  const res = await fetch(`${API}/me`, { headers: headers({ Cookie: cookie }) });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error(`/me failed: ${res.status}`);
  return res.json();
}

// Return a valid session cookie, re-logging-in with stored creds on 401.
export async function ensureSession() {
  const cookie = loadCookie();
  if (cookie && (await me(cookie))) return cookie;
  const creds = loadCreds();
  if (!creds) throw new Error('Not logged in — run `drumdrop login` first.');
  const { cookie: fresh } = await login(creds.email, creds.password);
  return fresh;
}
