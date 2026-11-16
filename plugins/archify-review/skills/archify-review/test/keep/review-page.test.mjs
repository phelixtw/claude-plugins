import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome } from './helpers/review-page.mjs';

// Browser checks of the code-review page, driven on the delivered html of a real review (PR-10922, Flutter plugin, 3 hops).
const spec = readFixture('PR-10922');
const CTRL = 'packages/webview_flutter/webview_flutter_wkwebview/lib/src/webkit_webview_controller.dart';
const TWO_HOPS = 'path-menu-ctrl-page'; // menu>ctrl then ctrl>page
const opts = { skip: skipChrome, timeout: 90000 };
const panel = '.path-panel:not([hidden])';

async function open(t, hash = '', mutate) {
  const next = structuredClone(spec);
  if (mutate) mutate(next);
  return openPage(t, deliverSpec(scratchDir(t), next), hash);
}
const currentEdge = (p) => p.run(`document.querySelector('${panel} .path-hop.is-current')?.dataset.edge`);
const stepper = (p) => p.run(`(() => { const s = document.querySelector('${panel} .path-stepper'); const [up, down] = s.querySelectorAll('button'); return { label: s.querySelector('span').textContent, upDisabled: up.disabled, downDisabled: down.disabled, hidden: s.hidden }; })()`);

test('change file tree lists every changed file with status and counts, and a file name lists the flows citing it', opts, async (t) => {
  const p = await open(t);
  const rows = await p.run(`[...document.querySelectorAll('.review-file')].map((r) => ({ name: r.querySelector('.review-name').title, mark: r.querySelector('.review-mark').textContent, add: r.querySelector('.review-add').textContent, state: r.dataset.state }))`);
  assert.equal(rows.length, spec.changes.files.length);
  assert.deepEqual(rows.map((r) => r.name).sort(), spec.changes.files.map((f) => f.path).sort());
  assert.ok(rows.every((r) => r.mark === 'M' && r.state === 'unseen' && /^\+\d+$/.test(r.add)));
  const ctrl = `[...document.querySelectorAll('.review-file')].find((r) => r.querySelector('.review-name').title === ${JSON.stringify(CTRL)})`;
  assert.equal(await p.run(`${ctrl}.querySelector('.review-flows').hidden`), true);
  await p.run(`${ctrl}.querySelector('.review-name').click(); true`);
  assert.equal(await p.run(`${ctrl}.querySelector('.review-flows').hidden`), false);
  const flows = await p.run(`[...${ctrl}.querySelectorAll('.review-flows li:not(.review-flows-head)')].map((li) => li.textContent)`);
  assert.equal(flows.length, 3, 'three connections cite the controller file');
});

test('a hop-path tab shows one hop at a time and the up/down arrows and keys step between hops', opts, async (t) => {
  const p = await open(t, `#ref=tab&tab=${TWO_HOPS}`);
  assert.equal(await currentEdge(p), 'menu-ctrl');
  assert.deepEqual(await stepper(p), { label: await p.run(`document.querySelector('${panel} .path-stepper span').textContent`), upDisabled: true, downDisabled: false, hidden: false });
  assert.match((await stepper(p)).label, /1 \/ 2$/);
  assert.equal(await p.run(`document.querySelectorAll('${panel} .path-hop.is-current').length`), 1);
  await p.run(`document.querySelectorAll('${panel} .path-stepper button')[1].click(); true`);
  assert.equal(await currentEdge(p), 'ctrl-page');
  const second = await stepper(p);
  assert.match(second.label, /2 \/ 2$/);
  assert.deepEqual([second.upDisabled, second.downDisabled], [false, true]);
  await p.run(`document.querySelectorAll('${panel} .path-stepper button')[0].click(); true`);
  assert.equal(await currentEdge(p), 'menu-ctrl');
  await p.run(`document.querySelector('${panel} .path-col').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); true`);
  assert.equal(await currentEdge(p), 'ctrl-page');
  await p.run(`document.querySelector('${panel} .path-col').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true })); true`);
  assert.equal(await currentEdge(p), 'menu-ctrl');
});

