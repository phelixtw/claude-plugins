import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const source = template.split('/* ARCHIFY:PATH_GRAPH:BEGIN */')[1].split('/* ARCHIFY:PATH_GRAPH:END */')[0];
const PathGraph = new Function(`${source}; return PathGraph;`)();

const box = (id) => ({ id, w: 290, h: 68 });
const fanIn = {
  nodes: ['a', 'b', 'c', 'd', 'e', 'f', 'hub', 'out'].map(box),
  edges: [...'abcdef'].map((from) => ({ from, to: 'hub' })).concat([{ from: 'hub', to: 'out' }]),
};
const chain = { nodes: ['a', 'b', 'c', 'd', 'e'].map(box), edges: ['ab', 'bc', 'cd', 'de'].map((p) => ({ from: p[0], to: p[1] })) };

for (const [name, graph] of Object.entries({ 'fan-in 6 to 1 to 1': fanIn, chain })) {
  for (const mode of ['columns', 'stack']) {
    test(`${name} has a clean ${mode} layout`, () => {
      const layout = PathGraph.layout(graph.nodes, graph.edges, mode);
      assert.deepEqual(PathGraph.check(layout), []);
      assert.equal(layout.edges.length, graph.edges.length);
    });
  }
  for (const width of [420, 560, 800, 1600]) {
    test(`${name} fits ${width}px without shrinking text below the floor`, () => {
      const chosen = PathGraph.plan(graph.nodes, graph.edges, width);
      assert.deepEqual(PathGraph.check(chosen.layout, chosen.scale), []);
      assert.ok(chosen.layout.width * chosen.scale <= width + 0.5);
    });
  }
}

test('check reports overlapping nodes, edges through nodes and small text', () => {
  const layout = PathGraph.layout(chain.nodes, chain.edges, 'stack');
  layout.nodes.b.y = layout.nodes.a.y;
  assert.ok(PathGraph.check(layout).some((issue) => issue.startsWith('node-overlap')));
  assert.ok(PathGraph.check(PathGraph.layout(chain.nodes, chain.edges, 'stack'), 0.5).includes('text-below-minimum'));
});

test('normalizeSize caps a stretched node to its text and keeps normal nodes', () => {
  assert.deepEqual(PathGraph.normalizeSize(200, 540, 27), { w: 200, h: 56 });
  assert.deepEqual(PathGraph.normalizeSize(400, 70, 40), { w: 340, h: 68 });
  assert.deepEqual(PathGraph.normalizeSize(200, 70, 12), { w: 200, h: 56 });
  // a long file name keeps the width it needs (up to the authored box), never the 340 cap
  assert.deepEqual(PathGraph.normalizeSize(460, 70, 40, 400), { w: 428, h: 68 });
  assert.deepEqual(PathGraph.normalizeSize(460, 70, 40, 500), { w: 460, h: 68 });
});
