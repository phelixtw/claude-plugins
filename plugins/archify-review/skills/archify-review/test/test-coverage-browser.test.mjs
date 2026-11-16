import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome } from './keep/helpers/review-page.mjs';

// A hop's test coverage sits on its diff: every changed function's lines carry its test status as a gutter bar
// (hatched when untested), its first line a chip that opens why the code matters and its tests as
// Given / When / Then, and the hop head a coverage ratio. Nothing is listed under the diff.
const opts = { skip: skipChrome, timeout: 90000 };
const panel = `document.querySelector('.path-panel:not([hidden])')`;
const hop = `${panel}.querySelector('.path-hop.is-current')`;
const T = 'setOnConsoleMessage called twice does not throw';

async function open(t, spec, tab) {
  const p = await openPage(t, deliverSpec(scratchDir(t), spec), `#ref=tab&tab=${tab}`);
  await p.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await p.run(`new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(() => done(true))))`);
  return p;
}

// What the current hop shows: marked lines as `[+n or -o, status]` in diff order, chips with the line they sit on,
// the head ratio, the bar and hatch drawn under each status, and what follows the diff block.
const view = (p) => p.run(`(() => {
  const card = ${hop};
  const id = (line) => line.dataset.o ? '-' + line.dataset.o : '+' + line.dataset.n;
  const token = (name) => { const probe = document.createElement('i'); probe.style.color = 'var(' + name + ')'; card.appendChild(probe); const c = getComputedStyle(probe).color; probe.remove(); return c; };
  const ratio = card.querySelector('.path-hop-head .ct-ratio');
  return {
    marks: [...card.querySelectorAll('.cd-line[data-cover]')].map((line) => [id(line), line.dataset.cover]),
    chips: [...card.querySelectorAll('.ct-chip')].map((chip) => [id(chip.closest('.cd-line')), chip.textContent]),
    ratio: ratio && { text: ratio.textContent, title: ratio.title, color: getComputedStyle(ratio).color },
    tokens: { seen: token('--review-seen'), partial: token('--review-partial'), gap: token('--risk-high'), gone: token('--text-faint') },
    images: Object.fromEntries([...card.querySelectorAll('.cd-line[data-cover]')].map((line) => [line.dataset.cover, getComputedStyle(line, '::before').backgroundImage])),
    afterDiff: card.querySelector('.code-diff').closest('.code-block').nextElementSibling?.className ?? null,
  };
})()`);

// Real mouse clicks, so the page's own scrolling and focus behaviour is what gets measured.
const center = (p, expr) => p.run(`(() => { const r = (${expr}).getBoundingClientRect(); return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) }; })()`);
async function clickAt(p, at) {
  await p.send('Input.dispatchMouseEvent', { type: 'mousePressed', ...at, button: 'left', clickCount: 1 });
  await p.send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...at, button: 'left', clickCount: 1 });
}
const chip = (index) => `${hop}.querySelectorAll('.ct-chip')[${index}]`;
const clickChip = async (p, index) => clickAt(p, await center(p, chip(index)));
// Scrolls the diff so a chip further down is in view, as a reader would before clicking it.
const reveal = (p, index) => p.run(`(() => { const line = ${chip(index)}.closest('.cd-line'), pre = line.closest('.code-diff'); pre.scrollTop = line.offsetTop - pre.clientHeight / 2; return true; })()`);
const scrolls = (p) => p.run(`({ page: scrollY, col: ${panel}.querySelector('.path-col').scrollTop, diff: ${hop}.querySelector('.code-diff').scrollTop })`);
// The open popover: where it sits against its chip's line and its diff block, and what it says.
const popover = (p) => p.run(`(() => {
  const pops = document.querySelectorAll('.ct-pop');
  if (!pops.length) return null;
  const pop = pops[0];
  const pre = pop.parentElement;
  const line = [...pre.querySelectorAll('.ct-chip')].find((c) => c.getAttribute('aria-expanded') === 'true').closest('.cd-line');
  const r = pop.getBoundingClientRect(), l = line.getBoundingClientRect(), b = pre.getBoundingClientRect();
  const text = (el) => el ? el.textContent : null;
  return {
    count: pops.length,
    inDiff: pre.classList.contains('code-diff'),
    anchored: Math.abs(r.top - l.bottom) < 2 || Math.abs(r.bottom - l.top) < 2,
    inside: r.left >= b.left - 1 && r.right <= b.right + 1 && r.top >= b.top - 1 && r.bottom <= b.bottom + 1,
    symbol: text(pop.querySelector('.ct-symbol')),
    status: text(pop.querySelector('.ct-status')),
    why: text(pop.querySelector('.ct-why')),
    whyWeight: Number(getComputedStyle(pop.querySelector('.ct-why')).fontWeight),
    cases: [...pop.querySelectorAll('.ct-case:not(.ct-suggested)')].map((c) => ({
      test: text(c.querySelector('.ct-test')),
      rows: [...c.querySelectorAll('.ct-gwt dt')].map((dt) => [dt.textContent, dt.nextElementSibling.textContent]),
    })),
    via: text(pop.querySelector('.ct-via')),
    suggestedTitle: text(pop.querySelector('.ct-suggested-title')),
    suggested: [...pop.querySelectorAll('.ct-suggested')].map((c) => [...c.querySelectorAll('.ct-gwt dt')].map((dt) => [dt.textContent, dt.nextElementSibling.textContent])),
  };
})()`);
const enter = (p) => p.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, text: '\r' })
  .then(() => p.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 }));