test('a hop shows its diff with numbered lines, the user action and the story', opts, async (t) => {
  const p = await open(t, '#ref=tab&tab=path-menu-ctrl');
  const conn = spec.connections.find((c) => c.id === 'menu-ctrl');
  const info = await p.run(`(() => { const hop = document.querySelector('${panel} .path-hop.is-current'); return {
    file: hop.querySelector('.code-diff').dataset.file, adds: hop.querySelectorAll('.cd-line.cd-add').length, dels: hop.querySelectorAll('.cd-line.cd-del').length,
    numbered: hop.querySelectorAll('.cd-line[data-n]').length, ops: document.querySelector('${panel} .path-ops').innerText }; })()`);
  assert.equal(info.file, conn.code.path);
  assert.ok(info.adds > 0 && info.dels > 0 && info.numbered > 0);
  assert.ok(info.ops.includes(conn.code.action.replace(/[`*]/g, '').slice(0, 8)), 'the user action of the hop is shown');
  assert.ok(info.ops.includes(conn.code.note.replace(/[`*]/g, '').slice(0, 8)), 'the note of the hop is shown');
});

test('ticking a connection marks its files and nodes, un-ticking restores them, a tick moves to the next hop', opts, async (t) => {
  const p = await open(t, `#ref=tab&tab=${TWO_HOPS}`);
  const ctrlState = () => p.run(`[...document.querySelectorAll('.review-file')].find((r) => r.querySelector('.review-name').title === ${JSON.stringify(CTRL)}).dataset.state`);
  assert.equal(await ctrlState(), 'unseen');
  await p.click(`${panel} .path-hop[data-edge="menu-ctrl"] input[data-review-flow]`);
  assert.equal(await ctrlState(), 'partial', 'another connection still cites unseen controller lines');
  await p.waitUntil(`document.querySelector('${panel} .path-hop.is-current')?.dataset.edge === 'ctrl-page'`, 'auto-advance to the next hop');
  await p.click(`${panel} .path-hop[data-edge="menu-ctrl"] input[data-review-flow]`);
  assert.equal(await ctrlState(), 'unseen');
  assert.equal(await p.run(`document.querySelector('${panel} .path-hop[data-edge="menu-ctrl"] input[data-review-flow]').checked`), false);
});

test('an uncited file carries its own tick and node watermark follows the files', opts, async (t) => {
  const extra = 'packages/webview_flutter/webview_flutter_wkwebview/lib/uncited.dart';
  const p = await open(t, '', (s) => { s.changes.files.push({ path: extra, status: 'added', additions: 1, deletions: 0 }); });
  const row = `[...document.querySelectorAll('.review-file')].find((r) => r.querySelector('.review-name').title === ${JSON.stringify(extra)})`;
  assert.equal(await p.run(`${row}.querySelector('input[data-review-file]').hidden`), false, 'own tick shown for a file no flow cites');
  await p.run(`${row}.querySelector('input[data-review-file]').click(); true`);
  assert.equal(await p.run(`${row}.dataset.state`), 'seen');
  assert.equal(await p.run(`document.querySelectorAll('.review-file[data-state="seen"] input[data-review-file]:not([hidden])').length`), 1);
  assert.equal(await p.run(`!!document.querySelector('.diagram-container > svg [data-review-seen-mark]')`), false, 'one seen file does not finish a node');
});

test('line ticks are shared: ticking a connection ticks the same lines in another connection showing them', opts, async (t) => {
  const p = await open(t, '#ref=tab&tab=path-menu-ctrl-ucc');
  const box = (id) => `document.querySelector('input[data-review-flow="${id}"]')`;
  await p.click(`input[data-review-flow="ctrl-ucc"]`);
  const shared = await p.run(`(() => { const b = ${box('menu-ctrl')}; return b.indeterminate ? 'partial' : b.checked ? 'seen' : 'unseen'; })()`);
  assert.ok(['partial', 'seen'].includes(shared), `menu-ctrl shares hunks with ctrl-ucc, got ${shared}`);
});

test('TODO tabs show even without security-review items; the Security Review tab is then empty', opts, async (t) => {
  const p = await open(t, '', (s) => { s.cards.forEach((c) => { if (c.items) c.items = c.items.filter((i) => !/security-review/.test(i)); }); });
  assert.deepEqual(await p.run(`[...document.querySelectorAll('.todo-tabs [role=tab]')].map((b) => b.textContent)`).then((l) => l.map((x) => x.replace(/ \(\d+\)$/, ''))), ['Code Review', 'Security Review', 'Code Smell']);
  await p.run(`document.querySelector('.todo-tabs [data-tab=security]').click(); true`);
  assert.equal(await p.run(`[...document.querySelector('.todo-list').children].filter((li) => !li.hidden).length`), 0);
});

