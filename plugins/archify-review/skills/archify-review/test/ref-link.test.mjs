import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const start = template.indexOf('/* ref-link pure */');
const end = template.indexOf('/* end ref-link pure */');
assert.ok(start > 0 && end > start, 'ref-link source not found');
const { refLines, refUrl, parseRef, parseLines } = new Function(`${template.slice(start, end)}; return { refLines, refUrl, parseRef, parseLines };`)();

test('lines: deleted rows use old numbers with -, added and context rows use new numbers with +', () => {
  const rows = [{ o: 342, n: 0 }, { o: 343, n: 0 }, { o: 0, n: 312 }, { o: 0, n: 313 }];
  assert.equal(refLines(rows), '-342..-343,+312..+313');
  assert.equal(refLines([{ o: 0, n: 7 }]), '+7');
  assert.equal(refLines([{ o: 9, n: 0 }]), '-9');
  assert.equal(refLines([{ o: 0, n: 0 }]), '');
});

test('url keeps slashes and plus signs readable and omits empty fields', () => {
  const url = refUrl('file:///private/tmp/a.html', { ref: 'hop', tab: 'path-a-b', edge: 'overlay-to-host', file: 'android/app/F oo.kt', lines: '-1..-2,+3', commit: '' });
  assert.equal(url, 'file:///private/tmp/a.html#ref=hop&tab=path-a-b&edge=overlay-to-host&file=android/app/F%20oo.kt&lines=-1..-2,+3');
});

test('parse round-trips a url hash, including plus signs in lines', () => {
  const url = refUrl('file:///x.html', { ref: 'node', node: 'host', file: 'a/b.kt', lines: '-5..-7,+1', commit: 'abc' });
  const ref = parseRef(url.slice(url.indexOf('#')));
  assert.deepEqual(ref, { ref: 'node', node: 'host', file: 'a/b.kt', lines: '-5..-7,+1', commit: 'abc' });
  assert.deepEqual(parseLines(ref.lines), [{ sign: '-', lo: 5, hi: 7 }, { sign: '+', lo: 1, hi: 1 }]);
  assert.deepEqual(parseLines('+1 2'), []);
  assert.equal(parseRef('#focus=x'), null);
});