const escape = (p) => p.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
  .then(() => p.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 }));

test('tested functions mark their diff lines green, chip their first line and fill the head ratio', opts, async (t) => {
  const p = await open(t, readFixture('PR-10922'), 'path-menu-ctrl');
  const got = await view(p);
  // +708 is a comment: no test runs it, so it carries no mark.
  assert.deepEqual(got.marks, ['+297', '-703', '+705', '+709', '+710', '+711', '-707', '+714', '-739', '+746'].map((n) => [n, 'new']));
  assert.deepEqual(got.chips, [['+297', '✓ 1 個測試'], ['-703', '✓ 1 個測試']]);
  assert.deepEqual(got.ratio, { text: '測試覆蓋 2 / 2', title: '測試覆蓋 2 / 2 個改動的函式有測試', color: got.tokens.seen });
  assert.ok(got.images.new.includes(got.tokens.seen), `green gutter bar: ${got.images.new}`);
  assert.ok(!got.images.new.includes('repeating-linear-gradient'), 'no hatch on tested code');
  assert.equal(got.afterDiff, null, 'nothing is rendered under the diff');
  assert.equal(await p.run(`document.querySelectorAll('.code-tests, .ct-functions').length`), 0);
});

test('a range that starts with a comment chips its first code line, and an all-comment change still shows its status', opts, async (t) => {
  const spec = readFixture('PR-10922');
  const fns = spec.connections.find((c) => c.id === 'menu-ctrl').code.tests.functions;
  fns[1].lines = '+708..+711';
  fns[0].lines = '+708';
  const got = await view(await open(t, spec, 'path-menu-ctrl'));
  assert.deepEqual(got.chips, [['+708', '✓ 1 個測試'], ['+709', '✓ 1 個測試']]);
  assert.deepEqual(got.marks, ['+708', '+709', '+710', '+711'].map((n) => [n, 'new']));
});

