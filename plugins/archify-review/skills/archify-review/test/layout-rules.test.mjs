// Per-rule coverage for the renderers' layout validators. The golden suite's
// negative cases mostly trip ajv SCHEMA rules; this file targets the hand-
// written LAYOUT rules (the `problems.push(...)` checks) — the layer that has
// regressed before — by mutating a valid example into exactly one violation
// and asserting the renderer exits non-zero with the expected message.
//
// It also locks the error-message CONTRACT: representative messages must carry
// both the numeric threshold and a remediation hint, since the consumer is an
// LLM that fixes the JSON from the message alone.
//
//   node --test test/*.test.mjs

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const skillRoot = path.resolve(__dirname, '..');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-rules-'));

const EXAMPLES = {
  architecture: 'web-app.architecture.json',
};

function load(mode) {
  return JSON.parse(fs.readFileSync(path.join(skillRoot, 'examples', EXAMPLES[mode]), 'utf8'));
}

// Returns { code, stderr }. Never throws on non-zero exit.
function render(mode, doc) {
  const input = path.join(tmp, `${mode}-${Math.abs(hash(JSON.stringify(doc)))}.json`);
  const outPath = path.join(tmp, `${mode}-${Math.abs(hash(JSON.stringify(doc)))}.html`);
  fs.writeFileSync(input, JSON.stringify(doc));
  try {
    execFileSync('node', [
      path.join(skillRoot, `renderers/${mode}/render-${mode}.mjs`),
      input,
      outPath,
    ], { stdio: ['ignore', 'ignore', 'pipe'] });
    return { code: 0, stderr: '', outPath };
  } catch (err) {
    return { code: err.status ?? 1, stderr: String(err.stderr || ''), outPath };
  }
}

function validateCli(mode, doc, quality = 'showcase') {
  const input = path.join(tmp, `${mode}-cli-${Math.abs(hash(JSON.stringify(doc)))}.json`);
  fs.writeFileSync(input, JSON.stringify(doc));
  try {
    const stdout = execFileSync('node', [
      path.join(skillRoot, 'bin', 'archify.mjs'),
      'validate',
      mode,
      input,
      '--quality',
      quality,
      '--json',
    ], { encoding: 'utf8' });
    return { code: 0, result: JSON.parse(stdout) };
  } catch (err) {
    return {
      code: err.status ?? 1,
      result: JSON.parse(String(err.stdout || '{}')),
    };
  }
}

function hash(s) {
  let h = 0;
  for (let i = 0; i < s.length; i += 1) h = (h * 31 + s.charCodeAt(i)) | 0;
  return h;
}

function edgePoints(html, id) {
  const escapedId = id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const pointsAttribute = html.match(
    new RegExp(`data-edge-id="${escapedId}" data-composition-points="([^"]+)"`),
  )?.[1];
  assert.ok(pointsAttribute, `expected rendered edge points for ${id}`);
  return pointsAttribute.split(';').map((point) => point.split(',').map(Number));
}

function nodeRect(html, id) {
  const escapedId = id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = html.match(new RegExp(
    `<g id="node-${escapedId}"[^>]*>[\\s\\S]*?<rect x="([^"]+)" y="([^"]+)" width="([^"]+)" height="([^"]+)"`,
  ));
  assert.ok(match, `expected rendered node rect for ${id}`);
  const [, x, y, width, height] = match.map(Number);
  return { x, y, width, height };
}

function boundaryFrameRect(html, index) {
  const match = html.match(new RegExp(
    `<rect data-graph-role="structural-frame"[^>]*data-composition-frame-id="${index}"[^>]*x="([^"]+)" y="([^"]+)" width="([^"]+)" height="([^"]+)"`,
  ));
  assert.ok(match, `expected boundary frame ${index}`);
  const [, x, y, width, height] = match.map(Number);
  return { x, y, width, height };
}

function boundaryTitleMasks(html) {
  return [...html.matchAll(
    /<g data-graph-role="structural-frame-label"[^>]*data-composition-frame-id="(\d+)"[^>]*>[\s\S]*?<rect data-graph-role="structural-frame-label-mask" x="([^"]+)" y="([^"]+)" width="([^"]+)" height="([^"]+)"/g,
  )].map((match) => ({
    index: Number(match[1]),
    x: Number(match[2]),
    y: Number(match[3]),
    width: Number(match[4]),
    height: Number(match[5]),
  }));
}

