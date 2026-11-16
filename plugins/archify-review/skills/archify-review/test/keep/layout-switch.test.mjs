import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome } from './helpers/review-page.mjs';

// Layout switch of a delivered code-review page: an app opens in MVVM and offers MVVM / Flutter, WebView on a WebView project. There is no Freeform.
const opts = { skip: skipChrome, timeout: 90000 };
const boxes = (p) => p.run(`Object.fromEntries([...document.querySelectorAll('.diagram-container > svg [data-node-id][data-node-kind]')].map((n) => { const r = n.getBoundingClientRect(); return [n.dataset.nodeId, [Math.round(r.x), Math.round(r.y)].join(',')]; }))`);
const mode = (p) => p.run(`({ html: document.documentElement.dataset.layout, pressed: [...document.querySelectorAll('#layout-switch button[data-layout]')].filter((b) => b.getAttribute('aria-pressed') === 'true').map((b) => b.dataset.layout) })`);
const pick = async (p, name) => { await p.click(`#layout-switch button[data-layout="${name}"]`); await p.run('Archify.viewerChromeLayout.whenStable()'); };

test('an app review opens in MVVM and offers MVVM and Flutter, no Freeform', opts, async (t) => {
  const p = await openPage(t, deliverSpec(scratchDir(t), readFixture('PR-10922')));
  assert.deepEqual(await p.run(`[...document.querySelectorAll('#layout-switch button[data-layout]')].map((b) => b.textContent)`), ['MVVM', 'Flutter']);
  assert.deepEqual(await mode(p), { html: 'mvvm', pressed: ['mvvm'] });
  assert.ok(await p.run(`document.querySelectorAll('.diagram-container > svg [data-tier]').length > 0`), 'the MVVM layer frames are drawn on load');
});

test('switching to Flutter moves the nodes, keeps all of them, and MVVM restores its places', opts, async (t) => {
  const spec = readFixture('PR-10922');
  const p = await openPage(t, deliverSpec(scratchDir(t), spec));
  const mvvm = await boxes(p);
  const ids = Object.keys(mvvm).sort();
  assert.equal(ids.length, spec.components.length);
  await pick(p, 'flutter');
  assert.deepEqual(await mode(p), { html: 'flutter', pressed: ['flutter'] });
  const flutter = await boxes(p);
  assert.deepEqual(Object.keys(flutter).sort(), ids);
  assert.notDeepEqual(flutter, mvvm);
  await pick(p, 'mvvm');
  assert.deepEqual(await boxes(p), mvvm);
  assert.deepEqual(await mode(p), { html: 'mvvm', pressed: ['mvvm'] });
});

test('in MVVM each layer gets its own column and the layer names are drawn', opts, async (t) => {
  const spec = readFixture('PR-10922');
  const p = await openPage(t, deliverSpec(scratchDir(t), spec));
  await pick(p, 'mvvm');
  const rects = await p.run(`Object.fromEntries([...document.querySelectorAll('.diagram-container > svg [data-node-id][data-node-kind]')].map((n) => { const r = n.getBoundingClientRect(); return [n.dataset.nodeId, [r.left, r.right]]; }))`);
  const span = {};
  for (const c of spec.components) {
    const s = (span[c.code.layers.mvvm] ||= [Infinity, -Infinity]);
    s[0] = Math.min(s[0], rects[c.id][0]); s[1] = Math.max(s[1], rects[c.id][1]);
  }
  const columns = Object.values(span).sort((a, b) => a[0] - b[0]);
  assert.ok(columns.length >= 2);
  columns.slice(1).forEach((column, i) => assert.ok(column[0] >= columns[i][1], 'columns do not overlap'));
  const text = await p.run(`document.querySelector('.diagram-container > svg').textContent`);
  for (const word of ['View', 'ViewModel', 'Model']) assert.ok(text.includes(word), `${word} band label`);
});

