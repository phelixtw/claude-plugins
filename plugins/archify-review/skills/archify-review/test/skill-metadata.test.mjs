import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import assert from 'node:assert/strict';

const here = path.dirname(fileURLToPath(import.meta.url));
const skillRoot = path.join(here, '..');
const skill = readFileSync(path.join(skillRoot, 'SKILL.md'), 'utf8');
const authoringContract = readFileSync(path.join(skillRoot, 'references', 'authoring-contract.md'), 'utf8');
const frontmatter = skill.match(/^---\n([\s\S]*?)\n---/);

test('skill description is portable across 1024-character runtimes and remains searchable', () => {
  assert.ok(frontmatter, 'SKILL.md must start with YAML frontmatter');
  const description = frontmatter[1].match(/^description:\s*(.+)$/m)?.[1]?.trim();
  assert.ok(description, 'frontmatter must include a one-line description');
  assert.ok(description.length <= 1024, `description is ${description.length} characters; maximum is 1024`);
  assert.ok(Buffer.byteLength(description, 'utf8') <= 1024, 'description must also fit a 1024-byte runtime limit');

  for (const trigger of ['architecture', 'code-review']) {
    assert.match(description, new RegExp(`\\b${trigger}\\b`, 'i'), `description must retain the ${trigger} trigger`);
  }
  assert.match(description, /standalone HTML/i);
  assert.match(description, /Use when/i);
  assert.doesNotMatch(description, /topology|infrastructure|visualize system architecture/i, 'the skill triggers on code review only');
});

test('the code-smell review ships its ponytail-review rules with the MIT notice and no Fowler dependency', () => {
  const rules = readFileSync(path.join(skillRoot, 'references', 'ponytail-review.md'), 'utf8');
  assert.match(rules, /github\.com\/DietrichGebert\/ponytail/);
  assert.match(rules, /MIT License[\s\S]*Copyright \(c\) 2026 DietrichGebert/);
  assert.match(rules, /`yagni:`/);
  assert.doesNotMatch(rules, /^---\nname:/, 'no frontmatter, so it never triggers as a skill of its own');
  const pipeline = readFileSync(path.join(skillRoot, 'references', 'code-review-pipeline.md'), 'utf8');
  assert.match(pipeline, /references\/ponytail-review\.md/);
  for (const doc of [pipeline, skill, readFileSync(path.join(skillRoot, '..', '..', 'README.md'), 'utf8')]) {
    assert.doesNotMatch(doc, /fowler-refactoring/i);
  }
});

test('plugin manifest describes a code-review tool only', () => {
  const manifest = JSON.parse(readFileSync(path.join(skillRoot, '..', '..', '.claude-plugin', 'plugin.json'), 'utf8'));
  assert.match(manifest.description, /code review/i);
  assert.doesNotMatch(manifest.description, /topology|infrastructure/i);
});

test('literal packaged-skill path references resolve inside the installed skill root', () => {
  const references = [...skill.matchAll(/`((?:assets|bin|examples|recipes|references|renderers|schemas|scripts)\/[^`\s]+)`/g)]
    .map((match) => match[1])
    .filter((reference) => !/[<>{}*\[\]]/.test(reference));

  assert.ok(references.length > 0, 'expected literal packaged-skill references');
  for (const reference of new Set(references)) {
    assert.equal(existsSync(path.join(skillRoot, reference)), true, `SKILL.md references missing packaged path ${reference}`);
  }
});

test('main skill stays a bounded authoring router with progressive references', () => {
  const lines = skill.trimEnd().split('\n');
  assert.ok(lines.length <= 160, `SKILL.md is ${lines.length} lines; keep the entrypoint at 160 or fewer`);
  for (const reference of [
    'references/authoring-contract.md',
    'references/viewer-runtime.md',
    'references/delivery-contract.md',
  ]) {
    assert.match(skill, new RegExp(reference.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
    assert.equal(existsSync(path.join(skillRoot, reference)), true, `${reference} must ship with the skill`);
  }
});

test('SKILL.md never asks for an upstream update check', () => {
  assert.doesNotMatch(skill, /check-update|update_available/);
});

test('language behavior stays within the bounded locale contract', () => {
  assert.match(skill, /one primary authored language/);
  assert.match(skill, /explicit user choice; otherwise follow the request or conversation's dominant language/);
  assert.match(skill, /`meta\.locale` controls only renderer-owned Viewer UI/);
  assert.match(skill, /use `"en"`, `"zh-CN"`, or `"zh-TW"`/);
  assert.match(skill, /For every other language, omit `meta\.locale`/);
  assert.match(skill, /fixed Viewer UI and `<html lang>` fall back to English/);
  assert.match(skill, /renderer never translates authored content/i);
  assert.match(skill, /product names.*code identifiers.*protocols.*API paths.*environment names/);
  assert.match(authoringContract, /`meta\.locale` controls only renderer-owned reader surfaces/);
  assert.match(authoringContract, /outside `en`, `zh-CN`, and `zh-TW`/);
  assert.match(authoringContract, /artifact is\s+not fully localized/);
  assert.match(authoringContract, /Do not silently substitute\s+`zh-CN` for another language or Chinese locale/);
  assert.match(authoringContract, /It never translates authored content/);
  assert.match(authoringContract, /Renderer-owned default legend labels follow `meta\.locale`/);
  assert.match(authoringContract, /The fallback\s+applies only to renderer-owned surfaces/);
});

test('skill keeps the title hierarchy compact by default', () => {
  assert.match(skill, /Omit `meta\.subtitle` by default/);
  assert.match(skill, /Never invent a subtitle that restates the title, nodes, or cards/);
  assert.match(authoringContract, /omitted or blank subtitle must not leave an empty visual row/);
});

test('skill never hard-codes how many artifact checks a receipt lists', () => {
  assert.doesNotMatch(skill, /\b(?:all )?\d+ artifact checks\b/);
});

test('code-review pipeline names only real layer tiers and puts Cubit, Bloc and ViewModel in the Flutter ui tier', async () => {
  const { SCHEMES } = await import('../renderers/architecture/layered.mjs');
  const pipeline = readFileSync(path.join(skillRoot, 'references', 'code-review-pipeline.md'), 'utf8');
  const tiers = new Set(Object.values(SCHEMES).flatMap((s) => s.tiers.map(([id]) => id)));
  const rule = /A node that holds no state[^\n]*/.exec(pipeline)?.[0] || '';
  const named = [...rule.matchAll(/`(\w+)`/g)].map((m) => m[1]);
  assert.ok(named.length >= 4, 'the state/draw rule names its tiers');
  for (const id of named) assert.ok(tiers.has(id), `pipeline names tier "${id}" which no scheme has`);
  assert.doesNotMatch(pipeline, /`business`/);
  assert.match(pipeline, /`ui` \([^)]*Cubit, Bloc[^)]*view model[^)]*"Views and view models make up the UI layer/);
  assert.match(pipeline, /`logic` \([^)]*Cubit, Bloc and ViewModel are not logic/);
});

test('code-review pipeline lists the catalog "no findings" sentence of every supported locale', async () => {
  const { SUPPORTED_LOCALES, translateMessage } = await import('../renderers/shared/i18n.mjs');
  const pipeline = readFileSync(path.join(skillRoot, 'references', 'code-review-pipeline.md'), 'utf8');
  for (const locale of SUPPORTED_LOCALES) assert.ok(pipeline.includes(translateMessage(locale, 'viewer.todo.noFindings')), locale);
});
