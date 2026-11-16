import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const rule = (selector) => {
  const row = template.split('\n').find((line) => line.trim().startsWith(`${selector} {`));
  assert.ok(row, `${selector} rule not found`);
  return row;
};

test('path column scrolls and the hop card never clips its diff', () => {
  assert.match(rule('.path-col'), /overflow: auto/);
  assert.doesNotMatch(rule('.path-col .path-hop'), /overflow: hidden/);
  assert.match(rule('.path-col .path-hop'), /flex: none/);
  assert.doesNotMatch(rule('.path-col :is(.code-block, .code-excerpt)'), /overflow: hidden/);
});

test('diff block keeps its own scroll within a viewport-bound height', () => {
  assert.match(rule('.path-hop .code-diff'), /height: min\(24rem, 60vh\)[^}]*overflow: auto/);
});

test('path panel height does not grow while the page scrolls', () => {
  assert.match(template, /innerHeight - 16 - Math\.max\(ref\.getBoundingClientRect\(\)\.top, 8\)/);
});

test('ticking the current hop advances to the next one and never wraps', () => {
  assert.match(template, /box\.matches\('\.review-flow-box'\) \|\| !box\.checked \|\| hopOf\(box\) !== current/);
  assert.match(template, /at < cards\.length - 1\) select\(at \+ 1\)/);
});