test('ticks and the open hop tab survive a layout switch', opts, async (t) => {
  const p = await openPage(t, deliverSpec(scratchDir(t), readFixture('PR-10922')), '#ref=tab&tab=path-menu-ctrl');
  await p.click('input[data-review-flow="menu-ctrl"]');
  await pick(p, 'flutter');
  await pick(p, 'mvvm');
  assert.equal(await p.run(`document.querySelector('input[data-review-flow="menu-ctrl"]').checked || document.querySelector('input[data-review-flow="menu-ctrl"]').indeterminate`), true);
  assert.equal(await p.run(`document.querySelectorAll('.path-panel:not([hidden])').length`), 1);
});

// A WebView project: PR-10922 with meta.webview and one webview layer per node.
const WEBVIEW_LAYERS = { changelog: 'host', menu: 'host', pubspec: 'host', ctrl: 'host', ucc: 'bridge', page: 'content' };
const webviewSpec = (locale = 'en', layers = WEBVIEW_LAYERS) => {
  const spec = readFixture('PR-10922');
  spec.meta.locale = locale;
  spec.meta.webview = true;
  spec.components.forEach((c) => { c.code.layers.webview = layers[c.id]; });
  return spec;
};
const buttons = (p) => p.run(`[...document.querySelectorAll('#layout-switch button[data-layout]')].map((b) => [b.dataset.layout, b.textContent])`);
const bands = (p) => p.run(`[...document.querySelectorAll('.diagram-container > svg [data-tier]')].map((g) => { const r = g.getBoundingClientRect(); return { tier: g.dataset.tier, top: r.top, bottom: r.bottom, left: r.left, right: r.right, text: g.textContent.replace(/\\s+/g, ' ').trim() }; })`);
const rects = (p) => p.run(`Object.fromEntries([...document.querySelectorAll('.diagram-container > svg [data-node-id][data-node-kind]')].map((n) => { const r = n.getBoundingClientRect(); return [n.dataset.nodeId, { cx: (r.left + r.right) / 2, cy: (r.top + r.bottom) / 2 }]; }))`);
// Real mouse click on a point of the edge's hit rail that no other element covers.
const clickEdge = async (p, id) => {
  const pt = await p.run(`(() => {
    const rail = document.querySelector('.diagram-container > svg [data-relationship-id="${id}"] > path.relationship-hit-rail');
    rail.scrollIntoView({ block: 'center', inline: 'center' });
    const m = rail.getScreenCTM(); const len = rail.getTotalLength();
    for (let i = 1; i < 40; i++) {
      const q = rail.getPointAtLength(len * i / 40);
      const x = m.a * q.x + m.c * q.y + m.e, y = m.b * q.x + m.d * q.y + m.f;
      if (document.elementFromPoint(x, y)?.closest('[data-relationship-id]')?.dataset.relationshipId === '${id}') return { x, y };
    }
    return null;
  })()`);
  assert.ok(pt, `edge ${id} has an uncovered point to click`);
  for (const type of ['mousePressed', 'mouseReleased']) await p.send('Input.dispatchMouseEvent', { type, x: pt.x, y: pt.y, button: 'left', clickCount: 1 });
  await p.waitUntil(`location.hash === '#relation=${id}'`, `edge ${id} opened`);
  const title = await p.run(`document.querySelector('.relationship-lens-title')?.textContent`);
  return title;
};

test('the WebView button shows with meta.webview true, beside MVVM and Flutter, and not when false', opts, async (t) => {
  const on = await openPage(t, deliverSpec(scratchDir(t), webviewSpec()));
  assert.deepEqual(await buttons(on), [['mvvm', 'MVVM'], ['flutter', 'Flutter'], ['webview', 'WebView']]);
  const spec = webviewSpec();
  spec.meta.webview = false;
  spec.components.forEach((c) => delete c.code.layers.webview);
  const off = await openPage(t, deliverSpec(scratchDir(t), spec));
  assert.deepEqual((await buttons(off)).map((b) => b[0]), ['mvvm', 'flutter']);
});

