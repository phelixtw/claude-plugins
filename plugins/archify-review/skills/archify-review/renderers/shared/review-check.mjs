import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { SCHEMES, schemesOf } from '../architecture/layered.mjs';
import { edgeLabelWidth } from '../architecture/routing.mjs';
import { SUPPORTED_LOCALES, translateMessage } from './i18n.mjs';
import { fittedNodeFontSize } from './text-fit.mjs';
import { REVIEW_DIAGRAM_WIDTH_PX, REVIEW_DIAGRAM_HEIGHT_PX, REVIEW_LABEL_PREFERRED_PX, REVIEW_MIN_LABEL_PX, REVIEW_MIN_FILL, REVIEW_EDGE_LABEL_PX } from './desktop-readability.mjs';

const HEADER_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;
const skillRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

export function loadHunks(repo, changes) {
  const select = changes?.slice ? ['--report', changes.slice.report] : (changes?.commits || []).map((c) => c.sha);
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'review-check-'));
  const out = path.join(tmp, 'hunks.json');
  try {
    const r = spawnSync(path.join(skillRoot, 'bin/review-kit'), ['hunks', '--repo', repo, ...select, '--out', out], { encoding: 'utf8', maxBuffer: 1 << 28 });
    if (r.status !== 0) throw new Error(`review-kit hunks failed: ${(r.stderr || r.error?.message || '').trim()}`);
    return JSON.parse(fs.readFileSync(out, 'utf8'));
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

// Maps each line of a hunk to {sign, oldNo, newNo} using the header counters.
function numberLines(header, lines) {
  const [, o, , n] = HEADER_RE.exec(header);
  let oldNo = +o;
  let newNo = +n;
  return lines.map((l) => {
    const c = l[0];
    if (c === '\\') return { sign: c };
    const r = { sign: c, oldNo: c === '+' ? null : oldNo, newNo: c === '-' ? null : newNo };
    if (c !== '+') oldNo++;
    if (c !== '-') newNo++;
    return r;
  });
}

const splitDiff = (diff) => diff.replace(/\n+$/, '').split(/\n(?=@@ )/).filter(Boolean);

// Returns the offset of `body` as a run inside a new-file git hunk, or -1.
function fragmentOffset(gitHunk, body, newStart) {
  if (!/^@@ -0,0 /.test(gitHunk.header)) return -1;
  const gs = +HEADER_RE.exec(gitHunk.header)[3];
  const off = newStart - gs;
  if (off < 0 || off + body.length > gitHunk.lines.length) return -1;
  return body.every((l, i) => l === gitHunk.lines[off + i]) ? off : -1;
}

// Validates one diff string against git hunks (R2, R3) and marks coverage (R4).
function checkDiff(diff, path, gitHunks, covered, report) {
  const parsed = [];
  for (const text of splitDiff(diff)) {
    const [header, ...body] = text.split('\n');
    const hm = HEADER_RE.exec(header);
    if (!hm) { report('R2', 'error', `diff chunk does not start with an @@ header: ${header.slice(0, 60)}`, 'Paste whole hunks from git show, including the @@ header.'); continue; }
    const same = gitHunks.filter((g) => g.text === text);
    let hit = same.find((g) => g.file === path);
    let off = 0;
    if (!hit && !same.length && hm[1] === '0' && hm[2] === '0') {
      for (const g of gitHunks.filter((x) => x.file === path)) {
        off = fragmentOffset(g, body, +hm[3]);
        if (off >= 0) { hit = g; break; }
      }
    }
    if (!hit && same.length) {
      report('R3', 'error', `hunk ${header} belongs to ${same[0].file}, not ${path}`, 'Point path at the file this hunk changes, or move the hunk to the right connection.');
      continue;
    }
    if (!hit) {
      const near = gitHunks.find((g) => g.header === header);
      report('R2', 'error', near ? `hunk ${header} differs from git (trimmed, picked or missing lines)` : `hunk ${header} matches no git hunk`, 'Paste the complete hunk from git show verbatim; never trim lines.');
      continue;
    }
    const mark = covered.get(hit) || new Set();
    const n = same.includes(hit) ? hit.lines.length : body.length;
    for (let i = 0; i < n; i++) mark.add(same.includes(hit) ? i : off + i);
    covered.set(hit, mark);
    parsed.push({ header, numbered: numberLines(header, body) });
  }
  return parsed;
}

function lineNumbers(spec) {
  const out = [];
  for (const part of spec.split(',')) {
    const m = /^([+-])(\d+)(?:\.\.[+-]?(\d+))?$/.exec(part.trim());
    if (!m) { out.push(null); continue; }
    for (let i = +m[2]; i <= +(m[3] ?? m[2]); i++) out.push([m[1], i]);
  }
  return out;
}

// A long identifier is skimmed, so a code span counts at most 20 characters.
const visible = (text) => text.replace(/`([^`]*)`/g, (_, code) => code.slice(0, 20)).replace(/\*\*/g, '').replace(/\[([^\]]*)\]\(#node:[^)]*\)/g, '$1').trim();

// R24: a hop's tests. Every changed function has a test status and says why it is worth a test; a tested one names
// its tests and each of them reads as one Given / When / Then; an untested one gets a suggested case to write.
const TEST_STATUSES = ['new', 'existing', 'none', 'removed'];
const CASE_MAX_CHARS = 60;
// A case tells the situation a person meets; more code than this reads as the test's inputs, not a scenario.
// A warning, since code with no user in sight (a plugin API, a parser) has no scenario but its calls.
const CASE_MAX_CODE_SPANS = 2;
const WHY_MAX_CHARS = 80;
function checkTests(tests, report) {
  const functions = tests?.functions || [];
  const cases = tests?.cases || [];
  const suggested = tests?.suggested || [];
  if (!functions.length) report('R24', 'error', 'code.tests.functions is missing or empty', 'List every function, method or property this hop\'s diff changes under tests.functions, each with lines, status (new, existing, none or removed) and by.');
  for (const f of functions) {
    const name = f.symbol || '?';
    const by = f.by || [];
    const why = [...visible(String(f.why ?? ''))].length;
    if (!why) report('R24', 'error', `function ${name} has no why`, 'Write why: the behaviour of this code that matters, as what would break for a user or caller if it were wrong (for a removed function, what it did and where it went), from the code, its comments, the commit message or the tests.');
    else if (why > WHY_MAX_CHARS) report('R24', 'error', `function ${name} why is ${why} characters (at most ${WHY_MAX_CHARS})`, `Say it in one sentence of at most ${WHY_MAX_CHARS} characters.`);
    if (!TEST_STATUSES.includes(f.status)) {
      report('R24', 'error', `function ${name} has status ${JSON.stringify(f.status)}`, 'Use new (a test added or changed in the commits calls it), existing (only unchanged tests call it), none (no test calls it) or removed (the commits delete it).');
      continue;
    }
    const tested = f.status === 'new' || f.status === 'existing';
    if (f.via !== undefined && !tested) report('R24', 'error', `function ${name} has via but status ${f.status}`, 'Set via only on a new or existing function that its tests reach through another symbol.');
    if (tested && !by.length) report('R24', 'error', `function ${name} is ${f.status} but by names no test`, 'List the tests that call it in by (TypeName.testMethod, or the test function name).');
    if (tested) {
      for (const t of by.filter((t) => !cases.some((c) => c.test === t))) report('R24', 'error', `test ${t} has no case`, 'Add a cases entry for it whose given, when and then come from the test code.');
    }
    if (f.status === 'none' && by.length) report('R24', 'error', `function ${name} is none but by names ${by.join(', ')}`, 'Leave by empty for a function no test calls, or use new or existing.');
    if (f.status === 'none' && !suggested.some((c) => c.covers === f.symbol)) report('R24', 'error', `untested function ${name} has no suggested case`, 'Add a suggested entry whose covers is this symbol, with the given, when and then of the test to write.');
  }
  for (const c of [...cases, ...suggested]) {
    const what = c.test ? `case ${c.test}` : `suggested case for ${c.covers}`;
    for (const k of ['given', 'when', 'then']) {
      const n = [...visible(String(c[k] ?? ''))].length;
      if (!n) report('R24', 'error', `${what} has no ${k}`, 'Write given, when and then, one short sentence each.');
      else if (n > CASE_MAX_CHARS) report('R24', 'error', `${what} ${k} is ${n} characters (at most ${CASE_MAX_CHARS})`, `Say it in one short sentence of at most ${CASE_MAX_CHARS} characters; keep only facts the test code shows.`);
    }
    const spans = ['given', 'when', 'then'].reduce((sum, k) => sum + (String(c[k] ?? '').match(/`[^`]*`/g) || []).length, 0);
    if (spans > CASE_MAX_CODE_SPANS) report('R24', 'warning', `${what} has ${spans} code spans (at most ${CASE_MAX_CODE_SPANS})`, 'Tell it as the situation a user meets (dates, items, what they tap or see), not the test\'s inputs: no internal ids, keys, arrays or enum values.');
  }
}

