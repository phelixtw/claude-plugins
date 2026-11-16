import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { findChrome } from '../bin/visual-check.mjs';
import { desktopBrowser } from './helpers/desktop-browser.mjs';
import { reviewPure } from './helpers/review-pure.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const chrome = process.env.ARCHIFY_CHROME ? findChrome() : null;
const { archifyDiffLines, flowLines, flowState, reviewStates, nodeSeen } = reviewPure();

// Shape of a real report: `channel-gate` shows an import hunk plus the hunk `handoff-gate` shows; `gate-vistest` shows only the test file.
const G = 'lib/gate.dart';
const T = 'test/vis_test.dart';
const importHunk = '@@ -3,2 +3,3 @@\n a\n+import b;\n c';
const gateHunk = '@@ -44,3 +45,4 @@\n x\n-  return a;\n+  return a &&\n+      b;';
const testHunk = '@@ -10,1 +10,3 @@\n y\n+late bool p;\n+p = true;';
const files = [{ path: G, status: 'modified', additions: 3, deletions: 1 }, { path: T, status: 'modified', additions: 2, deletions: 0 }];
const flows = [
  { id: 'handoff-gate', from: 'handoff', to: 'gate', code: { from_symbol: '_route', to_symbol: 'canShowOverlay', path: G, line: 45, diff: gateHunk } },
  { id: 'channel-gate', from: 'channel', to: 'gate', code: { from_symbol: 'isPip', to_symbol: 'canShowOverlay', path: G, line: 4, diff: importHunk + '\n' + gateHunk } },
  { id: 'gate-vistest', from: 'gate', to: 'vistest', code: { from_symbol: 'canShowOverlay', to_symbol: 'main', path: T, line: 11, diff: testHunk } },
];
const tick = (done, flow) => ({ ...done, ...Object.fromEntries(flowLines(flow).map((key) => [key, true])) });
const byId = (id) => flows.find((flow) => flow.id === id);

test('a file waits only on flows that show its code, not on flows of its node that show another file', () => {
  const done = tick(tick({}, byId('handoff-gate')), byId('channel-gate'));
  const states = reviewStates(files, flows, done);
  assert.equal(states[G].state, 'seen');
  assert.deepEqual(states[G].flows, ['handoff-gate', 'channel-gate']);
  assert.equal(states[T].state, 'unseen');
  assert.equal(nodeSeen([G], flows, states, done), true);
});

test('code ticked in one flow counts in every flow that shows it; extra lines leave a flow partial', () => {
  const done = tick({}, byId('handoff-gate'));
  assert.equal(flowState(byId('channel-gate'), done), 'partial');
  assert.equal(reviewStates(files, flows, done)[G].state, 'partial');
  const twin = { id: 'twin', code: { path: G, diff: gateHunk.replace(' x\n', ' other context\n') } };
  assert.equal(flowState(twin, done), 'seen', 'the same changed lines under different context are the same code');
  assert.equal(flowState(byId('channel-gate'), tick(done, byId('channel-gate'))), 'seen');
});

test('two commits changing different code at one line number do not share a tick', () => {
  const first = { id: 'c1', code: { path: G, diff: '@@ -50,1 +50,1 @@\n-a\n+first' } };
  const second = { id: 'c2', code: { path: G, diff: '@@ -50,1 +50,1 @@\n-first\n+second' } };
  assert.equal(flowState(second, tick({}, first)), 'unseen');
});

test('deleted files and unnumbered hunks still tick by line', () => {
  const removed = { id: 'r1', code: { path: G, diff: '@@ -1,2 +0,0 @@\n-a\n-b' } };
  assert.deepEqual(flowLines(removed), [G + '|-1|a', G + '|-2|b']);
  assert.equal(flowState({ id: 'r2', code: removed.code }, tick({}, removed)), 'seen');
  const snippet = { id: 's1', code: { path: G, diff: '-old\n+new' } };
  assert.deepEqual(flowLines(snippet), [G + '|-|old', G + '|+|new']);
});

test('diff numbering matches git: `\\ No newline` takes no number, `:N` numbers new lines only', () => {
  const nums = (diff) => archifyDiffLines(diff).map((row) => row.kind + row.num);
  assert.deepEqual(nums('@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file'),
    ['hunk', 'ctx1', 'del2', 'note', 'add2', 'note']);
  assert.deepEqual(nums('@@ foo.dart:120\n x\n-y\n+z'), ['hunk', 'ctx120', 'del', 'add121']);
  assert.deepEqual(nums('-a\n+b'), ['del', 'add']);
  assert.deepEqual(archifyDiffLines('@@ -1 +1 @@\n-a\n@@ -9 +9 @@\n+b').map((row) => row.hunk), [1, 1, 2, 2]);
});

