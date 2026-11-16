import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { readReport } from '../../renderers/shared/report.mjs';
import { readFixture, runCli, scratchDir } from './helpers/review-page.mjs';

const spec = readFixture('PR-10922');
const PLUGIN_VERSION = JSON.parse(fs.readFileSync(new URL('../../../../.claude-plugin/plugin.json', import.meta.url), 'utf8')).version;

function deliver(dir, next, ...extra) {
  const input = path.join(dir, 'spec.json');
  fs.writeFileSync(input, JSON.stringify(next));
  return runCli('deliver', 'architecture', input, path.join(dir, 'review.html'), '--json', ...extra);
}

test('deliver --report writes the shared viewer and reports/<ID>.js, and records the report in the receipt', (t) => {
  const dir = scratchDir(t);
  const run = deliver(dir, spec, '--report', 'PR-10922');
  assert.equal(run.status, 0, run.stdout + run.stderr);
  const receipt = JSON.parse(run.stdout);
  const reportPath = path.join(dir, 'reports/PR-10922.js');
  assert.deepEqual(receipt.report, { id: 'PR-10922', path: reportPath, bytes: fs.statSync(reportPath).size, viewer: { version: PLUGIN_VERSION, kept: false } });
  const report = readReport(reportPath);
  assert.equal(report.id, 'PR-10922');
  assert.deepEqual(report.spec, spec);
  assert.equal(report.page.title, spec.meta.title);
  const viewer = fs.readFileSync(path.join(dir, 'review.html'), 'utf8');
  assert.doesNotMatch(viewer, /PR-10922/);
  assert.ok(viewer.length + fs.statSync(reportPath).size < 1.2 * viewer.length + 200000);
  assert.deepEqual(fs.readdirSync(dir).sort(), ['reports', 'review.html', 'spec.json']);
});

test('a second ticket reuses the same viewer file', (t) => {
  const dir = scratchDir(t);
  assert.equal(deliver(dir, spec, '--report', 'PR-10922').status, 0);
  const before = fs.statSync(path.join(dir, 'review.html')).mtimeMs;
  assert.equal(deliver(dir, readFixture('PR-9151'), '--report', 'PR-9151').status, 0);
  assert.equal(fs.statSync(path.join(dir, 'review.html')).mtimeMs, before);
  assert.deepEqual(fs.readdirSync(path.join(dir, 'reports')).sort(), ['PR-10922.js', 'PR-9151.js', 'index.js']);
});

test('a failed deliver leaves the viewer and the report untouched', (t) => {
  const dir = scratchDir(t);
  assert.equal(deliver(dir, spec, '--report', 'PR-10922').status, 0);
  const reportPath = path.join(dir, 'reports/PR-10922.js');
  const before = [fs.readFileSync(path.join(dir, 'review.html')), fs.readFileSync(reportPath)];
  const broken = structuredClone(spec);
  broken.connections[0].from = 'no-such-node';
  assert.notEqual(deliver(dir, broken, '--report', 'PR-10922').status, 0);
  assert.ok(fs.readFileSync(path.join(dir, 'review.html')).equals(before[0]));
  assert.ok(fs.readFileSync(reportPath).equals(before[1]));
});

test('deliver --report rejects an id that is not a ticket key', (t) => {
  const dir = scratchDir(t);
  for (const bad of ['../x', 'PR-1/../y']) {
    const run = deliver(dir, spec, '--report', bad);
    assert.notEqual(run.status, 0);
    assert.match(run.stdout + run.stderr, /--report requires a ticket key/);
  }
  assert.equal(fs.existsSync(path.join(dir, 'reports')), false);
});

test('a feature report is delivered only under its own id', (t) => {
  const dir = scratchDir(t);
  const feature = structuredClone(spec);
  feature.changes.slice = { report: 'AC1-2', base: 'a'.repeat(40), head: 'b'.repeat(40), feature: 'Promo', batch: 1, next: null };
  for (const extra of [['--report', 'AC1-3'], []]) {
    const run = deliver(dir, feature, ...extra);
    assert.notEqual(run.status, 0);
    assert.match(run.stdout + run.stderr, /feature report AC1-2; deliver it with --report AC1-2/);
  }
  assert.equal(fs.existsSync(path.join(dir, 'reports')), false);
  assert.equal(deliver(dir, feature, '--report', 'AC1-2').status, 0);
  assert.equal(readReport(path.join(dir, 'reports/AC1-2.js')).spec.changes.slice.report, 'AC1-2');
});

test('a viewer built by a newer plugin is kept; an older one is replaced', (t) => {
  const dir = scratchDir(t);
  const viewer = path.join(dir, 'review.html');
  assert.equal(deliver(dir, spec, '--report', 'PR-10922').status, 0);
  const ours = fs.readFileSync(viewer, 'utf8');
  assert.match(ours, new RegExp(`<meta name="archify-viewer-version" content="${PLUGIN_VERSION.replace(/\./g, '\\.')}">`));
  const newer = ours.replace(/(name="archify-viewer-version" content=")[^"]*/, '$1999.0.0');
  fs.writeFileSync(viewer, newer);
  const kept = JSON.parse(deliver(dir, spec, '--report', 'PR-10922').stdout);
  assert.deepEqual(kept.report.viewer, { version: '999.0.0', kept: true });
  assert.equal(fs.readFileSync(viewer, 'utf8'), newer);
  fs.writeFileSync(viewer, ours.replace(/(name="archify-viewer-version" content=")[^"]*/, '$10.0.1'));
  assert.equal(JSON.parse(deliver(dir, spec, '--report', 'PR-10922').stdout).report.viewer.kept, false);
  assert.equal(fs.readFileSync(viewer, 'utf8'), ours);
});
