import test from 'node:test';
import assert from 'node:assert/strict';
import { sanitizeName, pad2 } from '../src/util.mjs';

test('sanitizeName replaces path separators and illegal chars', () => {
  assert.equal(sanitizeName('A/B: "C" <D>?'), 'A-B- -C- -D--');
});

test('sanitizeName collapses whitespace and trims', () => {
  assert.equal(sanitizeName('  Day  1   Workout  '), 'Day 1 Workout');
});

test('sanitizeName strips trailing dots/spaces', () => {
  assert.equal(sanitizeName('Lesson.'), 'Lesson');
});

test('sanitizeName falls back to "untitled" for empty input', () => {
  assert.equal(sanitizeName(''), 'untitled');
  assert.equal(sanitizeName(null), 'untitled');
});

test('sanitizeName truncates to max length', () => {
  assert.equal(sanitizeName('x'.repeat(200), 10).length, 10);
});

test('pad2 zero-pads single digits', () => {
  assert.equal(pad2(1), '01');
  assert.equal(pad2(12), '12');
});

import { extractId } from '../src/util.mjs';

test('extractId parses a bare numeric id', () => {
  assert.equal(extractId('409918'), 409918);
});

test('extractId takes the last numeric segment of a Musora URL', () => {
  assert.equal(
    extractId('https://app.musora.com/drumeo/lessons/course/409875/409918'),
    409918
  );
});

test('extractId returns null when there is no number', () => {
  assert.equal(extractId('nope'), null);
});
