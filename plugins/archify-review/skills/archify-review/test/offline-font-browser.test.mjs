import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ChromeVisualBrowser, findChrome } from '../bin/visual-check.mjs';

const skillRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const chrome = process.env.ARCHIFY_CHROME ? findChrome() : null;
const options = { skip: chrome ? false : 'Set ARCHIFY_CHROME for offline font and export browser acceptance.' };

async function evaluate(browser, expression) {
  const result = await browser.cdp.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, await browser.sessionPromise, 30000);
  assert.ok(!result.exceptionDetails, result.exceptionDetails?.exception?.description || result.exceptionDetails?.text);
  return result.result?.value;
}

async function prepare(browser, blocked) {
  const session = await browser.sessionPromise;
  await browser.cdp.send('Network.enable', {}, session);
  await browser.cdp.send('Network.setCacheDisabled', { cacheDisabled: true }, session);
  await browser.cdp.send('Network.setBlockedURLs', { urls: blocked ? ['http://*', 'https://*'] : [] }, session);
  await browser.cdp.send('DOM.enable', {}, session);
  await browser.cdp.send('CSS.enable', {}, session);
  await browser.cdp.send('CSS.setLocalFontsEnabled', { enabled: false }, session);
  const requests = [];
  let buffer = '';
  browser.cdp.readPipe.on('data', (chunk) => {
    buffer += chunk;
    let end;
    while ((end = buffer.indexOf('\0')) >= 0) {
      const message = JSON.parse(buffer.slice(0, end));
      buffer = buffer.slice(end + 1);
      if (message.method === 'Network.requestWillBeSent' && /^https?:/.test(message.params.request.url)) requests.push(message.params.request.url);
    }
  });
  return requests;
}

async function actualFonts(browser, selector) {
  const session = await browser.sessionPromise;
  const { root } = await browser.cdp.send('DOM.getDocument', {}, session);
  const { nodeId } = await browser.cdp.send('DOM.querySelector', { nodeId: root.nodeId, selector }, session);
  assert.ok(nodeId, selector);
  return (await browser.cdp.send('CSS.getPlatformFontsForNode', { nodeId }, session)).fonts;
}

function render(input, output, quality = 'standard') {
  execFileSync(process.execPath, [path.join(skillRoot, 'bin/archify.mjs'), 'render', 'architecture', input, output, '--quality', quality], { stdio: 'pipe' });
}

test('fresh viewers use bundled fonts with local fonts disabled and identical offline layout', options, async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-font-layout-'));
  const fixtures = [
    ['production', '../test/fixtures/nested-boundaries.architecture.json', 'showcase'],
    ['web', 'web-app.architecture.json', 'standard'],
  ];
  try {
    for (const [name, source, quality] of fixtures) render(path.join(skillRoot, 'examples', source), path.join(tmp, `${name}.html`), quality);
    const mixed = JSON.parse(fs.readFileSync(path.join(skillRoot, 'examples/web-app.architecture.json')));
    mixed.meta.title = 'Fonts A Ā Ѡ Ж Ω ắ 中文';
    fs.writeFileSync(path.join(tmp, 'mixed.json'), JSON.stringify(mixed));
    render(path.join(tmp, 'mixed.json'), path.join(tmp, 'mixed.html'));
    const online = new Map();
    for (const blocked of [false, true]) {
      const browser = new ChromeVisualBrowser(chrome);
      try {
        const requests = await prepare(browser, blocked);
        for (const name of ['production', 'web', 'mixed']) for (const theme of ['dark']) {
          const metrics = await browser.inspect({ artifactPath: path.join(tmp, `${name}.html`), width: 1440, height: 900, theme });
          const snapshot = { readerWidth: metrics.readerWidth, diagramWidth: metrics.diagramWidth, scrollHeight: metrics.scrollHeight };
          const key = `${name}/${theme}`;
          assert.ok(snapshot.readerWidth > 0 && snapshot.diagramWidth > 0, key);
          if (blocked) assert.deepEqual(snapshot, online.get(key), key);
          else online.set(key, snapshot);
          const fonts = await actualFonts(browser, '.diagram-container svg text[data-node-label]');
          assert.ok(fonts.some(f => f.isCustomFont && /JetBrains Mono/.test(f.familyName)), JSON.stringify(fonts));
          const loaded = await evaluate(browser, `(async () => {
            await Promise.all([400,500,600,700].map(w => document.fonts.load(w + ' 16px "JetBrains Mono"', 'A Ā Ѡ Ж Ω ắ')));
            return Array.from(document.fonts).filter(f => /JetBrains Mono/.test(f.family)).map(f => f.status);
          })()`);
          assert.deepEqual(loaded, Array(6).fill('loaded'));
        }
        assert.deepEqual(requests, [], 'viewers must not attempt an HTTP(S) request');
      } finally { await browser.close(); }
    }
  } finally { fs.rmSync(tmp, { recursive: true, force: true }); }
});
