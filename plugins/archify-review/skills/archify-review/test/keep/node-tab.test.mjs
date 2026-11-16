import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome } from './helpers/review-page.mjs';

// A node with no connection (PR-10922: changelog, pubspec) opens as a one-node path tab with its diff and a reviewed checkbox.
const opts = { skip: skipChrome, timeout: 90000 };
const panel = '.path-panel:not([hidden])';
const open = (t) => openPage(t, deliverSpec(scratchDir(t), readFixture('PR-10922')));
const rowState = (name) => `[...document.querySelectorAll('.review-file')].find((r) => r.querySelector('.review-name').title.endsWith(${JSON.stringify(name)})).getAttribute('data-state')`;

test('focusing an unconnected node offers its one-node tab: diff, checkbox and the file summary as change note', opts, async (t) => {
  const p = await open(t);
  await p.run(`Archify.focus.set('changelog', { updateUrl: false, toggle: false }); Archify.pathTabs.sync(); true`);
  assert.equal(await p.run(`document.getElementById('btn-reach-tab').hidden`), false);
  await p.run(`document.getElementById('btn-reach-tab').click(); true`);
  const tab = await p.run(`(() => { const pn = document.querySelector('${panel}'); return {
    id: pn.id,
    files: [...pn.querySelectorAll('.code-diff')].map((d) => d.getAttribute('data-file').split('/').pop()),
    boxes: pn.querySelectorAll('.path-hop input[data-review-file]').length,
    note: pn.querySelector('.path-ops').innerText,
  }; })()`);
  assert.equal(tab.id, 'panel-path-changelog');
  assert.deepEqual(tab.files, ['CHANGELOG.md']);
  assert.equal(tab.boxes, 1);
  const summary = readFixture('PR-10922').components.find((c) => c.id === 'changelog').code.files[0].summary;
  assert.ok(tab.note.includes(summary.replace(/[`*]/g, '').slice(0, 10)), tab.note);
  assert.equal(await p.run(rowState('CHANGELOG.md')), 'unseen');
  await p.run(`document.querySelector('${panel} .path-hop input[data-review-file]').click(); true`);
  assert.equal(await p.run(rowState('CHANGELOG.md')), 'seen');
});

test('a file tree file no flow cites opens its node tab; the tab link restores it', opts, async (t) => {
  const p = await open(t);
  await p.run(`[...document.querySelectorAll('.review-name')].find((b) => b.title.endsWith('pubspec.yaml')).click(); true`);
  assert.equal(await p.run(`document.querySelector('${panel}').id`), 'panel-path-pubspec');
  await p.run(`document.querySelector('#tab-path-pubspec').parentElement.querySelector('.ref-copy-tab').click(); true`);
  await p.waitUntil('window.__copied.length === 1');
  const link = await p.run('window.__copied[0]');
  assert.match(link, /[#&]tab=path-pubspec(&|$)/);
  const fresh = await openPage(t, p.html, link.slice(link.indexOf('#')));
  assert.equal(await fresh.run(`document.querySelector('${panel}')?.id`), 'panel-path-pubspec');
  assert.deepEqual(await fresh.run(`[...document.querySelectorAll('${panel} .code-diff')].map((d) => d.getAttribute('data-file').split('/').pop())`), ['pubspec.yaml']);
});

test('a node popover shows the file and its change note but no diff; its button reads "查看改動" and opens the tab with the diff', opts, async (t) => {
  const p = await open(t);
  await p.run(`Archify.focus.set('ctrl', { updateUrl: false, toggle: false }); Archify.pathTabs.sync(); true`);
  const chip = await p.run(`(() => { const c = document.querySelector('.focus-chip'); return {
    code: c.hasAttribute('data-code'),
    files: c.querySelectorAll('[data-node-file], .code-file').length,
    diffs: [...c.querySelectorAll('.code-diff')].filter((d) => d.offsetHeight > 0).length,
    button: document.getElementById('btn-reach-tab').textContent.trim(),
  }; })()`);
  assert.equal(chip.code, true);
  assert.ok(chip.files > 0, 'the file is listed');
  assert.equal(chip.diffs, 0, 'no diff in the popover');
  assert.equal(chip.button, '查看改動');
  await p.run(`document.getElementById('btn-reach-tab').click(); true`);
  assert.ok(await p.run(`[...document.querySelectorAll('${panel} .code-diff')].some((d) => d.offsetHeight > 0)`), 'the opened tab shows the diff');
});