// Code Smell items from the bundled ponytail-review rules: `[<risk>][code-smell] <tag>: <what to cut>. <replacement>. (<file>:<line>)`.
const SMELLS = [
  '[medium][code-smell] yagni: _onConsoleMessageChannelName constant shared by one caller and one test. Inline the literal. (webkit_webview_controller.dart:709, menu-ctrl)',
  '[low][code-smell] shrink: the test builds the handler list in a loop. One list literal. (webkit_webview_controller_test.dart:1650)',
  '[low][code-smell] delete: _isConsoleChannelAdded helper with one caller. Inline the check. (webkit_webview_controller.dart:720)',
];
function withSmells(s) {
  const todo = s.cards.find((c) => /TODO|待確認/.test(c.title));
  const lows = todo.items.findIndex((i) => /^\[low\]/.test(i));
  todo.items.splice(lows, 0, SMELLS[0]);
  todo.items.push(SMELLS[1], SMELLS[2]);
}

test('a Code Smell tab right of Security Review lists code-smell items; its LOW items copy a simplify prompt and Copy all LOW copies only that tab', opts, async (t) => {
  const p = await open(t, '', withSmells);
  const tabs = await p.run(`[...document.querySelectorAll('.todo-tabs [role=tab]')].map((b) => [b.dataset.tab, b.textContent])`);
  assert.deepEqual(tabs.map((x) => x[0]), ['code', 'security', 'smell']);
  assert.equal(tabs[2][1], 'Code Smell (3)');
  const visible = `[...document.querySelector('.todo-list').children].filter((li) => !li.hidden)`;
  assert.ok(await p.run(`${visible}.every((li) => !/code-smell/.test(li.textContent))`), 'Code Review tab holds no smells');
  await p.run(`document.querySelector('.todo-tabs [data-tab=smell]').click(); true`);
  assert.equal(await p.run(`${visible}.length`), 3);
  assert.ok(await p.run(`${visible}.every((li) => /code-smell/.test(li.textContent))`));
  assert.deepEqual(await p.run(`${visible}.map((li) => !!li.querySelector('.todo-fix'))`), [false, true, true], 'only LOW-risk smells get a copy button');
  await p.run(`${visible}[1].querySelector('.todo-fix').click(); true`);
  await p.waitUntil('window.__copied.length === 1');
  const one = await p.run('window.__copied[0]');
  assert.match(one, /^Simplify one LOW-risk over-engineering finding in this change\./);
  assert.match(one, /shrink: the test builds the handler list in a loop/);
  assert.match(one, /behavior must not change/);
  await p.run(`document.querySelector('.todo-fix-all').click(); true`);
  await p.waitUntil('window.__copied.length === 2');
  const all = await p.run('window.__copied[1]');
  assert.match(all, /^Simplify 2 LOW-risk over-engineering findings in this change\./);
  assert.doesNotMatch(all, /\[code-review\]|LOW-severity/);
  await p.run(`document.querySelector('.todo-tabs [data-tab=code]').click(); true`);
  await p.run(`document.querySelector('.todo-fix-all').click(); true`);
  await p.waitUntil('window.__copied.length === 3');
  const code = await p.run('window.__copied[2]');
  assert.match(code, /^Fix 2 LOW-severity code-review findings in this change\./);
  assert.doesNotMatch(code, /over-engineering|shrink:|delete:/);
});

