import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome } from './keep/helpers/review-page.mjs';

// Clicking or selecting text in the current hop keeps the code column where the reader scrolled it;
// only moving to another hop starts that hop at the top.
const opts = { skip: skipChrome, timeout: 90000 };
const col = `document.querySelector('.path-panel:not([hidden]) .path-col')`;

test('a click or a text selection in the current hop keeps the column scroll; another hop starts at the top', opts, async (t) => {
  const p = await openPage(t, deliverSpec(scratchDir(t), readFixture('PR-10922')), '#ref=tab&tab=path-menu-ctrl-page');
  await p.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  // A reader who resized the diffs taller than the column scrolls the column itself.
  await p.run(`${col}.querySelectorAll('.path-hop .code-diff').forEach((pre) => { pre.style.height = '1600px'; }); true`);
  await p.run(`${col}.scrollTop = 400; true`);
  assert.equal(await p.run(`${col}.scrollTop`), 400);
  // A diff line in view, away from the gutter, where a reader would click or start a selection.
  const point = (index) => p.run(`(() => {
    const box = ${col}.getBoundingClientRect();
    const line = [...${col}.querySelectorAll('.path-hop.is-current .cd-line')].filter((l) => {
      const r = l.getBoundingClientRect();
      return r.top > box.top + 20 && r.bottom < box.bottom - 20;
    })[${index}];
    const r = line.getBoundingClientRect();
    return { x: Math.round(r.left + 120), y: Math.round(r.top + r.height / 2) };
  })()`);
  const mouse = (type, at) => p.send('Input.dispatchMouseEvent', { type, ...at, button: 'left', clickCount: 1 });

  const at = await point(0);
  await mouse('mousePressed', at);
  await mouse('mouseReleased', at);
  assert.equal(await p.run(`${col}.scrollTop`), 400, 'a click on code text keeps the scroll');

  const from = await point(1);
  const to = await point(4);
  await mouse('mousePressed', from);
  await p.send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...to, button: 'left', buttons: 1 });
  await mouse('mouseReleased', to);
  assert.ok((await p.run('String(getSelection())')).trim().length > 0, 'the drag selected code text');
  assert.equal(await p.run(`${col}.scrollTop`), 400, 'a drag selection keeps the scroll');

  await p.run(`${col}.querySelector('.path-stepper button:nth-child(2)').click(); true`);
  assert.equal(await p.run(`${col}.querySelector('.path-hop.is-current').dataset.edge`), 'ctrl-page');
  assert.equal(await p.run(`${col}.scrollTop`), 0, 'the next hop starts at its top');
});
