import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { checkSpec, loadHunks, summarize } from '../renderers/shared/review-check.mjs';

const H1 = '@@ -10,3 +10,3 @@ class A {\n keep\n-old\n+new\n tail';
const H2 = '@@ -0,0 +1,4 @@\n+a\n+b\n+c\n+d';
const hunk = (file, text) => ({ file, header: text.split('\n')[0], text });
const commits = () => ({
  changes: {
    commits: [{ sha: 'abc', subject: 's' }],
    files: [
      { path: 'src/A.kt', status: 'modified', additions: 1, deletions: 1 },
      { path: 'src/B.kt', status: 'added', additions: 4, deletions: 0 },
      { path: 'logo.png', status: 'deleted' },
    ],
  },
  hunks: [hunk('src/A.kt', H1), hunk('src/B.kt', H2)],
});
function hunkTexts(patch) {
  const hunks = [];
  let cur = null;
  for (const line of patch.split('\n')) {
    if (line.startsWith('diff --git ')) cur = null;
    else if (line.startsWith('@@ ')) hunks.push(cur = [line]);
    else if (cur && /^[ +\\-]/.test(line)) cur.push(line);
  }
  return hunks.map((h) => h.join('\n'));
}

// One changed function `f` on `lines`, called by a new test that reads as Given / When / Then.
const tested = (lines) => ({
  functions: [{ symbol: 'f', lines, status: 'new', by: ['ATest.f'], why: 'w' }],
  cases: [{ test: 'ATest.f', covers: ['f'], given: 'g', when: 'w', then: 't' }],
});
const conn = ({ id = 'c1', to = 'y', ...over } = {}) => ({
  id, from: 'x', to,
  code: { from_symbol: 'a', to_symbol: 'b', path: 'src/A.kt', line: 11, note: 'One sentence.', story: 'Two files talk.', diff: H1, tests: tested('+11,-11'), ...over },
});
const NODES = [{ id: 'a', label: 'A.kt', code: { layers: { mvvm: 'view' } } }, { id: 'b', label: 'B.kt', code: { layers: { mvvm: 'model' } } }];
const RISK = { score: 0.5, callers: [], untested: [], tool: 'code-review-graph 2.4.0' };
const specOf = (connections, extra = {}) => ({
  connections: [connections].flat(),
  meta: { platform: 'app' },
  cards: [{ title: '根因', items: ['why'] }],
  changes: { files: [
    { path: 'src/A.kt', status: 'modified', additions: 1, deletions: 1 },
    { path: 'src/B.kt', status: 'added', additions: 4, deletions: 0 },
    { path: 'logo.png', status: 'deleted' },
  ] },
  components: NODES,
  ...extra,
});
const full = () => [conn(), conn({ id: 'c2', to: 'z', path: 'src/B.kt', line: 1, diff: H2, tests: tested('+1..+4') })];
const rules = (spec) => checkSpec(spec, commits()).map((f) => `${f.rule}:${f.subject}`);

test('clean spec has no findings', () => {
  assert.deepEqual(checkSpec(specOf(full()), commits()), []);
});

test('R1 flags missing diff, path, line', () => {
  const f = checkSpec(specOf(full().concat(conn({ id: 'c3', to: 'w', diff: '', path: undefined, line: undefined }))), commits());
  assert.match(f.find((x) => x.rule === 'R1').detail, /: diff, path, line$/);
});

test('R1 needs no line for a deleted file, which has no new-file line', () => {
  const H3 = '@@ -1,2 +0,0 @@\n-x\n-y';
  const del = conn({ id: 'c3', to: 'w', path: 'src/C.kt', line: undefined, diff: H3, tests: { functions: [{ symbol: 'gone', lines: '-1..-2', status: 'removed', by: [], why: 'w' }] } });
  const data = commits();
  data.changes.files.push({ path: 'src/C.kt', status: 'deleted', additions: 0, deletions: 2 });
  data.hunks.push(hunk('src/C.kt', H3));
  const spec = specOf(full().concat(del));
  spec.changes.files.push({ path: 'src/C.kt', status: 'deleted', additions: 0, deletions: 2 });
  spec.components = NODES.concat({ id: 'c', label: 'C.kt', code: { layers: { mvvm: 'model' } } });
  assert.deepEqual(checkSpec(spec, data).filter((f) => ['R1', 'R6', 'R24'].includes(f.rule)), []);
});

