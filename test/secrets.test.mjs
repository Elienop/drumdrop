import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

test('encrypt then decrypt returns the original plaintext', async () => {
  process.env.DRUMDROP_CONFIG_DIR = mkdtempSync(join(tmpdir(), 'drumdrop-'));
  const { encrypt, decrypt } = await import('../src/secrets.mjs');
  const secret = 'hunter2:correct horse';
  const blob = encrypt(secret);
  assert.notEqual(blob, secret);
  assert.equal(decrypt(blob), secret);
});

test('a tampered blob fails to decrypt (auth tag)', async () => {
  process.env.DRUMDROP_CONFIG_DIR = mkdtempSync(join(tmpdir(), 'drumdrop-'));
  const { encrypt, decrypt } = await import('../src/secrets.mjs');
  const blob = encrypt('secret');
  const bad = Buffer.from(blob, 'base64');
  bad[bad.length - 1] ^= 0xff;
  assert.throws(() => decrypt(bad.toString('base64')));
});
