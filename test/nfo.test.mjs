import test from 'node:test';
import assert from 'node:assert/strict';
import { buildNfo } from '../src/nfo.mjs';

const lesson = {
  id: 409918,
  title: 'Course Kick-Off',
  description: '<p>Welcome &amp; enjoy</p>',
  difficulty_string: 'Intermediate',
  brand: 'drumeo',
  published_on: '2024-06-11T15:00:00.000000Z',
  length_in_seconds: 90,
  instructor: [{ name: 'El Estepario Siberiano' }],
  genre: [{ name: 'Rock' }],
  parent_content_data: [{ title: '30-Day Independence' }],
};

test('buildNfo emits a movie element with the title', () => {
  const xml = buildNfo(lesson);
  assert.match(xml, /<movie>/);
  assert.match(xml, /<title>Course Kick-Off<\/title>/);
});

test('buildNfo strips HTML from the description and escapes entities', () => {
  const xml = buildNfo(lesson);
  assert.match(xml, /<plot>Welcome &amp; enjoy<\/plot>/);
  assert.doesNotMatch(xml, /<p>/);
});

test('buildNfo maps series, premiered, instructor and uniqueid', () => {
  const xml = buildNfo(lesson);
  assert.match(xml, /<set><name>30-Day Independence<\/name><\/set>/);
  assert.match(xml, /<premiered>2024-06-11<\/premiered>/);
  assert.match(xml, /<actor><name>El Estepario Siberiano<\/name>/);
  assert.match(xml, /<uniqueid type="musora" default="true">409918<\/uniqueid>/);
});