test('LOW TODO items copy a fix prompt, Copy all LOW copies every one, ticking an item updates the handled count', opts, async (t) => {
  const p = await open(t);
  const items = await p.run(`[...document.querySelector('.todo-list').querySelectorAll('li')].map((li) => ({ sev: li.querySelector('.todo-sev')?.dataset.sev, hasFix: !!li.querySelector('.todo-fix'), none: /審查的 hunk 沒有發現問題/.test(li.textContent) }))`);
  assert.deepEqual(await p.run(`[...document.querySelectorAll('.todo-tabs [role=tab]')].map((b) => b.textContent.replace(/ \\(\\d+\\)$/, ''))`), ['Code Review', 'Security Review', 'Code Smell']);
  assert.equal(await p.run(`[...document.querySelector('.todo-list').children].filter((li) => !li.hidden && /security-review/.test(li.textContent)).length`), 0, 'security items start hidden');
  await p.run(`document.querySelector('.todo-tabs [data-tab=security]').click(); true`);
  assert.ok(await p.run(`[...document.querySelector('.todo-list').children].filter((li) => !li.hidden && !li.classList.contains('todo-none')).every((li) => /security-review/.test(li.textContent))`), 'Security Review tab shows only security items');
  await p.run(`document.querySelector('.todo-tabs [data-tab=code]').click(); true`);
  const lows = items.filter((i) => i.sev === 'low' && !i.none); // a "no findings" item gets no fix button
  assert.ok(items.some((i) => i.none && !i.hasFix));
  assert.ok(lows.length > 1 && lows.every((i) => i.hasFix));
  assert.ok(items.filter((i) => i.sev !== 'low').every((i) => !i.hasFix), 'only LOW items get a copy button');
  await p.run(`document.querySelector('.todo-list').querySelector('li:has(.todo-fix) .todo-fix').click(); true`);
  await p.waitUntil('window.__copied.length === 1');
  const one = await p.run('window.__copied[0]');
  assert.match(one, /^Fix one LOW-severity code-review finding in this change\./);
  assert.match(one, /Ticket: PR-10922/);
  assert.match(one, /Commit: [0-9a-f]{7,}/);
  assert.match(one, /Make the minimal fix only/);
  await p.run(`document.querySelector('.todo-fix-all').click(); true`);
  await p.waitUntil('window.__copied.length === 2');
  const all = await p.run('window.__copied[1]');
  assert.ok(all.startsWith(`Fix ${lows.length} LOW-severity code-review findings in this change.`));
  assert.equal((all.match(/^\d+\. Finding: /gm) || []).length, lows.length);
  // A checkbox in front of each item marks it; there are no Done / Skip buttons.
  const total = items.filter((i) => !i.none).length; // a "no findings" line is not counted
  assert.equal(await p.run(`document.querySelectorAll('.todo-seg').length`), 0);
  assert.equal(await p.run(`document.querySelectorAll('.todo-list > li > input.todo-check:first-child').length`), total);
  assert.match(await p.run(`document.querySelector('.todo-count').textContent`), new RegExp(`0 / ${total}$`));
  const box = (i) => `document.querySelectorAll('.todo-list > li')[${i}].querySelector('input.todo-check')`;
  await p.run(`${box(0)}.click(); ${box(1)}.click(); true`);
  assert.deepEqual(await p.run(`[...document.querySelector('.todo-list').querySelectorAll('li')].slice(0, 3).map((li) => li.dataset.state || null)`), ['do', 'do', null]);
  assert.match(await p.run(`document.querySelector('.todo-count').textContent`), new RegExp(`2 / ${total}$`));
  await p.run(`${box(0)}.click(); true`);
  assert.match(await p.run(`document.querySelector('.todo-count').textContent`), new RegExp(`1 / ${total}$`), 'unticking clears the mark');
});

test('MEDIUM and HIGH items get Ask AI, LOW items get AI Fix; both copy a prompt and show a toast', opts, async (t) => {
  const p = await open(t);
  const rows = await p.run(`[...document.querySelectorAll('.todo-list > li')].filter((li) => !li.classList.contains('todo-none')).map((li) => [li.querySelector('.todo-sev').dataset.sev, [...li.querySelectorAll('.todo-fix, .todo-ask')].map((b) => b.textContent)])`);
  for (const [sev, buttons] of rows) {
    if (sev === 'low') assert.ok(buttons.length === 0 || (buttons.length === 1 && buttons[0] === 'AI Fix'), `${sev}: ${buttons}`);
    else assert.deepEqual(buttons, ['Ask AI'], sev);
  }
  await p.run(`document.querySelector('.todo-list .todo-ask').click(); true`);
  await p.waitUntil('window.__copied.length === 1');
  const ask = await p.run('window.__copied[0]');
  assert.match(ask, /^Look into one MEDIUM-severity code-review finding in this change and answer it\./);
  assert.match(ask, /Ticket: PR-10922/);
  assert.match(ask, /Location: webkit_webview_controller\.dart:\d+/);
  assert.match(ask, /propose the smallest fix\. Do not edit files\./);
  await p.waitUntil(`!!document.querySelector('.archify-toast.show')`);
  assert.ok((await p.run(`document.querySelector('.archify-toast').textContent`)).length > 0);
  assert.equal(await p.run(`document.querySelector('.archify-toast').getAttribute('role')`), 'status');
  await p.run(`document.getElementById('btn-refresh').click(); true`);
  await p.waitUntil('window.__copied.length === 2');
  await p.waitUntil(`!!document.querySelector('.archify-toast.show')`);
});