export function checkSpec(spec, out) {
  const findings = [];
  const gitHunks = out.hunks.map(({ file, header, text }) => ({ file, header, text, lines: text.split('\n').slice(1) }));
  const covered = new Map();
  const add = (subject) => (rule, severity, detail, fix) => findings.push({ rule, severity, subject, detail, fix });
  const tests = new Set(spec.changes?.tests || []);

  for (const c of spec.connections || []) {
    const report = add(c.id);
    const code = c.code || {};
    if (c.kind === 'counterpart') { // same component on two platforms: no flow, only R14 (story or note) applies
      if (!String(code.story || '').trim() && !String(code.note || '').trim()) report('R14', 'error', 'counterpart connection has neither code.story nor code.note', 'State in code.story why the two files are the same component, as the code or comments say.');
      continue;
    }
    const parsed = code.diff ? checkDiff(code.diff, code.path, gitHunks, covered, report) : [];
    const flat = parsed.flatMap((p) => p.numbered);
    const deletedOnly = parsed.length > 0 && flat.every((l) => l.sign === '-');
    const miss = ['diff', 'path', ...(deletedOnly ? [] : ['line'])].filter((k) => !code[k]);
    if (miss.length) report('R1', 'error', `code is missing or empty: ${miss.join(', ')}`, 'Fill code.path, line and diff from the commit, or remove a connection the commit does not touch.');
    const hasIn = (lines, sign, no) => lines.some((l) => (sign === '+' ? l.sign !== '-' && l.newNo === no : l.sign !== '+' && l.oldNo === no));
    for (const f of code.tests?.functions || []) {
      const name = f.symbol || '?';
      const hunks = [].concat(f.hunk ?? []);
      const pool = hunks.length ? hunks.flatMap((h) => parsed[h - 1]?.numbered ?? []) : flat;
      for (const r of lineNumbers(String(f.lines ?? ''))) {
        if (!r) { report('R5', 'error', `function ${name} has unparsable lines "${f.lines}"`, 'Use the +a..+b,-c form.'); break; }
        if (!hasIn(pool, r[0], r[1])) { report('R5', 'error', `function ${name} cites ${r[0]}${r[1]} which is not a ${r[0] === '+' ? 'new-file or context' : 'old-file'} line of ${hunks.length ? `hunk ${hunks.join(',')} of ` : ''}the diff`, 'Cite only line numbers shown in this connection diff, with the right sign.'); break; }
        if (!hunks.length && parsed.filter((p) => hasIn(p.numbered, r[0], r[1])).length > 1) { report('R10', 'error', `function ${name} cites ${r[0]}${r[1]} which appears in more than one hunk of the diff`, 'Add `hunk` (1-based index of the @@ hunk) to the function to disambiguate.'); break; }
      }
    }
    checkTests(code.tests, report);
    if (code.line && code.diff && !flat.some((l) => l.sign !== '-' && l.newNo === code.line)) {
      report('R6', 'error', `line ${code.line} is not a new-file line in the diff`, 'Set line to a + or context line number from the diff.');
    }
    if (code.diff && isCommentOnly(code.diff)) {
      report('R13', 'error', 'connection carries only comment changes', 'Remove this connection and put its hunk in the node code.files[].diff of that file; node diffs count for hunk coverage (R4). If the two files are the same component on two platforms, use kind: counterpart with no diff.');
    }
    if (!String(code.action || '').trim() && !String(code.story || '').trim()) {
      report('R14', 'error', 'connection has neither code.action nor code.story', 'Write code.action for a real user operation, otherwise code.story: what the two files do together in this change, matching the diff.');
    }
    const note = code.note || '';
    if (note.length > 120 || (note.match(/。|\.(?=\s|$)/g) || []).length >= 2) {
      report('R8', 'warning', `note is ${note.length} chars with multiple sentences`, 'Keep note to one summary sentence; put detail in code.story.');
    }
  }

  const pairs = new Map();
  for (const c of spec.connections || []) {
    const k = `${c.from}->${c.to}`;
    pairs.set(k, [...(pairs.get(k) || []), c.id]);
  }
  for (const [k, ids] of pairs) {
    if (ids.length > 1) add(ids.join(', '))('R9', 'warning', `${ids.length} connections share ${k}`, 'Merge them or route through distinct nodes.');
  }

  for (const n of spec.components || []) {
    for (const f of n.code?.files || []) {
      if (f.diff) checkDiff(f.diff, f.path, gitHunks, covered, add(n.id));
    }
    if (n.code?.files?.length && !n.code.risk) {
      add(n.id)('R23', 'error', 'component has code.files but no code.risk', 'Run bin/review-kit risk --repo <repo> --hunks hunks.json --out risk.json, then assemble with --risk risk.json; never write code.risk by hand.');
    }
  }

  for (const g of gitHunks) {
    if (!tests.has(g.file) && covered.get(g)?.size !== g.lines.length) {
      add(g.file)('R4', 'error', `hunk ${g.header} is not fully included in any diff`, 'Assign this hunk to a connection and paste it whole.');
    }
  }
  for (const f of out.changes.files.filter((x) => x.additions === undefined && !tests.has(x.path))) {
    if (!(spec.changes?.files || []).some((x) => x.path === f.path)) {
      add(f.path)('R4', 'error', 'binary file is missing from changes.files', 'List it in changes.files.');
    }
  }

  checkFiles(spec, out.changes.files.filter((f) => !tests.has(f.path)), tests, add);
  if (!(spec.cards || []).some((c) => /Story 的目的|story purpose|根因|root cause/i.test(c.title || ''))) {
    add('cards')('R11', 'error', 'no Story purpose card (title 這張 Story 的目的 or "Story purpose")', 'Add a card titled 這張 Story 的目的 that says what the story fixes and why the old behavior was wrong.');
  }
  // R22: a code-review report carries only the Story purpose card and the TODO card; anything else (test logs,
  // device runs, accounts) is not part of the review and stays out of the shared report.
  for (const c of spec.cards || []) {
    if (!/Story 的目的|story purpose|根因|root cause|TODO|待確認/i.test(c.title || '')) {
      add('cards')('R22', 'error', `card "${c.title}" is not a Story purpose or TODO card`, 'Remove it; put what changed and why in 這張 Story 的目的 and open questions in 待確認問題 / TODO.');
    }
  }
  checkLayers(spec, add);
  checkTodoSeverity(spec, add('cards'));
  checkNoFindingsLanguage(spec, add('cards'));
  checkDensity(spec, add('layout'));
  return findings;
}

