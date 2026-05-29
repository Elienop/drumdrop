import test from 'node:test';
import assert from 'node:assert/strict';

test('all modules import without throwing', async () => {
  await assert.doesNotReject(async () => {
    await import('../src/util.mjs');
    await import('../src/sanity.mjs');
    await import('../src/catalog.mjs');
    await import('../src/lesson.mjs');
    await import('../src/nfo.mjs');
  });
});
