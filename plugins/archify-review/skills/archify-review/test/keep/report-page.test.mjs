import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { desktopBrowser } from '../helpers/desktop-browser.mjs';
import { chrome, deliverSpec, readFixture, runCli, scratchDir, skipChrome } from './helpers/review-page.mjs';

// End-to-end checks of the report viewer: review.html?report=<ID> loads reports/<ID>.js,
// and Done / Skip and review ticks live in localStorage across reloads, tabs, tickets and new report versions.
const ID = 'PR-10922';
const OTHER = 'PR-9151';
const spec = readFixture(ID);
const CHANGELOG = 'packages/webview_flutter/webview_flutter_wkwebview/CHANGELOG.md';
const opts = { skip: skipChrome, timeout: 120000 };

function deliverReport(dir, next, id = ID) {
  const input = path.join(dir, `${id}.json`);
  fs.writeFileSync(input, JSON.stringify(next));
  const result = runCli('deliver', 'architecture', input, path.join(dir, 'review.html'), '--report', id, '--json');
  assert.equal(result.status, 0, result.stdout + result.stderr);
  return JSON.parse(result.stdout);
}

// Version 2 of the same ticket: the last added line of one hop changed, the third TODO item gone, one new item.
function secondVersion() {
  const next = structuredClone(spec);
  const conn = next.connections.find((c) => c.id === 'ctrl-page');
  const lines = conn.code.diff.split('\n');
  const last = lines.findLastIndex((l) => l.startsWith('+'));
  lines[last] = `${lines[last]} // changed`;
  conn.code.diff = lines.join('\n');
  const todo = next.cards.find((c) => /TODO/.test(c.title));
  todo.items.splice(2, 1);
  todo.items.unshift('[high][new][code-review] A finding raised by the second review.');
  return next;
}

// One Chrome profile for the whole test; each tab() is a new browser tab on the same localStorage.
function browserFor(t) {
  const browser = desktopBrowser(chrome);
  t.after(() => browser.close());
  const cdp = browser.cdp;
  async function tab(url) {
    const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' });
    const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true });
    const send = (method, params = {}) => cdp.send(method, params, sessionId);
    await send('Page.enable');
    await send('Runtime.enable');
    await send('Page.addScriptToEvaluateOnNewDocument', { source: `
      window.__copied = [];
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: (text) => { window.__copied.push(text); return Promise.resolve(); } } });
    ` });
    const run = async (expression) => {
      const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      assert.equal(r.exceptionDetails, undefined, r.exceptionDetails?.exception?.description || r.exceptionDetails?.text);
      return r.result?.value;
    };
    const settle = async () => {
      // A report page is settled once it reports ready or error; a standalone page has no viewer body.
      const done = 'document.readyState === "complete" && (!!document.documentElement.dataset.reportState || !document.getElementById("archify-viewer-body"))';
      for (let i = 0; i < 200 && !(await run(done)); i++) await new Promise((resolve) => setTimeout(resolve, 50));
      if (await run('typeof Archify === "object"')) await run('Archify.viewerChromeLayout ? Archify.viewerChromeLayout.whenStable() : true');
    };
    const go = async (next) => { await send('Page.navigate', { url: next }); await settle(); };
    const reload = async () => { await send('Page.reload'); await settle(); };
    const close = () => cdp.send('Target.closeTarget', { targetId });
    await go(url);
    return { run, go, reload, close, send };
  }
  return { tab };
}