// R18: meta.platform picks the layering schemes of the layout switch; every component needs one layer per scheme.
// WebView pages draw only changed code: a node must carry code.files (R2/R3 tie them to the diff) or be an end of a connection (R1-R3 tie its diff to the commits).
function checkLayers(spec, add) {
  if (!(spec.components || []).length) return;
  const schemes = schemesOf(spec.meta);
  if (!schemes.length) {
    const platform = spec.meta?.platform;
    const fix = platform === 'web'
      ? 'Replace "web" with "app"; for a project that embeds a WebView also set meta.webview to true and give every component code.layers.webview (host, bridge or content).'
      : 'Set meta.platform to "app" (Flutter, Android, iOS: layout MVVM; add meta.framework "flutter" for Flutter code to get the Flutter layers, meta.webview true for a WebView project to get the WebView layers).';
    add('meta')('R18', 'error', `meta.platform is ${platform === undefined ? 'missing' : `"${platform}"`}`, fix);
    return;
  }
  // meta.webview follows the diff, not the project's dependencies: a change with no bridge or web content is not a WebView change.
  if (schemes.includes('webview') && !(spec.components || []).some((n) => ['bridge', 'content'].includes(n.code?.layers?.webview))) {
    add('meta')('R18', 'error', 'meta.webview is true but no component is bridge or content: the change does not touch WebView code', 'Remove meta.webview and every code.layers.webview; set it only when the reviewed commits change a JS bridge or web content.');
  }
  for (const n of spec.components || []) {
    if (n.code?.layers && 'web' in n.code.layers) {
      add(n.id)('R18', 'error', 'component has code.layers.web', 'Remove code.layers.web; use code.layers.webview (host, bridge or content) with meta.webview true.');
    }
    if (schemes.includes('webview') && !n.code?.files?.length && !(spec.connections || []).some((c) => c.from === n.id || c.to === n.id)) {
      add(n.id)('R18', 'error', 'WebView component is in no diff (no code.files, no connection)', 'Remove it: the WebView layout draws only files changed in the commits; an unchanged web page or bridge is not a node.');
    }
    for (const scheme of schemes) {
      const layer = n.code?.layers?.[scheme];
      if (!SCHEMES[scheme].tiers.some(([id]) => id === layer)) {
        add(n.id)('R18', 'error', `component has no code.layers.${scheme} (${layer === undefined ? 'missing' : `"${layer}"`})`, `Set code.layers.${scheme} to one of ${SCHEMES[scheme].tiers.map(([id]) => `"${id}"`).join(', ')}; in plan.json write \`layers: {${schemes.map((x) => `${x}: ...`).join(', ')}}\` on the component.`);
      }
    }
  }
}