function rectContainsRect(outer, inner) {
  return outer.x <= inner.x
    && outer.y <= inner.y
    && outer.x + outer.width >= inner.x + inner.width
    && outer.y + outer.height >= inner.y + inner.height;
}

function rectanglesOverlap(left, right) {
  return left.x < right.x + right.width
    && left.x + left.width > right.x
    && left.y < right.y + right.height
    && left.y + left.height > right.y;
}

function edgeLabelPoint(html, id) {
  const escapedId = id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = html.match(new RegExp(
    `<g data-detail="context"[^>]*data-edge-id="${escapedId}"[^>]*>[\\s\\S]*?<text x="([^"]+)" y="([^"]+)"`,
  ));
  assert.ok(match, `expected rendered edge label for ${id}`);
  return match.slice(1).map(Number);
}

function axisOverlapLength(a1, a2, b1, b2) {
  return Math.max(0, Math.min(Math.max(a1, a2), Math.max(b1, b2))
    - Math.max(Math.min(a1, a2), Math.min(b1, b2)));
}

function nodeBorderOverlap(points, rect) {
  const hits = [];
  for (let index = 0; index < points.length - 1; index += 1) {
    const [start, end] = [points[index], points[index + 1]];
    if (start[0] === end[0] && (start[0] === rect.x || start[0] === rect.x + rect.width)) {
      const length = axisOverlapLength(start[1], end[1], rect.y, rect.y + rect.height);
      if (length > 0) hits.push({ segment: index, length, start, end });
    }
    if (start[1] === end[1] && (start[1] === rect.y || start[1] === rect.y + rect.height)) {
      const length = axisOverlapLength(start[0], end[0], rect.x, rect.x + rect.width);
      if (length > 0) hits.push({ segment: index, length, start, end });
    }
  }
  return hits;
}

function assertRelationshipsAvoidAllNodeBorders(html, relationships, diagramNodes) {
  for (const [edgeIndex, edge] of relationships.entries()) {
    const edgeId = edge.id || `edge-${edgeIndex}`;
    const points = edgePoints(html, edgeId);
    for (const node of diagramNodes) {
      const hits = nodeBorderOverlap(points, nodeRect(html, node.id));
      assert.deepEqual(
        hits,
        [],
        `edge ${edgeId} runs along node ${node.id} border: ${JSON.stringify(hits)}`,
      );
    }
  }
}

// [name, mode, mutate(doc), expectedSubstrings[]] — every mutation introduces
// exactly one layout violation. Each expected substring must appear in stderr.
const CASES = [
  // ---- architecture layout rules ----
  ['architecture: components overlap', 'architecture',
    (d) => { d.components[1].pos = [...d.components[0].pos]; }, ['less than 8px apart']],
  ['architecture: connection references unknown component', 'architecture',
    (d) => { d.connections[0].to = 'ghost'; }, ['unknown target "ghost"']],
  ['architecture: boundary wraps unknown component', 'architecture',
    (d) => { d.boundaries[0].wraps.push('ghost'); }, ['wraps unknown component "ghost"']],
  ['architecture: boundary title must fit inside its own frame', 'architecture',
    (d) => {
      d.meta.quality_profile = 'standard';
      d.meta.viewBox = [500, 400];
      d.components = [{
        id: 'narrow',
        type: 'backend',
        label: 'Narrow',
        pos: [330, 100],
        size: [128, 60],
      }];
      delete d.meta.views;
      d.connections = [];
      d.cards = [];
      d.boundaries = [{
        kind: 'region',
        label: 'A boundary title that cannot fit its narrow authored frame',
        wraps: ['narrow'],
        pad: 0,
      }];
    }, ['Boundary label', 'fit', 'shorten']],
  ['architecture: boundary title must stay inside the authored viewBox', 'architecture',
    (d) => {
      d.meta.quality_profile = 'standard';
      d.meta.viewBox = [500, 400];
      d.components = [{
        id: 'near-top',
        type: 'backend',
        label: 'Near top',
        pos: [120, 8],
        size: [128, 60],
      }];
      delete d.meta.views;
      d.connections = [];
      d.cards = [];
      d.boundaries = [{
        kind: 'region',
        label: 'Viewport title',
        wraps: ['near-top'],
        pad: 0,
      }];
    }, ['Boundary label', 'outside the viewBox', 'move wrapped components']],
  ['architecture: label wider than component', 'architecture',
    (d) => { d.components[0].label = 'An Extremely Long Component Label Overflow'; }, ['wider than component', 'shorten the label']],
  ['architecture: component sublabel wider than its legible minimum', 'architecture',
    (d) => { d.components[0].sublabel = 'This supporting sentence is far too long for one architecture component'; },
    ['Sublabel', 'legible', 'widen size']],
  ['architecture: component tag wider than its legible minimum', 'architecture',
    (d) => { d.components[0].tag = 'This tag is far too long to sit inside one architecture component box'; },
    ['Tag', 'legible', 'widen size']],
  ['architecture: component overlap suggests fix', 'architecture',
    (d) => { d.components[1].pos = [...d.components[0].pos]; }, ['Suggested fix', 'move "']],

];