const copied = async (p, n) => {
  for (let i = 0; i < 60 && (await p.run('window.__copied.length')) < n; i++) await new Promise((r) => setTimeout(r, 50));
  return p.run('window.__copied.at(-1)');
};
const todoStates = (p) => p.run(`[...document.querySelectorAll('.todo-list > li')].map((li) => li.dataset.state || null)`);
const flowState = (p, id) => p.run(`(() => { const b = document.querySelector('input[data-review-flow="${id}"]'); return b.indeterminate ? 'partial' : b.checked ? 'seen' : 'unseen'; })()`);
const fileTicked = (p, file) => p.run(`[...document.querySelectorAll('.review-file')].find((r) => r.querySelector('.review-name').title === ${JSON.stringify(file)}).querySelector('input[data-review-file]').checked`);
const tickTodo = (p, i) => p.run(`document.querySelectorAll('.todo-list > li')[${i}].querySelector('input.todo-check').click(); true`);
const tickFlow = (p, id) => p.run(`document.querySelector('input[data-review-flow="${id}"]').click(); true`);
const tickFile = (p, file) => p.run(`[...document.querySelectorAll('.review-file')].find((r) => r.querySelector('.review-name').title === ${JSON.stringify(file)}).querySelector('input[data-review-file]').click(); true`);
// Flow boxes live in path tabs, which a `#ref=tab` hash opens; this two-hop tab holds both flows the tests tick.
const TAB = 'path-menu-ctrl-page';
async function openTab(p) {
  await p.run(`location.hash = '#ref=tab&tab=${TAB}'; true`);
  for (let i = 0; i < 60 && !(await p.run(`!!document.querySelector('input[data-review-flow="ctrl-page"]')`)); i++) await new Promise((r) => setTimeout(r, 50));
}

async function markSome(p) {
  await tickTodo(p, 0);
  await tickTodo(p, 1);
  await tickTodo(p, 2);
  await openTab(p);
  await tickFlow(p, 'ctrl-page');
  await tickFlow(p, 'menu-ctrl');
  await tickFile(p, CHANGELOG);
}

async function expectMarked(p, what) {
  assert.deepEqual(await todoStates(p), ['do', 'do', 'do', null, null, null], `${what}: TODO ticks`);
  await openTab(p);
  assert.equal(await flowState(p, 'ctrl-page'), 'seen', `${what}: ctrl-page ticks`);
  assert.equal(await flowState(p, 'menu-ctrl'), 'seen', `${what}: menu-ctrl ticks`);
  assert.equal(await fileTicked(p, CHANGELOG), true, `${what}: own file tick`);
}

test('the viewer fills the page from reports/<ID>.js exactly like the standalone artifact', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const standalone = deliverSpec(scratchDir(t), spec);
  const b = browserFor(t);
  const report = await b.tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  const alone = await b.tab(pathToFileURL(standalone).href);
  const shape = `({ title: document.title, lang: document.documentElement.lang, review: document.documentElement.hasAttribute('data-review'),
    container: document.querySelector('.container').innerHTML.length, files: document.querySelectorAll('.review-file').length,
    todo: document.querySelectorAll('.todo-list > li').length, tabs: document.querySelectorAll('[id^="tab-path-"]').length })`;
  assert.equal(await report.run('document.documentElement.dataset.reportState'), 'ready');
  assert.deepEqual(await report.run(shape), await alone.run(shape));
  assert.equal(await report.run('document.readyState'), 'complete');
});

test('a missing report or an invalid id shows the error state', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const b = browserFor(t);
  const url = pathToFileURL(path.join(dir, 'review.html')).href;
  const missing = await b.tab(`${url}?report=PROJ-404`);
  assert.equal(await missing.run('document.documentElement.dataset.reportState'), 'error');
  assert.match(await missing.run(`document.getElementById('archify-report-status').textContent`), /reports\/PROJ-404\.js/);
  const bad = await b.tab(`${url}?report=../x`);
  assert.equal(await bad.run('document.documentElement.dataset.reportState'), 'error');
});

test('the viewer without ?report= lists every delivered report as one-click links, newest first', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  deliverReport(dir, readFixture(OTHER), OTHER);
  const b = browserFor(t);
  const p = await b.tab(pathToFileURL(path.join(dir, 'review.html')).href);
  assert.equal(await p.run('document.documentElement.dataset.reportState'), 'index');
  const links = await p.run(`[...document.querySelectorAll('#archify-report-index a')].map((a) => [a.textContent, new URL(a.href).search])`);
  assert.deepEqual(links, [[OTHER, `?report=${OTHER}`], [ID, `?report=${ID}`]]);
  await p.run(`document.querySelector('#archify-report-index a').click(); true`);
  await new Promise((r) => setTimeout(r, 1500));
  assert.equal(await p.run('document.documentElement.dataset.reportState'), 'ready');
  assert.equal(await p.run('location.search'), `?report=${OTHER}`);
});

