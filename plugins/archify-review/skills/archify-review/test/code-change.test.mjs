import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { reviewPure } from './helpers/review-pure.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-code-change-'));
const example = JSON.parse(fs.readFileSync(path.join(root, 'examples/web-app.architecture.json'), 'utf8'));

function render(spec, name) {
  const input = path.join(tmp, `${name}.json`);
  const output = path.join(tmp, `${name}.html`);
  fs.writeFileSync(input, JSON.stringify(spec));
  const run = spawnSync(process.execPath, [path.join(root, 'renderers/architecture/render-architecture.mjs'), input, output], { encoding: 'utf8' });
  return { run, html: run.status === 0 ? fs.readFileSync(output, 'utf8') : '' };
}

test('node and relationship code data render into the viewer payload', () => {
  const spec = structuredClone(example);
  spec.components[0].code = { commit: 'abc1234', files: [{ path: 'src/a.ts', lines: '1-9', symbols: ['a()'], summary: 'changed', diff: '-x\n+y' }] };
  spec.connections[0].code = { from_symbol: 'a()', to_symbol: 'b()', path: 'src/a.ts', line: 3, note: 'passes id' };
  const { run, html } = render(spec, 'with-code');
  assert.equal(run.status, 0, run.stderr);
  assert.match(html, /<script id="archify-code-data" type="application\/json">/);
  assert.match(html, /"from_symbol":"a\(\)"/);
  assert.match(html, /id="focus-code"/);
});

