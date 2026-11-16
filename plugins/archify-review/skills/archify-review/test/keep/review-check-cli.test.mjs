import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { cli, fixtureDir, readFixture, root, runCli, scratchDir } from './helpers/review-page.mjs';
import { checkSpec, summarize } from '../../renderers/shared/review-check.mjs';

// review-check against real review artifacts (hunks.json is the git evidence) and through the CLI on a real git repo.
const TICKETS = ['PR-10922', 'PR-9151'];
const commitsOf = (ticket) => JSON.parse(fs.readFileSync(path.join(fixtureDir, ticket, 'hunks.json'), 'utf8'));

const withRisk = (spec) => {
  for (const n of spec.components) if (n.code?.files?.length) n.code.risk = { score: 0.3, callers: [], untested: [], tool: 'code-review-graph 2.4.0' };
  return spec;
};

for (const ticket of TICKETS) {
  test(`${ticket}: the delivered review spec passes every rule against its git hunks`, () => {
    const findings = checkSpec(withRisk(readFixture(ticket)), commitsOf(ticket));
    assert.deepEqual(summarize(findings).errors, 0, JSON.stringify(findings, null, 1));
    const withFiles = readFixture(ticket).components.filter((n) => n.code?.files?.length).map((n) => n.id);
    assert.ok(withFiles.length > 0);
    assert.deepEqual(checkSpec(readFixture(ticket), commitsOf(ticket)).filter((f) => f.severity === 'error').map((f) => `${f.rule}:${f.subject}`), withFiles.map((id) => `R23:${id}`));
  });

  test(`${ticket}: a trimmed hunk, a wrong function line and a missing platform are each reported`, () => {
    const spec = readFixture(ticket);
    const diff = spec.connections[0].code.diff;
    spec.connections[0].code.diff = diff.split('\n').filter((l, i) => i !== 2).join('\n');
    spec.connections[1].code.tests.functions[0].lines = '+99999';
    delete spec.meta.platform;
    const rules = new Set(checkSpec(spec, commitsOf(ticket)).map((f) => f.rule));
    for (const rule of ['R2', 'R5', 'R18']) assert.ok(rules.has(rule), `${rule} reported, got ${[...rules]}`);
  });
}

function repoWithCommit(t) {
  const repo = scratchDir(t, 'archify-keep-repo-');
  const git = (...a) => execFileSync('git', ['-C', repo, '-c', 'user.name=t', '-c', 'user.email=t@t', ...a], { encoding: 'utf8' });
  git('init', '-q');
  fs.writeFileSync(path.join(repo, 'A.kt'), 'a\nb\nc\nkeep\nold\ntail\nz\n');
  git('add', '.'); git('commit', '-qm', 'one');
  fs.writeFileSync(path.join(repo, 'A.kt'), 'a\nb\nc\nkeep\nnew\ntail\nz\n');
  git('commit', '-qam', 'two');
  const sha = git('rev-parse', 'HEAD').trim();
  const diff = git('show', sha, '--format=', '-U2').split('\n').slice(4).join('\n').replace(/\n+$/, '');
  return { repo, sha, diff };
}
const specFor = ({ sha, diff }) => ({
  meta: { platform: 'app' },
  cards: [{ title: '根因', items: ['why'] }],
  changes: { commits: [{ sha, subject: 'two' }], files: [{ path: 'A.kt', status: 'modified', additions: 1, deletions: 1 }] },
  components: [{ id: 'y', label: 'A.kt', code: { layers: { mvvm: 'view' } } }],
  connections: [{ id: 'c1', from: 'x', to: 'y', code: { from_symbol: 'a', to_symbol: 'b', path: 'A.kt', line: 5, action: 'Press Home', diff, tests: { functions: [{ symbol: 'a', lines: '+5,-5', status: 'none', by: [], why: 'w' }], suggested: [{ covers: 'a', given: 'g', when: 'w', then: 't' }] } } }],
});

test('cli review-check: a correct spec exits 0 and prints zero errors', (t) => {
  const r = repoWithCommit(t);
  const dir = scratchDir(t);
  const spec = path.join(dir, 'spec.json');
  fs.writeFileSync(spec, JSON.stringify(specFor(r)));
  const out = runCli('review-check', spec, '--repo', r.repo);
  assert.equal(out.status, 0, out.stdout + out.stderr);
  assert.match(out.stdout, /review-check: 0 errors, 0 warnings/);
  const json = JSON.parse(runCli('review-check', spec, '--repo', r.repo, '--json').stdout);
  assert.deepEqual(json, { summary: { errors: 0, warnings: 0, byRule: {} }, findings: [] });
});

test('cli review-check: a wrong hunk exits 1 with the rule, subject and fix in json', (t) => {
  const r = repoWithCommit(t);
  const dir = scratchDir(t);
  const bad = specFor(r);
  bad.connections[0].code.diff = bad.connections[0].code.diff.replace('+new', '+newer');
  const spec = path.join(dir, 'spec.json');
  fs.writeFileSync(spec, JSON.stringify(bad));
  const out = runCli('review-check', spec, '--repo', r.repo, '--json');
  assert.equal(out.status, 1);
  const { summary, findings } = JSON.parse(out.stdout);
  assert.ok(summary.errors >= 1 && summary.byRule.R2 >= 1);
  assert.ok(findings.some((f) => f.rule === 'R2' && f.subject === 'c1' && f.fix));
  assert.match(runCli('review-check', spec, '--repo', r.repo).stdout, /error R2 c1: /);
});

