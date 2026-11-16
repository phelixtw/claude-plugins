import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const bin = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'bin', 'archify.mjs');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-counterpart-'));

test('counterpart connection renders dashed, without arrowhead or focus attributes', () => {
  const spec = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Counterpart', viewBox: [500, 240] },
    components: [
      { id: 'a', type: 'frontend', label: 'Dart', pos: [20, 60], size: [120, 60] },
      { id: 'b', type: 'backend', label: 'Layout', pos: [340, 60], size: [120, 60] },
    ],
    connections: [{ id: 'cp', from: 'a', to: 'b', kind: 'counterpart', label: 'same', code: { from_symbol: 'a', to_symbol: 'b', story: 'Same component.' } }],
  };
  const inPath = path.join(tmp, 'in.json');
  const outPath = path.join(tmp, 'out.html');
  fs.writeFileSync(inPath, JSON.stringify(spec));
  execFileSync('node', [bin, 'render', 'architecture', inPath, outPath], { encoding: 'utf8' });
  const edge = fs.readFileSync(outPath, 'utf8').match(/<path data-edge-kind="counterpart"[^>]*>/)?.[0];
  assert.ok(edge, 'counterpart path should exist');
  assert.match(edge, /class="a-dashed"/);
  assert.doesNotMatch(edge, /marker-end|data-edge-from/);
});
