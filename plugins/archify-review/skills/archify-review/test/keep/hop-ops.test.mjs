import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { root } from './helpers/review-page.mjs';

const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const start = template.indexOf('      function renderOps(');
const end = template.indexOf("      // Only this hop's own code");
assert.ok(start > 0 && end > start, 'renderOps source not found');

function fakeEl(tag, cls, text) {
  const node = { cls: cls || '', text: text || '', kids: [] };
  Object.defineProperty(node, 'firstChild', { get() { return node.kids[0] || null; } });
  node.appendChild = (kid) => { node.kids.push(kid); return kid; };
  Object.defineProperty(node, 'textContent', { set() { node.kids = []; node.text = ''; }, get() { return node.text; } });
  return node;
}
const flat = (node) => [node.cls + ':' + node.text, ...node.kids.flatMap(flat)];

function run(code) {
  const codeData = { richText: (target, text) => { target.text = text; return target; } };
  const renderOps = new Function('el', 'viewerText', 'hopCode', 'Archify', `${template.slice(start, end)}; return renderOps;`)(
    fakeEl, (key) => key, () => code, { codeData });
  const target = fakeEl('div');
  renderOps(target, { edges: [{ from: 'a', to: 'b' }] }, 0);
  return flat(target).join('\n');
}

test('a hop with an action shows the action and not the story', () => {
  const out = run({ action: 'press', story: 'tailor sews' });
  assert.match(out, /viewer\.path\.userAction/);
  assert.match(out, /path-action:press/);
  assert.doesNotMatch(out, /viewer\.path\.story|tailor sews/);
});

test('a hop without an action shows the story', () => {
  assert.match(run({ story: 'tailor sews' }), /viewer\.path\.story[\s\S]*path-action:tailor sews/);
});

test('a hop with neither shows the empty state', () => {
  assert.match(run({}), /viewer\.path\.noOps/);
});