// R20: the "no findings" item is the viewer catalog sentence of meta.locale, so a zh-TW report has no English line.
function checkNoFindingsLanguage(spec, report) {
  const locale = SUPPORTED_LOCALES.includes(spec.meta?.locale) ? spec.meta.locale : 'en';
  const want = translateMessage(locale, 'viewer.todo.noFindings');
  for (const item of todoCards(spec).flatMap((c) => c.items || [])) {
    const body = String(item).replace(/^(?:\[[^\]]*\]\s*)+/, '').trim().toLowerCase();
    const other = SUPPORTED_LOCALES.find((l) => l !== locale && body === translateMessage(l, 'viewer.todo.noFindings').toLowerCase());
    if (other) report('R20', 'warning', `the "no findings" item is written for ${other} but meta.locale is ${locale}`, `Write it as: ${want}`);
  }
}

function todoCards(spec) { return (spec.cards || []).filter((c) => /TODO|待確認/i.test(c.title || '')); }

// R17: TODO items from code-review/security-review/code-smell read `[low|medium|high][code-review] ...` (optionally `[low][new][code-review] ...` on a refresh) and are sorted most severe first.
const SEVERITY_RANK = { high: 0, medium: 1, low: 2 };
function checkTodoSeverity(spec, report) {
  for (const card of todoCards(spec)) checkTodoCard(card, report);
}
function checkTodoCard(card, report) {
  let last = -1;
  for (const item of card.items || []) {
    const text = String(item).trim();
    const m = /^(?:\[([^\]]*)\])?\s*(?:\[new\]\s*)?\[(code-review|security-review|code-smell)\]/i.exec(text);
    if (m) {
      if (!Object.hasOwn(SEVERITY_RANK, (m[1] || '').toLowerCase())) {
        report('R17', 'error', `TODO item has ${m[1] ? `unknown severity [${m[1]}]` : 'no severity'}: ${text.slice(0, 60)}`, 'Start the item with [low], [medium] or [high] before [code-review]/[security-review]/[code-smell].');
        continue;
      }
    }
    const rank = SEVERITY_RANK[(/^\[(low|medium|high)\]/i.exec(text)?.[1] || '').toLowerCase()];
    if (rank === undefined) continue;
    if (rank < last) report('R17', 'error', `TODO items are not sorted most severe first: ${text.slice(0, 60)}`, 'Order the TODO card high, then medium, then low.');
    last = Math.max(last, rank);
  }
}