test('specs without code data carry no payload', () => {
  const { run, html } = render(example, 'no-code');
  assert.equal(run.status, 0, run.stderr);
  assert.doesNotMatch(html, /archify-code-data" type/);
});

test('schema rejects malformed code data', () => {
  const noFiles = structuredClone(example);
  noFiles.components[0].code = { files: [] };
  assert.notEqual(render(noFiles, 'bad-files').run.status, 0);
  const badLines = structuredClone(example);
  badLines.components[0].code = { files: [{ path: 'a', summary: 's', lines: 'x' }] };
  assert.notEqual(render(badLines, 'bad-lines').run.status, 0);
  const badRel = structuredClone(example);
  badRel.connections[0].code = { from_symbol: 'a' };
  assert.notEqual(render(badRel, 'bad-rel').run.status, 0);
});

test('inline node links render as buttons, keep other text inert, and drop unknown ids', () => {
  const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
  const start = template.indexOf('function richText(parent, text)');
  const end = template.indexOf('function fileCard(file, options)');
  assert.ok(start > 0 && end > start, 'richText source not found');
  const make = (tag, cls, text) => ({ tag, cls, text, children: [], attrs: {}, appendChild(c) { this.children.push(c); }, setAttribute(k, v) { this.attrs[k] = v; }, addEventListener() {} });
  const document = {
    createTextNode: (text) => ({ text }),
    querySelector: (sel) => (/data-node-id="(bridge)"/.test(sel) ? {} : null),
  };
  const richText = new Function('document', 'el', 'Archify', `${template.slice(start, end)}; return richText;`)(document, make, {});
  const parent = make('p');
  richText(parent, 'a <b>x</b> [ok](#node:bridge) [gone](#node:nope) [js](javascript:alert(1))');
  const buttons = parent.children.filter((c) => c.tag === 'button');
  assert.equal(buttons.length, 1);
  assert.equal(buttons[0].text, 'ok');
  assert.equal(buttons[0].attrs['data-node-link'], 'bridge');
  const text = parent.children.filter((c) => c.tag === undefined).map((c) => c.text).join('');
  assert.equal(text, 'a <b>x</b>  gone [js](javascript:alert(1))');
});

test('relationship action, diff and variants render and the path tab shows per-hop code only', () => {
  const spec = structuredClone(example);
  spec.connections[0].code = {
    from_symbol: 'a()', to_symbol: 'b()', action: 'User taps the button',
    diff: '-x\n+y', variants: [{ via: [spec.components[0].id], action: 'Variant action' }]
  };
  const { run, html } = render(spec, 'hop-code');
  assert.equal(run.status, 0, run.stderr);
  assert.match(html, /"action":"User taps the button"/);
  assert.match(html, /"variants":\[/);
  assert.match(html, /function hopCode\(path, edge\)/);
  assert.doesNotMatch(html, /renderInto\(block, code, \{ collapseDiff: true \}\)/);
  const bad = structuredClone(example);
  bad.connections[0].code = { from_symbol: 'a', to_symbol: 'b', variants: [{ action: 'no via' }] };
  assert.notEqual(render(bad, 'bad-variant').run.status, 0);
});

test('relationship excerpts and variant excerpts validate and malformed ones are rejected', () => {
  const excerpt = { path: 'src/a.ts', start: 10, lines: ['a()', { skip: 3 }, 'b()'], marks: [{ line: 10, step: 1 }], added: [10], removed: [{ after: 10, text: 'old()' }] };
  const spec = structuredClone(example);
  spec.connections[0].code = { from_symbol: 'a()', to_symbol: 'b()', excerpts: [excerpt], variants: [{ via: ['x'], excerpts: [excerpt] }] };
  const ok = render(spec, 'excerpts');
  assert.equal(ok.run.status, 0, ok.run.stderr);
  assert.match(ok.html, /"excerpts":\[/);
  const bad = structuredClone(spec);
  bad.connections[0].code.excerpts = [{ path: 'src/a.ts', start: 0, lines: [] }];
  assert.notEqual(render(bad, 'excerpts-bad').run.status, 0);
});

function reviewStatesFn() {
  return reviewPure().reviewStates;
}

test('review state is seen, partial or unseen per file from the lines of citing flows, and uncited files carry their own tick', () => {
  const reviewStates = reviewStatesFn();
  const files = [{ path: 'a.kt' }, { path: 'b.kt' }, { path: 'c.kt' }, { path: 'd.kt' }];
  const flows = [
    { id: 'f1', code: { path: 'a.kt' } },
    { id: 'f2', code: { path: 'b.kt', diff: '@@ -1,2 +1,3 @@\n x\n-old\n+new\n+more' } },
    { id: 'f3', code: { path: 'b.kt', diff: '@@ -1,2 +1,2 @@\n x\n-old\n+new' } },
    { id: 'f4', code: { path: 'z.kt', excerpts: [{ path: 'b.kt' }] } },
  ];
  const at = (done) => reviewStates(files, flows, done);
  assert.deepEqual(Object.values(at({})).map((s) => s.state), ['unseen', 'unseen', 'unseen', 'unseen']);
  assert.equal(at({ f1: true })['a.kt'].state, 'seen');
  assert.equal(at({ f1: true })['b.kt'].state, 'unseen');
  const lines = { 'b.kt|-2|old': true, 'b.kt|+2|new': true, 'b.kt|+3|more': true };
  assert.equal(at(lines)['b.kt'].state, 'partial');
  assert.equal(at({ ...lines, f4: true })['b.kt'].state, 'seen');
  assert.deepEqual(at({})['b.kt'].flows, ['f2', 'f3', 'f4']);
  assert.equal(at({})['c.kt'].own, true);
  assert.equal(at({ 'file:c.kt': true })['c.kt'].state, 'seen');
});

test('identical hunks in two flows share ticks; extra lines leave the flow partial', () => {
  const { flowLines, flowState } = reviewPure();
  const hunk = '@@ -44,3 +45,4 @@\n ctx\n-old\n+new\n+more\n';
  const a = { id: 'a', code: { path: 'g.dart', diff: hunk } };
  const b = { id: 'b', code: { path: 'g.dart', diff: '@@ -3,1 +3,2 @@\n ctx\n+import\n' + hunk.split('\n').slice(1).join('\n').replace(/^/, '@@ -44,3 +45,4 @@\n') } };
  assert.deepEqual(flowLines(a), ['g.dart|-45|old', 'g.dart|+46|new', 'g.dart|+47|more']);
  const done = Object.fromEntries(flowLines(a).map((k) => [k, true]));
  assert.equal(flowState(a, done), 'seen');
  assert.equal(flowState(b, done), 'partial');
  assert.equal(flowState({ id: 'n', code: {} }, { n: true }), 'seen');
  assert.equal(flowState({ id: 'n', code: {} }, {}), 'unseen');
});

test('changes data renders the tree payload and schema rejects bad status', () => {
  const spec = structuredClone(example);
  spec.changes = { commits: [{ sha: 'abc1234', subject: 's' }], files: [{ path: 'src/a.ts', status: 'added', additions: 3, deletions: 0 }] };
  const { run, html } = render(spec, 'with-changes');
  assert.equal(run.status, 0, run.stderr);
  assert.match(html, /"changes":\{"commits":\[\{"sha":"abc1234"/);
  spec.changes.files[0].status = 'renamed';
  assert.notEqual(render(spec, 'bad-changes').run.status, 0);
});

test('a feature report carries changes.slice and node code.risk to the viewer; the schema rejects malformed ones', () => {
  const base = 'f400bdb307c789ee9a196f0acbb213bdf8d39acd';
  const head = '40f69c86edc67fab8dd2ec26c7dbd5284d328d5c';
  const make = (slice, risk) => {
    const spec = structuredClone(example);
    spec.changes = { commits: Array.from({ length: 28 }, (_, i) => ({ sha: `abc${1000 + i}` })), files: [{ path: 'src/a.ts', status: 'modified', additions: 3, deletions: 1 }], slice };
    spec.components[0].code = { files: [{ path: 'src/a.ts', summary: 'changed' }], risk };
    return spec;
  };
  const slice = { report: 'AC1-2', base, head, feature: '優惠券兌換', batch: 2, next: { kind: 'batch', files: 23 } };
  const risk = { score: 0.81, symbol: 'CartStore.redeem', factors: [{ key: 'flows', score: 0.25, count: 6 }, { key: 'tests', score: 0.3, count: 0 }], callers: [{ file: 'lib/home_page.dart', line: 88, symbol: 'build' }], untested: ['redeem'], tool: 'code-review-graph 2.4.0' };
  const { run, html } = render(make(slice, risk), 'slice-risk');
  assert.equal(run.status, 0, run.stderr);
  assert.ok(html.includes(JSON.stringify(slice)), 'changes.slice reaches the payload verbatim');
  assert.ok(html.includes(JSON.stringify(risk)), 'code.risk reaches the payload verbatim');
  for (const next of [null, { kind: 'feature', name: '訂單通知', files: 12 }]) {
    assert.equal(render(make({ ...slice, next }, risk), 'slice-next').run.status, 0, JSON.stringify(next));
  }
  const empty = { score: 0, callers: [], untested: [], tool: 'code-review-graph 2.4.0' };
  assert.equal(render(make(slice, empty), 'risk-empty').run.status, 0);
  const bad = [
    make({ ...slice, next: { kind: 'batch', files: 0 } }, risk),
    make({ ...slice, next: { kind: 'feature', files: 3 } }, risk),
    make({ ...slice, next: { kind: 'batch', files: 3, name: 'x' } }, risk),
    make({ ...slice, report: 'ac-1' }, risk),
    make({ ...slice, extra: 1 }, risk),
    make(slice, { ...risk, level: 'high' }),
    make(slice, { ...risk, tool: 'crg' }),
    make(slice, { ...risk, factors: [{ key: 'hubs', score: 0.1, count: 1 }] }),
    make(slice, { ...risk, factors: [] }),
    make(slice, { score: 0.5, tool: 'code-review-graph 2.4.0' }),
  ];
  bad.forEach((spec, i) => assert.notEqual(render(spec, `bad-slice-${i}`).run.status, 0, `bad case ${i} must fail`));
});

test('specs without changes render identically to before', () => {
  const { html } = render(example, 'no-changes');
  assert.doesNotMatch(html, /"changes":/);
  assert.doesNotMatch(html, /archify-code-data" type/);
});

test('brief renders Role, Task and Concepts plus the diffs of its files, and old nodes keep Code change', () => {
  const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
  const start = template.indexOf('function renderBrief(');
  const end = template.indexOf('return {\n        el: el,');
  assert.ok(start > 0 && end > start, 'renderBrief source not found');
  const make = (tag, cls, text) => ({ tag, cls, text, children: [], textContent: '', appendChild(c) { this.children.push(c); }, setAttribute() {}, addEventListener() {} });
  const document = { createTextNode: (text) => ({ text }), querySelector: () => null };
  const calls = [];
  const renderInto = new Function('document', 'el', 'viewerText', 'richText', 'fileCard', `${template.slice(start, end)}; return renderInto;`)(
    document, make, (key) => key, (parent, text) => { parent.appendChild({ text }); return parent; }, (file) => { calls.push(file.path); return make('article'); });
  const files = [{ path: 'src/a.ts', summary: 's', diff: '-x\n+y' }, { path: 'src/b.ts', summary: 'no diff' }];

  const withBrief = make('div');
  renderInto(withBrief, { commit: 'abc1234def', files, brief: { role: 'R', task: 'T', concepts: [{ term: 'MeasureSpec', text: 'sizes' }] } });
  const headings = [];
  (function walk(n) { if (n.tag === 'h4') headings.push(n.text); (n.children || []).forEach(walk); })(withBrief);
  assert.deepEqual(headings, ['viewer.brief.role', 'viewer.brief.task', 'viewer.brief.concepts']);
  assert.deepEqual(calls, ['src/a.ts'], 'a brief still shows files that carry a diff');
  assert.deepEqual([withBrief.children[0].cls, withBrief.children[0].text], ['code-commit', 'viewer.code.commit abc1234def'], 'the commit heads the brief');

  calls.length = 0;
  renderInto(make('div'), { files });
  assert.deepEqual(calls, ['src/a.ts', 'src/b.ts']);
});

test('schema accepts a brief and rejects more than six concepts', () => {
  const ok = structuredClone(example);
  ok.components[0].code = { files: [{ path: 'a', summary: 's' }], brief: { role: 'r', task: 't', concepts: [{ term: 'x', text: 'y' }] } };
  assert.equal(render(ok, 'brief-ok').run.status, 0);
  const bad = structuredClone(ok);
  bad.components[0].code.brief.concepts = Array.from({ length: 7 }, () => ({ term: 'x', text: 'y' }));
  assert.notEqual(render(bad, 'brief-bad').run.status, 0);
});

test('node code accepts brief without files and rejects neither', () => {
  const briefOnly = structuredClone(example);
  briefOnly.components[0].code = { brief: { role: 'r', task: 't' } };
  const ok = render(briefOnly, 'brief-only');
  assert.equal(ok.run.status, 0, ok.run.stderr);
  assert.match(ok.html, /"brief":\{"role":"r","task":"t"\}/);
  const neither = structuredClone(example);
  neither.components[0].code = { commit: 'abc1234' };
  assert.notEqual(render(neither, 'neither').run.status, 0);
});
