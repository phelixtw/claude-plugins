import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { cli, fixtureDir, readFixture, runCli, scratchDir } from './helpers/review-page.mjs';

// validate / deliver on the specs of real code reviews: changes, code.tests, layers, cards, meta.platform / framework.
const TICKETS = fs.readdirSync(fixtureDir).filter((d) => d.startsWith('PR-')).sort();
const write = (dir, name, spec) => { const file = path.join(dir, name); fs.writeFileSync(file, JSON.stringify(spec)); return file; };
const payloadKeys = (html) => {
  const m = /<script id="archify-layered-svg" type="application\/json">([\s\S]*?)<\/script>/.exec(html);
  return m ? Object.keys(JSON.parse(m[1])) : [];
};

test('three real reviews are available as fixtures', () => {
  assert.deepEqual(TICKETS, ['PR-10922', 'PR-9151', 'PR-9248']);
});

for (const ticket of TICKETS) {
  test(`${ticket}: validate accepts the review spec`, () => {
    const r = runCli('validate', 'architecture', path.join(fixtureDir, ticket, 'arch-review.json'), '--json');
    assert.equal(r.status, 0, r.stdout + r.stderr);
    assert.equal(JSON.parse(r.stdout).ok, true);
  });

  test(`${ticket}: deliver writes the page with change tree, hop code, cards and a matching receipt`, (t) => {
    const dir = scratchDir(t);
    const spec = readFixture(ticket);
    const output = path.join(dir, 'review.html');
    const r = runCli('deliver', 'architecture', path.join(fixtureDir, ticket, 'arch-review.json'), output, '--json');
    assert.equal(r.status, 0, r.stdout + r.stderr);
    const receipt = JSON.parse(r.stdout);
    assert.equal(receipt.ok, true);
    assert.equal(receipt.artifact.bytes, fs.statSync(output).size);
    assert.equal(receipt.validation.errors, 0);
    const html = fs.readFileSync(output, 'utf8');
    assert.match(html, /data-review/);
    for (const file of spec.changes.files) assert.ok(html.includes(file.path), `change tree carries ${file.path}`);
    for (const card of spec.cards) assert.ok(html.includes(card.title), `card ${card.title}`);
    const fn = spec.connections[0].code.tests.functions[0];
    assert.ok(html.includes(`"symbol":${JSON.stringify(fn.symbol)}`), 'the first hop carries its tests');
    // layout schemes follow meta.platform / meta.framework
    const { platform, framework, webview } = spec.meta;
    const want = platform === 'app' ? ['mvvm', ...(framework === 'flutter' ? ['flutter'] : []), ...(webview === true ? ['webview'] : [])] : [];
    assert.deepEqual(payloadKeys(html), want);
  });
}

test('deliver of a broken review fails and keeps the previous html byte for byte', (t) => {
  const dir = scratchDir(t);
  const output = path.join(dir, 'review.html');
  assert.equal(runCli('deliver', 'architecture', write(dir, 'good.json', readFixture('PR-10922')), output, '--json').status, 0);
  const before = fs.readFileSync(output);
  const broken = readFixture('PR-10922');
  broken.connections[0].to = 'no-such-node';
  const r = runCli('deliver', 'architecture', write(dir, 'broken.json', broken), output, '--json');
  assert.notEqual(r.status, 0);
  assert.equal(JSON.parse(r.stdout).ok, false);
  assert.deepEqual(fs.readFileSync(output), before);
});

test('deliver of a review with an unknown meta.platform fails and keeps the previous html', (t) => {
  const dir = scratchDir(t);
  const output = path.join(dir, 'review.html');
  assert.equal(runCli('deliver', 'architecture', write(dir, 'good.json', readFixture('PR-9151')), output, '--json').status, 0);
  const before = fs.readFileSync(output);
  const bad = readFixture('PR-9151');
  bad.meta.platform = 'desktop';
  const r = runCli('deliver', 'architecture', write(dir, 'bad.json', bad), output, '--json');
  assert.notEqual(r.status, 0);
  assert.deepEqual(fs.readFileSync(output), before);
});

test('validate rejects a malformed changes block and a layer name outside its scheme', (t) => {
  const dir = scratchDir(t);
  const badFile = readFixture('PR-10922');
  badFile.changes.files[0].status = 'renamed-ish';
  assert.notEqual(runCli('validate', 'architecture', write(dir, 'a.json', badFile), '--json').status, 0);
  const badLayer = readFixture('PR-10922');
  badLayer.components[0].code.layers.mvvm = 'controller';
  assert.notEqual(runCli('validate', 'architecture', write(dir, 'b.json', badLayer), '--json').status, 0);
});

test('doctor reports the installed skill as ready', (t) => {
  const bin = scratchDir(t);
  fs.writeFileSync(path.join(bin, 'code-review-graph'), '#!/bin/sh\necho "code-review-graph 2.4.0"\n', { mode: 0o755 });
  const r = spawnSync(process.execPath, [cli, 'doctor'], { encoding: 'utf8', env: { ...process.env, PATH: `${bin}${path.delimiter}${process.env.PATH}` } });
  assert.equal(r.status, 0, r.stdout + r.stderr);
  assert.match(r.stdout, /Archify is ready\./);
  assert.doesNotMatch(r.stdout, /\[(fail|missing|error)\]/i);
});

test('deliver rejects a label wider than its gap with the node, the connection and the fix as structured fields', (t) => {
  const dir = scratchDir(t);
  const spec = readFixture('PR-9151');
  spec.components.find((c) => c.id === 'ctrl').pos = [400, 144]; // gap to menu 80px, label "loadHtmlString" ~100px
  const r = runCli('deliver', 'architecture', write(dir, 'narrow.json', spec), path.join(dir, 'narrow.html'), '--json');
  assert.notEqual(r.status, 0);
  const found = JSON.parse(r.stdout).diagnostics.filter((d) => /^Label ".*" overlaps component/.test(d.message));
  assert.ok(found.length > 0, 'a label-overlap diagnostic is reported');
  for (const d of found) {
    assert.ok(d.subject.component && d.subject.connection, JSON.stringify(d.subject));
    assert.ok(d.evidence.labelWidth > 0 && d.evidence.labelRect && d.evidence.componentRect, JSON.stringify(d.evidence));
    assert.ok(d.supportedFixes.some((f) => /^labelAt \[\d+, \d+\]/.test(f)), JSON.stringify(d.supportedFixes));
  }
  const both = found.find((d) => d.subject.connection === 'menu-ctrl');
  assert.ok(both.supportedFixes.some((f) => /gap .*at least \d+px/.test(f)), 'widening the gap is offered');
});

test('deliver reports components that sit too close with their ids and a separation fix', (t) => {
  const dir = scratchDir(t);
  const spec = readFixture('PR-9151');
  spec.components.find((c) => c.id === 'changelog').pos = [20, 100]; // overlaps menu
  const r = runCli('deliver', 'architecture', write(dir, 'close.json', spec), path.join(dir, 'close.html'), '--json');
  assert.notEqual(r.status, 0);
  const d = JSON.parse(r.stdout).diagnostics.find((x) => /less than 8px apart/.test(x.message));
  assert.deepEqual([d.subject.components].flat().sort(), ['changelog', 'menu']);
  assert.ok(d.supportedFixes.length > 0);
});