test('R2 flags trimmed hunks, accepts trailing newline and new-file fragments', () => {
  assert.ok(rules(specOf(full().map((c, i) => (i ? c : conn({ diff: H1.replace('\n keep', '') }))))).includes('R2:c1'));
  assert.deepEqual(checkSpec(specOf(full().map((c, i) => (i ? c : conn({ diff: `${H1}\n` })))), commits()), []);
  const frag = '@@ -0,0 +3,2 @@\n+c\n+d';
  const lead = conn({ id: 'c0', to: 'q', path: 'src/B.kt', line: 1, diff: '@@ -0,0 +1,2 @@\n+a\n+b', tests: tested('+1') });
  const tail = conn({ id: 'c2', to: 'z', path: 'src/B.kt', line: 3, diff: frag, tests: tested('+3') });
  assert.deepEqual(checkSpec(specOf([conn(), lead, tail]), commits()), []);
  const bad = { ...tail, code: { ...tail.code, diff: '@@ -0,0 +2,2 @@\n+c\n+d' } };
  assert.ok(rules(specOf([conn(), lead, bad])).includes('R2:c2'));
});

test('R3 flags a hunk under the wrong path', () => {
  assert.ok(rules(specOf(full().map((c, i) => (i ? c : conn({ path: 'src/B.kt' }))))).includes('R3:c1'));
});

test('R4 flags uncovered hunks and unlisted binaries', () => {
  assert.ok(rules(specOf([conn()])).includes('R4:src/B.kt'));
  const spec = specOf(full());
  spec.changes.files.pop();
  assert.ok(rules(spec).includes('R4:logo.png'));
});

test('R5 flags function lines absent from the diff, with the wrong sign or unparsable', () => {
  const r = (lines) => rules(specOf(full().map((c, i) => (i ? c : conn({ tests: tested(lines) })))));
  assert.ok(!r('+11,-11').includes('R5:c1'));
  assert.ok(r('+99').includes('R5:c1'));
  assert.ok(r('-13').includes('R5:c1'));
  assert.ok(r('+13').includes('R5:c1'));
  assert.ok(r('11').includes('R5:c1'));
  assert.ok(rules(specOf([conn(), conn({ id: 'c2', to: 'z', path: 'src/B.kt', line: 1, diff: H2, tests: tested('-1') })])).includes('R5:c2'));
});

test('R6 flags a line outside the diff new-file lines', () => {
  assert.ok(rules(specOf(full().map((c, i) => (i ? c : conn({ line: 50 }))))).includes('R6:c1'));
});

test('R7 flags numbers, status and missing files', () => {
  const spec = specOf(full());
  spec.changes.files[0].additions = 9;
  spec.changes.files[1].status = 'modified';
  spec.changes.files.splice(2, 1);
  const d = checkSpec(spec, commits()).filter((f) => f.rule === 'R7').map((f) => f.detail).join('|');
  assert.match(d, /src\/A.kt.*\+9/);
  assert.match(d, /src\/B.kt.*status/);
  assert.match(d, /logo.png is missing/);
});

test('R8 and R9 warn without failing', () => {
  const spec = specOf(full().map((c, i) => (i ? { ...c, to: 'y' } : conn({ note: 'One. Two.' }))));
  const f = checkSpec(spec, commits());
  assert.deepEqual(summarize(f), { errors: 0, warnings: 2, byRule: { R8: 1, R9: 1 } });
});