for (const [name, mode, mutate, expected] of CASES) {
  test(name, () => {
    const doc = load(mode);
    mutate(doc);
    const { code, stderr } = render(mode, doc);
    assert.notEqual(code, 0, `expected non-zero exit; stderr:\n${stderr}`);
    assert.doesNotMatch(stderr, /TypeError|is not a function|Cannot read/, `crashed instead of reporting:\n${stderr}`);
    for (const sub of expected) {
      assert.ok(stderr.includes(sub), `expected "${sub}" in stderr:\n${stderr}`);
    }
  });
}

test('architecture: ordinary boundaries may express orthogonal overlapping memberships', () => {
  const d = load('architecture');
  d.boundaries[1].wraps.push('auth');
  const { code, stderr, outPath } = render('architecture', d);
  assert.equal(code, 0, stderr);
  const html = fs.readFileSync(outPath, 'utf8');
  assert.equal(rectanglesOverlap(boundaryFrameRect(html, 0), boundaryFrameRect(html, 1)), true);
  const masks = boundaryTitleMasks(html);
  assert.equal(masks.length, 2);
  assert.equal(rectanglesOverlap(masks[0], masks[1]), false);
});

test('architecture: profile-less v1 keeps rendering when a boundary title cannot meet strict composition', () => {
  const d = load('architecture');
  delete d.meta.quality_profile;
  d.meta.viewBox = [500, 400];
  d.components = [{
    id: 'narrow',
    type: 'backend',
    label: 'Narrow',
    pos: [330, 100],
    size: [128, 60],
  }];
  delete d.meta.views;
  d.connections = [];
  d.cards = [];
  d.boundaries = [{
    kind: 'region',
    label: 'A boundary title that cannot fit its narrow authored frame',
    wraps: ['narrow'],
    pad: 0,
  }];

  const { code, stderr } = render('architecture', d);
  assert.equal(code, 0, stderr);
});

test('architecture: profile-less v1 keeps legacy boundary geometry at the top edge', () => {
  const d = load('architecture');
  delete d.meta.quality_profile;
  d.meta.viewBox = [500, 400];
  d.components = [{
    id: 'near-top',
    type: 'backend',
    label: 'Near top',
    pos: [120, 22],
    size: [128, 60],
  }];
  delete d.meta.views;
  d.connections = [];
  d.cards = [];
  d.boundaries = [{
    kind: 'region',
    label: 'Legacy top edge',
    wraps: ['near-top'],
    pad: 0,
  }];

  const { code, stderr } = render('architecture', d);
  assert.equal(code, 0, stderr);
});

test('architecture: boundary labels reserve readable space above wrapped components', () => {
  const d = load('architecture');
  d.boundaries = [{
    kind: 'security-group',
    label: 'Tool effects and permissions',
    wraps: ['lb', 'api'],
    pad: 14,
  }];

  const { code, stderr, outPath } = render('architecture', d);
  assert.equal(code, 0, stderr);
  const html = fs.readFileSync(outPath, 'utf8');
  const label = html.match(
    /<text data-boundary-label="" x="[^"]+" y="([^"]+)" class="t-security" font-size="[^"]+" font-weight="600">Tool effects and permissions<\/text>/,
  );
  assert.ok(label, 'expected the security boundary label');
  const labelBaseline = Number(label[1]);
  const firstWrappedNodeY = Math.min(
    nodeRect(html, 'lb').y,
    nodeRect(html, 'api').y,
  );
  assert.ok(
    labelBaseline <= firstWrappedNodeY - 4,
    `expected the boundary label baseline (${labelBaseline}) to clear the first wrapped node (${firstWrappedNodeY})`,
  );
});

