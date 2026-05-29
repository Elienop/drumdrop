import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { randomBytes, createCipheriv, createDecipheriv } from 'node:crypto';
import { configDir, secretKeyPath } from './config.mjs';

const ALGO = 'aes-256-gcm';

function loadKey() {
  const path = secretKeyPath();
  if (!existsSync(path)) {
    mkdirSync(configDir(), { recursive: true });
    writeFileSync(path, randomBytes(32), { mode: 0o600 });
  }
  const key = readFileSync(path);
  if (key.length !== 32) throw new Error('secret.key must be 32 bytes');
  return key;
}

// Returns base64( iv(12) | tag(16) | ciphertext )
export function encrypt(plaintext) {
  const key = loadKey();
  const iv = randomBytes(12);
  const cipher = createCipheriv(ALGO, key, iv);
  const ct = Buffer.concat([cipher.update(String(plaintext), 'utf8'), cipher.final()]);
  return Buffer.concat([iv, cipher.getAuthTag(), ct]).toString('base64');
}

export function decrypt(blob) {
  const key = loadKey();
  const buf = Buffer.from(blob, 'base64');
  const iv = buf.subarray(0, 12);
  const tag = buf.subarray(12, 28);
  const ct = buf.subarray(28);
  const decipher = createDecipheriv(ALGO, key, iv);
  decipher.setAuthTag(tag);
  return Buffer.concat([decipher.update(ct), decipher.final()]).toString('utf8');
}