// A label sits in the gap between its two nodes, so it must fit: a wider one is drawn over a node (deliver fails on it).
const LABEL_GAP_MARGIN_PX = 8;
const labelGaps = (spec, nodes) => {
  const at = new Map(nodes.map((c) => [c.id, c]));
  return (spec.connections || []).flatMap((e) => {
    const [a, b] = [at.get(e.from), at.get(e.to)];
    if (!a || !b || !e.label) return [];
    const need = Math.ceil(edgeLabelWidth(e.label, REVIEW_EDGE_LABEL_PX)) + LABEL_GAP_MARGIN_PX;
    const across = Math.max(a.pos[0] - (b.pos[0] + b.size[0]), b.pos[0] - (a.pos[0] + a.size[0])); // horizontal gap, <= 0 when the columns overlap
    const aside = Math.max(a.pos[1] - (b.pos[1] + b.size[1]), b.pos[1] - (a.pos[1] + a.size[1]));
    return across > 0 && aside <= 0 ? [{ e, a, b, gap: across, need }] : []; // side by side: the label sits on the horizontal run
  });
};

// R15/R16/R19: projected label size, node fill and edge-label room at a 1440px viewport, from pos/size alone.
function checkDensity(spec, report) {
  const nodes = (spec.components || []).filter((c) => Array.isArray(c.pos) && Array.isArray(c.size));
  if (!nodes.length) return;
  const vb = spec.meta?.viewBox || [Math.max(...nodes.map((c) => c.pos[0] + c.size[0])) + 40, Math.max(...nodes.map((c) => c.pos[1] + c.size[1])) + 80]; // 80 = margin + legend
  const scale = Math.min(REVIEW_DIAGRAM_WIDTH_PX / vb[0], REVIEW_DIAGRAM_HEIGHT_PX / vb[1]);
  const px = Math.min(...nodes.map((c) => fittedNodeFontSize(c.label, c.size[0], REVIEW_LABEL_PREFERRED_PX, 8))) * scale;
  if (px < REVIEW_MIN_LABEL_PX) report('R15', 'error', `node labels render at ~${px.toFixed(1)}px at 1440x900 (need ${REVIEW_MIN_LABEL_PX}px; viewBox ${vb[0]}x${vb[1]})`, `Pack the nodes: size each to its label (~${Math.round(REVIEW_LABEL_PREFERRED_PX * 0.6)}px per character + 16), use 3 columns, gaps of 30-50px, so the viewBox is at most ~${Math.floor(REVIEW_DIAGRAM_WIDTH_PX * REVIEW_LABEL_PREFERRED_PX / REVIEW_MIN_LABEL_PX)}x${Math.floor(REVIEW_DIAGRAM_HEIGHT_PX * REVIEW_LABEL_PREFERRED_PX / REVIEW_MIN_LABEL_PX)} (the viewer fits the diagram to a ~${REVIEW_DIAGRAM_WIDTH_PX}x${REVIEW_DIAGRAM_HEIGHT_PX} panel at 1440x900).`);
  const fill = nodes.reduce((a, c) => a + c.size[0] * c.size[1], 0) / (vb[0] * vb[1]);
  const gaps = labelGaps(spec, nodes);
  const minGap = Math.max(0, ...gaps.map((g) => g.need));
  if (fill < REVIEW_MIN_FILL) report('R16', 'warning', `nodes cover ${Math.floor(fill * 1000) / 10}% of the canvas (want at least ${REVIEW_MIN_FILL * 100}%)`, `Cut the gaps between rows and columns, but keep at least ${minGap || 40}px between connected nodes${minGap ? ' (the widest edge label plus margin)' : ''}; do not leave empty bands.`);
  for (const { e, a, b, gap, need } of gaps) {
    if (gap < need) report('R19', 'error', `label "${e.label}" (~${need - LABEL_GAP_MARGIN_PX}px) is wider than the ${gap}px gap between ${a.id} and ${b.id}`, `Move ${a.id} and ${b.id} apart to a gap of at least ${need}px, or shorten the label; deliver rejects a label drawn over a node.`);
  }
}

