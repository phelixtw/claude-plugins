import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { findChrome } from '../bin/visual-check.mjs';
import { serializeScriptJson } from '../renderers/shared/utils.mjs';
import { desktopBrowser } from './helpers/desktop-browser.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const chrome = process.env.ARCHIFY_CHROME ? findChrome() : null;
const tool = 'code-review-graph 2.4.0';
const risk = {
  screen: { score: 0.81, symbol: 'Screen.build', factors: [{ key: 'flows', score: 0.25, count: 6 }, { key: 'security', score: 0, count: 0 }, { key: 'tests', score: 0.3, count: 0 }, { key: 'churn', score: 0.26, count: 9 }],
    callers: [{ file: 'lib/app.dart', line: 88, symbol: 'build' }], untested: ['Screen.build'], tool },
  coord: { score: 0.52, callers: [], untested: [], tool },
};

function page(scratch, slice) {
  const spec = JSON.parse(fs.readFileSync(path.join(root, 'test/fixtures/review-tiers.json'), 'utf8'));
  const sha = 'abc1234def';
  spec.components.forEach((c) => { c.code.files = [{ path: `lib/${c.label}`, summary: c.label }]; });
  spec.components[0].code.commit = sha;
  spec.connections[0].code = { from_symbol: 'tap', to_symbol: 'open', path: 'lib/screen.dart', line: 2, diff: '@@ -1,1 +1,2 @@\n a\n+b' };
  spec.changes = { commits: [{ sha, subject: 's', new: true }, { sha: 'fed9876543', subject: 'older' }], files: spec.components.map((c) => ({ path: `lib/${c.label}`, status: 'modified' })) };
  const input = path.join(scratch, 'spec.json');
  const output = path.join(scratch, 'review.html');
  fs.writeFileSync(input, JSON.stringify(spec));
  const rendered = spawnSync(process.execPath, [path.join(root, 'renderers/architecture/render-architecture.mjs'), input, output], { encoding: 'utf8' });
  assert.equal(rendered.status, 0, rendered.stderr);
  const html = fs.readFileSync(output, 'utf8').replace(/(<script id="archify-code-data" type="application\/json">)([\s\S]*?)(<\/script>)/, (_, open, json, close) => {
    const data = JSON.parse(json);
    Object.entries(risk).forEach(([id, value]) => { data.nodes[id].risk = value; });
    data.changes.slice = slice;
    return open + serializeScriptJson(data) + close;
  });
  fs.writeFileSync(output, html);
  return output;
}