test('architecture: auto viewBox keeps expanded boundary titles readable at desktop scale', () => {
  const d = load('architecture');
  delete d.meta.viewBox;
  d.meta.quality_profile = 'showcase';
  delete d.meta.views;
  d.components = [{
    id: 'node',
    type: 'backend',
    label: 'Current node',
    pos: [800, 100],
    size: [120, 60],
  }];
  d.connections = [];
  d.cards = [];
  d.boundaries = [{
    kind: 'region',
    label: 'Disaster recovery boundary Disaster recovery boundary Disaster recovery boundary',
    wraps: ['node'],
    pad: 30,
  }];

  const { code, stderr, outPath } = render('architecture', d);
  assert.equal(code, 0, stderr);
  const html = fs.readFileSync(outPath, 'utf8');
  const viewBoxWidth = Number(html.match(/\bviewBox="0 0 ([\d.]+) [\d.]+"/)?.[1]);
  const sourceFontPx = Number(html.match(/<text data-boundary-label[^>]*font-size="([\d.]+)"/)?.[1]);
  const projectedFontPx = sourceFontPx * Math.min(1, 930 / viewBoxWidth);
  assert.ok(
    projectedFontPx >= 6,
    `expected the final ${viewBoxWidth}px viewBox to project its ${sourceFontPx}px boundary title at 6px or larger, got ${projectedFontPx}px`,
  );
});

test('architecture: boundary labels and their masks paint above relationship routes', () => {
  const d = load('architecture');
  const { code, stderr, outPath } = render('architecture', d);
  assert.equal(code, 0, stderr);
  const html = fs.readFileSync(outPath, 'utf8');
  const lastRoute = html.lastIndexOf('data-composition-points=');
  const firstBoundaryLabel = html.indexOf('data-graph-role="structural-frame-label"');
  assert.ok(lastRoute >= 0, 'expected at least one rendered relationship route');
  assert.ok(firstBoundaryLabel >= 0, 'expected a foreground boundary label group');
  assert.ok(
    firstBoundaryLabel > lastRoute,
    'boundary labels must paint after relationship routes so routes cannot cross the title text',
  );
  assert.match(
    html.slice(firstBoundaryLabel, firstBoundaryLabel + 500),
    /data-graph-role="structural-frame-label-mask"/,
    'expected an opaque mask behind the foreground boundary title',
  );
});

test('architecture: nested boundary title rails stay inside frames and avoid labels and nodes', () => {
  const d = JSON.parse(fs.readFileSync(
    path.join(skillRoot, 'test/fixtures/nested-boundaries.architecture.json'),
    'utf8',
  ));
  const { code, stderr, outPath } = render('architecture', d);
  assert.equal(code, 0, stderr);
  const html = fs.readFileSync(outPath, 'utf8');
  const masks = boundaryTitleMasks(html);
  assert.equal(masks.length, d.boundaries.length);

  for (const mask of masks) {
    assert.ok(
      rectContainsRect(boundaryFrameRect(html, mask.index), mask),
      `boundary title mask ${mask.index} must stay inside its frame: ${JSON.stringify(mask)}`,
    );
    for (const component of d.components) {
      assert.equal(
        rectanglesOverlap(mask, nodeRect(html, component.id)),
        false,
        `boundary title mask ${mask.index} overlaps component ${component.id}`,
      );
    }
  }

  for (let left = 0; left < masks.length; left += 1) {
    for (let right = left + 1; right < masks.length; right += 1) {
      assert.equal(
        rectanglesOverlap(masks[left], masks[right]),
        false,
        `boundary title masks ${left} and ${right} overlap`,
      );
    }
  }
});

test('architecture: boundary title masks cannot obscure connection labels', () => {
  const d = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Boundary and connection labels', quality_profile: 'standard', viewBox: [700, 500] },
    components: [
      { id: 'scoped', type: 'backend', label: 'Scoped', pos: [250, 100], size: [120, 60] },
      { id: 'source', type: 'external', label: 'Source', pos: [50, 300], size: [100, 50] },
      { id: 'target', type: 'external', label: 'Target', pos: [500, 300], size: [100, 50] },
    ],
    boundaries: [{ kind: 'region', label: 'Runtime scope', wraps: ['scoped'] }],
    connections: [{
      from: 'source',
      to: 'target',
      label: 'Route label',
      route: 'straight',
      labelAt: [270, 88],
    }],
  };

  const { code, stderr } = render('architecture', d);
  assert.notEqual(code, 0);
  assert.match(stderr, /Boundary label "Runtime scope" overlaps connection label "Route label"/);
  assert.match(stderr, /labelAt\/labelDx\/labelDy\/labelSegment/);
});