test('the theme follows the system until the theme button picks light or dark, and the pick survives a reload', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const b = browserFor(t);
  const p = await b.tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  const theme = () => p.run(`[document.documentElement.dataset.theme, document.documentElement.dataset.themeChoice, localStorage.getItem('archify-review-theme')]`);
  const scheme = async (value) => { await p.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value }] }); await new Promise((r) => setTimeout(r, 200)); };
  await scheme('light');
  assert.deepEqual(await theme(), ['light', 'auto', null]);
  await scheme('dark');
  assert.deepEqual(await theme(), ['dark', 'auto', null]);
  const click = () => p.run(`document.getElementById('btn-theme').click(); true`);
  await click();
  assert.deepEqual(await theme(), ['light', 'light', 'light']);
  await p.reload();
  assert.deepEqual(await theme(), ['light', 'light', 'light']);
  await click();
  assert.deepEqual(await theme(), ['dark', 'dark', 'dark']);
  await scheme('light');
  await click();
  assert.deepEqual(await theme(), ['light', 'auto', null]);
  await p.go(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}&theme=dark`);
  assert.deepEqual(await theme(), ['dark', 'dark', null]);
});

test('a report written before the viewer gained new messages still loads', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const file = path.join(dir, 'reports', `${ID}.js`);
  const old = fs.readFileSync(file, 'utf8').replace(/"viewer\.theme\.[a-z]+":"[^"]*",?/g, '');
  assert.doesNotMatch(old, /viewer\.theme\./);
  fs.writeFileSync(file, old);
  const p = await browserFor(t).tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  assert.equal(await p.run('document.documentElement.dataset.reportState'), 'ready');
  assert.ok(await p.run(`document.getElementById('theme-label').textContent.length > 0`));
});

test('a Code Smell item keeps its tick across a reload', opts, async (t) => {
  const dir = scratchDir(t);
  const next = structuredClone(spec);
  next.cards.find((c) => /TODO/.test(c.title)).items.push('[low][code-smell] delete: unused helper in the test. Nothing replaces it. (a_test.dart:3)');
  deliverReport(dir, next);
  const p = await browserFor(t).tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  const smell = `[...document.querySelectorAll('.todo-list > li')].find((li) => /code-smell/.test(li.textContent))`;
  await p.run(`document.querySelector('.todo-tabs [data-tab=smell]').click(); ${smell}.querySelector('input.todo-check').click(); true`);
  await p.reload();
  assert.equal(await p.run(`${smell}.dataset.state`), 'do');
});

test('a TODO item saved as Done or Skip by an older viewer shows ticked', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const p = await browserFor(t).tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  await tickTodo(p, 0);
  await tickTodo(p, 1);
  await p.run(`(() => { const k = 'archify-review-state:${ID}'; const s = JSON.parse(localStorage.getItem(k)); const keys = Object.keys(s.todo); s.todo[keys[0]] = 'do'; s.todo[keys[1]] = 'skip'; localStorage.setItem(k, JSON.stringify(s)); return true; })()`);
  await p.reload();
  assert.deepEqual(await todoStates(p), ['do', 'do', null, null, null, null]);
  assert.deepEqual(await p.run(`[...document.querySelectorAll('.todo-list > li input.todo-check')].map((b) => b.checked)`), [true, true, false, false, false], 'the "no findings" line has no checkbox');
});

test('viewer wording comes from the viewer, not from the copy an older report carries', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const file = path.join(dir, 'reports', `${ID}.js`);
  const text = fs.readFileSync(file, 'utf8');
  const old = text.replace(/("viewer\.path\.open":")[^"]*"/, '$1an old label"');
  assert.notEqual(old, text);
  fs.writeFileSync(file, old);
  const p = await browserFor(t).tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  assert.equal(await p.run(`document.getElementById('btn-reach-tab').textContent.trim()`), '查看改動');
});

test('1-2. TODO ticks and review ticks survive a reload and a closed and reopened tab', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const b = browserFor(t);
  const url = `${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`;
  const p = await b.tab(url);
  assert.deepEqual(await todoStates(p), [null, null, null, null, null, null]);
  await markSome(p);
  await p.reload();
  await expectMarked(p, 'after reload');
  await p.close();
  const again = await b.tab(url);
  await expectMarked(again, 'in a new tab');
});

test('3. two tickets keep separate states when switching between them', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  deliverReport(dir, readFixture(OTHER), OTHER);
  const b = browserFor(t);
  const url = pathToFileURL(path.join(dir, 'review.html')).href;
  const p = await b.tab(`${url}?report=${ID}`);
  await markSome(p);
  await p.go(`${url}?report=${OTHER}`);
  assert.equal(await p.run('archifyReportPayload.id'), OTHER);
  const fresh = await todoStates(p);
  assert.ok(fresh.every((s) => s === null), 'the other ticket starts unmarked');
  await tickTodo(p, 0);
  await p.go(`${url}?report=${ID}`);
  await expectMarked(p, 'back on the first ticket');
  await p.go(`${url}?report=${OTHER}`);
  assert.deepEqual(await todoStates(p), ['do', ...fresh.slice(1)]);
});

test('4. #report= becomes ?report=, and state survives hash-changing navigation', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const b = browserFor(t);
  const url = pathToFileURL(path.join(dir, 'review.html')).href;
  const p = await b.tab(`${url}#report=${ID}&ref=tab&tab=path-menu-ctrl`);
  assert.equal(await p.run('location.search'), `?report=${ID}`);
  assert.equal(await p.run('location.hash'), '#ref=tab&tab=path-menu-ctrl');
  assert.equal(await p.run('document.documentElement.dataset.reportState'), 'ready');
  await markSome(p);
  await p.run(`document.querySelector('.path-panel:not([hidden]) .path-hop.is-current .ref-copy-block').click(); true`);
  const link = await copied(p, 1);
  assert.ok(link.includes(`review.html?report=${ID}#`), `copied link keeps the report: ${link}`);
  await p.go(link);
  assert.equal(await p.run('location.search'), `?report=${ID}`);
  await p.reload();
  await expectMarked(p, 'after following a reference link and reloading');
});