test('a chip opens why and the tests as Given / When / Then inside the diff, without scrolling anything', opts, async (t) => {
  const p = await open(t, readFixture('PR-10922'), 'path-menu-ctrl');
  const before = await scrolls(p);
  await clickChip(p, 0);
  const pop = await popover(p);
  assert.ok(pop, 'the chip opens a popover');
  assert.deepEqual({ count: pop.count, inDiff: pop.inDiff, anchored: pop.anchored, inside: pop.inside }, { count: 1, inDiff: true, anchored: true, inside: true });
  assert.equal(pop.symbol, '_onConsoleMessageChannelName');
  assert.equal(pop.status, '新測試');
  assert.match(pop.why, /^為什麼要測：/);
  assert.ok(pop.whyWeight >= 600, 'why is the emphasised line');
  assert.deepEqual(pop.cases, [{ test: T, rows: [
    ['Given', '用 mock 的 WKUserContentController 建出 controller'],
    ['When', '連續 await 兩次 setOnConsoleMessage，callback 都是空函式'],
    ['Then', '沒有對 fltConsoleMessage 呼叫 removeScriptMessageHandler'],
  ] }]);
  assert.equal(pop.via, '經 setOnConsoleMessage');
  assert.equal(pop.suggestedTitle, null);
  assert.deepEqual(await scrolls(p), before, 'opening scrolls neither the page, the column nor the diff');

  await escape(p);
  assert.equal(await popover(p), null, 'Esc closes it');
  assert.equal(await p.run(`document.documentElement.getAttribute('data-view-tab')`), 'path', 'Esc closes only the popover, not the path tab');

  // One popover at a time: another chip swaps it (from the keyboard, as an open popover may cover that chip),
  // and the open chip closes it.
  await clickChip(p, 0);
  await p.run(`${chip(1)}.focus({ preventScroll: true }); true`);
  await enter(p);
  const second = await popover(p);
  assert.deepEqual([second.count, second.symbol, second.via, second.anchored], [1, 'setOnConsoleMessage', null, true]);
  assert.deepEqual(await p.run(`[...${hop}.querySelectorAll('.ct-chip')].map((c) => c.getAttribute('aria-expanded'))`), ['false', 'true']);
  await enter(p);
  assert.equal(await popover(p), null, 'the open chip closes its popover');

  await clickChip(p, 0);
  await clickAt(p, await center(p, `${hop}.querySelector('.path-hop-head')`));
  assert.equal(await popover(p), null, 'an outside click closes it');
  assert.deepEqual(await scrolls(p), before, 'closing scrolls nothing either');
  assert.deepEqual(await p.run(`[...${hop}.querySelectorAll('.ct-chip')].map((c) => c.getAttribute('aria-expanded'))`), ['false', 'false']);
});

test('untested code is red and hatched, its chip opens the suggested cases', opts, async (t) => {
  const p = await open(t, readFixture('PR-9151'), 'path-menu-ctrl');
  const got = await view(p);
  assert.deepEqual(got.chips.map(([, text]) => text), Array(4).fill('✗ 沒有測試'));
  assert.ok(got.marks.length >= 4 && got.marks.every(([, status]) => status === 'none'));
  assert.deepEqual(got.ratio, { text: '測試覆蓋 0 / 4', title: '測試覆蓋 0 / 4 個改動的函式有測試', color: got.tokens.partial });
  assert.ok(got.images.none.includes(got.tokens.gap), `red gutter bar: ${got.images.none}`);
  assert.match(got.images.none, /repeating-linear-gradient/, 'hatched background over untested code');
  await reveal(p, 3);
  await clickChip(p, 3);
  const pop = await popover(p);
  assert.equal(pop.symbol, '_onViewportMetaExample');
  assert.equal(pop.status, '沒有測試');
  assert.deepEqual(pop.cases, []);
  assert.equal(pop.suggestedTitle, '建議補的案例');
  assert.deepEqual(pop.suggested, [[
    ['Given', '用 mock 的 WebViewController 建立 SampleMenu'],
    ['When', '選「Viewport meta example」'],
    ['Then', 'loadHtmlString 收到 kViewportMetaPage'],
  ]]);
});