// ---- sublabel/tag shrink-to-fit: the render half of the same rule ----
// Validation only rejects text that cannot fit even at its legible minimum.
// Everything between "fits at the preferred size" and that floor must shrink,
// not overflow — otherwise the common case still paints over its neighbours.
const SHRINK_CASES = [
  // [mode, mutate(doc), preferredFontSize, selector for the sublabel <text>]
  ['architecture', (d) => { d.components[0].sublabel = 'Browser and mobile apps'; }, 9],
];

for (const [mode, mutate, preferred] of SHRINK_CASES) {
  test(`${mode}: an over-long sublabel shrinks to fit instead of overflowing`, () => {
    const d = load(mode);
    mutate(d);
    const { code, stderr, outPath } = render(mode, d);
    assert.equal(code, 0, stderr);
    const html = fs.readFileSync(outPath, 'utf8');
    const sub = html.match(/<text data-detail="context"[^>]*font-size="([\d.]+)"[^>]*>/);
    assert.ok(sub, 'expected a sublabel <text> in the rendered SVG');
    const fontSize = Number(sub[1]);
    assert.ok(
      fontSize < preferred,
      `expected the sublabel to shrink below the ${preferred}px preferred size, got ${fontSize}`,
    );
    assert.ok(fontSize >= 6, `expected the sublabel to stay legible, got ${fontSize}`);
  });
}

const TAG_SHRINK_CASES = [
  // [mode, collection, tag, preferredFontSize]
  ['architecture', 'components', 'owner: platform operations team', 9],
];

for (const [mode, collection, tag, preferred] of TAG_SHRINK_CASES) {
  test(`${mode}: an over-long tag shrinks to fit instead of overflowing`, () => {
    const d = load(mode);
    d[collection][0].tag = tag;
    const { code, stderr, outPath } = render(mode, d);
    assert.equal(code, 0, stderr);
    const html = fs.readFileSync(outPath, 'utf8');
    const escapedTag = tag.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const match = html.match(new RegExp(
      `<text data-detail="(?:fine|context)"[^>]*font-size="([\\d.]+)"[^>]*>${escapedTag}</text>`,
    ));
    assert.ok(match, `expected a tag <text> for "${tag}" in the rendered SVG`);
    const fontSize = Number(match[1]);
    assert.ok(
      fontSize < preferred,
      `expected the tag to shrink below the ${preferred}px preferred size, got ${fontSize}`,
    );
    assert.ok(fontSize >= 6, `expected the tag to stay legible, got ${fontSize}`);
  });
}

test('contract: a too-wide label is never redirected into sublabel', () => {
  // Every renderer used to advise "move detail to sublabel" for an over-long
  // label. Sublabels are measured now, so that advice would move the problem
  // rather than fix it.
  const LABELS = {
    architecture: 'An Extremely Long Component Label Overflow',
  };
  const FIELD = { architecture: 'components' };
  for (const [mode, label] of Object.entries(LABELS)) {
    const d = load(mode);
    d[FIELD[mode]][0].label = label;
    const { code, stderr } = render(mode, d);
    assert.notEqual(code, 0, `${mode}: expected non-zero exit; stderr:\n${stderr}`);
    assert.ok(stderr.includes('wider than'), `${mode}: expected a width message:\n${stderr}`);
    assert.ok(
      !/move detail to sublabel/.test(stderr),
      `${mode}: label advice still points at the measured sublabel field:\n${stderr}`,
    );
  }
});

// ---- error-message contract: threshold + remediation, not just a path ----
test('contract: ajv path errors are annotated with the element id', () => {
  const d = load('architecture');
  d.components[3].colour = 'red'; // unknown property → ajv additionalProperties
  const { stderr } = render('architecture', d);
  // Only meaningful when ajv is installed; skip the assertion in degraded mode.
  if (!/schema validation failed/.test(stderr)) return;
  assert.match(stderr, /id\/label:/);
});

