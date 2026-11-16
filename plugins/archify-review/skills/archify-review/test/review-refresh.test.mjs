import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-refresh-'));
const example = JSON.parse(fs.readFileSync(path.join(root, 'examples/web-app.architecture.json'), 'utf8'));
const store = template.slice(template.indexOf('/* review-store pure */'), template.indexOf('/* end review-store pure */'));
const { archifyRefreshCommand, archifyNextCommand, archifyRiskLevel, archifyStateStore } = new Function(`${store}; return { archifyRefreshCommand, archifyNextCommand, archifyRiskLevel, archifyStateStore };`)();

function render(spec, name) {
  const input = path.join(tmp, `${name}.json`);
  const output = path.join(tmp, `${name}.html`);
  fs.writeFileSync(input, JSON.stringify(spec));
  const run = spawnSync(process.execPath, [path.join(root, 'renderers/architecture/render-architecture.mjs'), input, output], { encoding: 'utf8' });
  return { run, html: run.status === 0 ? fs.readFileSync(output, 'utf8') : '' };
}
const withChanges = (changes, locale) => {
  const spec = structuredClone(example);
  if (locale) spec.meta = { ...spec.meta, locale };
  spec.changes = { files: [{ path: 'src/a.ts', status: 'added' }], ...changes };
  return spec;
};

test('refresh command carries ticket, repo and the Done / Skip item numbers', () => {
  assert.equal(archifyRefreshCommand('PROJ-1', '/r/app', [1, 3], [2]), '/archify-review refresh PROJ-1 --repo /r/app --done 1,3 --skip 2');
  assert.equal(archifyRefreshCommand('PROJ-1', '/r/my app', [], []), '/archify-review refresh PROJ-1 --repo "/r/my app"');
  assert.equal(archifyRefreshCommand('PROJ-1', '', [], [4]), '/archify-review refresh PROJ-1 --skip 4');
});

test('review page carries a localized Refresh button and no Present button', () => {
  const { run, html } = render(withChanges({ commits: [{ sha: 'abc1234' }] }, 'zh-TW'), 'toolbar');
  assert.equal(run.status, 0, run.stderr);
  assert.doesNotMatch(html, /id="btn-present"/);
  assert.match(html, /<button id="btn-refresh" type="button" hidden/);
  assert.match(html, /<span id="refresh-label">刷新<\/span>/);
  assert.match(template, /Archify\.copyThen\(archifyRefreshCommand\(/);
  assert.match(template, /refreshBtn\.hidden = false/);
});

test('Next copies the command that builds the report after this one', () => {
  assert.equal(archifyNextCommand('AC1-2'), '/archify-review next AC1-2');
});

test('risk level uses code-review-graph cut-offs: high above 0.70, medium above 0.40', () => {
  assert.deepEqual([0.81, 0.71, 0.7, 0.41, 0.4, 0.12, 0].map(archifyRiskLevel), ['high', 'high', 'medium', 'medium', 'low', 'low', 'low']);
});

test('review page carries a hidden, localized Next button right after Refresh', () => {
  const { run, html } = render(withChanges({ commits: [{ sha: 'abc1234' }] }, 'zh-TW'), 'next');
  assert.equal(run.status, 0, run.stderr);
  assert.match(html, /id="btn-refresh"[\s\S]*?<\/button>\s*<button id="btn-next" type="button" hidden/);
  assert.match(html, /<span id="next-label">下一批<\/span>/);
});

test('new commits get a localized badge on the node top-right corner', () => {
  assert.match(template, /review-new-badge/);
  assert.match(template, /viewerText\('viewer\.review\.newCommit'\)/);
  const { html } = render(withChanges({ commits: [{ sha: 'abc1234', new: true }] }), 'badge');
  assert.match(html, /"new":true/);
});

test('TODO items marked [new] get a NEW tag next to the severity tag', () => {
  assert.match(template, /\.todo-new/);
  assert.match(template, /\\\[new\\\]/);
  assert.match(template, /sev\.after\(tag\)/);
});

test('review state is stored only under archify-review-state:<ID>, and only on a report page; the only other key is the panel layout', () => {
  const review = template.slice(template.indexOf('Archify.reviewTree = (function'), template.indexOf('Semantic Lens \u2014'));
  const todo = template.slice(template.indexOf('/* end todo-fix pure */'), template.indexOf('var Archify = {};'));
  // The one exception is a viewer preference, not review state: which panels are collapsed, shared by every report.
  const panels = template.slice(template.indexOf('Archify.panels = (function'), template.indexOf('Archify.pathTabs = (function'));
  assert.match(panels, /var KEY = 'archify-review-rail';/);
  assert.equal((panels.match(/localStorage\.setItem/g) || []).length, 1);
  for (const part of [review, todo]) { assert.ok(part.length > 1000); assert.doesNotMatch(part, /localStorage\.setItem|sessionStorage/); }
  assert.match(todo, /if \(window\.archifyReportPayload\) archifyReviewState = archifyStateStore\(/);
});

test('the state store keeps keys it does not touch and survives a corrupt entry', () => {
  const data = new Map([['archify-review-state:PROJ-1', JSON.stringify({ todo: { gone: 'skip' }, ticks: { 'a|+1|x': true } })], ['archify-review-state:PROJ-2', '{']]);
  const storage = { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => data.set(k, v) };
  const one = archifyStateStore('PROJ-1', storage);
  one.todo.kept = 'do';
  one.save();
  const saved = JSON.parse(data.get('archify-review-state:PROJ-1'));
  assert.deepEqual(saved.todo, { gone: 'skip', kept: 'do' });
  assert.deepEqual(saved.ticks, { 'a|+1|x': true });
  assert.ok(saved.updatedAt);
  const two = archifyStateStore('PROJ-2', storage);
  assert.deepEqual([two.todo, two.ticks], [{}, {}]);
});

test('schema accepts commits[].new and changes.repo, rejects a non-boolean new', () => {
  assert.equal(render(withChanges({ repo: '/r/app', commits: [{ sha: 'abc1234', new: true }] }), 'ok').run.status, 0);
  assert.notEqual(render(withChanges({ commits: [{ sha: 'abc1234', new: 'yes' }] }), 'bad').run.status, 0);
});