test('review page: restored tabs carry checkboxes and ticks follow the code across tabs', {
  skip: chrome ? false : 'Set ARCHIFY_CHROME to run real-browser review checks.',
}, async (t) => {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-review-tick-'));
  t.after(() => fs.rmSync(scratch, { recursive: true, force: true }));
  const spec = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'PROJ-1 review ticks', locale: 'en' },
    components: [
      { id: 'handoff', label: 'handoff.dart', pos: [40, 40], size: [220, 70], type: 'frontend' },
      { id: 'gate', label: 'gate.dart', pos: [340, 180], size: [220, 70], type: 'frontend', code: { files: [{ path: G, summary: 'Gate.', diff: importHunk + '\n' + gateHunk }] } },
      { id: 'channel', label: 'channel.dart', pos: [760, 180], size: [220, 70], type: 'frontend' },
      { id: 'vistest', label: 'vis_test.dart', pos: [40, 320], size: [220, 70], type: 'frontend', code: { files: [{ path: T, summary: 'Test.', diff: testHunk }] } },
    ],
    connections: flows.map(({ id, from, to, code }) => ({ id, from, to, label: id, code })),
    changes: { commits: [{ sha: 'abc1234', subject: 'fix' }], files },
  };
  const input = path.join(scratch, 'spec.json');
  const output = path.join(scratch, 'review.html');
  fs.writeFileSync(input, JSON.stringify(spec));
  const rendered = spawnSync(process.execPath, [path.join(root, 'renderers/architecture/render-architecture.mjs'), input, output], { encoding: 'utf8' });
  assert.equal(rendered.status, 0, rendered.stderr);

  const browser = desktopBrowser(chrome);
  t.after(() => browser.close());
  const session = await browser.sessionPromise;
  const send = (method, params = {}) => browser.cdp.send(method, params, session);
  async function run(expression) {
    const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    assert.equal(r.exceptionDetails, undefined, r.exceptionDetails?.exception?.description);
    return r.result?.value;
  }
  const loaded = browser.cdp.waitFor('Page.loadEventFired', session);
  await send('Page.navigate', { url: pathToFileURL(output).href + '#ref=tab&tab=path-handoff-gate' });
  await loaded;

  assert.equal(await run(`document.querySelectorAll('.path-panel:not([hidden]) .review-flow-box').length`), 1, 'a tab opened from a copied link has its checkbox');
  await run(`Archify.pathTabs.restore('path-channel-gate'); Archify.pathTabs.restore('path-gate-vistest'); true`);
  const state = () => run(`(() => {
    const box = (id) => { const b = document.querySelector('input[data-review-flow="' + id + '"]'); return b.indeterminate ? 'partial' : b.checked ? 'seen' : 'unseen'; };
    const row = (p) => [...document.querySelectorAll('.review-file')].find((item) => item.querySelector('.review-name').title === p).getAttribute('data-state');
    return { handoff: box('handoff-gate'), channel: box('channel-gate'), vistest: box('gate-vistest'), gateFile: row(${JSON.stringify(G)}), testFile: row(${JSON.stringify(T)}),
      gateMark: !!document.querySelector('.diagram-container > svg [data-node-id="gate"] [data-review-seen-mark]') };
  })()`);
  const click = (id) => run(`document.querySelector('input[data-review-flow="${id}"]').click(); true`);

  await click('handoff-gate');
  assert.deepEqual(await state(), { handoff: 'seen', channel: 'partial', vistest: 'unseen', gateFile: 'partial', testFile: 'unseen', gateMark: false });
  await click('channel-gate');
  assert.deepEqual(await state(), { handoff: 'seen', channel: 'seen', vistest: 'unseen', gateFile: 'seen', testFile: 'unseen', gateMark: true });
  await click('handoff-gate');
  assert.deepEqual(await state(), { handoff: 'unseen', channel: 'partial', vistest: 'unseen', gateFile: 'partial', testFile: 'unseen', gateMark: false });

  // Collapsing the file tree hands its width to the diff panel; expanding gives it back.
  const widths = () => run(`new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(() => done({
    tree: document.querySelector('.review-tree').getBoundingClientRect().width,
    panel: document.querySelector('.path-panel:not([hidden])').getBoundingClientRect().width,
    files: !!document.querySelector('.review-file').offsetParent,
    expanded: document.getElementById('btn-tree').getAttribute('aria-pressed'),
  }))))`);
  const open = await widths();
  await run(`document.getElementById('btn-tree').click(); true`);
  const shut = await widths();
  assert.ok(shut.tree < open.tree / 3, `collapsed tree ${shut.tree}px vs ${open.tree}px`);
  assert.ok(shut.panel > open.panel + 100, `diff panel ${shut.panel}px vs ${open.panel}px`);
  assert.deepEqual([shut.files, shut.expanded], [false, 'false']);
  await run(`document.getElementById('btn-tree').click(); true`);
  assert.deepEqual(await widths(), open);
});