test('architecture: Clean Flow Gate rejects a connection through a component', () => {
  const d = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Opaque component crossing', quality_profile: 'standard' },
    components: [
      { id: 'left', type: 'frontend', label: 'Left', pos: [60, 120], size: [100, 54] },
      { id: 'middle', type: 'security', label: 'Middle', pos: [270, 120], size: [100, 54] },
      { id: 'right', type: 'backend', label: 'Right', pos: [480, 120], size: [100, 54] },
    ],
    connections: [{ id: 'direct', from: 'left', to: 'right', route: 'straight' }],
  };
  const { code, stderr } = render('architecture', d);
  assert.notEqual(code, 0, `expected non-zero exit; stderr:\n${stderr}`);
  assert.match(stderr, /\[clean-flow\/edge-through-node\] architecture connections\[0\] id "direct"/);
  assert.match(stderr, /crosses component "middle"/);
  assert.match(stderr, /segment 0 .*2px clearance/);
});

test('architecture: showcase rejects a connection label that hides another route', () => {
  const d = load('architecture');
  d.connections[0].labelAt = [620, 330];
  const { code, stderr } = render('architecture', d);
  assert.notEqual(code, 0, `expected non-zero exit; stderr:\n${stderr}`);
  assert.match(stderr, /\[composition\/label-route-clearance\] showcase architecture/);
  assert.match(stderr, /HTTPS.*lb-to-api/);
});

function autoRoutePassThroughDocument(connection) {
  return {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Auto-route pass-through regression' },
    components: [
      { id: 'api', type: 'backend', label: 'API', pos: [400, 280], size: [160, 76] },
      { id: 'cache', type: 'database', label: 'Cache', pos: [645, 130], size: [130, 60] },
      { id: 'queue', type: 'cloud', label: 'Queue', pos: [880, 130] },
    ],
    connections: [connection],
  };
}

test('architecture: default auto route selects a safe orthogonal candidate around an unrelated component', () => {
  const d = autoRoutePassThroughDocument({ from: 'api', to: 'queue', variant: 'dashed' });
  const { code, stderr, outPath } = render('architecture', d);
  assert.equal(code, 0, stderr);
  const html = fs.readFileSync(outPath, 'utf8');
  assert.match(html, /data-composition-points="560,318;856,318;856,160;880,160"/);
});

test('architecture: auto route enters explicit top and bottom ports perpendicularly', () => {
  const d = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Endpoint direction regression' },
    components: [
      { id: 'cli-agents', type: 'external', label: 'CLI Agents', pos: [300, 100], size: [100, 60] },
      { id: 'tasks-watch', type: 'backend', label: 'Tasks Watcher', pos: [100, 240], size: [100, 60] },
    ],
    connections: [
      { id: 'tasks-file', from: 'cli-agents', to: 'tasks-watch', variant: 'dashed', fromSide: 'bottom', toSide: 'top' },
    ],
  };
  const { code, stderr, outPath } = render('architecture', d);
  assert.equal(code, 0, stderr);
  const html = fs.readFileSync(outPath, 'utf8');
  assert.match(html, /data-composition-points="350,160;350,200;150,200;150,240"/);
});

test('architecture: auto route preserves inferred side normals when the primary dogleg is blocked', () => {
  const d = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Inferred endpoint direction regression' },
    components: [
      { id: 'workspace', type: 'frontend', label: 'Workspace UI', pos: [40, 300], size: [120, 60] },
      { id: 'runtime-server', type: 'backend', label: 'Runtime Server', pos: [220, 300], size: [120, 60] },
      { id: 'runtime-store', type: 'backend', label: 'Runtime Store', pos: [400, 300], size: [120, 60] },
      { id: 'stream-hub', type: 'messagebus', label: 'Terminal Stream Hub', pos: [700, 100], size: [120, 60] },
    ],
    connections: [
      { id: 'terminal-return', from: 'stream-hub', to: 'workspace' },
    ],
  };
  const { code, stderr, outPath } = render('architecture', d);
  assert.equal(code, 0, stderr);
  const html = fs.readFileSync(outPath, 'utf8');
  assert.match(html, /data-composition-points="700,130;184,130;184,330;160,330"/);
  assert.doesNotMatch(html, /data-composition-points="700,130;700,230;160,230;160,330"/);
});

