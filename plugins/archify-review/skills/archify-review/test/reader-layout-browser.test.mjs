import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { ChromeVisualBrowser, findChrome } from '../bin/visual-check.mjs';

const skillRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const chromePath = process.env.ARCHIFY_CHROME ? findChrome() : null;
const cases = {
  architecture: 'web-app.architecture.json',
};

test('Reader Layout preserves final-artifact behavior across its ownership boundaries', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async (t) => {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-reader-browser-'));
  const evidence = process.env.ARCHIFY_READER_EVIDENCE;
  if (evidence) fs.mkdirSync(evidence, { recursive: true });
  const artifacts = {};
  for (const [mode, example] of Object.entries(cases)) {
    const output = path.join(scratch, `${mode}.html`);
    // Optional captured base artifacts let the same behavioral cases establish
    // a pre-extraction baseline without changing the implementation under test.
    if (process.env.ARCHIFY_READER_BASELINE_DIR) {
      fs.copyFileSync(path.join(process.env.ARCHIFY_READER_BASELINE_DIR, `${mode}.html`), output);
    } else {
      execFileSync(process.execPath, [path.join(skillRoot, `renderers/${mode}/render-${mode}.mjs`),
        path.join(skillRoot, 'examples', example), output]);
    }
    artifacts[mode] = output;
  }
  const browser = new ChromeVisualBrowser(chromePath);
  const records = [];
  try {
    const session = await browser.sessionPromise;
    await browser.cdp.send('Browser.setDownloadBehavior', { behavior: 'deny' });
    const send = (method, params = {}) => browser.cdp.send(method, params, session);
    async function evaluate(expression, awaitPromise = false) {
      const result = await send('Runtime.evaluate', { expression, awaitPromise, returnByValue: true });
      assert.equal(result.exceptionDetails, undefined, result.exceptionDetails?.exception?.description);
      return result.result?.value;
    }
    await send('Page.addScriptToEvaluateOnNewDocument', {
      source: `window.readerTestErrors = [];
        addEventListener('error', function (event) { readerTestErrors.push(event.message); });
        addEventListener('unhandledrejection', function (event) { readerTestErrors.push(String(event.reason)); });`,
    });
    async function viewport(width, height) {
      await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
    }
    async function media(theme = 'dark', reduced = false, print = false) {
      await send('Emulation.setEmulatedMedia', { media: print ? 'print' : '', features: [
        { name: 'prefers-color-scheme', value: theme },
        { name: 'prefers-reduced-motion', value: reduced ? 'reduce' : 'no-preference' },
      ] });
    }
    async function stable() {
      await evaluate(`(async function () {
        for (var i = 0; i < 2; i += 1) {
          await Archify.readerLayout.whenStable();
          await Archify.viewerChromeLayout.whenStable();
        }
      })()`, true);
    }
    async function snapshot(label) {
      const value = await evaluate(`(function () {
        var html = document.documentElement;
        var diagram = document.querySelector('.diagram-container');
        var svg = diagram.querySelector(':scope > svg');
        return {
          active: Archify.readerLayout.active(), receipt: Archify.readerLayout.receipt(),
          width: html.style.getPropertyValue('--archify-reader-width'),
          layout: html.getAttribute('data-reader-layout'), overflow: html.getAttribute('data-reader-overflow'),
          wide: diagram.getAttribute('data-wide-diagram'), shape: html.getAttribute('data-diagram-shape'),
          geometry: ['viewBox', 'width', 'height'].map(function (name) { return svg.getAttribute(name); }),
          shellWidth: document.querySelector('.container').getBoundingClientRect().width,
          scrollHeight: Math.max(html.scrollHeight, document.body.scrollHeight),
          innerWidth: innerWidth, innerHeight: innerHeight,
          theme: html.getAttribute('data-theme'), reduced: matchMedia('(prefers-reduced-motion: reduce)').matches,
          errors: window.readerTestErrors,
          externalResources: performance.getEntriesByType('resource').map(function (entry) { return entry.name; }).filter(function (name) { return /^https?:/.test(name); })
        };
      })()`);
      assert.deepEqual(value.errors, [], `${label}: uncaught Viewer errors`);
      assert.deepEqual(value.externalResources, [], `${label}: external runtime assets`);
      records.push({ label, ...value });
      return value;
    }
    async function load(file, { width = 1440, height = 900, theme = 'dark', reduced = false, query = '', print = false, waitForLayout = true } = {}) {
      await viewport(width, height);
      await media(theme, reduced, print);
      const loaded = browser.cdp.waitFor('Page.loadEventFired', session);
      const result = await send('Page.navigate', { url: pathToFileURL(file).href + `?theme=${theme}${query}` });
      assert.equal(result.errorText, undefined, result.errorText);
      await loaded;
      assert.deepEqual(await evaluate('window.readerTestErrors'), [], 'Viewer initialization');
      if (waitForLayout) await stable();
    }
    function variant(name, { ratio, beforeViewer = '' } = {}) {
      let html = fs.readFileSync(artifacts.architecture, 'utf8');
      if (ratio !== undefined) assert.match(html, /<svg\b[^>]*\bviewBox="[^"]+"/, 'Reader viewBox fixture anchor');
      if (beforeViewer) assert.ok(html.includes('  <script>\n    var Archify = {};'), 'Reader setup fixture anchor');
      if (ratio !== undefined) html = html.replace(/(<svg\b[^>]*\bviewBox=")[^"]+(")/, (_, start, end) => `${start}0 0 ${ratio * 1000} 1000${end}`);
      if (beforeViewer) html = html.replace('  <script>\n    var Archify = {};', () => `  <script>${beforeViewer}</script>\n  <script>\n    var Archify = {};`);
      const file = path.join(scratch, `${name}.html`);
      fs.writeFileSync(file, html);
      return file;
    }
    function inactive(state, wide = true) {
      assert.equal(state.active, false);
      assert.equal(state.width, '');
      assert.equal(state.layout, null);
      assert.equal(state.overflow, null);
      assert.equal(state.receipt.width, 0);
      assert.equal(state.wide, wide ? 'true' : null);
      assert.equal(state.shape, wide ? 'wide' : null);
    }


    await t.test('ratio and desktop thresholds preserve shape while clearing temporary state', async () => {
      for (const ratio of [1.549, 1.55, 1.551]) {
        await load(variant(`ratio-${ratio}`, { ratio }));
        const before = await snapshot(`ratio-${ratio}`);
        assert.equal(before.active, ratio >= 1.55);
        if (ratio < 1.55) inactive(before, false);
        for (const width of [1023, 1024, 1025, 1023, 1440]) {
          await viewport(width, 900);
          await stable();
          const state = await snapshot(`ratio-${ratio}-width-${width}`);
          assert.deepEqual(state.geometry, before.geometry);
          if (ratio >= 1.55 && width >= 1024) assert.equal(state.active, true);
          else inactive(state, ratio >= 1.55);
        }
      }
    });

    const wide = variant('wide', { ratio: 3 });
    await t.test('desktop budgets, extreme content and limited horizontal space preserve geometry', async () => {
      for (const [width, height] of [[1440, 900], [1600, 1000], [1920, 1080], [2048, 1320]]) {
        await load(wide, { width, height });
        const state = await snapshot(`desktop-${width}x${height}`);
        assert.equal(state.active, true);
        assert.ok(state.receipt.width >= 960 && state.receipt.width <= Math.min(width, 1920));
      }
      await load(wide, { width: 2048, height: 3000 });
      assert.equal((await snapshot('maximum-width')).receipt.width, 1920);
      await evaluate(`document.body.style.paddingLeft = '100px'; document.body.style.paddingRight = '100px'`);
      await viewport(1024, 900);
      await stable();
      assert.equal((await snapshot('available-width-below-floor')).receipt.width, 824);
      await load(wide, { width: 1440, height: 300 });
      const geometry = (await snapshot('short-window')).geometry;
      await evaluate(`document.querySelector('.header').style.minHeight = '1000px';
        document.querySelector('.cards').innerHTML = '<div style="height:1200px">Long content</div>'`);
      await stable();
      const overflow = await snapshot('long-content');
      assert.equal(overflow.receipt.width, 960);
      assert.equal(overflow.overflow, 'authored');
      assert.ok(overflow.scrollHeight > overflow.innerHeight);
      assert.deepEqual(overflow.geometry, geometry);
    });

    await t.test('font readiness gates sampling and pending-frame timeout remains explicit', async () => {
      const delayedFonts = variant('delayed-fonts', { ratio: 3, beforeViewer: `
        window.readerTestOriginalFonts = document.fonts;
        Object.defineProperty(document, 'fonts', { configurable: true, value: {
          ready: new Promise(function (resolve) { window.readerTestReleaseFonts = resolve; })
        } });
      ` });
      await load(delayedFonts, { waitForLayout: false });
      const fontGate = await evaluate(`(async function () {
        var resolved = false;
        var ready = Archify.readerLayout.whenStable().then(function () { resolved = true; });
        document.querySelector('.cards').style.minHeight = '450px';
        await new Promise(function (resolve) { requestAnimationFrame(function () { requestAnimationFrame(resolve); }); });
        var beforeReady = resolved;
        readerTestReleaseFonts();
        await ready;
        Object.defineProperty(document, 'fonts', { configurable: true, value: readerTestOriginalFonts });
        return { beforeReady: beforeReady, afterReady: resolved };
      })()`, true);
      assert.deepEqual(fontGate, { beforeReady: false, afterReady: true });
      await stable();
      assert.equal((await snapshot('delayed-fonts-and-content')).active, true);
      await load(wide);
      const result = await evaluate(`(async function () {
        var original = document.fonts;
        var release;
        var scheduled = 0;
        Object.defineProperty(document, 'fonts', { configurable: true, value: { ready: new Promise(function (resolve) { release = resolve; }) } });
        try {
          var waiting = Archify.waitForStableLayout({ maximumFrames: 1, schedule: function () { scheduled += 1; }, pending: function () { return true; } });
          var observed = waiting.then(function () { return 'unexpected success'; }, function (error) { return error.message; });
          await new Promise(function (resolve) { requestAnimationFrame(function () { requestAnimationFrame(resolve); }); });
          var beforeReady = scheduled;
          release();
          return { beforeReady: beforeReady, outcome: await observed, afterReady: scheduled };
        } finally { Object.defineProperty(document, 'fonts', { configurable: true, value: original }); }
      })()`, true);
      assert.equal(result.beforeReady, 0);
      assert.equal(result.afterReady, 1);
      assert.match(result.outcome, /did not reach stable dimensions/);
      await stable();
    });
  } finally {
    if (evidence) fs.writeFileSync(path.join(evidence, 'reader-observations.json'), `${JSON.stringify(records, null, 2)}\n`);
    await browser.close();
    fs.rmSync(scratch, { recursive: true, force: true });
  }
});
