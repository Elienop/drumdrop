import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

function withTmpConfig() {
  process.env.DRUMDROP_CONFIG_DIR = mkdtempSync(join(tmpdir(), 'drumdrop-'));
}

function stubFetch(handler) {
  const orig = globalThis.fetch;
  globalThis.fetch = handler;
  return () => { globalThis.fetch = orig; };
}

function jsonRes(body, { status = 200, setCookie = [] } = {}) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { getSetCookie: () => setCookie },
    async json() { return body; },
    async text() { return JSON.stringify(body); },
  };
}

test('login posts credentials, stores the session cookie, returns the user', async () => {
  withTmpConfig();
  const restore = stubFetch(async (url, opts) => {
    assert.match(url, /user-management-system\/v1\/sessions$/);
    assert.equal(JSON.parse(opts.body).email, 'a@b.com');
    return jsonRes({ user: { id: 1, email: 'a@b.com' } }, {
      setCookie: ['musora_platform_backend_session=abc123; Path=/; HttpOnly'],
    });
  });
  const { login, loadCookie } = await import('../src/auth.mjs');
  const { user } = await login('a@b.com', 'pw');
  assert.equal(user.email, 'a@b.com');
  assert.equal(loadCookie(), 'musora_platform_backend_session=abc123');
  restore();
});

test('ensureSession re-logs-in when the saved cookie is rejected (401)', async () => {
  withTmpConfig();
  const { saveCreds } = await import('../src/auth.mjs');
  saveCreds('a@b.com', 'pw');
  let calls = 0;
  const restore = stubFetch(async (url) => {
    if (url.endsWith('/v1/me')) return jsonRes({ message: 'Unauthenticated' }, { status: 401 });
    if (url.endsWith('/v1/sessions')) {
      calls++;
      return jsonRes({ user: { id: 1 } }, { setCookie: ['musora_platform_backend_session=fresh'] });
    }
    throw new Error('unexpected ' + url);
  });
  const { ensureSession } = await import('../src/auth.mjs');
  const cookie = await ensureSession();
  assert.equal(cookie, 'musora_platform_backend_session=fresh');
  assert.equal(calls, 1);
  restore();
});

test('ensureSession throws a helpful error when no credentials are stored', async () => {
  withTmpConfig();
  const { ensureSession } = await import('../src/auth.mjs');
  await assert.rejects(ensureSession(), /drumdrop login/);
});
