import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { deliverSpec, openPage, readFixture, runCli, scratchDir, skipChrome } from './helpers/review-page.mjs';
import { serializeScriptJson } from '../../renderers/shared/utils.mjs';

// visual-check on delivered code-review pages in a real Chrome (ARCHIFY_CHROME).
const opts = { skip: skipChrome, timeout: 120000 };

for (const ticket of ['PR-10922', 'PR-9151']) {
  test(`${ticket}: visual-check passes four viewports, writes the receipt and screenshots, and leaves the html untouched`, opts, (t) => {
    const dir = scratchDir(t);
    const html = deliverSpec(dir, readFixture(ticket));
    const before = fs.readFileSync(html);
    const r = runCli('visual-check', html, '--json');
    assert.equal(r.status, 0, r.stdout + r.stderr);
    const receipt = JSON.parse(r.stdout);
    assert.equal(receipt.status, 'pass');
    assert.equal(receipt.containment.status, 'pass');
    const sizes = receipt.containment.viewports.map((v) => `${v.width}x${v.height}`);
    for (const size of ['1440x900', '1600x1000', '1920x1080', '2048x1320']) assert.ok(sizes.includes(size), `viewport ${size}`);
    assert.ok(receipt.containment.viewports.length >= 4 && receipt.containment.viewports.every((v) => v.ok && v.readabilityOk));
    const shots = fs.readdirSync(dir).filter((f) => f.endsWith('.png'));
    assert.ok(shots.length >= 4, `screenshots written, got ${shots}`);
    assert.ok(fs.existsSync(path.join(dir, 'review.visual-check.json')));
    assert.deepEqual(fs.readFileSync(html), before);
  });
}

test('visual-check fails a review page that lost a review feature (file tree collapse toggle)', opts, (t) => {
  const dir = scratchDir(t);
  const html = deliverSpec(dir, readFixture('PR-10922'));
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace('treeButton.hidden = false;', '');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.deepEqual(d?.evidence.missingReviewFeatures, ['file tree collapse toggle']);
});

// The CHANGELOG diff moves onto the connected node `menu`, so no connection carries it; the menu path tab must show it.
function nodeOnlyDiffSpec() {
  const spec = readFixture('PR-10922');
  const node = (id) => spec.components.find((c) => c.id === id);
  node('menu').code = { ...node('menu').code, files: node('changelog').code.files };
  node('changelog').code = { ...node('changelog').code, files: node('changelog').code.files.map(({ diff, ...file }) => file) };
  return spec;
}

test('a file whose diff sits only on a node shows in the path tab of that node; visual-check fails when it does not', opts, (t) => {
  const dir = scratchDir(t);
  const html = deliverSpec(dir, nodeOnlyDiffSpec());
  assert.equal(JSON.parse(runCli('visual-check', html, '--json').stdout).status, 'pass');
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace('if (!file.diff || shown[file.path]) return;', 'return;');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.deepEqual(d?.evidence.missingReviewFeatures, [
    'path-tab diff for packages/webview_flutter/webview_flutter_wkwebview/CHANGELOG.md',
    'path-tab diff for packages/webview_flutter/webview_flutter_wkwebview/pubspec.yaml',
  ]);
});

test('visual-check fails when a path-tab diff has no reviewed checkbox', opts, (t) => {
  const dir = scratchDir(t);
  const html = deliverSpec(dir, nodeOnlyDiffSpec());
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace('if (Archify.reviewTree) card.appendChild(Archify.reviewTree.fileBox(file.path));', '');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.deepEqual(d?.evidence.missingReviewFeatures, [
    'reviewed checkbox on the path-tab diff of packages/webview_flutter/webview_flutter_wkwebview/CHANGELOG.md',
    'reviewed checkbox on the path-tab diff of packages/webview_flutter/webview_flutter_wkwebview/pubspec.yaml',
  ]);
});

