import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';
import { findChrome } from '../../../bin/visual-check.mjs';
import { desktopBrowser } from '../../helpers/desktop-browser.mjs';

export const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
export const cli = path.join(root, 'bin/archify.mjs');
export const fixtureDir = path.join(root, 'test/keep/fixtures');
export const chrome = process.env.ARCHIFY_CHROME ? findChrome() : null;
export const skipChrome = chrome ? false : 'Set ARCHIFY_CHROME to run real-browser review checks.';

export const readFixture = (ticket) => JSON.parse(fs.readFileSync(path.join(fixtureDir, ticket, 'arch-review.json'), 'utf8'));
export const scratchDir = (t, prefix = 'archify-keep-') => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
};
export const runCli = (...args) => spawnSync(process.execPath, [cli, ...args], { encoding: 'utf8' });

// Delivers a spec through the real CLI and returns the committed html path.
export function deliverSpec(dir, spec, name = 'review') {
  const input = path.join(dir, `${name}.json`);
  const output = path.join(dir, `${name}.html`);
  fs.writeFileSync(input, JSON.stringify(spec));
  const result = runCli('deliver', 'architecture', input, output, '--json');
  assert.equal(result.status, 0, result.stdout + result.stderr);
  return output;
}

// Opens an html in headless Chrome; every clipboard write is recorded in window.__copied.
export async function openPage(t, html, hash = '', init = '') {
  const browser = desktopBrowser(chrome);
  t.after(() => browser.close());
  const session = await browser.sessionPromise;
  const send = (method, params = {}) => browser.cdp.send(method, params, session);
  await send('Page.enable');
  await send('Page.addScriptToEvaluateOnNewDocument', { source: `
    window.__copied = [];
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: (text) => { window.__copied.push(text); return Promise.resolve(); } } });
    ${init}
  ` });
  const run = async (expression) => {
    const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    assert.equal(r.exceptionDetails, undefined, r.exceptionDetails?.exception?.description || r.exceptionDetails?.text);
    return r.result?.value;
  };
  const load = async (url) => {
    const loaded = browser.cdp.waitFor('Page.loadEventFired', session);
    await send('Page.navigate', { url });
    await loaded;
    await run('Archify.viewerChromeLayout ? Archify.viewerChromeLayout.whenStable() : true');
  };
  await load(pathToFileURL(html).href + hash);
  const click = (selector) => run(`document.querySelector(${JSON.stringify(selector)}).click(); true`);
  const waitUntil = async (expression, what = expression) => {
    for (let i = 0; i < 60; i++) {
      if (await run(expression)) return;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    assert.fail(`timed out waiting for ${what}`);
  };
  return { run, send, load, click, waitUntil, html, url: pathToFileURL(html).href };
}
