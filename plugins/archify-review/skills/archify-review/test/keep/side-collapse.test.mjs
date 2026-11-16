import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome } from './helpers/review-page.mjs';

// On a path tab the toolbar's cards button collapses the side column; with the file tree also collapsed (#btn-tree),
// path-tab diffs go side by side.
const opts = { skip: skipChrome, timeout: 90000 };
const panel = '.path-panel:not([hidden])';

// A desktop width: below 901px the path panel stacks its columns.
async function open(t) {
  const p = await openPage(t, deliverSpec(scratchDir(t), readFixture('PR-10922')), '#ref=tab&tab=path-menu-ctrl');
  await p.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await p.run(`new Promise((done) => requestAnimationFrame(() => done(true)))`);
  return p;
}
const frame = (p, expr) => p.run(`new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(() => done(${expr}))))`);
const widths = (p) => frame(p, `({
  col: document.querySelector('${panel} .path-col').getBoundingClientRect().width,
  side: document.querySelector('${panel} .path-side').getBoundingClientRect().width,
  sections: [...document.querySelectorAll('${panel} .path-side .path-section')].filter((s) => s.offsetParent).length,
  expanded: document.getElementById('btn-cards').getAttribute('aria-pressed'),
})`);
const clickSide = (p) => p.run(`document.getElementById('btn-cards').click(); true`);
const clickTree = (p) => p.run(`document.getElementById('btn-tree').click(); true`);

test('the toolbar cards button collapses the path side column and gives its width to the diff column', opts, async (t) => {
  const p = await open(t);
  const open1 = await widths(p);
  assert.equal(open1.expanded, 'true');
  assert.equal(open1.sections, 2);
  await clickSide(p);
  const shut = await widths(p);
  assert.equal(shut.expanded, 'false');
  assert.equal(shut.sections, 0);
  assert.equal(shut.side, 0, 'the side column is hidden');
  assert.ok(shut.col > open1.col + 200, `diff column ${shut.col}px vs ${open1.col}px`);
  // one state for every path tab
  await p.run(`Archify.pathTabs.restore('path-ctrl-page'); true`);
  assert.equal(await p.run(`document.querySelector('${panel} .path-side').offsetWidth`), 0);
  await p.run(`Archify.pathTabs.restore('path-menu-ctrl'); true`);
  await clickSide(p);
  assert.deepEqual(await widths(p), open1);
});

test('with both sides collapsed the diff is side by side: old lines left, new lines right, paired by row', opts, async (t) => {
  const p = await open(t);
  const mode = () => p.run(`[...document.querySelectorAll('${panel} .code-diff')].map((pre) => pre.getAttribute('data-diff-mode') || 'unified')`);
  await clickSide(p);
  assert.ok((await mode()).every((m) => m === 'unified'), 'one side collapsed stays unified');
  await clickTree(p);
  assert.ok((await mode()).every((m) => m === 'split'));
  const layout = await p.run(`(() => {
    const pre = document.querySelector('${panel} .code-diff');
    const cell = (line) => ({ row: line.style.gridRow, col: line.style.gridColumn });
    const ctx = [...pre.querySelectorAll('.cd-line.cd-ctx[data-n]')];
    const ghosts = [...pre.querySelectorAll('.cd-ghost')];
    const del = pre.querySelector('.cd-line.cd-del');
    let add = del.nextElementSibling;
    while (add && !add.classList.contains('cd-add')) add = add.nextElementSibling;
    return {
      ctx: ctx.length,
      ghosts: ghosts.length,
      ghostNumbers: ghosts.map((g) => g.querySelector('.cx-n').textContent),
      ctxOld: ctx.map((l) => l.getAttribute('data-ctx-o')),
      ctxCells: ctx.map(cell).every((c, i) => c.col === '2' && c.row === ghosts[i].style.gridRow && ghosts[i].style.gridColumn === '1'),
      del: cell(del), add: cell(add),
      delNo: del.querySelector('.cx-n').textContent, delO: del.getAttribute('data-o'),
      hunkCol: pre.querySelector('.cd-line.cd-hunk').style.gridColumn,
    };
  })()`);
  assert.ok(layout.ctx > 0);
  assert.equal(layout.ghosts, layout.ctx);
  assert.deepEqual(layout.ghostNumbers, layout.ctxOld);
  assert.ok(layout.ctxOld.every(Boolean));
  assert.equal(layout.ctxCells, true);
  assert.equal(layout.del.col, '1');
  assert.equal(layout.add.col, '2');
  assert.equal(layout.del.row, layout.add.row, 'the first - line and its + line share a row');
  assert.equal(layout.delNo, layout.delO);
  assert.equal(layout.hunkCol, '1 / -1');
  // expanding either side brings the unified diff back, without leftovers
  await clickTree(p);
  assert.ok((await mode()).every((m) => m === 'unified'));
  assert.equal(await p.run(`document.querySelectorAll('${panel} .cd-ghost').length`), 0);
  assert.equal(await p.run(`[...document.querySelectorAll('${panel} .cd-line')].every((l) => !l.style.gridRow && !l.style.gridColumn)`), true);
});

test('in side-by-side mode the copy link and the reviewed checkbox work as in unified mode', opts, async (t) => {
  const p = await open(t);
  await p.run(`document.querySelector('${panel} .path-hop .ref-copy-block').click(); true`);
  await p.waitUntil('window.__copied.length === 1');
  const unified = await p.run('window.__copied[0]');
  await clickSide(p);
  await clickTree(p);
  assert.equal(await p.run(`document.querySelector('${panel} .code-diff').getAttribute('data-diff-mode')`), 'split');
  await p.run(`document.querySelector('${panel} .path-hop .ref-copy-block').click(); true`);
  await p.waitUntil('window.__copied.length === 2');
  assert.equal(await p.run('window.__copied[1]'), unified);
  const box = `document.querySelector('${panel} .path-hop input[data-review-flow]')`;
  await p.run(`${box}.click(); true`);
  assert.equal(await p.run(`${box}.checked`), true);
  assert.equal(await p.run(`document.querySelector('.path-edge-g[data-edge-id="menu-ctrl"]').classList.contains('is-seen')`), true);
});

test('each side section collapses on its own, for every path tab, and the other section takes the room', opts, async (t) => {
  const p = await open(t);
  const sections = () => frame(p, `[...document.querySelectorAll('${panel} .path-side .path-section')].map((s) => Math.round(s.getBoundingClientRect().height))`);
  const before = await sections();
  await p.run(`document.querySelector('${panel} .path-section .card-collapse').click(); true`);
  const after = await sections();
  assert.ok(after[0] < before[0] / 2, `collapsed notes ${after[0]}px vs ${before[0]}px`);
  assert.ok(after[1] > before[1] + 50, 'the path diagram takes the room');
  await p.run(`Archify.pathTabs.restore('path-ctrl-page'); true`);
  assert.equal(await p.run(`document.querySelector('${panel} .path-section').hasAttribute('data-collapsed')`), true);
});
