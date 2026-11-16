import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome, root } from './helpers/review-page.mjs';

// The diagram fits its frame at 100%; a zoom slider and a trackpad pinch (ctrl+wheel) scale it up to 400%,
// and only a zoomed diagram pans (drag or two-finger scroll).
const opts = { skip: skipChrome, timeout: 90000 };
const SVG = '.diagram-container > svg';
const svgTransform = (p) => p.run(`getComputedStyle(document.querySelector('${SVG}')).transform`);
const svgRect = (p) => p.run(`(() => { const r = document.querySelector('${SVG}').getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height }; })()`);
const frame = (p) => p.run(`(() => { const r = document.querySelector('.diagram-container').getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height }; })()`);
const slide = (p, v) => p.run(`(() => { const s = document.querySelector('.diagram-nav input.zoom-slider'); s.value = ${v}; s.dispatchEvent(new Event('input', { bubbles: true })); return true; })()`);
const wheel = (p, init) => p.run(`(() => { const c = document.querySelector('.diagram-container'); const e = new WheelEvent('wheel', Object.assign({ cancelable: true, bubbles: true }, ${JSON.stringify(init)})); c.dispatchEvent(e); return e.defaultPrevented; })()`);
const zoomValue = (p) => p.run(`Number(document.querySelector('.diagram-nav input.zoom-slider').value)`);

async function open(t, w = 1280, h = 800) {
  const p = await openPage(t, deliverSpec(scratchDir(t), readFixture('PR-10922')));
  await p.send('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: 1, mobile: false });
  await p.load(p.url);
  return p;
}

test('the template ships none of the old camera engine', () => {
  const html = fs.readFileSync(`${root}/assets/template.html`, 'utf8');
  for (const word of ['is-pannable', 'diagram-scroll', 'data-camera', 'is-camera-moving', 'readableState', 'frameDesktop', 'overview-map', 'Archify.radar']) {
    assert.ok(!html.includes(word), `template still contains ${word}`);
  }
});

test('at 100% the diagram is untransformed: wheel and drag pass through and a node click still focuses', opts, async (t) => {
  const p = await open(t, 1280, 640);
  assert.equal(await zoomValue(p), 100);
  assert.equal(await svgTransform(p), 'none');
  assert.equal(await wheel(p, { deltaY: 120 }), false, 'plain wheel is not intercepted at 100%');
  const box = await svgRect(p);
  await p.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: box.x + 4, y: box.y + 4, button: 'left', clickCount: 1 });
  await p.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: box.x + 84, y: box.y + 44, button: 'left' });
  await p.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: box.x + 84, y: box.y + 44, button: 'left', clickCount: 1 });
  assert.equal(await svgTransform(p), 'none');
  await p.run(`document.querySelector('${SVG} [data-node-id][data-node-kind]').dispatchEvent(new MouseEvent('click', { bubbles: true })); true`);
  await p.waitUntil(`location.hash.startsWith('#focus=')`, 'node focus');
});

test('the zoom slider sits in the diagram toolbar and scales the diagram; a zoomed node still opens on click', opts, async (t) => {
  const p = await open(t);
  assert.equal(await p.run(`!!document.querySelector('.diagram-nav input.zoom-slider[type=range][min="100"][max="400"]')`), true);
  assert.ok(await p.run(`document.querySelector('.diagram-nav input.zoom-slider').getAttribute('aria-label').length > 0`));
  const before = await svgRect(p);
  await slide(p, 200);
  const after = await svgRect(p);
  assert.ok(Math.abs(after.w / before.w - 2) < 0.02, `width ratio ${after.w / before.w}`);
  assert.match(await p.run(`document.querySelector('.diagram-nav .zoom-value').textContent`), /200%/);
  // the diagram stays inside the frame's bounds: no gap at any edge
  const f = await frame(p);
  assert.ok(after.x <= f.x + 1 && after.y <= f.y + 1 && after.x + after.w >= f.x + f.w - 1 && after.y + after.h >= f.y + f.h - 1, JSON.stringify({ after, f }));
  const node = await p.run(`(() => { for (const n of document.querySelectorAll('${SVG} [data-node-id][data-node-kind]')) { const r = n.getBoundingClientRect(); const c = document.querySelector('.diagram-container').getBoundingClientRect(); const l = Math.max(r.left, c.left), rt = Math.min(r.right, c.right), tp = Math.max(r.top, c.top), bt = Math.min(r.bottom, c.bottom); if (rt - l < 8 || bt - tp < 8) continue; const x = (l + rt) / 2, y = (tp + bt) / 2; const hit = document.elementFromPoint(x, y); if (hit && n.contains(hit)) return { x, y }; } return null; })()`);
  assert.ok(node, 'a zoomed node is on screen and takes a click');
  await p.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: node.x, y: node.y, button: 'left', clickCount: 1 });
  await p.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: node.x, y: node.y, button: 'left', clickCount: 1 });
  await p.waitUntil(`location.hash.startsWith('#focus=')`, 'node focus while zoomed');
});

