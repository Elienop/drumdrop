import test from 'node:test';
import assert from 'node:assert/strict';
import { applyPermissions } from '../src/sanity.mjs';

test('applyPermissions keeps the default [92] when env is unset', () => {
  const q = 'x array::intersects(permission_v2, [92]) y';
  assert.equal(applyPermissions(q), 'x array::intersects(permission_v2, [92]) y');
});

test('applyPermissions leaves non-permission queries untouched', () => {
  assert.equal(applyPermissions('*[railcontent_id == 1]{title}'), '*[railcontent_id == 1]{title}');
});