test('ticking a node-only file in its path tab marks the file reviewed in the file tree', opts, async (t) => {
  const p = await openPage(t, deliverSpec(scratchDir(t), nodeOnlyDiffSpec()), '#ref=tab&tab=path-menu-ctrl');
  const box = `document.querySelector('.path-panel:not([hidden]) [data-node-file] input[data-review-file]')`;
  const row = `[...document.querySelectorAll('.review-file')].find((r) => r.querySelector('.review-name').title.endsWith('CHANGELOG.md')).getAttribute('data-state')`;
  assert.equal(await p.run(`${row}`), 'unseen');
  await p.run(`${box}.click(); true`);
  assert.equal(await p.run(`${row}`), 'seen');
  assert.equal(await p.run(`${box}.checked`), true);
});

test('visual-check fails when a path-tab diff stacks overlapping hunks of one file', opts, (t) => {
  const spec = readFixture('PR-10922');
  const code = spec.connections.find((c) => c.id === 'menu-ctrl').code;
  const first = code.diff.replace(/\n+$/, '').split(/\n(?=@@ )/)[0];
  code.diff = code.diff.replace(/\n+$/, '') + '\n' + first + '\n';
  const html = deliverSpec(scratchDir(t), spec);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.deepEqual(d?.evidence.missingReviewFeatures, [`one net diff instead of stacked diff hunks in ${code.path.replace(/:\d+$/, '')}`]);
});

test('visual-check fails a review page whose path tabs lost the side column toggle', opts, (t) => {
  const dir = scratchDir(t);
  const html = deliverSpec(dir, readFixture('PR-10922'));
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace('sideButton.hidden = false;', '');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.ok(d?.evidence.missingReviewFeatures.includes('path side column collapse toggle'), JSON.stringify(d?.evidence));
});

test('visual-check fails a review page whose path tabs do not go side by side with both sides collapsed', opts, (t) => {
  const dir = scratchDir(t);
  const html = deliverSpec(dir, readFixture('PR-10922'));
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace("pre.setAttribute('data-diff-mode', 'split');", '');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.deepEqual(d?.evidence.missingReviewFeatures, ['side-by-side diff with both sides collapsed']);
});

test('files of a node with no connection must show in its one-node path tab; visual-check fails when they do not', opts, (t) => {
  const dir = scratchDir(t);
  const html = deliverSpec(dir, readFixture('PR-10922'));
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace('if (!file.diff || shown[file.path]) return;', 'return;');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.deepEqual(d?.evidence.missingReviewFeatures, [
    'path-tab diff for packages/webview_flutter/webview_flutter_wkwebview/CHANGELOG.md',
    'path-tab diff for packages/webview_flutter/webview_flutter_wkwebview/pubspec.yaml',
  ]);
});

test('visual-check fails with a structured json for a missing html', opts, (t) => {
  const r = runCli('visual-check', path.join(scratchDir(t), 'nope.html'), '--json');
  assert.notEqual(r.status, 0);
  assert.equal(JSON.parse(r.stdout).ok, false);
});

test('visual-check fails a side-by-side diff whose long lines push the new side out of its box', opts, (t) => {
  const spec = readFixture('PR-10922');
  const edge = spec.connections.find((c) => c.code?.diff);
  edge.code.diff = edge.code.diff.replace(/\n /, `\n ${'x'.repeat(400)}`);
  const dir = scratchDir(t);
  const html = deliverSpec(dir, spec);
  assert.equal(JSON.parse(runCli('visual-check', html, '--json').stdout).status, 'pass');
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace('grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); min-width: 0;', 'grid-template-columns: repeat(2, minmax(max-content, 1fr));');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.ok(d?.evidence.missingReviewFeatures.some((m) => m.startsWith('side-by-side diff fits its box')), JSON.stringify(d?.evidence));
});

