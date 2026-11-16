import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { fillBody, fillTemplate, splitTemplate, templateParts } from '../renderers/shared/utils.mjs';
import { REPORT_ID_RE, buildViewerShell, readReport, reportPayload, reportSource } from '../renderers/shared/report.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-report-payload-'));
const parts = templateParts({
  title: 'PROJ-1 $& and </script><!--',
  subtitle: 'sub $$',
  svg: '<svg viewBox="0 0 10 10"></svg>',
  cards: '<div class="cards"></div>',
  locale: 'zh-TW',
  codeData: { nodes: {}, edges: [], changes: { files: [] }, note: '</script>' },
});

test('fillBody in a Function built from its own source gives the same page as in Node', () => {
  const { body } = splitTemplate(template);
  const browserFill = new Function(`return (${fillBody})`)();
  assert.equal(browserFill(body, parts), fillBody(body, parts));
  assert.match(fillTemplate(template, parts), /<h1>PROJ-1 \$&amp; and &lt;\/script&gt;&lt;!--<\/h1>/);
  assert.match(fillTemplate(template, parts), /<html lang="zh-TW" data-theme="dark" data-review>/);
});

test('a report file round-trips and never carries raw </script> or <!--', () => {
  const payload = reportPayload({ id: 'PROJ-1', parts, spec: { meta: { title: '</script>' } } });
  const source = reportSource(payload);
  assert.ok(source.startsWith('archifyReport({') && source.endsWith(');\n'));
  assert.doesNotMatch(source, /<\/script|<!--/);
  const file = path.join(tmp, 'PROJ-1.js');
  fs.writeFileSync(file, source);
  assert.deepEqual(readReport(file), payload);
  fs.writeFileSync(file, '{}');
  assert.throws(() => readReport(file), /not an archify report/);
});

test('the viewer shell is ticket-free, offline, and parses into exactly three body scripts', () => {
  const shell = buildViewerShell(template);
  assert.doesNotMatch(shell.slice(0, shell.indexOf('<body>')), /\[PROJECT NAME\]/);
  assert.equal(buildViewerShell(template), shell, 'the shell does not depend on any report');
  const body = shell.slice(shell.indexOf('<body>'));
  assert.deepEqual([...new Set(body.match(/https?:\/\/[^"'\\ ]*/g))], ['http://www.w3.org/2000/svg'], 'no network address besides the SVG namespace');
  assert.equal(body.match(/<script\b/g).length, 3);
  assert.equal(body.match(/<\/script>/g).length, 3);
  assert.match(body, /<p id="archify-report-status"/);
});

test('report ids are ticket keys only', () => {
  for (const ok of ['PROJ-1', 'PR-10922', 'ab2-7']) assert.match(ok, REPORT_ID_RE);
  for (const bad of ['../x', 'PROJ', 'PROJ-1/..', '1-2', 'PROJ-1.js', '']) assert.doesNotMatch(bad, REPORT_ID_RE);
});
