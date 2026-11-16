import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome } from './helpers/review-page.mjs';

// The fixed file tree must line up with the diagram panel as soon as the page has loaded, without a scroll or resize.
const opts = { skip: skipChrome, timeout: 90000 };
const tops = (p) => p.run(`(() => { const t = document.querySelector('.review-tree').getBoundingClientRect(); const d = document.querySelector('.diagram-container').getBoundingClientRect();
  return { treeTop: t.top, treeBottom: t.bottom, panelTop: d.top, panelBottom: Math.min(d.bottom, innerHeight - 8), ih: innerHeight }; })()`);

for (const [width, height] of [[1600, 1000], [1440, 900], [1280, 720]]) {
  test(`on first load at ${width}x${height} the file tree top and bottom match the diagram panel`, opts, async (t) => {
    const p = await openPage(t, deliverSpec(scratchDir(t), readFixture('PR-9151')));
    await p.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
    await p.load(p.url);
    await new Promise((resolve) => setTimeout(resolve, 300)); // a frame or two for observers; no scroll, no resize
    const m = await tops(p);
    assert.ok(Math.abs(m.treeTop - Math.max(m.panelTop, 8)) <= 1, `tree top ${m.treeTop} vs panel top ${m.panelTop}`);
    assert.ok(Math.abs(m.treeBottom - m.panelBottom) <= 1, `tree bottom ${m.treeBottom} vs panel bottom ${m.panelBottom}`);
  });
}