test('architecture: explicit via cannot run tangentially into an authored top port', () => {
  const d = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Tangent endpoint regression' },
    components: [
      { id: 'cli-agents', type: 'external', label: 'CLI Agents', pos: [300, 100], size: [100, 60] },
      { id: 'tasks-watch', type: 'backend', label: 'Tasks Watcher', pos: [100, 240], size: [100, 60] },
    ],
    connections: [
      {
        id: 'tasks-file',
        from: 'cli-agents',
        to: 'tasks-watch',
        variant: 'dashed',
        fromSide: 'bottom',
        toSide: 'top',
        via: [[350, 200], [100, 200], [100, 240]],
      },
    ],
  };
  const { code, stderr } = render('architecture', d);
  assert.notEqual(code, 0, `expected non-zero exit; stderr:\n${stderr}`);
  assert.match(stderr, /\[clean-flow\/endpoint-side-direction\] architecture connections\[0\] id "tasks-file"/);
  assert.match(stderr, /final segment 3 \[100, 240\] -> \[150, 240\]/);
  assert.match(stderr, /toSide "top".*vertical downward from above/);
});

test('architecture: explicit orthogonal route remains authoritative when it crosses a component', () => {
  const d = autoRoutePassThroughDocument({
    from: 'api',
    to: 'queue',
    variant: 'dashed',
    route: 'orthogonal-h',
  });
  const { code, stderr } = render('architecture', d);
  assert.notEqual(code, 0, `expected non-zero exit; stderr:\n${stderr}`);
  assert.match(stderr, /connections\[0\] "api" -> "queue" crosses component "cache"/);
});

test('architecture: auto route still fails closed when doglegs and side-aware bridges are blocked', () => {
  const d = autoRoutePassThroughDocument({ from: 'api', to: 'queue', variant: 'dashed' });
  d.components.push({ id: 'guard', type: 'security', label: 'Guard', pos: [825, 215], size: [70, 50] });
  const { code, stderr } = render('architecture', d);
  assert.notEqual(code, 0, `expected non-zero exit; stderr:\n${stderr}`);
  assert.match(stderr, /connections\[0\] "api" -> "queue" crosses component "cache"/);
});

test('architecture: explicit waypoints around an obstacle remain valid by default', () => {
  const d = autoRoutePassThroughDocument({
    from: 'api',
    to: 'queue',
    variant: 'dashed',
    fromSide: 'right',
    toSide: 'top',
    via: [[620, 318], [620, 100], [940, 100]],
  });
  const { code, stderr } = render('architecture', d);
  assert.equal(code, 0, stderr);
});

test('architecture: showcase rejects an unrelated proper edge crossing', () => {
  const d = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Showcase crossing', quality_profile: 'showcase' },
    components: [
      { id: 'a', type: 'frontend', label: 'A', pos: [60, 80], size: [60, 40] },
      { id: 'b', type: 'backend', label: 'B', pos: [360, 260], size: [60, 40] },
      { id: 'c', type: 'database', label: 'C', pos: [60, 260], size: [60, 40] },
      { id: 'd', type: 'external', label: 'D', pos: [360, 80], size: [60, 40] },
    ],
    connections: [
      { id: 'down-right', from: 'a', to: 'b', route: 'orthogonal-h' },
      { id: 'up-right', from: 'c', to: 'd', route: 'orthogonal-v', fromSide: 'top', toSide: 'bottom' },
    ],
  };
  const { code, stderr } = render('architecture', d);
  assert.notEqual(code, 0, `expected non-zero exit; stderr:\n${stderr}`);
  assert.match(stderr, /\[composition\/proper-crossing\] showcase architecture/);
  assert.match(stderr, /connections\[0\] id "down-right"/);
  assert.match(stderr, /connections\[1\] id "up-right"/);
  assert.match(stderr, /at \[240, 190\]/);
  assert.match(stderr, /segments 1 and 1/);
  assert.match(stderr, /route\/via|fromSide\/toSide/);
});

test('architecture: showcase preserves a straight-through explicit waypoint as an authored touch', () => {
  const d = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: {
      title: 'Forward-collinear waypoint compatibility',
      quality_profile: 'showcase',
      viewBox: [600, 320],
      legend: { mode: 'hidden' },
    },
    components: [
      { id: 'a', type: 'backend', label: 'A', pos: [50, 100], size: [80, 60] },
      { id: 'b', type: 'backend', label: 'B', pos: [450, 100], size: [80, 60] },
      { id: 'c', type: 'backend', label: 'C', pos: [260, 0], size: [80, 60] },
      { id: 'd', type: 'backend', label: 'D', pos: [260, 200], size: [80, 60] },
    ],
    connections: [
      { id: 'horizontal', from: 'a', to: 'b', via: [[300, 130]] },
      { id: 'vertical', from: 'c', to: 'd' },
    ],
  };

  const { code, stderr } = render('architecture', d);
  assert.equal(code, 0, stderr);
});