for (const locale of ['en', 'zh-CN', 'zh-TW']) {
  test(`WebView layout in ${locale}: WebView button, Host / Bridge / Content bands, every node in its layer`, opts, async (t) => {
    const spec = webviewSpec(locale);
    const p = await openPage(t, deliverSpec(scratchDir(t), spec));
    assert.equal((await buttons(p)).find((b) => b[0] === 'webview')[1], 'WebView');
    await pick(p, 'webview');
    assert.deepEqual(await mode(p), { html: 'webview', pressed: ['webview'] });
    const bs = await bands(p);
    assert.deepEqual(bs.map((b) => b.tier), ['host', 'bridge', 'content']);
    for (const [b, title] of bs.map((x, i) => [x, ['Host', 'Bridge', 'Content'][i]])) assert.ok(b.text.includes(title), `${title} band title`);
    bs.slice(1).forEach((b, i) => assert.ok(b.top >= bs[i].bottom, 'bands stack top to bottom without overlap'));
    const at = await rects(p);
    assert.equal(Object.keys(at).length, spec.components.length);
    for (const c of spec.components) {
      const band = bs.find((b) => b.tier === c.code.layers.webview);
      assert.ok(at[c.id].cy > band.top && at[c.id].cy < band.bottom && at[c.id].cx > band.left && at[c.id].cx < band.right, `${c.id} sits in ${band.tier}`);
    }
  });
}

test('after the WebView layout re-routes the edges, each is clickable and opens its own change', opts, async (t) => {
  const spec = webviewSpec();
  const html = deliverSpec(scratchDir(t), spec);
  for (const c of spec.connections) { // a click leaves the focus on the node, so every edge gets a fresh page
    const p = await openPage(t, html);
    await pick(p, 'webview');
    const from = spec.components.find((n) => n.id === c.from);
    assert.equal(await clickEdge(p, c.id), from.code.files ? from.code.files.map((f) => f.path).join('\n') : from.label, `edge ${c.id} opens the change of ${from.id}`);
  }
});

test('switching WebView -> MVVM / Flutter keeps every node and restores MVVM', opts, async (t) => {
  const p = await openPage(t, deliverSpec(scratchDir(t), webviewSpec()));
  const mvvm = await boxes(p);
  const ids = Object.keys(mvvm).sort();
  await pick(p, 'webview');
  const webview = await boxes(p);
  assert.deepEqual(Object.keys(webview).sort(), ids);
  assert.notDeepEqual(webview, mvvm);
  for (const name of ['flutter', 'webview', 'mvvm']) {
    await pick(p, name);
    assert.deepEqual(await mode(p), { html: name, pressed: [name] });
    const now = await boxes(p);
    assert.deepEqual(Object.keys(now).sort(), ids);
    if (name === 'mvvm') assert.deepEqual(now, mvvm);
    if (name === 'webview') assert.deepEqual(now, webview);
  }
});

test('an empty Content layer keeps a flat frame and the layout stays intact', opts, async (t) => {
  const layers = Object.fromEntries(Object.keys(WEBVIEW_LAYERS).map((id) => [id, WEBVIEW_LAYERS[id] === 'content' ? 'host' : WEBVIEW_LAYERS[id]]));
  const spec = webviewSpec('en', layers);
  const p = await openPage(t, deliverSpec(scratchDir(t), spec));
  await pick(p, 'webview');
  const bs = await bands(p);
  assert.deepEqual(bs.map((b) => b.tier), ['host', 'bridge', 'content']);
  const content = bs[2];
  assert.ok(content.bottom - content.top < bs[0].bottom - bs[0].top, 'empty frame is flatter than a filled one');
  assert.ok(content.text.includes('Content'));
  const at = await rects(p);
  assert.equal(Object.keys(at).length, spec.components.length);
  for (const c of spec.components) assert.ok(at[c.id].cy < content.top || at[c.id].cy > content.bottom, `${c.id} is not inside the empty frame`);
  assert.ok(await clickEdge(p, spec.connections[0].id), 'an edge still opens its change');
  await pick(p, 'mvvm');
  assert.deepEqual(await mode(p), { html: 'mvvm', pressed: ['mvvm'] });
});