test('cli review-check: usage errors exit non-zero', (t) => {
  const dir = scratchDir(t);
  const spec = path.join(dir, 'spec.json');
  fs.writeFileSync(spec, JSON.stringify({ changes: { commits: [] } }));
  assert.notEqual(runCli('review-check', spec).status, 0, 'missing --repo');
  assert.notEqual(runCli('review-check', spec, '--repo', dir).status, 0, 'no commits in the spec');
});

function twoFeatureRange(t) {
  const repo = scratchDir(t, 'archify-keep-range-');
  const home = scratchDir(t, 'archify-keep-home-');
  const git = (...a) => execFileSync('git', ['-C', repo, '-c', 'user.name=t', '-c', 'user.email=t@t', ...a], { encoding: 'utf8' });
  const lines = Array.from({ length: 30 }, (_, i) => `line${i + 1}`);
  const write = (file, text) => fs.writeFileSync(path.join(repo, file), text);
  git('init', '-q');
  write('a.txt', lines.join('\n') + '\n');
  git('add', '.'); git('commit', '-qm', 'base');
  const base = git('rev-parse', 'HEAD').trim();
  lines[1] = 'todo';
  const withTmp = [...lines.slice(0, 15), 'tmp', ...lines.slice(15)];
  write('a.txt', withTmp.join('\n') + '\n'); write('b.txt', 'b\n');
  git('add', '.'); git('commit', '-qm', 'todo');
  const todo = git('rev-parse', 'HEAD').trim();
  lines[27] = 'chat';
  write('a.txt', lines.join('\n') + '\n'); write('c.txt', 'c\n');
  git('add', '.'); git('commit', '-qm', 'chat');
  const head = git('rev-parse', 'HEAD').trim();
  const kit = (...a) => {
    const r = spawnSync(path.join(root, 'bin/review-kit'), a, { encoding: 'utf8', env: { ...process.env, ARCHIFY_REVIEW_HOME: home } });
    assert.equal(r.status, 0, r.stdout + r.stderr);
    return r.stdout;
  };
  const features = path.join(home, 'features.json');
  fs.writeFileSync(features, JSON.stringify({ features: [{ name: 'Todo', change: 'Todo label', commits: [todo] }, { name: 'Chat', change: 'Chat send', commits: [head] }] }));
  kit('slice', 'plan', '--repo', repo, '--range', `${base}..${head}`, '--features', features);
  const report = /^(\S+) {2}feature 1\/2 Todo/.exec(kit('slice', 'next', '--repo', repo, '--range', `${base}..${head}`))[1];
  const hunksPath = path.join(home, 'hunks.json');
  kit('hunks', '--repo', repo, '--report', report, '--out', hunksPath);
  return { repo, home, report, hunks: JSON.parse(fs.readFileSync(hunksPath, 'utf8')) };
}

const batchSpec = ({ hunks }) => ({
  meta: { platform: 'app' },
  cards: [{ title: '這張 Story 的目的', items: ['why'] }],
  changes: hunks.changes,
  components: hunks.changes.files.map((f) => ({
    id: f.path.replace(/\W/g, '_'), label: path.basename(f.path),
    code: { layers: { mvvm: 'view' }, files: [{ path: f.path, summary: 's', diff: hunks.hunks.filter((h) => h.file === f.path).map((h) => h.text).join('\n') }], risk: { score: 0.2, callers: [], untested: [], tool: 'code-review-graph 2.4.0' } },
  })),
  connections: [],
});

test('cli review-check: a feature report is checked against its own batch of the final diff', (t) => {
  const range = twoFeatureRange(t);
  assert.deepEqual(range.hunks.changes.files.map((f) => f.path), ['a.txt', 'b.txt']);
  assert.equal(range.hunks.hunks.filter((h) => h.file === 'a.txt').length, 1, 'Chat\'s a.txt hunk is not in the Todo batch');
  assert.ok(!range.hunks.hunks.some((h) => h.text.includes('tmp')), 'a line Chat removed again is not shown');
  const check = (spec) => {
    const file = path.join(range.home, 'spec.json');
    fs.writeFileSync(file, JSON.stringify(spec));
    const r = spawnSync(process.execPath, [cli, 'review-check', file, '--repo', range.repo, '--json'], { encoding: 'utf8', env: { ...process.env, ARCHIFY_REVIEW_HOME: range.home } });
    return JSON.parse(r.stdout);
  };
  assert.deepEqual(check(batchSpec(range)), { summary: { errors: 0, warnings: 0, byRule: {} }, findings: [] });
  const dropped = batchSpec(range);
  dropped.components[1].code.files[0].diff = '';
  assert.deepEqual(check(dropped).findings.map((f) => `${f.rule}:${f.subject}`), ['R4:b.txt']);
  const widened = batchSpec(range);
  widened.changes = { ...widened.changes, files: [...widened.changes.files, { path: 'c.txt', status: 'added', additions: 1, deletions: 0 }] };
  assert.ok(check(widened).findings.some((f) => f.rule === 'R7' && /c\.txt is in changes\.files but git has no change to it/.test(f.detail)), 'another feature\'s file is not part of this report');
});
