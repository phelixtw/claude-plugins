import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { validateSchema } from '../renderers/shared/validator.mjs';
import { layeredPlans, SCHEMES, schemesOf } from '../renderers/architecture/layered.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-layout-'));
const fixture = (name) => JSON.parse(fs.readFileSync(path.join(root, 'test/fixtures', name), 'utf8'));
const renderer = path.join(root, 'renderers/architecture/render-architecture.mjs');

function render(spec, name) {
  const input = path.join(tmp, `${name}.json`);
  const output = path.join(tmp, `${name}.html`);
  fs.writeFileSync(input, JSON.stringify(spec));
  const run = spawnSync(process.execPath, [renderer, input, output], { encoding: 'utf8' });
  return { run, output, html: run.status === 0 ? fs.readFileSync(output, 'utf8') : '' };
}
// Same three-node fixture as a WebView project: MVVM and WebView schemes.
const webviewFixture = () => {
  const spec = fixture('review-tiers.json');
  delete spec.meta.framework;
  spec.meta.webview = true;
  spec.components.forEach((c, i) => { c.code.layers = { mvvm: c.code.layers.mvvm, webview: ['host', 'bridge', 'content'][i] }; });
  return spec;
};
const payloadOf = (html) => JSON.parse(/<script id="archify-layered-svg" type="application\/json">([\s\S]*?)<\/script>/.exec(html)[1].replace(/\\u003c/g, '<').replace(/\\u003e/g, '>').replace(/\\u0026/g, '&'));