test('architecture: standard keeps the same proper crossing renderable', () => {
  const d = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Standard crossing', quality_profile: 'standard' },
    components: [
      { id: 'a', type: 'frontend', label: 'A', pos: [60, 80], size: [60, 40] },
      { id: 'b', type: 'backend', label: 'B', pos: [360, 260], size: [60, 40] },
      { id: 'c', type: 'database', label: 'C', pos: [60, 260], size: [60, 40] },
      { id: 'd', type: 'external', label: 'D', pos: [360, 80], size: [60, 40] },
    ],
    connections: [
      { from: 'a', to: 'b', route: 'orthogonal-h' },
      { from: 'c', to: 'd', route: 'orthogonal-v', fromSide: 'top', toSide: 'bottom' },
    ],
  };
  const { code, stderr } = render('architecture', d);
  assert.equal(code, 0, stderr);
});

function ambiguousCorridorDocument(profile) {
  return {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Ambiguous corridor', quality_profile: profile },
    components: [
      { id: 'a', type: 'frontend', label: 'A', pos: [40, 60], size: [60, 40] },
      { id: 'b', type: 'backend', label: 'B', pos: [400, 60], size: [60, 40] },
      { id: 'c', type: 'database', label: 'C', pos: [120, 220], size: [60, 40] },
      { id: 'd', type: 'external', label: 'D', pos: [480, 220], size: [60, 40] },
    ],
    connections: [
      { id: 'first', from: 'a', to: 'b', fromSide: 'right', toSide: 'left', route: 'straight' },
      { id: 'second', from: 'c', to: 'd', fromSide: 'top', toSide: 'top', via: [[150, 80], [390, 80], [390, 180], [510, 180]] },
    ],
  };
}

test('architecture: showcase rejects an unrelated shared route corridor', () => {
  const { code, stderr } = render('architecture', ambiguousCorridorDocument('showcase'));
  assert.notEqual(code, 0, `expected non-zero exit; stderr:\n${stderr}`);
  assert.match(stderr, /\[composition\/ambiguous-corridor\] showcase architecture/);
  assert.match(stderr, /connections\[0\] id "first" "a" -> "b" shares a 240px corridor with connections\[1\] id "second" "c" -> "d"/);
  assert.match(stderr, /\[150, 80\] -> \[390, 80\]/);
  assert.match(stderr, /do not visually merge/);
});

test('architecture: standard keeps an ambiguous corridor renderable for repair', () => {
  const { code, stderr } = render('architecture', ambiguousCorridorDocument('standard'));
  assert.equal(code, 0, stderr);
});

test('architecture: route rhythm warns in standard and blocks a showcase micro segment', () => {
  const base = {
    schema_version: 1,
    diagram_type: 'architecture',
    meta: { title: 'Readable turn rhythm' },
    components: [
      { id: 'a', type: 'frontend', label: 'A', pos: [60, 80], size: [60, 40] },
      { id: 'b', type: 'backend', label: 'B', pos: [360, 80], size: [60, 40] },
    ],
    connections: [
      { id: 'tight', from: 'a', to: 'b', fromSide: 'right', toSide: 'bottom', via: [[125, 100], [125, 160], [390, 160]] },
    ],
  };
  const standard = structuredClone(base);
  standard.meta.quality_profile = 'standard';
  assert.equal(render('architecture', standard).code, 0);

  const showcase = structuredClone(base);
  showcase.meta.quality_profile = 'showcase';
  const { code, stderr } = render('architecture', showcase);
  assert.notEqual(code, 0);
  assert.match(stderr, /\[composition\/micro-segment\] showcase architecture connections\[0\] id "tight"/);
  assert.match(stderr, /5px source-stub segment 0/);
  assert.match(stderr, /wider corridor|move the component/);
});

test('architecture: container border run is blocking in standard and showcase', () => {
  for (const profile of ['standard', 'showcase']) {
    const d = load('architecture');
    d.meta.quality_profile = profile;
    d.connections.find((connection) => connection.id === 'jwt-verification').via = [[620, 142], [620, 270], [735, 270]];
    const { code, stderr } = render('architecture', d);
    assert.notEqual(code, 0, `expected ${profile} to reject a border run`);
    assert.match(stderr, /\[composition\/container-border-run\] architecture connections\[1\] id "jwt-verification"/);
    assert.match(stderr, /security-group "sg-api :443\/:8000" top border/);
  }
});

process.on('exit', () => fs.rmSync(tmp, { recursive: true, force: true }));
