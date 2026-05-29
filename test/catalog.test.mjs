import test from 'node:test';
import assert from 'node:assert/strict';
import { withId } from '../src/sanity.mjs';
import { collectLeaves } from '../src/catalog.mjs';

test('withId replaces only the first railcontent_id filter', () => {
  const tpl = "*[railcontent_id == 409918 && status]{ 'parent_id': parent_content_reference[0]->railcontent_id }";
  const out = withId(tpl, 123);
  assert.match(out, /railcontent_id == 123 && status/);
  // the projection reference (no `==`) is untouched
  assert.match(out, /parent_content_reference\[0\]->railcontent_id/);
});

test('collectLeaves returns the node itself when it has no children', () => {
  const node = { railcontent_id: 7 };
  assert.deepEqual(collectLeaves(node).map((n) => n.railcontent_id), [7]);
});

test('collectLeaves flattens nested children depth-first in order', () => {
  const tree = {
    railcontent_id: 1,
    children: [
      { railcontent_id: 2, children: [{ railcontent_id: 4 }, { railcontent_id: 5 }] },
      { railcontent_id: 3, children: [] },
    ],
  };
  assert.deepEqual(collectLeaves(tree).map((n) => n.railcontent_id), [4, 5, 3]);
});

test('collectLeaves treats null children as a leaf', () => {
  assert.deepEqual(collectLeaves({ railcontent_id: 9, children: null }).map((n) => n.railcontent_id), [9]);
});