test('the cards sit in a right rail: each card collapses on its own and the toolbar button hides the rail', opts, async (t) => {
  const p = await open(t);
  await p.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await p.run(`window.dispatchEvent(new Event('resize')); Archify.viewerChromeLayout.whenStable()`);
  const box = (sel, i = 0) => p.run(`(() => { const r = document.querySelectorAll('${sel}')[${i}].getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, height: r.height, width: r.width }; })()`);
  const settle = () => p.run(`new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))).then(() => Archify.viewerChromeLayout.whenStable()).then(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))`);
  await settle();
  const [diagram, cards] = [await box('.diagram-container'), await box('.cards')];
  assert.ok(cards.left >= diagram.right, 'the cards sit right of the diagram');
  assert.ok(Math.abs(cards.top - diagram.top) < 2 && Math.abs(cards.bottom - diagram.bottom) < 2, 'the rail lines up with the diagram panel');
  assert.ok(await p.run('document.documentElement.scrollHeight <= innerHeight'), 'the page fits the viewport');
  const second = await box('.cards .card', 1);
  await p.run(`document.querySelectorAll('.cards .card-collapse')[0].click(); true`);
  await settle();
  assert.equal(await p.run(`document.querySelectorAll('.cards .card-collapse')[0].getAttribute('aria-expanded')`), 'false');
  assert.ok((await box('.cards .card', 1)).height > second.height + 50, 'the other card takes the room');
  await p.run(`document.getElementById('btn-cards').click(); true`);
  await settle();
  assert.equal(await p.run(`document.querySelector('.cards').offsetHeight`), 0, 'the rail is hidden');
  assert.ok((await box('.diagram-container')).width > diagram.width + 100, 'the diagram takes the width');
  await p.load(p.url);
  await settle();
  assert.equal(await p.run(`document.querySelector('.cards').offsetHeight`), 0, 'the hidden rail is remembered after a reload');
  await p.run(`document.getElementById('btn-cards').click(); true`);
  await settle();
  assert.equal(await p.run(`document.querySelector('.cards .card').hasAttribute('data-collapsed')`), true, 'the collapsed card is remembered');
});

test('the story card shows the purpose of the ticket', opts, async (t) => {
  const p = await open(t);
  const story = spec.cards.find((c) => c.title.includes('目的'));
  const text = await p.run(`[...document.querySelectorAll('.cards .card')].find((c) => c.querySelector('h3').textContent.includes('目的')).innerText`);
  assert.ok(text.includes(story.title));
  assert.ok(text.includes(story.items[0].replace(/\*/g, '').slice(0, 12)));
});

test('copying a hop link gives a #ref=hop url, and opening it jumps to that hop and marks the referenced lines', opts, async (t) => {
  const p = await open(t, `#ref=tab&tab=${TWO_HOPS}`);
  await p.run(`document.querySelectorAll('${panel} .path-stepper button')[1].click(); true`);
  await p.run(`document.querySelector('${panel} .path-hop.is-current .ref-copy-block').click(); true`);
  await p.waitUntil('window.__copied.length === 1');
  const link = await p.run('window.__copied[0]');
  const hash = link.slice(link.indexOf('#'));
  const fields = Object.fromEntries(hash.slice(1).split('&').map((pair) => pair.split(/=(.*)/s).slice(0, 2)));
  assert.equal(fields.ref, 'hop');
  assert.equal(fields.tab, TWO_HOPS);
  assert.equal(fields.edge, 'ctrl-page');
  assert.equal(fields.file, spec.connections.find((c) => c.id === 'ctrl-page').code.path);
  assert.match(fields.lines, /^-\d+\.\.-\d+,\+\d+\.\.\+\d+$/);
  assert.match(fields.commit, /^[0-9a-f]{7,}/);
  // a fresh page opened with the copied hash lands on the same hop with lines marked
  const fresh = await openPage(t, p.html, hash);
  assert.equal(await currentEdge(fresh), 'ctrl-page');
  assert.ok(await fresh.run(`document.querySelectorAll('${panel} .cd-line.ref-hit').length`) > 0);
});