test('R24 wants every changed function with a status, its tests as cases, and a suggested case when untested', () => {
  const r24 = (tests) => checkSpec(specOf(full().map((c, i) => (i ? c : conn({ tests })))), commits()).filter((f) => f.rule === 'R24').map((f) => f.detail);
  const fn = (over) => ({ symbol: 'f', lines: '+11', status: 'new', by: ['ATest.f'], why: '舊存檔沒有 `edited` 欄位時，解碼要給空字典，否則整個購物車讀不出來', ...over });
  const gwt = { given: '購物車有一件商品', when: '呼叫 `f`', then: '存成今天的結帳時間' };
  const cases = [{ test: 'ATest.f', covers: ['f'], ...gwt }];
  const suggested = [{ covers: 'f', ...gwt }];
  assert.deepEqual(r24({ functions: [fn()], cases }), []);
  assert.deepEqual(r24(undefined), ['code.tests.functions is missing or empty']);
  assert.deepEqual(r24({ functions: [] }), ['code.tests.functions is missing or empty']);
  assert.deepEqual(r24({ functions: [fn({ status: 'tested' })], cases }), ['function f has status "tested"']);
  assert.deepEqual(r24({ functions: [fn({ by: [] })] }), ['function f is new but by names no test']);
  assert.deepEqual(r24({ functions: [fn({ status: 'existing' })] }), ['test ATest.f has no case']);
  assert.deepEqual(r24({ functions: [fn({ status: 'none' })], suggested }), ['function f is none but by names ATest.f']);
  assert.deepEqual(r24({ functions: [fn({ status: 'none', by: [] })] }), ['untested function f has no suggested case']);
  assert.deepEqual(r24({ functions: [fn({ status: 'none', by: [] })], suggested }), []);
  assert.deepEqual(r24({ functions: [fn({ status: 'removed', lines: '-11', by: ['ATest.old'] })] }), [], 'old tests of a removed function need no case');
  // via marks coverage through another symbol, so only a tested function carries it
  assert.deepEqual(r24({ functions: [fn({ via: 'CartBoard.items' })], cases }), []);
  assert.deepEqual(r24({ functions: [fn({ status: 'none', by: [], via: 'g' })], suggested }), ['function f has via but status none']);
  // given, when and then: one short sentence each, where a code span counts at most 20 characters
  const one = (over) => r24({ functions: [fn()], cases: [{ ...cases[0], ...over }] });
  assert.deepEqual(one({ when: '' }), ['case ATest.f has no when']);
  assert.deepEqual(one({ then: '字'.repeat(61) }), ['case ATest.f then is 61 characters (at most 60)']);
  assert.deepEqual(one({ then: '字'.repeat(60) }), []);
  assert.deepEqual(one({ given: `呼叫 \`${'x'.repeat(200)}\`` }), []);
  // a case reads as a scenario, so more than two code spans across given, when and then is a warning
  assert.deepEqual(checkSpec(specOf(full().map((c, i) => (i ? c : conn({ tests: { functions: [fn()], cases: [{ ...cases[0], given: '`a` `b` `c`' }] } })))), commits()).filter((f) => f.rule === 'R24').map((f) => f.severity), ['warning']);
  assert.deepEqual(one({ given: '訊息有三項，`addedItems` 為 `[0, 2]`', then: '只得到 `m1#0`' }), ['case ATest.f has 4 code spans (at most 2)']);
  assert.deepEqual(one({ then: '先存成 10/28 並記 `editedAt`' }), []);
  assert.deepEqual(r24({ functions: [fn({ status: 'none', by: [] })], suggested: [{ ...suggested[0], given: ' ' }] }), ['suggested case for f has no given']);
  // why: what would break if this code were wrong, at most 80 visible characters, on every function
  assert.deepEqual(r24({ functions: [fn({ why: undefined })], cases }), ['function f has no why']);
  assert.deepEqual(r24({ functions: [fn({ status: 'removed', by: [], why: ' ' })] }), ['function f has no why']);
  assert.deepEqual(r24({ functions: [fn({ why: '字'.repeat(81) })], cases }), ['function f why is 81 characters (at most 80)']);
  assert.deepEqual(r24({ functions: [fn({ why: '字'.repeat(80) })], cases }), []);
  assert.deepEqual(r24({ functions: [fn({ why: `${'字'.repeat(60)}\`${'x'.repeat(40)}\`` })], cases }), [], 'a code span counts at most 20');
});

test('R2 and R3 apply to node code files', () => {
  const node = (path, diff) => ({ id: 'n1', code: { layers: { mvvm: 'view' }, files: [{ path, summary: 's', diff }], risk: RISK } });
  assert.ok(rules(specOf(full(), { components: [node('src/A.kt', H1.replace('-old\n', ''))] })).includes('R2:n1'));
  assert.ok(rules(specOf(full(), { components: [node('src/B.kt', H1)] })).includes('R3:n1'));
  assert.deepEqual(checkSpec(specOf(full(), { components: [...NODES, node('src/A.kt', H1)] }), commits()), []);
});

