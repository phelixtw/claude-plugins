import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import assert from 'node:assert/strict';

const root = fileURLToPath(new URL('../', import.meta.url));
const cli = path.join(root, 'bin/archify.mjs');
const architecture = path.join(root, 'examples/web-app.architecture.json');
const marker = 'MARKER=do-not-destroy';
function workspace(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-cli-types-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}
function run(args, cwd) {
  return spawnSync(process.execPath, [cli, ...args], { cwd, encoding: 'utf8' });
}

for (const command of ['render', 'deliver']) {
  test(`${command} rejects a non-HTML target without replacing existing bytes`, t => {
    const dir = workspace(t);
    const cwd = path.join(dir, 'working');
    fs.mkdirSync(cwd);
    const output = path.join(dir, 'marker.env');
    fs.writeFileSync(output, marker);
    const result = run([command, 'architecture', architecture, '../marker.env', ...(command === 'render' ? [] : ['--json'])], cwd);
    assert.equal(result.status, 1, result.stderr);
    if (command === 'render') assert.match(result.stderr, /output\/cli-extension/);
    else assert.equal(JSON.parse(result.stdout).diagnostics[0].code, 'output/cli-extension');
    assert.equal(fs.readFileSync(output, 'utf8'), marker);
    assert.deepEqual(fs.readdirSync(cwd), []);
  });
}

test('render and deliver reject HTML symlinks resolving to non-HTML files', t => {
  const cwd = workspace(t);
  const target = path.join(cwd, 'marker.env');
  const output = path.join(cwd, 'diagram.html');
  fs.writeFileSync(target, marker);
  try { fs.symlinkSync(target, output, 'file'); }
  catch (error) {
    if (error.code === 'EPERM') { t.skip('symlink creation requires permission'); return; }
    throw error;
  }
  for (const command of ['render', 'deliver']) {
    const result = run([command, 'architecture', architecture, output, ...(command === 'deliver' ? ['--json'] : [])], cwd);
    assert.equal(result.status, 1, result.stderr);
    if (command === 'deliver') assert.equal(JSON.parse(result.stdout).diagnostics[0].code, 'output/cli-resolved-extension');
    else assert.match(result.stderr, /output\/cli-resolved-extension/);
    assert.equal(fs.readFileSync(target, 'utf8'), marker);
    assert.equal(fs.lstatSync(output).isSymbolicLink(), true);
  }
});

test('absolute HTML outputs outside cwd remain supported', t => {
  const dir = workspace(t);
  const cwd = path.join(dir, 'working');
  fs.mkdirSync(cwd);
  const output = path.join(dir, 'diagram.HTML');
  const delivered = run(['deliver', 'architecture', architecture, output, '--json'], cwd);
  assert.equal(delivered.status, 0, delivered.stderr);
  assert.equal(JSON.parse(delivered.stdout).ok, true);
  assert.match(fs.readFileSync(output, 'utf8'), /<!DOCTYPE html>/i);
});

test('validate retains its internal no-output behavior', t => {
  const cwd = workspace(t);
  const result = run(['validate', 'architecture', architecture, '--json'], cwd);
  assert.equal(result.status, 0, result.stderr);
  assert.doesNotThrow(() => JSON.parse(result.stdout));
  assert.deepEqual(fs.readdirSync(cwd), []);
});
