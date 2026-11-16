import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const start = template.indexOf('      function renderOps(');
const end = template.indexOf("      // Only this hop's own code");
assert.ok(start > 0 && end > start, 'renderOps source not found');

function fakeEl(tag, cls, text) {
  const node = { tag, cls: cls || '', text: text || '', kids: [] };
  Object.defineProperty(node, 'firstChild', { get() { return node.kids[0] || null; } });
  node.appendChild = (kid) => { node.kids.push(kid); return kid; };
  Object.defineProperty(node, 'textContent', { set() { node.kids = []; node.text = ''; }, get() { return node.text; } });
  return node;
}
const flat = (node) => [node.cls + ':' + node.text, ...node.kids.flatMap(flat)];

function run({ action, briefs }) {
  const calls = [];
  const codeData = {
    node: (id) => (briefs[id] ? { brief: { role: briefs[id] } } : null),
    richText: (target, text) => { target.text = text; return target; },
    renderInto: (body, code) => { body.appendChild(fakeEl('p', 'brief-role', code.brief.role)); calls.push(code); },
  };
  const hopCode = () => (action ? { action } : {});
  const body = template.slice(start, end);
  const renderOps = new Function('el', 'viewerText', 'labelOf', 'hopCode', 'Archify',
    `${body}; return renderOps;`)(fakeEl, (key) => key, (id) => id, hopCode, { codeData });
  const target = fakeEl('div');
  renderOps(target, { edges: [{ from: 'plugin', to: 'overlay' }] }, 0);
  return flat(target).join('\n');
}

test('user action block appears only when the hop has an action', () => {
  const withAction = run({ action: 'press Home', briefs: {} });
  assert.match(withAction, /viewer\.path\.userAction/);
  assert.match(withAction, /path-action:press Home/);
  assert.doesNotMatch(run({ briefs: {} }), /viewer\.path\.userAction/);
});

test('briefs are not rendered; a hop with no action or story shows the empty state', () => {
  const out = run({ briefs: { plugin: 'plugin role', overlay: 'overlay role' } });
  assert.doesNotMatch(out, /plugin role|overlay role|viewer\.path\.components/);
  assert.match(out, /viewer\.path\.noOps/);
  assert.doesNotMatch(run({ action: 'press Home', briefs: {} }), /viewer\.path\.noOps/);
});

test('a hop without an action never renders a from-to card', () => {
  const out = run({ briefs: {} });
  assert.doesNotMatch(out, /→/);
  assert.doesNotMatch(out, /path-step|path-loc/);
});