test('R10 flags function lines matching several hunks; hunk disambiguates and R5 respects it', () => {
  const HB = '@@ -10,2 +10,2 @@\n keep\n-old2\n+new2';
  const cs = { changes: { commits: [{ sha: 'abc' }], files: [] }, hunks: [hunk('src/A.kt', H1), hunk('src/A.kt', HB)] };
  const run = (fn) => checkSpec({ connections: [conn({ diff: `${H1}\n${HB}`, tests: { ...tested(), functions: [{ ...tested().functions[0], ...fn }] } })] }, cs).map((f) => `${f.rule}:${f.subject}`);
  assert.ok(run({ lines: '+11' }).includes('R10:c1'));
  assert.ok(!run({ lines: '+11', hunk: 1 }).includes('R10:c1'));
  assert.ok(!run({ lines: '+11', hunk: [2] }).includes('R5:c1'));
  assert.ok(run({ lines: '+12', hunk: 2 }).includes('R5:c1'));
});

test('R23 wants the code-review-graph risk on every node with changed files', () => {
  const node = (id, code) => ({ id, label: 'A.kt', code: { layers: { mvvm: 'view' }, ...code } });
  const files = [{ path: 'src/A.kt', summary: 's', diff: H1 }];
  const r23 = (components) => checkSpec(specOf(full(), { components: [...NODES, ...components] }), commits()).filter((f) => f.rule === 'R23');
  const [missing] = r23([node('n1', { files })]);
  assert.equal(missing.subject, 'n1');
  assert.equal(missing.severity, 'error');
  assert.match(missing.fix, /bin\/review-kit risk .* --risk risk\.json/);
  assert.deepEqual(r23([node('n1', { files, risk: RISK }), node('n2', { brief: { role: 'r', task: 't' } })]), []);
});

test('node code without files raises no finding', () => {
  const spec = specOf(full(), { components: [...NODES, { id: 'x', code: { layers: { mvvm: 'model' }, brief: { role: 'r', task: 't' } } }] });
  assert.deepEqual(checkSpec(spec, commits()), []);
});

test('R11 requires a root-cause card (根因 or Root cause)', () => {
  const noCard = { ...specOf(full()), cards: [{ title: '背景', items: ['x'] }] };
  assert.deepEqual(checkSpec(noCard, commits()).filter((f) => f.rule === 'R11').map((f) => f.severity), ['error']);
  assert.equal(checkSpec({ ...noCard, cards: undefined }, commits()).filter((f) => f.rule === 'R11').length, 1);
  assert.equal(checkSpec({ ...noCard, cards: [{ title: 'Root Cause' }] }, commits()).filter((f) => f.rule === 'R11').length, 0);
  assert.equal(checkSpec({ ...noCard, cards: [{ title: '這張 Story 的目的' }] }, commits()).filter((f) => f.rule === 'R11').length, 0);
  assert.equal(checkSpec(specOf(full()), commits()).filter((f) => f.rule === 'R11').length, 0);
});

test('R13 rejects comment-only connections, not on mixed ones', () => {
  const only = '@@ -1,2 +1,2 @@\n keep\n-// old\n+// new';
  const mixed = '@@ -1,2 +1,2 @@\n keep\n-// old\n+val x = 1';
  const w = (diff) => checkSpec(specOf(full().map((c, i) => (i ? c : conn({ diff })))), commits()).filter((f) => f.rule === 'R13');
  assert.deepEqual(w(only).map((f) => f.severity), ['error']);
  assert.match(w(only)[0].detail, /only comment changes/);
  assert.equal(w(mixed).length, 0);
  assert.equal(w('@@ -1,3 +1,3 @@\n keep\n-<!-- a -->\n+<!-- b -->\n tail').length, 1);
});

test('counterpart connections skip R1/R13, need story or note, and leave R4 to the node diff', () => {
  const only = '@@ -1,2 +1,2 @@\n keep\n-// old\n+// new';
  const cp = (code) => ({ id: 'cp', from: 'x', to: 'q', kind: 'counterpart', code: { from_symbol: 'a', to_symbol: 'b', ...code } });
  const run = (c) => checkSpec(specOf(full().concat(c)), commits()).filter((f) => f.subject === 'cp');
  assert.deepEqual(run(cp({ story: 'Same component, two platforms.' })), []);
  assert.deepEqual(run(cp({ note: 'Same component.', diff: only })), []);
  assert.deepEqual(run(cp({})).map((f) => f.rule), ['R14']);
  assert.match(run(conn({ id: 'cp', diff: only })).find((f) => f.rule === 'R13').fix, /kind: counterpart/);
});

