import { test } from 'node:test';
import assert from 'node:assert/strict';
import { extractSvgs, parseXml } from './helpers/xml.mjs';

test('artifact SVG extraction follows HTML quoting and preserves SVG document boundaries', () => {
  const extracted = extractSvgs(`
    <script>const ignored = '<svg data-node-label></svg>';</script>
    <template><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"/></template>
    <iframe srcdoc='&lt;svg xmlns=&quot;http://www.w3.org/2000/svg&quot;&gt;&lt;svg viewBox=&quot;0 0 1 1&quot;/&gt;&lt;/svg&gt;'></iframe>
  `);
  assert.equal(extracted.direct.length, 1, 'template SVG is markup while script text is not');
  assert.equal(extracted.embedded.length, 1, 'srcdoc contributes one top-level SVG document');
  for (const svg of [...extracted.direct, ...extracted.embedded]) assert.doesNotThrow(() => parseXml(svg));

  const inheritedNamespace = extractSvgs(`
    <svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
      <svg><use xlink:href="#icon"/></svg>
    </svg>
  `);
  assert.equal(inheritedNamespace.direct.length, 1, 'nested SVG remains inside its XML document');
  assert.doesNotThrow(() => parseXml(inheritedNamespace.direct[0]));
});
