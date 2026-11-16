import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const skillRoot = path.resolve(__dirname, '..');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-v1-compat-'));

function render(mode, doc) {
  const input = path.join(tmp, `${mode}.json`);
  const output = path.join(tmp, `${mode}.html`);
  fs.writeFileSync(input, JSON.stringify(doc));
  try {
    execFileSync('node', [
      path.join(skillRoot, `renderers/${mode}/render-${mode}.mjs`),
      input,
      output,
    ], { stdio: ['ignore', 'ignore', 'pipe'] });
    return { code: 0, stderr: '', output };
  } catch (error) {
    return { code: error.status ?? 1, stderr: String(error.stderr || ''), output };
  }
}

function validate(mode, doc) {
  const input = path.join(tmp, `${mode}-validate.json`);
  fs.writeFileSync(input, JSON.stringify(doc));
  try {
    execFileSync('node', [
      path.join(skillRoot, 'bin/archify.mjs'),
      'validate',
      mode,
      input,
      '--json',
    ], { stdio: ['ignore', 'ignore', 'pipe'] });
    return { code: 0, stderr: '' };
  } catch (error) {
    return { code: error.status ?? 1, stderr: String(error.stderr || error.stdout || '') };
  }
}

const OFFICIAL_V1_EXAMPLES = {
  architecture: 'web-app.architecture.json',
};

for (const [mode, filename] of Object.entries(OFFICIAL_V1_EXAMPLES)) {
  test(`official v1 ${mode} baseline remains renderable and valid`, () => {
    const doc = JSON.parse(fs.readFileSync(path.join(__dirname, 'fixtures/v1-baseline', filename), 'utf8'));
    assert.equal(doc.schema_version, 1);
    assert.equal(doc.meta.quality_profile, undefined);
    const rendered = render(mode, doc);
    assert.equal(rendered.code, 0, rendered.stderr);
    assert.ok(fs.statSync(rendered.output).size > 0);
    const validated = validate(mode, doc);
    assert.equal(validated.code, 0, validated.stderr);
  });
}

test('legacy v1 architecture auto viewBox accommodates all seven implicit auto legend kinds', () => {
  const types = ['frontend', 'backend', 'database', 'cloud', 'security', 'messagebus', 'external'];
  const doc = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Narrow legacy architecture' },
    components: types.map((type, index) => ({
      id: `component_${index}`,
      type,
      label: type,
      pos: [0, 40 + index * 76],
      size: [120, 52],
    })),
    connections: [],
  };

  const rendered = render('architecture', doc);
  assert.equal(rendered.code, 0, rendered.stderr);
  const html = fs.readFileSync(rendered.output, 'utf8');
  const svg = html.match(/<svg\b[\s\S]*?<\/svg>/)?.[0] || '';
  const viewBox = svg.match(/viewBox="0 0 ([\d.]+) ([\d.]+)"/)?.slice(1).map(Number);
  const baselines = [...svg.matchAll(/data-legend-baseline="([\d.]+)"/g)].map((match) => Number(match[1]));
  assert.deepEqual([...svg.matchAll(/data-legend-semantic-kind="([^"]+)"/g)].map((match) => match[1]), types);
  assert.ok(viewBox && baselines.length === types.length);
  assert.ok(viewBox[0] > 160, 'auto viewBox should widen for its widest measured legend entry');
  assert.ok(Math.max(...baselines) < viewBox[1]);
  assert.ok(Math.min(...baselines) > 40 + (types.length - 1) * 76 + 52);

  const validated = validate('architecture', doc);
  assert.equal(validated.code, 0, validated.stderr);
});

function narrowExplicitViewBoxDocuments() {
  const componentTypes = ['frontend', 'backend', 'database', 'cloud', 'security', 'messagebus', 'external'];
  return {
    architecture: {
      schema_version: 1,
      diagram_type: 'architecture',
      meta: { title: 'Legacy narrow architecture', viewBox: [320, 800] },
      components: componentTypes.map((type, index) => ({
        id: `component_${index}`,
        type,
        label: type,
        pos: [40, 90 + index * 90],
        size: [120, 52],
      })),
      connections: [],
    },
  };
}

test('legacy v1 explicit narrow viewBoxes never hard-fail on an implicit auto legend', () => {
  const expectedNodeCounts = { architecture: 7 };
  for (const [mode, doc] of Object.entries(narrowExplicitViewBoxDocuments())) {
    assert.equal(doc.meta.legend, undefined);
    const rendered = render(mode, doc);
    assert.equal(rendered.code, 0, `${mode}: ${rendered.stderr}`);
    const svg = fs.readFileSync(rendered.output, 'utf8').match(/<svg\b[\s\S]*?<\/svg>/)?.[0] || '';
    assert.equal((svg.match(/data-node-id=/g) || []).length, expectedNodeCounts[mode], `${mode}: topology must remain intact`);
    assert.doesNotMatch(svg, />Legend</, `${mode}: an unfit implicit legend should degrade without overlap`);
    const validated = validate(mode, doc);
    assert.equal(validated.code, 0, `${mode}: ${validated.stderr}`);
  }
});

test('legacy v1 architecture geometry remains renderable without an explicit quality profile', () => {
  const doc = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Legacy architecture' },
    components: [
      { id: 'auth', type: 'security', label: 'Auth Provider', pos: [40, 110], size: [120, 64] },
      { id: 'lb', type: 'cloud', label: 'Load Balancer', pos: [460, 300], size: [130, 60] },
      { id: 'api', type: 'backend', label: 'API Server', pos: [670, 300], size: [130, 60] },
    ],
    connections: [
      {
        from: 'auth',
        to: 'api',
        label: 'verify JWT',
        fromSide: 'right',
        toSide: 'left',
        via: [[620, 142], [620, 330]],
      },
    ],
  };

  const result = render('architecture', doc);
  assert.equal(result.code, 0, result.stderr);
  assert.ok(fs.statSync(result.output).size > 0);
});

process.on('exit', () => fs.rmSync(tmp, { recursive: true, force: true }));
