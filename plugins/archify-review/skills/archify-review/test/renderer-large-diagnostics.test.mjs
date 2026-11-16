import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));

test('validate returns every renderer diagnostic when the payload exceeds the pipe buffer', () => {
  const r = spawnSync(process.execPath, ['bin/archify.mjs', 'validate', 'architecture',
    'test/fixtures/overlapping-components.architecture.json', '--quality', 'showcase', '--json'],
  { cwd: root, encoding: 'utf8' });
  const out = JSON.parse(r.stdout);
  assert.equal(out.ok, false);
  assert.ok(out.diagnostics.length > 100);
  assert.ok(out.diagnostics.every((d) => d.code !== 'internal/unclassified'));
});
