import test from 'node:test';
import assert from 'node:assert/strict';
import { formatSelector } from '../src/lesson.mjs';

test('formatSelector returns best by default', () => {
  assert.equal(formatSelector(), 'bv*+ba/b');
  assert.equal(formatSelector('best'), 'bv*+ba/b');
});

test('formatSelector caps to a numeric height', () => {
  assert.equal(formatSelector('720'), 'bv*[height<=720]+ba/b[height<=720]/bv*+ba/b');
});

test('formatSelector ignores non-numeric junk', () => {
  assert.equal(formatSelector('huge'), 'bv*+ba/b');
});