test('R14 requires code.action or code.story on every connection', () => {
  const r15 = (extra) => checkSpec(specOf(full().map((c, i) => (i ? c : conn(extra)))), commits()).filter((f) => f.rule === 'R14');
  assert.equal(r15({ story: '' }).length, 1);
  assert.equal(r15({ story: '  ', action: '' }).length, 1);
  assert.equal(r15({ story: 'Toolbar wakes up.' }).length, 0);
  assert.equal(r15({ story: '', action: 'Press Home' }).length, 0);
});

// A file changed by several selected commits: c1 and c2 both rewrite line 5; `outside` (not selected) may sit between them.
function twoCommitRepo({ outside = false } = {}) {
  const repo = fs.mkdtempSync(path.join(os.tmpdir(), 'net-'));
  const git = (...a) => execFileSync('git', ['-C', repo, '-c', 'user.name=t', '-c', 'user.email=t@t', ...a], { encoding: 'utf8' });
  const lines = Array.from({ length: 12 }, (_, i) => `line${i + 1}`);
  const write = (edit) => { edit(lines); fs.writeFileSync(path.join(repo, 'A.kt'), lines.join('\n') + '\n'); };
  git('init', '-q');
  write(() => {});
  git('add', '.'); git('commit', '-qm', 'base');
  write((l) => { l[4] = 'mid'; });
  git('commit', '-qam', 'c1');
  const c1 = git('rev-parse', 'HEAD').trim();
  if (outside) { write((l) => { l[9] = 'other'; }); git('commit', '-qam', 'outside'); }
  write((l) => { l[4] = 'new'; l[5] = 'SIX'; });
  git('commit', '-qam', 'c2');
  const c2 = git('rev-parse', 'HEAD').trim();
  return { repo, git, c1, c2 };
}

test('a file changed by several selected commits is checked against its net diff, and stacked per-commit diffs are rejected', () => {
  const { repo, git, c1, c2 } = twoCommitRepo();
  try {
    const loaded = loadHunks(repo, { commits: [{ sha: c1 }, { sha: c2 }] });
    const net = hunkTexts(git('diff', '--no-renames', '-U2', `${c1}^`, c2, '--', 'A.kt'));
    assert.deepEqual(loaded.hunks.filter((h) => h.file === 'A.kt').map((h) => h.text), net);
    assert.deepEqual(loaded.changes.files.filter((f) => f.path === 'A.kt'), [{ path: 'A.kt', status: 'modified', additions: 2, deletions: 2 }]);
    const spec = (diff) => ({
      ...specOf(conn({ path: 'A.kt', line: 5, diff, tests: tested('+5') })),
      changes: { files: [{ path: 'A.kt', status: 'modified', additions: 2, deletions: 2 }] },
      components: [{ id: 'x', label: 'A.kt', code: { layers: { mvvm: 'view' } } }, { id: 'y', label: 'B.kt', code: { layers: { mvvm: 'model' } } }],
    });
    const errors = (diff) => checkSpec(spec(diff), loaded).filter((f) => ['R2', 'R4', 'R7'].includes(f.rule));
    assert.deepEqual(errors(net.join('\n')), []);
    const stacked = [c1, c2].flatMap((sha) => hunkTexts(git('show', '--format=', '--no-renames', '-U2', sha))).join('\n');
    assert.ok(errors(stacked).some((f) => f.rule === 'R2'), 'per-commit hunks of a net-diffed file are not git hunks any more');
  } finally { fs.rmSync(repo, { recursive: true, force: true }); }
});

test('a commit outside the selection that changed the file in between still gives one net diff, like review-kit', () => {
  const { repo, git, c1, c2 } = twoCommitRepo({ outside: true });
  try {
    const loaded = loadHunks(repo, { commits: [{ sha: c1 }, { sha: c2 }] });
    const net = hunkTexts(git('diff', '--no-renames', '-U2', `${c1}^`, c2, '--', 'A.kt'));
    assert.deepEqual(loaded.hunks.map((h) => h.text), net);
    assert.ok(net.some((h) => h.split('\n').includes('+other')), 'the outside commit\'s line is part of the final change');
  } finally { fs.rmSync(repo, { recursive: true, force: true }); }
});