test('review page: risk badges in every layout, popup tabs and the Next button', {
  skip: chrome ? false : 'Set ARCHIFY_CHROME to run real-browser review checks.',
}, async (t) => {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-review-risk-'));
  t.after(() => fs.rmSync(scratch, { recursive: true, force: true }));
  const output = page(scratch, { report: 'AC1-1', base: 'a'.repeat(40), head: 'b'.repeat(40), feature: 'Redeem', batch: 1, next: { kind: 'batch', files: 23 } });

  const browser = desktopBrowser(chrome);
  t.after(() => browser.close());
  const session = await browser.sessionPromise;
  const send = (method, params = {}) => browser.cdp.send(method, params, session);
  async function run(expression) {
    const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    assert.equal(r.exceptionDetails, undefined, r.exceptionDetails?.exception?.description);
    return r.result?.value;
  }
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  const loaded = browser.cdp.waitFor('Page.loadEventFired', session);
  await send('Page.navigate', { url: pathToFileURL(output).href });
  await loaded;

  const corners = () => run(`Object.fromEntries([...document.querySelectorAll('.diagram-container > svg g[data-node-id]')].map((g) => {
    const box = g.querySelector('rect:not(.c-mask)');
    const pills = [...g.querySelectorAll(':scope > [data-review-badge]')].map((b) => { const r = b.querySelector('rect'); return [b.textContent, +r.getAttribute('x') + +r.getAttribute('width'), +r.getAttribute('y') + +r.getAttribute('height') / 2, +r.getAttribute('x')]; }).sort((a, b) => b[1] - a[1]);
    return [g.getAttribute('data-node-id'), { right: +box.getAttribute('x') + +box.getAttribute('width'), top: +box.getAttribute('y'), pills }];
  }))`);
  function assertCorners(c, layout) {
    const [riskPill, newPill] = c.screen.pills;
    assert.deepEqual([riskPill[0], riskPill[1], riskPill[2]], ['High 0.81', c.screen.right, c.screen.top], `${layout}: risk badge flush with the top-right corner`);
    assert.equal(newPill[0], 'new commit', `${layout}: new-commit pill beside the risk badge`);
    assert.ok(newPill[1] <= riskPill[3], `${layout}: the two pills do not overlap`);
    assert.deepEqual(c.coord.pills.map((p) => p[0]), ['Medium 0.52'], layout);
    assert.deepEqual(c.store.pills, [], `${layout}: no risk, no badge`);
  }
  assert.equal(await run('Archify.layoutSwitch.current()'), 'mvvm');
  assertCorners(await corners(), 'mvvm');
  await run(`document.querySelector('#layout-switch button[data-layout="flutter"]').click(); true`);
  assert.equal(await run('Archify.layoutSwitch.current()'), 'flutter');
  const flutter = await corners();
  assertCorners(flutter, 'flutter');
  assert.equal(await run(`document.querySelectorAll('.diagram-container > svg [data-review-badge]').length`), 3, 'switching repaints, never duplicates');

  await run(`Archify.focus.set('screen'); true`);
  const popup = () => run(`(() => { const chip = document.getElementById('focus-chip'); const box = (s) => chip.querySelector(s).getBoundingClientRect();
    return { title: document.getElementById('focus-label').textContent, tabs: [...chip.querySelectorAll('[role=tab]')].map((b) => b.textContent + (b.getAttribute('aria-selected') === 'true' ? '*' : '')),
      shown: chip.querySelector('[role=tabpanel]:not([hidden])').innerText, copyRow: box('#btn-focus-copy').top === box('#btn-reach-tab').top && box('#btn-focus-copy').height === box('#btn-reach-tab').height,
      eyebrows: [...chip.querySelectorAll('.relationship-lens-eyebrow')].filter((e) => e.offsetParent).length }; })()`);
  const brief = await popup();
  assert.equal(brief.title, 'lib/screen.dart');
  assert.deepEqual(brief.tabs, ['Brief*', 'Blast radiusHigh 0.81']);
  assert.match(brief.shown, /^Commit abc1234def/, 'the commit heads the Brief tab');
  assert.equal(brief.copyRow, true, 'Copy link sits on the View changes row at the same height');
  assert.equal(brief.eyebrows, 0);
  await run(`document.querySelectorAll('#focus-chip [role=tab]')[1].click(); true`);
  const blast = await popup();
  assert.deepEqual(blast.tabs, ['Brief', 'Blast radiusHigh 0.81*']);
  for (const line of ['In 6 flows', '+0.25', '0 tests reach it', '9 commits to the file', 'Score from Screen.build', 'lib/app.dart:88 build', 'Screen.build', tool]) assert.ok(blast.shown.includes(line), line);
  assert.ok(!blast.shown.includes('Security-looking name'), 'a zero term gets no bar');
  await run(`Archify.focus.set('coord'); document.querySelectorAll('#focus-chip [role=tab]')[1].click(); true`);
  assert.match((await popup()).shown, /did not break this score down/);
  await run(`Archify.codeData.node('coord').risk = { score: 0, callers: [], untested: [], tool: '${tool}' }; Archify.focus.set('store'); Archify.focus.set('coord'); document.querySelectorAll('#focus-chip [role=tab]')[1].click(); true`);
  assert.equal((await popup()).shown.trim(), tool, 'a zero score with no breakdown shows only the tool line');
  await run(`Archify.focus.set('store'); true`);
  assert.equal(await run(`document.querySelectorAll('#focus-chip [role=tab]').length`), 0, 'no risk, no tabs');

  assert.equal(await run(`document.getElementById('btn-next').innerText.trim()`), '23', 'the button shows only the icon and the count');
  assert.match(await run(`document.getElementById('btn-next').title`), /\(23 files left\)$/);
  await run(`window.copied = null; Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: (v) => { window.copied = v; return Promise.resolve(); } } }); document.getElementById('btn-next').click(); true`);
  await run('new Promise((done) => setTimeout(done, 50))');
  assert.equal(await run('window.copied'), '/archify-review next AC1-1');
  assert.equal(await run(`document.querySelector('.archify-toast').textContent`), 'Copied /archify-review next AC1-1; run it in Claude (1 hop in this batch is not ticked yet)');
});