function checkFiles(spec, files, tests, add) {
  const report = add('changes');
  if (!spec.changes?.files) { report('R7', 'error', 'changes.files is missing', 'Add changes.files from git show --numstat.'); return; }
  const want = new Map(files.map((f) => [f.path, f]));
  const have = new Map(spec.changes.files.map((f) => [f.path, f]));
  for (const [p, w] of want) {
    const h = have.get(p);
    if (!h) { report('R7', 'error', `${p} is missing from changes.files`, 'Add it.'); continue; }
    const diffs = [];
    if (h.status !== w.status) diffs.push(`status ${h.status} vs ${w.status}`);
    if (w.additions !== undefined && (h.additions !== w.additions || h.deletions !== w.deletions)) diffs.push(`+${h.additions}/-${h.deletions} vs git +${w.additions}/-${w.deletions}`);
    if (diffs.length) report('R7', 'error', `${p}: ${diffs.join('; ')}`, 'Copy path, status and numbers from git show --numstat.');
  }
  for (const p of have.keys()) {
    if (!want.has(p)) report('R7', 'error', `${p} is in changes.files but git has no change to it in this review`, 'Remove it.');
  }
  // R21: every changed file that still exists is drawn as its own node (label = file name), so the change tree and the diagram agree;
  // a test file (changes.tests) is never drawn: no node label, node file or hop path names it.
  const labels = new Set((spec.components || []).map((n) => n.label));
  const name = (p) => p.split('/').pop();
  for (const [p, f] of have) {
    if (f.status === 'deleted') continue;
    if (!labels.has(name(p))) report('R21', 'error', `${p} has no node in the diagram`, `Add a component labeled ${name(p)}.`);
  }
  const names = new Set([...have.keys()].map(name));
  const fix = 'Remove it: test files stay out of the report; record what their tests cover in code.tests of the hop they test.';
  for (const t of tests) {
    for (const n of spec.components || []) {
      if (n.label === t || (n.label === name(t) && !names.has(name(t)))) add(n.id)('R21', 'error', `component is labeled like the test file ${t}`, fix);
      if ((n.code?.files || []).some((f) => f.path === t)) add(n.id)('R21', 'error', `component lists the test file ${t}`, fix);
    }
    for (const c of spec.connections || []) {
      if (c.code?.path === t) add(c.id)('R21', 'error', `connection shows the test file ${t}`, fix);
    }
  }
}

export function summarize(findings) {
  const byRule = {};
  for (const f of findings) byRule[f.rule] = (byRule[f.rule] || 0) + 1;
  return {
    errors: findings.filter((f) => f.severity === 'error').length,
    warnings: findings.filter((f) => f.severity === 'warning').length,
    byRule,
  };
}

const COMMENT_RE = /^(\/\/|#|\/\*|\*|<!--|-->)/;

// True when every added/removed non-blank line of the diff starts like a comment.
function isCommentOnly(diff) {
  const changed = diff.split('\n').filter((l) => /^[+-]/.test(l) && !l.startsWith('+++') && !l.startsWith('---')).map((l) => l.slice(1).trim()).filter(Boolean);
  return changed.length > 0 && changed.every((l) => COMMENT_RE.test(l));
}