test('R15 flags a sparse code-review layout, R16 warns on low fill; a packed one passes', () => {
  const node = (id, x, y) => ({ id, label: 'strings.xml', pos: [x, y], size: [250, 64] });
  const rules = (components) => checkSpec({ ...specOf(full()), components }, commits()).filter((f) => ['R15', 'R16'].includes(f.rule)).map((f) => f.rule);
  const packed = [0, 1, 2].flatMap((c) => [0, 1].map((r) => node(`n${c}${r}`, 40 + c * 274, 40 + r * 114)));
  assert.deepEqual(rules(packed), []);
  assert.deepEqual(rules([node('a', 40, 40), node('b', 2000, 1200)]), ['R15', 'R16']);
});

test('R16 prints the fill with a decimal so 29.6% never reads as 30%', () => {
  const spec = { ...specOf(full()), meta: { viewBox: [1000, 100] }, components: [{ id: 'a', label: 'a.dart', pos: [0, 0], size: [296, 100] }] };
  const [f] = checkSpec(spec, commits()).filter((x) => x.rule === 'R16');
  assert.match(f.detail, /cover 29\.6% of the canvas \(want at least 30%\)/);
});

test('R19 flags an edge label wider than the gap it sits in, and R16 advice names the gap the labels need', () => {
  const nodes = (gap) => [{ id: 'a', label: 'a.dart', pos: [40, 40], size: [310, 90] }, { id: 'b', label: 'b.dart', pos: [350 + gap, 40], size: [310, 90] }];
  const run = (gap, label = 'child: scaledWebView') => checkSpec({ ...specOf(full()), components: nodes(gap), connections: [{ id: 'ab', from: 'a', to: 'b', label }] }, commits());
  const narrow = run(90).filter((x) => x.rule === 'R19');
  assert.equal(narrow.length, 1);
  assert.equal(narrow[0].severity, 'error');
  assert.match(narrow[0].detail, /"child: scaledWebView" \(~142px\) is wider than the 90px gap between a and b/);
  assert.match(narrow[0].fix, /at least 150px/);
  assert.equal(run(210).filter((x) => x.rule === 'R19').length, 0);
  assert.equal(run(90, '').filter((x) => x.rule === 'R19').length, 0);
  assert.match(run(300).find((x) => x.rule === 'R16').fix, /keep at least 150px between connected nodes/);
});

test('R20 wants the "no findings" TODO item in the report language', () => {
  const run = (locale, none) => checkSpec({ ...specOf(full()), meta: { ...specOf(full()).meta, locale }, cards: [{ title: '根因', items: ['why'] }, { title: '待確認問題 / TODO', items: [`[low][security-review] ${none}`] }] }, commits()).filter((f) => f.rule === 'R20');
  assert.equal(run('zh-TW', '審查的 hunk 沒有發現問題').length, 0);
  assert.equal(run('en', 'no findings in the reviewed hunks').length, 0);
  assert.equal(run('zh-CN', '审查的 hunk 没有发现问题').length, 0);
  const wrong = run('zh-TW', 'no findings in the reviewed hunks');
  assert.equal(wrong.length, 1);
  assert.equal(wrong[0].severity, 'warning');
  assert.match(wrong[0].fix, /審查的 hunk 沒有發現問題/);
  assert.equal(run('en', '審查的 hunk 沒有發現問題').length, 1);
});

test('R17 requires low/medium/high on code-review and security-review TODO items, most severe first', () => {
  const r18 = (items) => checkSpec({ ...specOf(full()), cards: [{ title: '根因', items: ['why'] }, { title: '待確認問題 / TODO', items }] }, commits()).filter((f) => f.rule === 'R17');
  assert.equal(r18(['[high][security-review] a', '[medium][code-review] b', '[low][code-review] c', '[low] d', '[skipped] security-review: dirty']).length, 0);
  assert.match(r18(['[code-review] no severity'])[0].detail, /no severity/);
  assert.match(r18(['[critical][code-review] too high'])[0].detail, /unknown severity \[critical\]/);
  assert.match(r18(['[low][code-review] a', '[high][code-review] b'])[0].detail, /not sorted/);
  assert.equal(r18(['[LOW][code-review] ok']).length, 0);
  assert.equal(r18(['[high][new][security-review] a', '[low][new][code-review] b']).length, 0);
  assert.match(r18(['[new][code-review] no severity'])[0].detail, /unknown severity \[new\]/);
  assert.match(r18(['[low][new][code-review] a', '[high][new][code-review] b'])[0].detail, /not sorted/);
});