// Both themes must keep text, diff highlights and the reviewed tick readable.
for (const [what, from, to, expected] of [
  ['light text too faint', '      --text: #0f172a;\n', '      --text: #cbd5e1;\n', 'readable text contrast for'],
  ['reviewed tick too faint', 'fill-opacity: 0.85; stroke: var(--bg)', 'fill-opacity: 0.2; stroke: var(--bg)', 'visible reviewed tick on diagram nodes'],
  ['diff highlight too faint', '.cd-add { background: color-mix(in srgb, var(--backend-stroke) 16%, transparent); }', '.cd-add { background: color-mix(in srgb, var(--backend-stroke) 2%, transparent); }', 'visible highlight for .cd-add'],
  ['theme ignored', "document.documentElement.setAttribute('data-theme', theme);", '', 'light color theme'],
]) {
  test(`visual-check fails a review page with ${what}`, opts, (t) => {
    const html = deliverSpec(scratchDir(t), readFixture('PR-10922'));
    const page = fs.readFileSync(html, 'utf8');
    const broken = page.replace(from, to);
    assert.notEqual(broken, page);
    fs.writeFileSync(html, broken);
    const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
    assert.equal(receipt.status, 'fail');
    const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
    assert.ok(d?.evidence.missingReviewFeatures.some((m) => m.startsWith(expected)), JSON.stringify(d?.evidence));
  });
}

test('visual-check fails a review page that lost the Code Smell tab', opts, (t) => {
  const html = deliverSpec(scratchDir(t), readFixture('PR-10922'));
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace(", ['smell', 'Code Smell']]", ']');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.ok(d?.evidence.missingReviewFeatures.includes('Code Smell tab'), JSON.stringify(d?.evidence));
});

// TODO checkboxes, the copy toast and the card rail are review features too.
for (const [what, from, to, expected] of [
  ['TODO checkboxes', 'li.appendChild(check); ', '', 'TODO item checkboxes'],
  ['Ask AI / AI Fix buttons', 'if (fix) li.appendChild(fix); if (ask) li.appendChild(ask);', '', 'Ask AI or AI Fix buttons'],
  ['copy toast', "node.classList.add('show');", '', 'copy toast'],
  ['clickable hop checkboxes', '.path-col .path-hop-head { font-size: 0.8125rem; }', '.path-col .path-hop-head { font-size: 0.8125rem; position: relative; z-index: 5; padding-right: 3rem; background: var(--panel); }', 'reviewed checkbox takes a click'],
  ['space above the first hop card', '.path-col { --tint: var(--frontend-fill); display: flex; flex-direction: column; overflow: auto; padding-top: 0.75rem; }', '.path-col { --tint: var(--frontend-fill); display: flex; flex-direction: column; overflow: auto; }', 'space above the first hop card'],
  ['hop file names', "head.appendChild(hopFile(path, edge));", '', 'file name on every hop card'],
  ['MVVM on open', "if (buttons.length) show(layouts.mvvm ? 'mvvm' : buttons[0].getAttribute('data-layout'), true);", '', 'MVVM layout on open'],
  ['TODO tabs that hide other tabs\' items', '.todo-list li.todo-none:not([hidden]) {', '.todo-list li.todo-none {', 'TODO items of other tabs stay out of view'],
  ['Ask AI button style', '.todo-fix, .todo-ask {', '.todo-fix {', 'Ask AI and AI Fix buttons share one style'],
  ['cards rail button', 'sideButton.hidden = false;', '', 'cards rail button'],
  ['diagram zoom slider', '<input class="zoom-slider', '<input class="no-zoom', 'diagram zoom slider'],
  ['pinch zoom', 'if (!event.ctrlKey && zoom === 1) return; event.preventDefault();', 'if (!event.ctrlKey && zoom === 1) return;', 'pinch zoom on the diagram'],
  ['fitted size while zoomed', 'var rect = Archify.zoom ? Archify.zoom.fitted() : svg.getBoundingClientRect();', 'var rect = svg.getBoundingClientRect();', 'a zoomed diagram keeps its fitted size on relayout'],
  ['card collapse that keeps the room', "target.toggleAttribute('data-collapsed', collapsed);", '', 'collapsing a card gives the other card its room'],
  ['side rails that miss the diagram panel', '[".cards", ".path-panel:not([hidden]) .path-side"]', '[".path-panel:not([hidden]) .path-side"]', 'side rails line up with the diagram panel'],
  ['a title row that stacks', '.header { display: flex; flex-wrap: wrap;', '.header { display: block; flex-wrap: wrap;', 'title, view tabs and toolbar share one row'],
  ['toolbar labels', 'html[data-review] .toolbar :is(#theme-label, #refresh-label, #next-label) { display: none; }', '', 'toolbar shows icons only'],
  ['a TODO heading hidden in the rail', 'html[data-review] .cards .card-header:has(.todo-tabs) > :is(h3, .card-dot) { display: block; }', '', 'rail card headings line up'],
  ['path notes inside the path panel', 'html[data-review] .path-side { position: fixed;', 'html[data-review] .path-side { position: static;', 'path notes sit in the right rail'],
  ['a path title on two rows', '.path-head { grid-column: 1 / -1; display: flex;', '.path-head { grid-column: 1 / -1; display: grid;', 'path title stays one row'],
  ['cards button that keeps the rail', "html.toggleAttribute('data-review-cards-collapsed', panels.get('rail'));", '', 'the cards button hides the rail and widens the diagram'],
]) {
  test(`visual-check fails a review page without working ${what}`, opts, (t) => {
    const html = deliverSpec(scratchDir(t), readFixture('PR-10922'));
    const page = fs.readFileSync(html, 'utf8');
    const broken = page.replace(from, to);
    assert.notEqual(broken, page);
    fs.writeFileSync(html, broken);
    const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
    assert.equal(receipt.status, 'fail');
    const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
    assert.ok(d?.evidence.missingReviewFeatures.some((m) => m.startsWith(expected)), JSON.stringify(d?.evidence));
  });
}