test('5. Refresh copies the ticked item numbers; a new report version keeps matching state, and the old version gets its state back', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const b = browserFor(t);
  const p = await b.tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  await markSome(p);
  await p.run(`document.getElementById('btn-refresh').click(); true`);
  assert.equal(await copied(p, 1), `/archify-review refresh ${ID} --done 1,2,3`);

  deliverReport(dir, secondVersion());
  await p.reload();
  // New first item; old items 1 and 2 follow; old item 3 is gone.
  assert.deepEqual(await todoStates(p), [null, 'do', 'do', null, null, null], 'matching TODO items keep their tick');
  assert.match(await p.run(`document.querySelectorAll('.todo-list > li')[0].textContent`), /second review/);
  await openTab(p);
  assert.equal(await flowState(p, 'ctrl-page'), 'partial', 'the changed line is no longer ticked, the others are');
  assert.equal(await flowState(p, 'menu-ctrl'), 'seen', 'unchanged lines stay ticked');
  assert.equal(await fileTicked(p, CHANGELOG), true);

  deliverReport(dir, spec);
  await p.reload();
  await expectMarked(p, 'back on the first version');
});

test('6. viewer operations (layout, motion) do not lose the state', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const b = browserFor(t);
  const p = await b.tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  await markSome(p);
  await p.run(`document.querySelector('#layout-switch button[data-layout="mvvm"]').click(); true`);
  await p.run('Archify.viewerChromeLayout.whenStable()');
  await p.run(`document.getElementById('btn-motion').click(); true`);
  await expectMarked(p, 'after switching layout and motion');
  await p.reload();
  await expectMarked(p, 'after reload');
});

test('the state lives under one localStorage key per report, and a standalone page stores nothing', opts, async (t) => {
  const dir = scratchDir(t);
  deliverReport(dir, spec);
  const b = browserFor(t);
  const p = await b.tab(`${pathToFileURL(path.join(dir, 'review.html')).href}?report=${ID}`);
  await tickTodo(p, 0);
  const saved = await p.run(`JSON.parse(localStorage.getItem('archify-review-state:${ID}'))`);
  assert.deepEqual(Object.values(saved.todo), ['do']);
  const alone = await b.tab(pathToFileURL(deliverSpec(scratchDir(t), spec)).href);
  const before = await alone.run('Object.keys(localStorage).sort()');
  await tickTodo(alone, 0);
  assert.deepEqual(await alone.run('Object.keys(localStorage).sort()'), before);
});