test('a hand-written #ref with +new and -old line spans marks exactly those lines', opts, async (t) => {
  const conn = spec.connections.find((c) => c.id === 'menu-ctrl');
  const hash = `#ref=hop&tab=path-menu-ctrl&edge=menu-ctrl&file=${conn.code.path}&lines=-703,+708..+710`;
  const p = await open(t, hash);
  const hits = await p.run(`[...document.querySelectorAll('${panel} .cd-line.ref-hit')].map((l) => (l.dataset.o ? '-' + l.dataset.o : '') + (l.dataset.n && l.dataset.n !== '0' ? '+' + l.dataset.n : ''))`);
  assert.deepEqual(hits.sort(), ['+708', '+709', '+710', '-703'].sort());
});

test('a node #ref opens that node code change and marks the lines', opts, async (t) => {
  const conn = spec.connections.find((c) => c.id === 'menu-ctrl');
  // a node with a brief shows Role / Task instead of its diff, so the diff is shown by dropping the brief
  const p = await open(t, `#ref=node&node=ctrl&file=${conn.code.path}&lines=%2B708..%2B710`, (s) => { delete s.components.find((c) => c.id === 'ctrl').code.brief; });
  await p.waitUntil(`document.querySelectorAll('#focus-code .cd-line.ref-hit').length >= 3`, 'marked lines in the node code panel');
  assert.deepEqual(await p.run(`[...new Set([...document.querySelectorAll('#focus-code .cd-line.ref-hit')].map((l) => l.dataset.n))]`), ['708', '709', '710']);
});

test('an unknown tab or file in #ref is ignored without breaking the page', opts, async (t) => {
  const p = await open(t, '#ref=tab&tab=path-nope-nothing');
  assert.equal(await p.run(`document.querySelectorAll('.path-panel:not([hidden])').length`), 0);
  assert.equal(await p.run(`document.querySelectorAll('.review-file').length`), spec.changes.files.length);
});

test('Refresh copies the refresh command with ticket, repo and the ticked item numbers', opts, async (t) => {
  const p = await open(t, '', (s) => { s.changes.repo = '/home/me/my repo'; });
  assert.equal(await p.run(`document.getElementById('btn-refresh').hidden`), false);
  await p.run(`document.querySelectorAll('.todo-list > li')[1].querySelector('input.todo-check').click(); true`);
  await p.run(`document.getElementById('btn-refresh').click(); true`);
  await p.waitUntil('window.__copied.length === 1');
  assert.equal(await p.run('window.__copied[0]'), '/archify-review refresh PR-10922 --repo "/home/me/my repo" --done 2');
});

test('copy-link tab button copies a #ref=tab url for the open tab', opts, async (t) => {
  const p = await open(t, `#ref=tab&tab=${TWO_HOPS}`);
  await p.run(`document.querySelector('#tab-${TWO_HOPS}').parentElement.querySelector('.ref-copy-tab').click(); true`);
  await p.waitUntil('window.__copied.length === 1');
  assert.match(await p.run('window.__copied[0]'), new RegExp(`#ref=tab&tab=${TWO_HOPS}$`));
});

test('a tab that has items does not also show its "no findings" line', opts, async (t) => {
  const p = await open(t, '', (s) => {
    s.cards.find((c) => /TODO|待確認/.test(c.title)).items = [
      '[medium] An open question without a review tag (a.dart:70)',
      '[low][security-review] 審查的 hunk 沒有發現問題',
      '[low][code-review] 審查的 hunk 沒有發現問題',
    ];
  });
  const visible = () => p.run(`[...document.querySelectorAll('.todo-list > li')].filter((li) => li.offsetHeight > 0).map((li) => li.textContent.replace(/\\s+/g, ' ').trim().slice(0, 30))`);
  assert.deepEqual(await visible(), ['mediumAn open question without'], 'Code Review shows the question, not "no findings"');
  await p.run(`document.querySelector('.todo-tabs [data-tab=security]').click(); true`);
  assert.deepEqual(await visible(), ['審查的 hunk 沒有發現問題'], 'a tab with nothing else keeps its line');
});