test('existing, removed and untested functions each mark their own lines; a pure deletion shows no ratio (English)', opts, async (t) => {
  const spec = readFixture('PR-10922');
  spec.meta.locale = 'en';
  const edge = (id) => spec.connections.find((c) => c.id === id).code;
  edge('menu-ctrl').tests = { functions: [{ symbol: 'oldName', lines: '-707', status: 'removed', by: ['OldTest.name'], why: 'Named the channel inline; the constant replaces it.' }] };
  edge('ctrl-page').tests = {
    functions: [
      { symbol: '_injectConsoleOverride', lines: '+913..+914', status: 'existing', by: ['T.inject'], why: 'The page script must post to the channel the app listens on.' },
      { symbol: 'gone', lines: '-935', status: 'removed', by: [], why: 'Posted to a hard-coded channel name.' },
      { symbol: 'script', lines: '+943', status: 'none', by: [], why: 'With the wrong channel name console logs never reach Dart.' },
    ],
    cases: [{ test: 'T.inject', covers: ['_injectConsoleOverride'], given: 'a controller', when: 'it injects', then: 'the script is added' }],
    suggested: [{ covers: 'script', given: 'a page', when: 'console.log runs', then: 'Dart gets the message' }],
  };
  const p = await open(t, spec, 'path-menu-ctrl-page');
  const deletion = await view(p);
  assert.deepEqual(deletion.marks, [['-707', 'removed']]);
  assert.deepEqual(deletion.chips, [['-707', 'Removed']]);
  assert.equal(deletion.ratio, null, 'a pure deletion has no ratio');
  assert.ok(deletion.images.removed.includes(deletion.tokens.gone), `grey gutter bar: ${deletion.images.removed}`);
  await reveal(p, 0);
  await clickChip(p, 0);
  assert.match((await popover(p)).why, /^What it did:/);
  assert.deepEqual((await popover(p)).cases, [{ test: 'OldTest.name', rows: [] }], 'an old test of a removed function shows by name');

  await p.run(`${panel}.selectEdge('ctrl-page'); true`);
  const mixed = await view(p);
  assert.deepEqual(mixed.marks, [['+913', 'existing'], ['+914', 'existing'], ['-935', 'removed'], ['+943', 'none']]);
  assert.deepEqual(mixed.chips, [['+913', '✓ 1 test'], ['-935', 'Removed'], ['+943', '✗ No test']]);
  assert.deepEqual(mixed.ratio, { text: 'Test coverage 1 / 2', title: 'Test coverage: 1 of 2 changed functions are tested', color: mixed.tokens.partial });
  assert.ok(mixed.images.existing.includes(mixed.tokens.seen));
  await reveal(p, 2);
  await clickChip(p, 2);
  const pop = await popover(p);
  assert.match(pop.why, /^Why test it:/);
  assert.equal(pop.suggestedTitle, 'Suggested test cases');
  assert.deepEqual(pop.suggested[0].map(([label]) => label), ['Given', 'When', 'Then']);
});

test('side by side, the marks stay on their lines, chips are not copied to the old side, and a chip still opens', opts, async (t) => {
  const p = await open(t, readFixture('PR-10922'), 'path-menu-ctrl');
  const unified = await view(p);
  await p.run(`document.getElementById('btn-cards').click(); document.getElementById('btn-tree').click(); true`);
  assert.equal(await p.run(`${hop}.querySelector('.code-diff').getAttribute('data-diff-mode')`), 'split');
  const split = await view(p);
  assert.deepEqual(split.marks, unified.marks);
  assert.deepEqual(split.chips, unified.chips);
  assert.deepEqual(await p.run(`[...${hop}.querySelectorAll('.cd-line[data-cover]')].map((l) => [l.classList.contains('cd-del') ? '1' : '2', l.style.gridColumn])
    .filter(([want, got]) => want !== got)`), [], 'deleted lines sit left, added lines right');
  assert.equal(await p.run(`${hop}.querySelectorAll('.cd-ghost .ct-chip').length`), 0);
  const before = await scrolls(p);
  await clickChip(p, 1);
  const pop = await popover(p);
  assert.deepEqual({ symbol: pop.symbol, anchored: pop.anchored, inside: pop.inside }, { symbol: 'setOnConsoleMessage', anchored: true, inside: true });
  assert.deepEqual(await scrolls(p), before);
});
