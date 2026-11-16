import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { reviewPure } from './helpers/review-pure.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
const { nodeSeen, pathSeen } = reviewPure();
const seen = (...paths) => Object.fromEntries(paths.map((p) => [p, { state: 'seen' }]));

test('node with files is done only when every tracked file is seen', () => {
  assert.equal(nodeSeen(['a', 'b'], [], seen('a', 'b'), {}), true);
  assert.equal(nodeSeen(['a', 'b'], [], { ...seen('a'), b: { state: 'partial' } }, {}), false);
  assert.equal(nodeSeen(['a', 'b'], [], { ...seen('a'), b: { state: 'unseen' } }, {}), false);
});

test('node whose files are all outside the tree is never done', () => {
  assert.equal(nodeSeen(['x'], [flow('f1')], {}, { f1: true }), false);
});

const flow = (id) => ({ id, code: {} });

test('node without code is done when every connected flow is ticked', () => {
  assert.equal(nodeSeen([], [flow('f1'), flow('f2')], {}, { f1: true, f2: true }), true);
  assert.equal(nodeSeen([], [flow('f1'), flow('f2')], {}, { f1: true }), false);
});

test('node without code or flows shows no mark', () => {
  assert.equal(nodeSeen([], [], {}, {}), false);
});

test('path is done when every hop is ticked and has at least one hop', () => {
  assert.equal(pathSeen([flow('f1'), flow('f2')], { f1: true, f2: true }), true);
  assert.equal(pathSeen([flow('f1'), flow('f2')], { f1: true }), false);
  assert.equal(pathSeen([], {}), false);
});

const storeStart = template.indexOf('/* review-store pure */');
const storeEnd = template.indexOf('/* end review-store pure */');
const { archifyTicketOf } = new Function(`${template.slice(storeStart, storeEnd)}; return { archifyTicketOf };`)();

test('ticket key comes from the report title', () => {
  assert.equal(archifyTicketOf('PROJ-123 toolbar restyle'), 'PROJ-123');
  assert.equal(archifyTicketOf('Review of OPS-2408'), 'OPS-2408');
  assert.equal(archifyTicketOf('toolbar restyle'), '');
});

const fixStart = template.indexOf('/* todo-fix pure */');
const fixEnd = template.indexOf('/* end todo-fix pure */');
const { archifyFixPrompt, archifyAskPrompt } = new Function(`${template.slice(fixStart, fixEnd)}; return { archifyFixPrompt, archifyAskPrompt };`)();

test('fix prompt carries finding, location, ticket, commit and the minimal-fix instruction', () => {
  const changes = { commits: [{ sha: 'abc123', subject: 'fix dialog' }] };
  const p = archifyFixPrompt(['Magic number repeated. (dialogs.dart:307, mini-ok step 4)'], 'PROJ-1 Dialog fix', changes);
  assert.match(p, /Ticket: PROJ-1 Dialog fix/);
  assert.match(p, /Commit: abc123 fix dialog/);
  assert.match(p, /Finding: Magic number repeated\.\n/);
  assert.match(p, /Location: dialogs\.dart:307 \(mini-ok step 4\)/);
  assert.match(p, /minimal fix.*test.*do not commit/);
  assert.equal(/Location/.test(archifyFixPrompt(['No location here'], '', null)), false);
  assert.match(archifyFixPrompt(['a (x.dart:1)', 'b (y.dart:2)'], '', null), /2 LOW-severity[\s\S]*1\. Finding: a[\s\S]*2\. Finding: b/);
});

test('viewer tags LOW items distinctly and wires the copy-fix button to the shared copy helper', () => {
  assert.match(template, /\.todo-sev\[data-sev="low"\]/);
  assert.match(template, /\.todo-sev\[data-sev="medium"\]/);
  assert.match(template, /\.todo-sev\[data-sev="high"\]/);
  assert.match(template, /viewer\.todo\.copyFix/);
  assert.match(template, /Archify\.copyThen = copyThen/);
  assert.match(template, /Archify\.copyThen\(prompt\(\)/);
  assert.match(template, /archifyFixPrompt\(items\(\), heading\(\)/);
  assert.match(template, /archifyAskPrompt\(plain, /);
});

test('a code-smell fix prompt asks to cut the over-engineering with behavior unchanged', () => {
  const changes = { commits: [{ sha: 'abc123', subject: 'share' }] };
  const one = archifyFixPrompt(['yagni: ShareCache wrapper with one caller. Call the map directly. (share.dart:40, a-b step 2)'], 'PROJ-1 Share', changes, 'smell');
  assert.match(one, /^Simplify one LOW-risk over-engineering finding in this change\./);
  assert.match(one, /Finding: yagni: ShareCache wrapper with one caller\. Call the map directly\.\n   Location: share\.dart:40 \(a-b step 2\)/);
  assert.match(one, /Cut the code or swap in the named replacement; behavior must not change\./);
  assert.match(one, /run the existing tests/);
  assert.doesNotMatch(one, /named refactoring/);
  assert.match(one, /do not commit/);
  assert.doesNotMatch(one, /minimal fix/);
  assert.match(archifyFixPrompt(['a (x.dart:1)', 'b (y.dart:2)'], '', null, 'smell'), /^Simplify 2 LOW-risk over-engineering findings in this change\./);
  // The code-review prompt is unchanged.
  assert.equal(archifyFixPrompt(['a (x.dart:1)'], 'T', changes), archifyFixPrompt(['a (x.dart:1)'], 'T', changes, 'code'));
  assert.match(archifyFixPrompt(['a (x.dart:1)'], 'T', changes), /^Fix one LOW-severity code-review finding/);
});

test('an Ask AI prompt asks about one MEDIUM or HIGH item without editing files', () => {
  const changes = { commits: [{ sha: 'abc123', subject: 'share' }] };
  const code = archifyAskPrompt('Does the cache keep a failed future? (share.dart:36, a-b step 1)', 'medium', 'PROJ-1 Share', changes, 'code');
  assert.match(code, /^Look into one MEDIUM-severity code-review finding in this change and answer it\./);
  assert.match(code, /Ticket: PROJ-1 Share\nCommit: abc123 share\nFinding: Does the cache keep a failed future\?\n   Location: share\.dart:36 \(a-b step 1\)/);
  assert.match(code, /say whether it is a real problem and why, and propose the smallest fix\. Do not edit files\.$/);
  assert.match(archifyAskPrompt('x (a.dart:1)', 'high', '', null, 'security'), /^Look into one HIGH-severity security-review finding/);
  const smell = archifyAskPrompt('yagni: ShareRepository interface with one implementation. Inline it. (share.dart:9)', 'medium', '', null, 'smell');
  assert.match(smell, /^Look into one MEDIUM-risk over-engineering finding in this change and answer it\./);
  assert.match(smell, /whether cutting it is worth it here[\s\S]*Do not edit files\.$/);
});

test('fix prompt reads a full-width parenthesized location too', () => {
  assert.match(archifyFixPrompt(['問題。（a.dart:5, c1 step 2）'], '', null), /Finding: 問題。\n   Location: a\.dart:5 \(c1 step 2\)/);
});
