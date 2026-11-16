import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

function block(template, name) {
  const start = template.indexOf(`/* ${name} */`);
  const end = template.indexOf(`/* end ${name} */`);
  assert.ok(start > 0 && end > start, `${name} source not found`);
  return template.slice(start, end);
}

// The review-tick pure functions of the viewer, with the diff numbering they share with the code blocks.
export function reviewPure() {
  const template = fs.readFileSync(path.join(root, 'assets/template.html'), 'utf8');
  const names = 'archifyDiffLines, flowLines, flowUnits, flowState, flowPaths, reviewStates, nodeSeen, pathSeen';
  return new Function(`${block(template, 'diff-lines pure')}\n${block(template, 'review-seen pure')}; return { ${names} };`)();
}
