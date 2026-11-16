import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

// Golden test against two public commits of flutter/packages (BSD-3-Clause).
// Set ARCHIFY_GOLDEN_REPO to a flutter/packages checkout to run it; skipped otherwise.
const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const kit = path.join(root, 'bin/review-kit');
const REPO = process.env.ARCHIFY_GOLDEN_REPO || '';
const SHAS = ['44536a0bd', 'f3ce6cbfa'];
const SPECS = [path.join(root, 'test/fixtures/review-kit-spec.json')];
const ready = !!REPO && [kit, REPO, ...SPECS].every((p) => fs.existsSync(p));
const run = (cmd, args) => spawnSync(cmd, args, { encoding: 'utf8', maxBuffer: 1 << 28 });
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'review-kit-'));
const hunksPath = path.join(tmp, 'hunks.json');

test('hunks: 6 hunks, files and numstat match git', { skip: !ready }, () => {
  const r = run(kit, ['hunks', '--repo', REPO, ...SHAS, '--out', hunksPath]);
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /^6 hunks, /);
  const { changes, hunks } = JSON.parse(fs.readFileSync(hunksPath, 'utf8'));
  assert.equal(hunks.length, 6);
  const want = new Map();
  for (const sha of SHAS) {
    for (const l of run('git', ['-C', REPO, 'show', sha, '--format=', '--no-renames', '--numstat']).stdout.split('\n').filter(Boolean)) {
      const [a, d, p] = l.split('\t');
      const e = want.get(p) || { additions: 0, deletions: 0, binary: false };
      if (a === '-') e.binary = true; else { e.additions += +a; e.deletions += +d; }
      want.set(p, e);
    }
  }
  assert.deepEqual(changes.files.map((f) => f.path), [...want.keys()]);
  for (const f of changes.files) {
    const e = want.get(f.path);
    if (e.binary) assert.equal(f.additions, undefined); else assert.deepEqual([f.additions, f.deletions], [e.additions, e.deletions], f.path);
  }
});

const diffs = (spec) => [
  ...spec.connections.map((c) => ({ id: c.id, path: c.code.path, diff: c.code.diff, functions: c.code.tests?.functions || [] })),
  ...spec.components.flatMap((n) => (n.code?.files || []).filter((f) => f.diff).map((f) => ({ id: n.id, path: f.path, diff: f.diff, functions: [] }))),
].filter((d) => d.diff);

for (const specPath of SPECS) {
  test(`diff and lines reproduce ${specPath}`, { skip: !ready }, () => {
    if (!fs.existsSync(hunksPath)) run(kit, ['hunks', '--repo', REPO, ...SHAS, '--out', hunksPath]);
    const { hunks } = JSON.parse(fs.readFileSync(hunksPath, 'utf8'));
    const spec = JSON.parse(fs.readFileSync(specPath, 'utf8'));
    for (const d of diffs(spec)) {
      const chunks = d.diff.replace(/\n+$/, '').split(/\n(?=@@ )/);
      const fileHunks = hunks.filter((h) => h.file === d.path);
      const idx = chunks.map((t) => fileHunks.findIndex((h) => h.text === t) + 1);
      idx.forEach((n, i) => assert.ok(n > 0, `${d.id}: chunk ${i + 1} matches no hunk of ${d.path}`));
      // one `diff` call with every selected hunk must byte-match the spec diff
      const out = run(kit, ['diff', '--hunks', hunksPath, '--file', d.path, '--hunk', ...idx.map(String)]);
      assert.equal(out.stdout, d.diff.replace(/\n+$/, '') + '\n', `${d.id}: diff output differs`);
      for (const f of d.functions) {
        const sel = [].concat(f.hunk ?? []).flatMap((h) => ['--hunk', String(idx[h - 1])]);
        const r = run(kit, ['lines', '--hunks', hunksPath, '--file', d.path, ...sel, f.lines]);
        assert.equal(r.status, 0, `${d.id} ${f.symbol}: ${r.stderr}`);
        assert.equal(r.stdout.trim(), f.lines, `${d.id} ${f.symbol}`);
      }
    }
  });
}

test('caller returns commit-version lines', { skip: !ready }, () => {
  const at = (sha, file, symbol) => run(kit, ['caller', '--repo', REPO, '--sha', sha, '--file', file, '--symbol', symbol]).stdout;
  const A = 'packages/webview_flutter/webview_flutter_android/example/android/app/src/';
  assert.match(at(SHAS[0], `${A}main/AndroidManifest.xml`, 'usesCleartextTraffic'), /AndroidManifest\.xml:8:/);
  assert.doesNotMatch(at(SHAS[1], `${A}main/AndroidManifest.xml`, 'usesCleartextTraffic'), /AndroidManifest\.xml:/);
  assert.match(at(SHAS[1], `${A}main/AndroidManifest.xml`, 'networkSecurityConfig'), /AndroidManifest\.xml:7:/);
  assert.match(at(SHAS[0], `${A}debug/AndroidManifest.xml`, 'WebViewTestActivity'), /AndroidManifest\.xml:9:/);
});

test('lines fails on a number outside the hunk', { skip: !ready }, () => {
  const r = run(kit, ['lines', '--hunks', hunksPath, '--file', `packages/webview_flutter/webview_flutter_android/example/android/app/src/main/AndroidManifest.xml`, '+99999']);
  assert.equal(r.status, 1);
});

test('assemble rebuilds the spec from its plan', { skip: !ready }, () => {
  if (!fs.existsSync(hunksPath)) run(kit, ['hunks', '--repo', REPO, ...SHAS, '--out', hunksPath]);
  const outPath = path.join(tmp, 'assembled.json');
  const r = run(kit, ['assemble', '--hunks', hunksPath, '--plan', path.join(root, 'test/fixtures/review-kit-plan.json'), '--out', outPath]);
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(JSON.parse(fs.readFileSync(outPath, 'utf8')), JSON.parse(fs.readFileSync(SPECS[0], 'utf8')));
});