test('a trackpad pinch (ctrl+wheel) zooms around the pointer and never zooms the page', opts, async (t) => {
  const p = await open(t);
  const f = await frame(p);
  const at = { x: f.x + f.w * 0.3, y: f.y + f.h * 0.4 };
  const pointIn = `((x, y) => { const s = document.querySelector('${SVG}'); const r = s.getBoundingClientRect(); return { u: (x - r.x) / r.width, v: (y - r.y) / r.height }; })(${at.x}, ${at.y})`;
  const before = await p.run(pointIn);
  assert.equal(await wheel(p, { ctrlKey: true, deltaY: -50, clientX: at.x, clientY: at.y }), true, 'pinch is intercepted');
  assert.ok(await zoomValue(p) > 100);
  const after = await p.run(pointIn);
  const r = await svgRect(p);
  assert.ok(Math.abs((after.u - before.u) * r.w) < 2 && Math.abs((after.v - before.v) * r.h) < 2, JSON.stringify({ before, after }));
  await wheel(p, { ctrlKey: true, deltaY: 5000, clientX: at.x, clientY: at.y });
  assert.equal(await zoomValue(p), 100, 'pinching out stops at 100%');
  assert.equal(await svgTransform(p), 'none');
});

test('a zoomed diagram pans by two-finger scroll and by drag, within its bounds, and a drag does not click a node', opts, async (t) => {
  const p = await open(t);
  await slide(p, 300);
  const a = await svgRect(p);
  assert.equal(await wheel(p, { deltaX: 60, deltaY: 40 }), true, 'scroll pans a zoomed diagram');
  const b = await svgRect(p);
  assert.ok(b.x < a.x && b.y < a.y, JSON.stringify({ a, b }));
  const f = await frame(p);
  await p.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: f.x + f.w / 2, y: f.y + f.h / 2, button: 'left', clickCount: 1 });
  await p.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: f.x + f.w / 2 + 50, y: f.y + f.h / 2 + 30, button: 'left' });
  await p.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: f.x + f.w / 2 + 50, y: f.y + f.h / 2 + 30, button: 'left', clickCount: 1 });
  const c = await svgRect(p);
  assert.ok(Math.abs(c.x - b.x - 50) < 2 && Math.abs(c.y - b.y - 30) < 2, JSON.stringify({ b, c }));
  assert.ok(!(await p.run('location.hash')).startsWith('#focus='), 'a drag is not a node click');
  await wheel(p, { deltaX: -100000, deltaY: -100000 });
  const d = await svgRect(p);
  assert.ok(Math.abs(d.x - f.x) < 1 && Math.abs(d.y - f.y) < 1, 'panning stops at the top-left edge');
});

test('switching the layout resets the zoom to 100%', opts, async (t) => {
  const p = await open(t);
  await slide(p, 250);
  await p.run(`document.querySelector('#layout-switch [data-layout=flutter]').click(); true`);
  assert.equal(await zoomValue(p), 100);
  assert.equal(await svgTransform(p), 'none');
});

test('a zoomed diagram keeps its fitted size through a relayout and a trip to a path tab', opts, async (t) => {
  const p = await open(t, 1600, 800);
  const height = () => p.run(`getComputedStyle(document.querySelector('${SVG}')).height`);
  const h0 = await height();
  await slide(p, 220);
  await p.run(`window.dispatchEvent(new Event('resize')); true`);
  await p.run(`new Promise((r) => setTimeout(r, 300)).then(() => true)`);
  assert.equal(await height(), h0, 'a resize while zoomed keeps the fitted height');
  await p.run(`location.hash = '#ref=tab&tab=path-menu-ctrl'; true`);
  await p.waitUntil(`document.documentElement.dataset.viewTab === 'path'`, 'path tab');
  await p.run(`document.querySelector('[role=tab]').click(); true`);
  await p.waitUntil(`document.documentElement.dataset.viewTab !== 'path'`, 'back to the diagram');
  await p.run(`new Promise((r) => setTimeout(r, 300)).then(() => true)`);
  assert.equal(await height(), h0, 'the diagram keeps its fitted height after a path tab');
  assert.equal(await zoomValue(p), 220);
});
