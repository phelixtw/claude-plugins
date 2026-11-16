import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const start = template.indexOf('/* hop-step pure */');
const end = template.indexOf('/* end hop-step pure */');
assert.ok(start > 0 && end > start, 'hop-step source not found');
const { stepHop, edgeHop } = new Function(`${template.slice(start, end)}; return { stepHop, edgeHop };`)();

test('stepping moves one hop and stops at both ends', () => {
  assert.equal(stepHop(0, 1, 3), 1);
  assert.equal(stepHop(1, -1, 3), 0);
  assert.equal(stepHop(0, -1, 3), 0);
  assert.equal(stepHop(2, 1, 3), 2);
  assert.equal(stepHop(0, 1, 1), 0);
});

test('deep-link edge selects its hop and unknown edges keep the current one', () => {
  assert.equal(edgeHop(['a-to-b', 'b-to-c'], 'b-to-c', 0), 1);
  assert.equal(edgeHop(['a-to-b', 'b-to-c'], 'nope', 1), 1);
  assert.equal(edgeHop([undefined], 'x', 0), 0);
});
