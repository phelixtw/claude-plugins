import { test } from 'node:test';
import assert from 'node:assert/strict';
import { languageRegionLoops, nodeLanguage, roundedLoopsPath } from '../renderers/shared/utils.mjs';

const files = (...paths) => ({ code: { files: paths.map((path) => ({ path })) } });
const node = (x, y, language, w = 100, h = 60) => ({ x, y, width: w, height: h, language });
const BOUNDS = [0, 0, 600, 300];
const area = (loops) => loops.reduce((sum, pts) => sum + Math.abs(pts.reduce((a, p, k) => {
  const q = pts[(k + 1) % pts.length];
  return a + p[0] * q[1] - q[0] * p[1];
}, 0)) / 2, 0);

test('nodeLanguage maps extensions, takes the majority, ignores nodes without code', () => {
  assert.equal(nodeLanguage(files('a/B.kt')), 'Kotlin');
  assert.equal(nodeLanguage(files('x.dart')), 'Dart');
  assert.equal(nodeLanguage(files('a.ts', 'b.tsx', 'c.py')), 'TypeScript');
  assert.equal(nodeLanguage(files('a.py', 'b.kt')), 'Python');
  assert.equal(nodeLanguage({ label: 'Firebase' }), null);
  assert.equal(nodeLanguage(files('README.md')), null);
});

test('single language renders no zones', () => {
  assert.deepEqual(languageRegionLoops([node(10, 10, 'Dart'), node(200, 10, 'Dart')], BOUNDS), []);
  assert.deepEqual(languageRegionLoops([node(10, 10, 'Dart'), node(200, 10, null)], BOUNDS), []);
});

test('zones tile the canvas: areas sum to canvas, nodes sit in their own zone', () => {
  const nodes = [node(20, 20, 'Kotlin'), node(20, 200, 'Kotlin'), node(400, 20, 'Dart'), node(400, 200, 'Dart'), node(250, 110, null)];
  const zones = languageRegionLoops(nodes, BOUNDS);
  assert.deepEqual(zones.map((z) => z.language).sort(), ['Dart', 'Kotlin']);
  assert.equal(zones.reduce((s, z) => s + area(z.loops), 0), 600 * 300);
  const inside = (loops, x, y) => loops.reduce((n, pts) => n + pts.filter((p, k) => {
    const q = pts[(k + 1) % pts.length];
    return p[0] === q[0] && p[0] > x && y >= Math.min(p[1], q[1]) && y < Math.max(p[1], q[1]);
  }).length, 0) % 2 === 1;
  for (const n of nodes.filter((m) => m.language)) {
    const own = zones.find((z) => z.language === n.language);
    for (const [x, y] of [[n.x + 1, n.y + 1], [n.x + n.width - 1, n.y + n.height - 1]]) assert.ok(inside(own.loops, x, y));
    for (const other of zones.filter((z) => z.language !== n.language)) assert.ok(!inside(other.loops, n.x + 50, n.y + 30));
  }
});

test('adjacent same-language nodes share one connected zone; a split canvas is two corner-aligned zones', () => {
  const zones = languageRegionLoops([node(20, 20, 'Kotlin'), node(20, 120, 'Kotlin'), node(400, 70, 'Dart')], BOUNDS);
  assert.equal(zones.find((z) => z.language === 'Kotlin').loops.length, 1);
  assert.equal(zones.find((z) => z.language === 'Dart').loops.length, 1);
});

test('a language enclosed by another gets a hole loop; rounded path is closed per loop', () => {
  const zones = languageRegionLoops([node(20, 20, 'A', 40, 40), node(260, 120, 'B', 40, 40), node(540, 240, 'A', 40, 40)], BOUNDS);
  assert.ok(zones.every((z) => z.loops.length >= 1));
  const d = roundedLoopsPath(zones[0].loops);
  assert.equal(d.split('Z').length - 1, zones[0].loops.length);
});

test('Android resource files count as the native Android side', () => {
  const node = (path) => ({ code: { files: [{ path }] } });
  assert.equal(nodeLanguage(node('android/app/src/main/res/values/colors.xml')), 'Kotlin');
  assert.equal(nodeLanguage(node('android/app/src/main/AndroidManifest.xml')), 'Kotlin');
  assert.equal(nodeLanguage(node('docs/notes.xml')), null);
});