test('visual-check fails a review page that shows "no findings" on a tab that has items', opts, (t) => {
  const spec = readFixture('PR-10922');
  spec.cards.find((c) => /TODO|待確認/.test(c.title)).items = ['[medium] An open question (a.dart:70)', '[low][security-review] 審查的 hunk 沒有發現問題', '[low][code-review] 審查的 hunk 沒有發現問題'];
  const html = deliverSpec(scratchDir(t), spec);
  assert.equal(JSON.parse(runCli('visual-check', html, '--json').stdout).status, 'pass');
  const page = fs.readFileSync(html, 'utf8');
  const broken = page.replace("if (others) none.classList.add('todo-none-moot');", '');
  assert.notEqual(broken, page);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.ok(d?.evidence.missingReviewFeatures.includes('no "no findings" line on a tab with items'), JSON.stringify(d?.evidence));
});

test('visual-check fails a review page whose path diagram badges leave their node corner', opts, (t) => {
  const html = deliverSpec(scratchDir(t), readFixture('PR-10922'));
  // Every node gets a risk badge, so each path diagram node carries one.
  const withRisk = fs.readFileSync(html, 'utf8').replace(/(<script id="archify-code-data" type="application\/json">)([\s\S]*?)(<\/script>)/, (_, open, json, close) => {
    const data = JSON.parse(json);
    Object.values(data.nodes).forEach((node) => { node.risk = { score: 0.81, callers: [], untested: [], tool: 'code-review-graph 2.4.0' }; });
    return open + serializeScriptJson(data) + close;
  });
  fs.writeFileSync(html, withRisk);
  assert.equal(JSON.parse(runCli('visual-check', html, '--json').stdout).status, 'pass');
  // The badge text is centred by class, so a path diagram clone (stripped of data- attributes) keeps it centred.
  const broken = withRisk.replace(':is([data-review-badge], .review-risk-badge, .review-new-badge) text { text-anchor: middle;', '[data-review-badge] text { text-anchor: middle;');
  assert.notEqual(broken, withRisk);
  fs.writeFileSync(html, broken);
  const receipt = JSON.parse(runCli('visual-check', html, '--json').stdout);
  assert.equal(receipt.status, 'fail');
  const d = receipt.diagnostics.find((x) => x.code === 'viewer/review-feature-missing');
  assert.ok(d?.evidence.missingReviewFeatures.includes('path diagram badges sit on their node corner'), JSON.stringify(d?.evidence));
});
