import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const skillRoot = path.resolve(__dirname, '..');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-animation-'));
const example = 'web-app.architecture.json';

function render(animation = 'trace') {
  const doc = JSON.parse(fs.readFileSync(path.join(skillRoot, 'examples', example), 'utf8'));
  if (animation) doc.meta = { ...doc.meta, animation };
  else delete doc.meta.animation;
  const input = path.join(tmp, `architecture-${animation || 'static'}.json`);
  const output = path.join(tmp, `architecture-${animation || 'static'}.html`);
  fs.writeFileSync(input, JSON.stringify(doc));
  execFileSync('node', [path.join(skillRoot, 'renderers/architecture/render-architecture.mjs'), input, output], {
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  return fs.readFileSync(output, 'utf8');
}

function svgBlock(html) {
  return html.match(/<svg\b[\s\S]*?<\/svg>/)?.[0] || '';
}

test('static output omits animation attributes', () => {
  const svg = svgBlock(render(null));
  assert.doesNotMatch(svg, /data-animation=/);
  assert.doesNotMatch(svg, /data-animate=/);
});

test('the page is always dark and carries no visual preset', () => {
  const html = render(null);
  assert.match(html, /<html lang="en" data-theme="dark">/);
  assert.doesNotMatch(html, /data-preset/);
});

test('unknown meta.visual_preset is rejected by schema validation', () => {
  const doc = JSON.parse(fs.readFileSync(path.join(skillRoot, 'examples', example), 'utf8'));
  doc.meta.visual_preset = 'classic';
  const input = path.join(tmp, 'preset.json');
  fs.writeFileSync(input, JSON.stringify(doc));
  assert.throws(
    () => execFileSync('node', [path.join(skillRoot, 'renderers/architecture/render-architecture.mjs'), input, path.join(tmp, 'preset.html')], { stdio: ['ignore', 'ignore', 'pipe'] }),
    /visual_preset/,
  );
});

test('architecture adds one geometry-neutral semantic sigil per primary node', () => {
  const source = JSON.parse(fs.readFileSync(path.join(skillRoot, 'examples', example), 'utf8'));
  const expected = source.components.length;
  const staticHtml = render(null);
  const staticSvg = svgBlock(staticHtml);
  const traceSvg = svgBlock(render('trace'));
  const sigils = (svg) => [...svg.matchAll(/<g aria-hidden="true" data-semantic-sigil="[^"]+"[\s\S]*?<\/g>/g)].map((match) => match[0]);

  assert.equal(sigils(staticSvg).length, expected);
  assert.deepEqual(sigils(traceSvg), sigils(staticSvg), 'trace must not change sigil geometry');
  assert.match(staticHtml, /svg \.semantic-sigil \{/i);
  assert.match(staticHtml, /svg \.s-database\s+\{ color: var\(--database-stroke\); \}/);
});

test('architecture: trace animation annotates svg, edges, and nodes', () => {
  const svg = svgBlock(render());
  assert.match(svg, /<svg[^>]+data-animation="trace"/);
  assert.match(svg, /data-animate="edge" style="--step:0"/);
  assert.match(svg, /data-animate="node" style="--step:0"/);
  assert.match(svg, /aria-labelledby="archify-diagram-title archify-diagram-description"/);
  assert.match(svg, /<title id="archify-diagram-title">[^<]+<\/title>/);
  assert.match(svg, /<desc id="archify-diagram-description">[^<]+<\/desc>/);
  assert.match(svg, /id="node-[^"]+" data-node-id="[^"]+"[^>]+role="button"[^>]+aria-pressed="false"/);
  assert.match(svg, /data-edge-from="[^"]+" data-edge-to="[^"]+"/);
});

test('semantic SVG identity is deterministic for unchanged input', () => {
  const hooks = (svg) => [...svg.matchAll(/(?:id="node-|data-edge-from=")[^>]+/g)].map((match) => match[0]);
  assert.deepEqual(hooks(svgBlock(render())), hooks(svgBlock(render())));
});

process.on('exit', () => fs.rmSync(tmp, { recursive: true, force: true }));
