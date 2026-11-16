import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertFontCss, assertOfflineArtifact, inspectDocuments } from './helpers/offline-fonts.mjs';

const skillRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const cli = path.join(skillRoot, 'bin/archify.mjs');
const template = fs.readFileSync(path.join(skillRoot, 'assets/template.html'), 'utf8');
const DIAGRAMS = [
  ['architecture', 'web-app.architecture.json', 'web-app-rendered.html'],
];

test('the viewer template carries its own font and readable provenance', () => {
  assertOfflineArtifact(template, 'template');
  const license = fs.readFileSync(path.join(skillRoot, 'assets/JetBrainsMono-OFL.txt'), 'utf8').trim();
  assert.ok(inspectDocuments(template)[0].styles.some((css) => css.includes(license)), 'standalone font CSS must carry the full license');
  const notices = fs.readFileSync(path.join(skillRoot, 'THIRD_PARTY_NOTICES.md'), 'utf8');
  assert.match(notices, /## JetBrains Mono/);
  assert.match(notices, /assets\/JetBrainsMono-OFL\.txt/);
});

test('font checks accept equivalent CSS but reject missing bytes, coverage and local overrides', () => {
  const css = inspectDocuments(template)[0].styles.join('\n');
  const reordered = css.replaceAll("font-family: 'JetBrains Mono'; font-style: normal;", 'font-style:normal; font-family:"JetBrains Mono";');
  assertFontCss(reordered, 'equivalent CSS');
  assert.throws(() => assertFontCss(css.replace(/@font-face\s*\{[^}]+\}/, ''), 'missing face'));
  assert.throws(() => assertFontCss(css.replace('base64,', 'base64,A'), 'corrupt bytes'));
  assert.throws(() => assertFontCss(css.replace('U+0460-052F', 'U+0460-052E'), 'missing character'));
  assert.throws(() => assertFontCss(css.replace('src: url(', "src: local('JetBrains Mono'), url("), 'local override'));
});

test('a freshly delivered artifact reaches no external origin', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-offline-'));
  try {
    for (const [type, input] of DIAGRAMS) {
      const output = path.join(dir, `${type}.html`);
      const result = spawnSync(process.execPath, [cli, 'deliver', type, path.join(skillRoot, 'examples', input), output, '--quality', 'showcase', '--json'], { encoding: 'utf8' });
      assert.equal(result.status, 0, `${type}: ${result.stderr}`);
      assertOfflineArtifact(fs.readFileSync(output, 'utf8'), type);
    }
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