test('a localized "no findings" TODO item gets no fix button, same as the English one', { skip: skipChrome, timeout: 90000 }, async (t) => {
  for (const [locale, none] of [['zh-TW', '審查的 hunk 沒有發現問題'], ['zh-CN', '审查的 hunk 没有发现问题'], ['en', 'no findings in the reviewed hunks']]) {
    const next = structuredClone(readFixture('PR-9151'));
    next.meta.locale = locale;
    next.cards[next.cards.length - 1].items = ['[medium][code-review] a medium finding (a.dart:1)', '[low][code-review] a low finding (a.dart:2)', `[low][security-review] ${none}`];
    const p = await openPage(t, deliverSpec(scratchDir(t), next));
    assert.equal(await p.run(`document.querySelectorAll('.todo-list .todo-fix').length`), 1, `${locale}: one fix button, for the real finding`);
    assert.equal(await p.run(`document.querySelectorAll('.todo-fix-all').length`), 0, `${locale}: no copy-all for a single finding`);
    // "No findings" is one plain line: no badge, tag, checkbox or button, and the tab count leaves it out.
    const line = await p.run(`(() => { const li = document.querySelector('.todo-list > li.todo-none'); return li && { text: li.textContent.trim(), sev: !!li.querySelector('.todo-sev'), check: !!li.querySelector('input'), button: !!li.querySelector('button') }; })()`);
    assert.deepEqual(line, { text: none, sev: false, check: false, button: false }, locale);
    assert.equal(await p.run(`document.querySelector('.todo-tabs [data-tab=security]').textContent`), 'Security Review (0)', locale);
    // On the Code Review tab the security "no findings" line is out of view, not only marked hidden.
    assert.deepEqual(await p.run(`[...document.querySelectorAll('.todo-list > li')].filter((li) => li.offsetHeight > 0).map((li) => li.textContent.slice(0, 20))`).then((l) => l.length), 2, `${locale}: only the two code-review items are visible`);
    await p.run(`document.querySelector('.todo-tabs [data-tab=security]').click(); true`);
    assert.deepEqual(await p.run(`[...document.querySelectorAll('.todo-list > li')].filter((li) => li.offsetHeight > 0).map((li) => li.textContent)`), [none], `${locale}: the Security tab shows its one line`);
    assert.match(await p.run(`document.querySelector('.todo-count').textContent`), /0 \/ 2$/, locale);
  }
});

test('a path tab has no file-list heading; the current hop file name sits right under the hop stepper', opts, async (t) => {
  const p = await open(t, `#ref=tab&tab=${TWO_HOPS}`);
  assert.equal(await p.run(`document.querySelectorAll('${panel} .path-files').length`), 0);
  const gap = () => p.run(`(() => { const col = document.querySelector('${panel} .path-col'); const step = col.querySelector('.path-stepper'); const above = step && step.offsetHeight ? step.getBoundingClientRect().bottom : col.getBoundingClientRect().top; return Math.round(col.querySelector('.path-hop.is-current .path-hop-head').getBoundingClientRect().top - above); })()`);
  assert.ok(await gap() <= 24, `only the card padding sits above the hop file name, gap ${await gap()}px`);
});

test('every hop card names its file, on a one-hop tab as on a two-hop tab', opts, async (t) => {
  for (const tab of ['path-menu-ctrl', TWO_HOPS]) {
    const p = await open(t, `#ref=tab&tab=${tab}`);
    const heads = await p.run(`[...document.querySelectorAll('${panel} .path-hop:not(.path-node-file) .path-hop-head')].map((h) => [...h.children].filter((c) => !c.matches('.path-hop-n, .ct-ratio')).map((c) => c.textContent).join('').trim())`);
    assert.ok(heads.length > 0 && heads.every((h) => /\.\w+$/.test(h)), `${tab}: ${JSON.stringify(heads)}`);
  }
});

test('the first hop card keeps a gap below the column top, and its reviewed checkbox takes a real mouse click', opts, async (t) => {
  const p = await open(t, '#ref=tab&tab=path-menu-ctrl');
  const box = `document.querySelector('${panel} .path-hop.is-current input.review-flow-box')`;
  const at = await p.run(`(() => { const col = document.querySelector('${panel} .path-col'); const card = col.querySelector('.path-hop.is-current'); const r = ${box}.getBoundingClientRect(); return { gap: card.getBoundingClientRect().top - col.getBoundingClientRect().top, x: r.x + r.width / 2, y: r.y + r.height / 2 }; })()`);
  assert.ok(at.gap >= 8, `card gap below the column top: ${at.gap}px`);
  assert.equal(await p.run(`${box}.checked`), false);
  await p.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: at.x, y: at.y, button: 'left', clickCount: 1 });
  await p.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: at.x, y: at.y, button: 'left', clickCount: 1 });
  assert.equal(await p.run(`${box}.checked`), true, 'a real click ticks the hop');
});