test('R17 treats code-smell TODO items like the other reviews: a risk level first, most severe first', () => {
  const r17 = (items) => checkSpec({ ...specOf(full()), cards: [{ title: '根因', items: ['why'] }, { title: '待確認問題 / TODO', items }] }, commits()).filter((f) => f.rule === 'R17');
  assert.equal(r17(['[high][code-smell] yagni: ApiClient interface with one implementation. Inline it. (api.dart:4)', '[medium][code-review] b', '[low][code-smell] a.dart has Long Function, use Extract Function', '[low][new][code-smell] c']).length, 0);
  assert.match(r17(['[code-smell] no risk level'])[0].detail, /no severity/);
  assert.match(r17(['[low][code-review] a', '[high][code-smell] b'])[0].detail, /not sorted/);
});

test('R22 allows only the Story purpose and TODO cards on a code-review report', () => {
  const r22 = (cards) => checkSpec({ ...specOf(full()), cards }, commits()).filter((f) => f.rule === 'R22');
  assert.deepEqual(r22([{ title: '這張 Story 的目的', items: ['why'] }, { title: '待確認問題 / TODO', items: ['[low][code-review] x (a.kt:1)'] }]), []);
  assert.deepEqual(r22([{ title: 'Story purpose', items: ['why'] }, { title: 'Open questions / TODO', items: [] }]), []);
  const extra = r22([{ title: '這張 Story 的目的', items: ['why'] }, { title: '實機 E2E（測試機）', items: ['account a logged in'] }]);
  assert.equal(extra.length, 1);
  assert.equal(extra[0].severity, 'error');
  assert.match(extra[0].detail, /實機 E2E/);
});

test('R18 requires meta.platform and one layer per scheme of that platform on every component', () => {
  const node = (id, layers) => ({ id, label: 'a.dart', pos: [40, 40], size: [250, 64], code: layers === undefined ? {} : { layers } });
  const r19 = (platform, components, framework) => checkSpec({ ...specOf(full(), { meta: platform ? { platform, ...(framework ? { framework } : {}) } : {} }), components }, commits()).filter((f) => f.rule === 'R18');
  const app = { mvvm: 'view', flutter: 'ui' };
  assert.deepEqual(r19('app', [node('a', app), node('b', { mvvm: 'viewmodel', flutter: 'logic' }), node('c', { mvvm: 'model', flutter: 'data' })], 'flutter'), []);
  // flutter code: both schemes are required, wrong names are rejected
  const missing = r19('app', [node('a'), node('b', { mvvm: 'view', flutter: 'view' }), node('c', { mvvm: 'view' })], 'flutter');
  assert.deepEqual(missing.map((f) => `${f.subject}:${f.severity}`), ['a:error', 'a:error', 'b:error', 'c:error']);
  assert.match(missing[0].detail, /no code\.layers\.mvvm \(missing\)/);
  assert.match(missing[1].detail, /no code\.layers\.flutter \(missing\)/);
  assert.match(missing[2].detail, /layers\.flutter \("view"\)/);
  assert.match(missing[0].fix, /"view", "viewmodel", "model"/);
  // other app code (no meta.framework): MVVM only, flutter layers are not asked for
  assert.deepEqual(r19('app', [node('a', { mvvm: 'view' })]), []);
  assert.equal(r19('app', [node('a', { flutter: 'ui' })]).length, 1);
  // webview: asked for only with meta.webview true, next to mvvm; names are host / bridge / content
  const wv = (components, webview = true, connections = [{ id: 'k', from: 'a', to: 'b' }]) => checkSpec({ ...specOf(full(), { meta: { platform: 'app', webview } }), components, connections: [...full(), ...connections] }, commits()).filter((f) => f.rule === 'R18');
  assert.deepEqual(wv([node('a', { mvvm: 'view', webview: 'host' }), node('b', { mvvm: 'view', webview: 'content' })]), []);
  assert.deepEqual(wv([node('a', { mvvm: 'view' }), node('b', { mvvm: 'view', webview: 'bridge' })]).map((f) => f.detail), ['component has no code.layers.webview (missing)']);
  assert.match(wv([node('a', { mvvm: 'view', webview: 'ui' }), node('b', { mvvm: 'view', webview: 'content' })])[0].fix, /"host", "bridge", "content"/);
  assert.deepEqual(wv([node('a', { mvvm: 'view' }), node('b', { mvvm: 'view' })], false), []);
  // a layers.web key is an error; a node in no diff is not drawn
  assert.deepEqual(wv([node('a', { mvvm: 'view', webview: 'host', web: 'ui' }), node('b', { mvvm: 'view', webview: 'content' })]).map((f) => f.detail), ['component has code.layers.web']);
  // meta.webview follows the diff: a change with only host code (no bridge, no web content) is not a WebView change
  const onlyHost = wv([node('a', { mvvm: 'view', webview: 'host' }), node('b', { mvvm: 'view', webview: 'host' })]);
  assert.deepEqual(onlyHost.map((f) => `${f.subject}:${f.severity}`), ['meta:error']);
  assert.match(onlyHost[0].detail, /no component is bridge or content/);
  assert.match(onlyHost[0].fix, /Remove meta\.webview/);
  assert.deepEqual(wv([node('a', { mvvm: 'view', webview: 'host' }), node('w', { mvvm: 'view', webview: 'content' })]).map((f) => `${f.subject}:${f.detail}`), ['w:WebView component is in no diff (no code.files, no connection)']);
  // platform "web" is rejected with the way to fix it
  const [web] = r19('web', [node('a', app)]);
  assert.match(web.detail, /meta\.platform is "web"/);
  assert.match(web.fix, /"app".*meta\.webview/);
  // platform itself
  for (const platform of [undefined, 'desktop']) {
    const [finding] = r19(platform, [node('a', app)]);
    assert.equal(finding.subject, 'meta');
    assert.match(finding.detail, /meta\.platform is/);
  }
  assert.deepEqual(checkSpec(specOf(full()), commits()), []);
});

