import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const skillRoot = path.resolve(__dirname, '..');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-route-probe-'));

const CASES = {
  architecture: 'web-app.architecture.json',
};

function render(mode, example) {
  const output = path.join(tmp, `${mode}.html`);
  execFileSync(process.execPath, [
    path.join(skillRoot, `renderers/${mode}/render-${mode}.mjs`),
    path.join(skillRoot, 'examples', example),
    output,
  ]);
  return fs.readFileSync(output, 'utf8');
}

function canonicalSvg(html) {
  return html.match(/<svg\b[\s\S]*?<\/svg>/)?.[0] || '';
}

test('all typed renderers inherit one viewer-only Route Probe', () => {
  for (const [mode, example] of Object.entries(CASES)) {
    const html = render(mode, example);
    assert.match(html, /id="route-probe" hidden role="region" aria-labelledby="route-probe-title"/, mode);
    assert.match(html, /id="btn-route-probe"[^>]+aria-label="Trace a directed route"[^>]+aria-pressed="false"[^>]+aria-controls="route-probe"/, mode);
    assert.match(html, /Archify\.routeProbe = \(function \(\)/, mode);
    assert.match(html, /Route Probe — shortest directed path over compiled semantics/, mode);
    assert.equal((html.match(/<svg\b/g) || []).length, 1, `${mode} keeps one static canonical SVG`);
    assert.doesNotMatch(canonicalSvg(html), /data-route-|route-probe-flow/, mode);
  }
});

test('Route Probe uses deterministic authored-direction BFS and exposes reachability first', () => {
  const html = render('architecture', CASES.architecture);
  assert.match(html, /function outgoingByNode\(\)/);
  assert.match(html, /var from = edge\.getAttribute\('data-edge-from'\)/);
  assert.match(html, /var to = edge\.getAttribute\('data-edge-to'\)/);
  assert.match(html, /if \(!byId\[from\] \|\| !byId\[to\] \|\| from === to\) return/);
  assert.match(html, /outgoing\[from\]\.push\(\{ edge: edge, to: to \}\)/);
  assert.match(html, /function reachableFrom\(source\)/);
  assert.match(html, /data-route-candidate/);
  assert.match(html, /function shortestDirectedPath\(source, target\)/);
  assert.match(html, /var queue = \[source\]/);
  assert.match(html, /previous\[link\.to\] = \{ from: queue\[cursor\], edge: link\.edge \}/);
  assert.match(html, /routeEdges\.unshift\(step\.edge\)/);
  assert.match(html, /nodeIds\.unshift\(step\.from\)/);
});

test('Route Probe turns a two-node question into a readable route receipt and stable link', () => {
  const html = render('architecture', CASES.architecture);
  assert.match(html, /svg\.setAttribute\('data-route-picking', 'target'\)/);
  assert.match(html, /svg\.setAttribute\('data-route-active', startId \+ '~' \+ endId\)/);
  assert.match(html, /node\.setAttribute\('data-route-step', String\(step\)\)/);
  assert.match(html, /edge\.setAttribute\('data-route-match', ''\)/);
  assert.match(html, /#route=' \+ encodeURIComponent\(startId\) \+ '~' \+ encodeURIComponent\(endId\)/);
  assert.match(html, /new URLSearchParams\(location\.hash\.replace/);
  assert.match(html, /Archify\.view\.reveal\(result\.nodes, \{ includeNeighbors: false, reason: 'route' \}\)/);
  assert.match(html, /shortest authored route/);
});

test('Route Probe keeps pointer, keyboard, motion, embed, and export boundaries explicit', () => {
  const html = render('architecture', CASES.architecture);
  assert.match(html, /svg\.addEventListener\('click', interceptSelection, true\)/);
  assert.match(html, /svg\.addEventListener\('keydown', interceptSelection, true\)/);
  assert.match(html, /event\.key !== 'Enter' && event\.key !== ' '/);
  assert.match(html, /e\.key === 'r' \|\| e\.key === 'R'/);
  assert.match(html, /e\.key === 'Escape' && Archify\.routeProbe\.active\(\)/);
  assert.match(html, /html\[data-embed="true"\] \.route-probe/);
  assert.match(html, /html\.getAttribute\('data-embed'\) === 'true'/);
  assert.match(html, /\.route-probe\[data-route-dock="top"\]/);
  assert.match(html, /function overlapArea\(a, b\)/);
  assert.match(html, /score\(topCandidate\) <= score\(bottomCandidate\)/);
  assert.match(html, /container\.addEventListener\('scroll', updateDocking, \{ passive: true \}\)/);
  assert.match(html, /@keyframes archify-route-probe-flow/);
  assert.match(html, /@media \(prefers-reduced-motion: reduce\)[\s\S]+\.route-probe-flow \{[\s\S]+animation: none !important/);
  assert.doesNotMatch(canonicalSvg(html), /data-route-|route-probe-flow/);
});

process.on('exit', () => fs.rmSync(tmp, { recursive: true, force: true }));