test('review page: Path / Lens are hidden and unbound, the layout switch takes their place', () => {
  const { run, html } = render(fixture('review-tiers.json'), 'toolbar-en');
  assert.equal(run.status, 0, run.stderr);
  assert.match(html, /<html lang="en" data-theme="dark" data-review>/);
  assert.match(html, /<div class="layout-switch" id="layout-switch" role="group" aria-label="Diagram layout" hidden>/);
  assert.doesNotMatch(html, /data-layout="freeform"/);
  assert.match(html, /data-layout="mvvm" aria-pressed="true"[^>]*>MVVM<\/button>/);
  assert.match(html, /data-layout="flutter" aria-pressed="false"[^>]*>Flutter<\/button>/);
  assert.doesNotMatch(html, /data-layout="bloc"/);
  // buttons and panels are hidden on review pages only; the shortcuts R / L do nothing there
  assert.match(html, /html\[data-review\] #btn-route-probe,\s*html\[data-review\] #btn-semantic-lens,/);
  assert.match(html, /html\[data-review\] #route-probe,\s*html\[data-review\] #semantic-lens \{ display: none !important/);
  assert.match(html, /hasAttribute\('data-review'\) && \/\^\[lr\]\$\/i\.test\(e\.key\)\) return;/);
  assert.match(html, /\['btn-route-probe', 'btn-semantic-lens'\]\.forEach\(function \(id\) \{ document\.getElementById\(id\)\.disabled = true;/);
  for (const id of ['btn-node-finder', 'btn-diagram-guide']) assert.doesNotMatch(html, new RegExp(`id="${id}"`));
  assert.doesNotMatch(html, /data-view="(?:out|in|reset)"/);
});

test('toolbar: app pages carry MVVM and Flutter buttons, WebView pages MVVM and WebView; the viewer drops the unused ones', () => {
  const app = render(fixture('review-tiers.json'), 'toolbar-app');
  assert.deepEqual(Object.keys(payloadOf(app.html)), ['mvvm', 'flutter']);
  const web = render(webviewFixture(), 'toolbar-webview');
  assert.equal(web.run.status, 0, web.run.stderr);
  assert.deepEqual(Object.keys(payloadOf(web.html)), ['mvvm', 'webview']);
  assert.match(web.html, /data-layout="webview" aria-pressed="false"[^>]*>WebView<\/button>/);
  // the viewer removes the buttons whose scheme has no geometry
  assert.match(web.html, /button\.remove\(\); \/\/ schemes of the other platform/);
});

test('zh-TW, zh-CN and en labels: MVVM, Flutter and WebView', () => {
  const spec = fixture('review-tiers.json');
  spec.meta.locale = 'zh-TW';
  const { html } = render(spec, 'toolbar-zh');
  assert.match(html, /aria-pressed="true"[^>]*>MVVM<\/button>/);
  assert.match(html, /data-layout="mvvm"[^>]*>MVVM<\/button>/);
  assert.match(html, /data-layout="flutter"[^>]*>Flutter<\/button>/);
  for (const locale of ['en', 'zh-CN', 'zh-TW']) {
    const web = webviewFixture();
    web.meta.locale = locale;
    assert.match(render(web, `toolbar-${locale}-webview`).html, /data-layout="webview"[^>]*>WebView<\/button>/);
  }
});

test('non-review pages keep Path / Lens and carry no layered geometry', () => {
  const example = JSON.parse(fs.readFileSync(path.join(root, 'examples/web-app.architecture.json'), 'utf8'));
  const { run, html } = render(example, 'plain');
  assert.equal(run.status, 0, run.stderr);
  assert.doesNotMatch(html, /<html[^>]* data-review/);
  assert.doesNotMatch(html, /<script id="archify-layered-svg"/);
  assert.doesNotMatch(html, /<g data-layout-slot=/);
  for (const id of ['btn-route-probe', 'btn-semantic-lens']) assert.match(html, new RegExp(`<button id="${id}"`));
  // the hide rules only apply under html[data-review]
  assert.doesNotMatch(template, /\n\s*(\.diagram-nav )?#btn-(route-probe|semantic-lens)\s*\{[^}]*display:\s*none/);
});

test('review pages ship one routed layered svg per scheme next to the freeform one', () => {
  const { run, html, output } = render(fixture('review-tiers.json'), 'tiers');
  assert.equal(run.status, 0, run.stderr);
  const payload = payloadOf(html);
  assert.deepEqual(Object.keys(payload), ['mvvm', 'flutter']);
  const freeform = /<svg\b[\s\S]*?<\/svg>/.exec(html)[0];
  // one svg in the page body; the layered ones live in a script
  assert.equal((html.match(/<svg\b/g) || []).length, 1);
  assert.doesNotMatch(freeform, /data-tier=/);
  const ids = (svg, attr) => [...svg.matchAll(new RegExp(`<(?:g|path)[^>]* ${attr}="([^"]+)"`, 'g'))].map((m) => m[1]);
  const labels = { mvvm: ['View', 'ViewModel', 'Model'], flutter: ['UI', 'Logic', 'Data'] };
  for (const scheme of ['mvvm', 'flutter']) {
    const layered = payload[scheme];
    assert.match(layered, /^<svg /);
    assert.deepEqual(SCHEMES[scheme].tiers.map(([id]) => id).length, 3);
    for (const [index, [tier]] of SCHEMES[scheme].tiers.entries()) {
      assert.match(layered, new RegExp(`data-tier="${tier}"`));
      assert.match(layered, new RegExp(`>${labels[scheme][index]}</tspan>`));
    }
    // same nodes, edges and labels in both
    assert.deepEqual(ids(layered, 'data-node-id'), ids(freeform, 'data-node-id'));
    assert.deepEqual(ids(layered, 'data-edge-id'), ids(freeform, 'data-edge-id'));
    // MVVM reads left to right, the Flutter layers top to bottom
    const at = (id, axis) => Number(new RegExp(`data-node-id="${id}"[\\s\\S]*?<rect x="([\\d.]+)" y="([\\d.]+)"`).exec(layered)[axis]);
    const axis = scheme === 'mvvm' ? 1 : 2;
    assert.ok(at('screen', axis) < at('coord', axis) && at('coord', axis) < at('store', axis));
    // both svgs expose the swap slots; the layered one differs in size
    for (const svg of [layered, freeform]) for (const slot of ['back', 'front', 'legend']) assert.match(svg, new RegExp(`data-layout-slot="${slot}"`));
    assert.notEqual(/viewBox="([^"]+)"/.exec(layered)[1], /viewBox="([^"]+)"/.exec(freeform)[1]);
  }
  // frames use the dashed boundary look with the title at their top-left
  assert.match(payload.mvvm, /<rect class="c-region" [^>]*\/>\s*<text class="t-cloud"[^>]*><tspan font-weight="700">Model<\/tspan><tspan class="t-tier-sub" dx="6">— Repository \/ Service<\/tspan>/);
  assert.match(payload.flutter, /<tspan class="t-tier-sub" dx="6">— Presentation<\/tspan>/);
  // check-render-output validates every layered svg too
  const check = spawnSync(process.execPath, [path.join(root, 'scripts/check-render-output.mjs'), output], { encoding: 'utf8' });
  assert.equal(check.status, 0, check.stdout);
  const report = JSON.parse(check.stdout);
  assert.deepEqual(report.checks.filter((c) => c.name.startsWith('layered_layout_')).map((c) => [c.name, c.ok]), [['layered_layout_mvvm', true], ['layered_layout_flutter', true]]);
});

test('WebView pages ship the Host / Bridge / Content geometry; without meta.webview there is none', () => {
  const { run, html } = render(webviewFixture(), 'webview-tiers');
  assert.equal(run.status, 0, run.stderr);
  const { webview } = payloadOf(html);
  for (const [tier, label] of [['host', 'Host'], ['bridge', 'Bridge'], ['content', 'Content']]) {
    assert.match(webview, new RegExp(`data-tier="${tier}"`));
    assert.match(webview, new RegExp(`>${label}</tspan>`));
  }
  const off = webviewFixture();
  off.meta.webview = false;
  assert.deepEqual(Object.keys(payloadOf(render(off, 'webview-off').html)), ['mvvm']);
});

test('a review without a layer for every node ships no geometry for that scheme', () => {
  const spec = fixture('review-tiers.json');
  delete spec.components[1].code.layers.flutter;
  const { run, html } = render(spec, 'no-layer');
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(Object.keys(payloadOf(html)), ['mvvm']);
  delete spec.components[1].code.layers.mvvm;
  assert.doesNotMatch(render(spec, 'no-layer-any').html, /<script id="archify-layered-svg"/);
  const noPlatform = fixture('review-tiers.json');
  delete noPlatform.meta.platform;
  assert.doesNotMatch(render(noPlatform, 'no-platform').html, /<script id="archify-layered-svg"/);
});

test('a layered layout that fails validation aborts delivery with a diagnostic', () => {
  const spec = fixture('review-tiers-hard.json');
  const input = path.join(tmp, 'hard.json');
  const output = path.join(tmp, 'hard.html');
  fs.writeFileSync(input, JSON.stringify(spec));
  const run = spawnSync(process.execPath, [path.join(root, 'bin/archify.mjs'), 'deliver', 'architecture', input, output, '--json'], { encoding: 'utf8' });
  assert.notEqual(run.status, 0);
  assert.equal(fs.existsSync(output), false);
  const receipt = JSON.parse(run.stdout);
  assert.equal(receipt.ok, false);
  assert.match(receipt.error, /validation failed/);
  assert.ok(receipt.diagnostics.some((d) => /code\.layers\.(mvvm|flutter)/.test(d.supportedFixes.join(' '))));
});

test('code.layers accepts the layer names of each scheme only; meta.platform is app', () => {
  const spec = fixture('review-tiers.json');
  assert.doesNotThrow(() => validateSchema('architecture', spec));
  spec.components[0].code.layers.mvvm = 'controller';
  assert.throws(() => validateSchema('architecture', spec));
  spec.components[0].code.layers.mvvm = 'view';
  spec.components[0].code.layers.webview = 'host';
  spec.components[0].layer = 'view';
  spec.meta.webview = true;
  assert.doesNotThrow(() => validateSchema('architecture', spec)); // the code-map layer stays a separate top-level field
  spec.components[0].code.layers.webview = 'ui';
  assert.throws(() => validateSchema('architecture', spec));
  spec.components[0].code.layers.webview = 'host';
  spec.meta.platform = 'web';
  assert.throws(() => validateSchema('architecture', spec));
  spec.meta.platform = 'desktop';
  assert.throws(() => validateSchema('architecture', spec));
});

test('layeredPlans: band order and labels per scheme, integer positions, empty without layers', () => {
  const spec = fixture('review-tiers.json');
  for (const scheme of ['mvvm', 'flutter']) {
    const [plan] = layeredPlans(spec.components, spec.connections, { scheme, profile: 'showcase', edgeLabelFont: 11 });
    assert.equal(plan.problems, 0);
    const axis = SCHEMES[scheme].columns ? 0 : 1; // columns left to right, bands top to bottom
    const y = (id) => plan.positions[id][axis];
    assert.ok(y('screen') < y('coord') && y('coord') < y('store'));
    assert.deepEqual(plan.bands.map((b) => b.tier), SCHEMES[scheme].tiers.map(([id]) => id));
    for (const position of Object.values(plan.positions)) assert.ok(position.every(Number.isInteger));
  }
  assert.deepEqual(layeredPlans(spec.components, spec.connections, { scheme: 'flutter' })[0].bands[0].label, ['UI', 'Presentation']);
  const missing = structuredClone(spec.components);
  delete missing[0].code.layers.mvvm;
  assert.deepEqual(layeredPlans(missing, spec.connections, { scheme: 'mvvm' }), []);
});

test('mvvm columns: equal-height dashed frames left to right, nodes inside their frame, empty layers keep a titled frame', () => {
  const spec = fixture('review-tiers.json');
  spec.components = spec.components.filter((c) => c.id !== 'store'); // the Model column stays empty
  spec.connections = spec.connections.filter((e) => e.to !== 'store');
  const [plan] = layeredPlans(spec.components, spec.connections, { scheme: 'mvvm', profile: 'showcase' });
  assert.equal(plan.problems, 0);
  const [view, viewmodel, model] = plan.bands;
  assert.deepEqual(plan.bands.map((b) => b.tier), ['view', 'viewmodel', 'model']);
  assert.ok(view.x + view.width < viewmodel.x && viewmodel.x + viewmodel.width < model.x);
  assert.equal(new Set(plan.bands.map((b) => `${b.y}/${b.height}`)).size, 1);
  assert.ok(model.width > 0 && model.label[0] === 'Model');
  for (const [id, band] of [['screen', view], ['coord', viewmodel]]) {
    const [x, y] = plan.positions[id];
    assert.ok(x >= band.x && x + 200 <= band.x + band.width && y > band.y && y + 70 < band.y + band.height, id);
  }
  // edges between columns leave right and enter left
  assert.deepEqual(plan.sides['screen-coord'], ['right', 'left']);
  const { run, html } = render(spec, 'columns-empty');
  assert.equal(run.status, 0, run.stderr);
  assert.match(payloadOf(html).mvvm, /data-tier="model"[\s\S]*?<rect class="c-region"/);
});

test('planner score matches the validation of the real render: no problems means the child render passes, problems means it fails', () => {
  for (const name of ['review-tiers.json', 'review-tiers-hard.json']) {
    const spec = fixture(name);
    const input = path.join(tmp, `agree-${name}`);
    fs.writeFileSync(input, JSON.stringify(spec));
    for (const scheme of ['mvvm', 'flutter']) {
      for (const plan of layeredPlans(spec.components, spec.connections, { scheme, profile: spec.meta.quality_profile })) {
        const child = spawnSync(process.execPath, [renderer, input, path.join(tmp, 'agree.html')], { encoding: 'utf8', env: { ...process.env, ARCHIFY_LAYERED: JSON.stringify(plan) } });
        assert.equal(child.status === 0, plan.problems === 0, `${name} ${scheme}: planner says ${plan.problems} problems, render exit ${child.status}`);
      }
    }
  }
});

test('meta.framework "flutter" gates the Flutter mode: other app code ships MVVM only and R18 does not ask for flutter layers', () => {
  const spec = fixture('review-tiers.json');
  assert.deepEqual(Object.keys(payloadOf(render(spec, 'fw-flutter').html)), ['mvvm', 'flutter']);
  delete spec.meta.framework;
  for (const c of spec.components) delete c.code.layers.flutter;
  const { run, html } = render(spec, 'fw-native');
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(Object.keys(payloadOf(html)), ['mvvm']);
  // the viewer drops the Flutter button of a page without that geometry
  assert.match(html, /button\.remove\(\); \/\/ schemes of the other platform/);
  assert.deepEqual(schemesOf(spec.meta), ['mvvm']);
  assert.deepEqual(schemesOf({ platform: 'app', framework: 'flutter' }), ['mvvm', 'flutter']);
  assert.deepEqual(schemesOf({ platform: 'app', webview: true }), ['mvvm', 'webview']);
  assert.deepEqual(schemesOf({ platform: 'app', framework: 'flutter', webview: true }), ['mvvm', 'flutter', 'webview']);
  assert.deepEqual(schemesOf({ platform: 'app', webview: false }), ['mvvm']);
  assert.deepEqual(schemesOf({ platform: 'web', webview: true }), []);
  assert.deepEqual(schemesOf({}), []);
  spec.meta.framework = 'react';
  assert.throws(() => validateSchema('architecture', spec));
});

test('layered layouts drop freeform route and label hints, so a freeform labelAt cannot strand a label away from its layered route', () => {
  const spec = fixture('review-tiers.json');
  const edge = spec.connections.find((e) => e.label);
  edge.labelAt = [270, 70]; // on the freeform route between screen and coord
  const { run, html } = render(spec, 'freeform-hints');
  assert.equal(run.status, 0, run.stderr);
  const labelRect = (svg) => new RegExp(`data-edge-id="${edge.id}">\\s*<rect x="([\\d.]+)" y="([\\d.]+)"`).exec(svg)?.slice(1).join(',');
  const freeform = labelRect(html.slice(html.indexOf('<svg'), html.indexOf('</svg>')));
  assert.ok(freeform);
  for (const svg of Object.values(payloadOf(html))) assert.notEqual(labelRect(svg), freeform, 'layered label kept the freeform labelAt');
});

test('review pages fail validation when a label sits away from its own connection', () => {
  const spec = fixture('review-tiers.json');
  const edge = spec.connections.find((e) => e.label);
  const [x, y] = spec.components.find((c) => c.id === edge.from).pos;
  edge.labelAt = [x - 400, y - 400];
  const { run } = render(spec, 'detached-label');
  assert.notEqual(run.status, 0);
  assert.match(run.stderr, /sits \d+px from its own connection/);
});