test('R21 wants a node for every changed file', () => {
  const spec = specOf(full());
  const extra = { path: 'src/extra.dart', status: 'modified', additions: 0, deletions: 0 };
  const r21 = (s) => checkSpec(s, commits()).filter((f) => f.rule === 'R21');
  assert.equal(r21(spec).length, 0);
  assert.match(r21({ ...spec, changes: { ...spec.changes, files: [...spec.changes.files, extra] } })[0].detail, /extra\.dart has no node/);
});

test('R21 keeps the test files of changes.tests out of the diagram, and R4 and R7 skip them', () => {
  const T = 'test/ATest.kt';
  const data = commits();
  data.changes.files.push({ path: T, status: 'modified', additions: 1, deletions: 0 });
  data.hunks.push(hunk(T, '@@ -1,1 +1,2 @@\n x\n+y'));
  const spec = { ...specOf(full()), changes: { ...specOf(full()).changes, tests: [T] } };
  const found = (s) => checkSpec(s, data).map((f) => `${f.rule}:${f.subject}`);
  assert.deepEqual(found(spec), [], 'no node, no hop and no changes.files entry for the test file');
  const unlisted = { ...spec, changes: { ...spec.changes, tests: undefined } };
  assert.ok(found(unlisted).includes(`R4:${T}`) && found(unlisted).includes('R7:changes'), 'review-check never classifies a path itself');
  const drawn = {
    ...spec,
    components: [...NODES, { id: 't', label: 'ATest.kt', code: { layers: { mvvm: 'view' }, files: [{ path: T, summary: 's' }] } }],
    connections: [...full(), { id: 't1', from: 'a', to: 't', code: { path: T } }],
  };
  assert.deepEqual(checkSpec(drawn, data).filter((f) => f.rule === 'R21').map((f) => `${f.subject}:${f.detail}`), [
    't:component is labeled like the test file test/ATest.kt',
    't:component lists the test file test/ATest.kt',
    't1:connection shows the test file test/ATest.kt',
  ]);
  // a changed file that shares the test file's name keeps its node
  const shared = { ...spec, changes: { ...spec.changes, tests: ['test/A.kt'] } };
  assert.deepEqual(checkSpec(shared, { ...data, changes: { ...data.changes, files: [...data.changes.files, { path: 'test/A.kt', status: 'modified' }] } }).filter((f) => f.rule === 'R21'), []);
});
