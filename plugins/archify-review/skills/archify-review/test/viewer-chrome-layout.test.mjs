import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

import { ChromeVisualBrowser, findChrome } from '../bin/visual-check.mjs';
import { MIN_PROJECTED_NODE_TEXT_PX } from '../renderers/shared/desktop-readability.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const skillRoot = path.resolve(__dirname, '..');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-viewer-chrome-layout-'));
const chromePath = process.env.ARCHIFY_CHROME ? findChrome() : null;

const CASES = {
  architecture: 'web-app.architecture.json',
};

function render(mode, example) {
  const output = path.join(tmp, `${mode}.html`);
  execFileSync(process.execPath, [
    path.join(skillRoot, 'bin', 'archify.mjs'),
    'render',
    mode,
    path.join(skillRoot, 'examples', example),
    output,
  ]);
  return output;
}

function renderWithoutLegend() {
  const source = JSON.parse(fs.readFileSync(
    path.join(skillRoot, 'examples', CASES.architecture),
    'utf8',
  ));
  source.meta = { ...source.meta, legend: { mode: 'hidden' } };
  const input = path.join(tmp, 'architecture-no-legend.json');
  const output = path.join(tmp, 'architecture-no-legend.html');
  fs.writeFileSync(input, `${JSON.stringify(source, null, 2)}\n`);
  execFileSync(process.execPath, [
    path.join(skillRoot, 'bin', 'archify.mjs'),
    'render',
    'architecture',
    input,
    output,
  ]);
  return output;
}

function canonicalSvg(html) {
  return html.match(/<svg\b[\s\S]*?<\/svg>/)?.[0] || '';
}

async function evaluate(browser, sessionId, expression, awaitPromise = false) {
  const response = await browser.cdp.send('Runtime.evaluate', {
    expression,
    awaitPromise,
    returnByValue: true,
  }, sessionId);
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.exception?.description
      || response.exceptionDetails.text
      || 'Runtime.evaluate failed');
  }
  return response.result?.value;
}

async function waitForLayout(browser, sessionId) {
  return evaluate(browser, sessionId, `(function () {
    var fontsReady = document.fonts && document.fonts.ready
      ? document.fonts.ready.catch(function () {})
      : Promise.resolve();
    return fontsReady.then(function () {
      return new Promise(function (resolve, reject) {
        var previous = '';
        var stableFrames = 0;
        var sampledFrames = 0;
        function rect(element) {
          if (!element) return 'missing';
          var value = element.getBoundingClientRect();
          return [value.left, value.top, value.right, value.bottom].map(function (entry) {
            return Math.round(entry * 100) / 100;
          }).join(',');
        }
        function sample() {
          sampledFrames += 1;
          var container = document.querySelector('.diagram-container');
          var current = [
            rect(container),
            rect(container && container.querySelector(':scope > svg')),
            rect(document.querySelector('.diagram-nav')),
            rect(document.querySelector('[data-legend]')),
            rect(document.getElementById('semantic-lens')),
            container ? getComputedStyle(container).getPropertyValue('--archify-nav-reserve') : ''
          ].join('|');
          if (current === previous) stableFrames += 1;
          else {
            previous = current;
            stableFrames = 0;
          }
          /* Final-artifact tests cannot inspect private scheduler flags. Eight
             equal frames cover the public three-frame contract plus any
             reader/viewer handoff queued after a ResizeObserver callback. */
          if (stableFrames >= 8) {
            resolve({ stable: true, snapshot: current, sampledFrames: sampledFrames });
            return;
          }
          if (sampledFrames >= 240) {
            reject(new Error('Final Viewer geometry did not stabilize.'));
            return;
          }
          requestAnimationFrame(sample);
        }
        requestAnimationFrame(sample);
      });
    });
  })()`, true);
}

async function finalGeometry(browser, sessionId) {
  return evaluate(browser, sessionId, `(function () {
    function area(a, b) {
      if (!a || !b || !a.width || !a.height || !b.width || !b.height) return 0;
      return Math.max(0, Math.min(a.right, b.right) - Math.max(a.left, b.left)) *
        Math.max(0, Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top));
    }
    var container = document.querySelector('.diagram-container');
    var legend = document.querySelector('[data-legend]');
    var nav = document.querySelector('.diagram-nav');
    var svg = container && container.querySelector(':scope > svg');
    var lens = document.getElementById('semantic-lens');
    var passport = document.getElementById('focus-chip');
    var chromeReceipt = window.Archify && Archify.viewerChromeLayout
      && typeof Archify.viewerChromeLayout.receipt === 'function'
      ? Archify.viewerChromeLayout.receipt()
      : null;
    var viewBox = svg && svg.viewBox && svg.viewBox.baseVal;
    var projectedScale = svg && viewBox && viewBox.width > 0
      ? Math.min(1, svg.getBoundingClientRect().width / viewBox.width)
      : 0;
    var minimumProjectedNodeTextPx = null;
    if (svg && projectedScale > 0) {
      Array.from(svg.querySelectorAll('text[data-node-label], text[data-boundary-label], text[data-detail="context"]')).forEach(function (text) {
        if (text.hasAttribute('data-detail') && !text.closest('[data-node-id]')) return;
        var sourceFontPx = parseFloat(text.getAttribute('font-size') || '');
        if (!Number.isFinite(sourceFontPx)) return;
        var projectedFontPx = sourceFontPx * projectedScale;
        if (minimumProjectedNodeTextPx == null || projectedFontPx < minimumProjectedNodeTextPx) {
          minimumProjectedNodeTextPx = projectedFontPx;
        }
      });
    }
    var legendRect = legend && getComputedStyle(legend).display !== 'none' ? legend.getBoundingClientRect() : null;
    var navRect = nav && getComputedStyle(nav).display !== 'none' ? nav.getBoundingClientRect() : null;
    var stageRect = window.Archify && Archify.viewerChromeLayout
      && typeof Archify.viewerChromeLayout.stageRect === 'function'
      ? Archify.viewerChromeLayout.stageRect()
      : null;
    var lensRect = lens && !lens.hidden && getComputedStyle(lens).display !== 'none' ? lens.getBoundingClientRect() : null;
    var passportRect = passport && !passport.hidden && getComputedStyle(passport).display !== 'none' ? passport.getBoundingClientRect() : null;
    var stageDockIntersectionArea = area(stageRect, navRect);
    var semanticDockIntersectionArea = stageDockIntersectionArea > 0 && navRect && svg
      ? Array.from(svg.querySelectorAll('[data-node-id]')).reduce(function (maximum, node) {
          return Math.max(maximum, area(node.getBoundingClientRect(), navRect));
        }, 0)
      : 0;
    return {
      reserve: parseFloat(getComputedStyle(container).getPropertyValue('--archify-nav-reserve')) || 0,
      receiptReserve: chromeReceipt ? chromeReceipt.reserve : null,
      receiptEligible: chromeReceipt ? chromeReceipt.eligible : null,
      receiptStageIntersectionArea: chromeReceipt ? chromeReceipt.stageIntersectionArea : null,
      minimumProjectedNodeTextPx: minimumProjectedNodeTextPx,
      stageGap: navRect && stageRect ? navRect.top - stageRect.bottom : null,
      dockStageIntersectionArea: stageDockIntersectionArea,
      legendDockIntersectionArea: stageDockIntersectionArea > 0 ? area(legendRect, navRect) : 0,
      semanticDockIntersectionArea: semanticDockIntersectionArea,
      legendLensIntersectionArea: area(legendRect, lensRect),
      navLensIntersectionArea: area(navRect, lensRect),
      legendPassportIntersectionArea: area(legendRect, passportRect),
      navPassportIntersectionArea: area(navRect, passportRect),
      legendRect: legendRect ? { left: legendRect.left, right: legendRect.right, top: legendRect.top, bottom: legendRect.bottom } : null,
      navRect: navRect ? { left: navRect.left, right: navRect.right, top: navRect.top, bottom: navRect.bottom } : null,
      passportRect: passportRect ? { left: passportRect.left, right: passportRect.right, top: passportRect.top, bottom: passportRect.bottom } : null,
      hasLegend: Boolean(legendRect && legendRect.width && legendRect.height),
      scrollWidth: document.documentElement.scrollWidth,
      scrollHeight: document.documentElement.scrollHeight,
      innerWidth: window.innerWidth,
      innerHeight: window.innerHeight,
      containerBottom: container ? container.getBoundingClientRect().bottom : null,
      containerHeight: container ? container.clientHeight : null,
      receiptGap: chromeReceipt ? chromeReceipt.gap : null,
      navBottom: navRect ? navRect.bottom : null
    };
  })()`);
}

async function edgePaintHitsUnderDock(browser, sessionId, selector) {
  return evaluate(browser, sessionId, `(function () {
    var edge = document.querySelector(${JSON.stringify(selector)});
    var nav = document.querySelector('.diagram-nav');
    if (!edge || !nav || typeof edge.getTotalLength !== 'function') return null;
    var navRect = nav.getBoundingClientRect();
    var matrix = edge.getScreenCTM();
    var length = edge.getTotalLength();
    var hits = [];
    for (var offset = 0; offset <= length; offset += 0.25) {
      var point = edge.getPointAtLength(offset).matrixTransform(matrix);
      if (point.x < navRect.left || point.x > navRect.right || point.y < navRect.top || point.y > navRect.bottom) continue;
      if (document.elementsFromPoint(point.x, point.y).includes(edge)) {
        hits.push({ x: point.x, y: point.y });
        if (hits.length >= 5) break;
      }
    }
    return hits;
  })()`);
}

async function load(browser, artifactPath, { width = 1440, height = 900, query = '' } = {}) {
  const sessionId = await browser.sessionPromise;
  await browser.cdp.send('Emulation.setDeviceMetricsOverride', {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: false,
  }, sessionId);
  const loaded = browser.cdp.waitFor('Page.loadEventFired', sessionId);
  const navigation = await browser.cdp.send('Page.navigate', {
    url: pathToFileURL(artifactPath).href + query,
  }, sessionId);
  if (navigation.errorText) throw new Error(`Chrome navigation failed: ${navigation.errorText}`);
  await loaded;
  await evaluate(browser, sessionId, `document.documentElement.setAttribute('data-motion', 'still')`);
  await waitForLayout(browser, sessionId);
  return sessionId;
}

test('the public CLI gives all typed renderers one final Viewer contract', () => {
  const directChildSvg = /<div class="diagram-container"[^>]*>\s*<svg\b/;
  assert.doesNotMatch(
    '<div class="diagram-container"><section></section><svg>',
    directChildSvg,
    'the direct-child assertion must not cross an intervening wrapper',
  );
  for (const [mode, example] of Object.entries(CASES)) {
    const output = render(mode, example);
    const html = fs.readFileSync(output, 'utf8');
    assert.match(html, /class="[^"]*\bdiagram-nav\b[^"]*"/, mode);
    assert.match(html, /data-legend/, mode);
    assert.match(html, directChildSvg, `${mode} keeps the SVG as a direct child`);
    assert.doesNotMatch(html, /class="diagram-stage"/, `${mode} does not re-nest the exported SVG`);
    assert.doesNotMatch(canonicalSvg(html), /nav-safe-rail|archify-nav-reserve|viewerChromeLayout/, mode);
    execFileSync(process.execPath, [path.join(skillRoot, 'scripts', 'check-render-output.mjs'), output]);
  }
});

test('Viewer chrome remains outside the canonical SVG export boundary', () => {
  const html = fs.readFileSync(render('architecture', CASES.architecture), 'utf8');
  const svg = canonicalSvg(html);
  assert.match(svg, /data-legend/);
  assert.doesNotMatch(svg, /diagram-nav|data-nav-stage-rail|viewerChromeLayout/);
});

test('an artifact with no Legend still receives the desktop stage rail', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async () => {
  const browser = new ChromeVisualBrowser(chromePath);
  try {
    const sessionId = await load(browser, renderWithoutLegend());
    const receipt = await finalGeometry(browser, sessionId);

    assert.equal(receipt.hasLegend, false, JSON.stringify(receipt));
    assert.ok(receipt.reserve > 0, JSON.stringify(receipt));
    assert.ok(receipt.stageGap >= 9, JSON.stringify(receipt));
    assert.ok(receipt.navBottom <= receipt.containerBottom + 0.5, JSON.stringify(receipt));
  } finally {
    await browser.close();
  }
});

test('Dock Safe Rail resolves a forced Legend collision across the shared diagram viewer', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async () => {
  const browser = new ChromeVisualBrowser(chromePath);
  try {
    for (const [mode, example] of Object.entries(CASES)) {
      const sessionId = await load(browser, render(mode, example));
      const setup = await evaluate(browser, sessionId, `(function () {
        var nav = document.querySelector('.diagram-nav');
        var legendElement = document.querySelector('[data-legend]');
        if (!legendElement) {
          return { noLegend: true };
        }
        var initialLegend = legendElement.getBoundingClientRect();
        var containerRect = document.querySelector('.diagram-container').getBoundingClientRect();
        nav.style.right = 'auto';
        nav.style.left = Math.max(0, initialLegend.left - containerRect.left) + 'px';
        nav.style.bottom = Math.max(0, containerRect.bottom - initialLegend.bottom) + 'px';
        nav.style.width = Math.max(240, initialLegend.width) + 'px';
        window.dispatchEvent(new Event('resize'));
        return { noLegend: false };
      })()`);

      if (setup.noLegend) {
        const receipt = await finalGeometry(browser, sessionId);
        assert.ok(receipt.reserve > 0, mode);
        assert.ok(receipt.stageGap >= 9, mode);
        continue;
      }
      await waitForLayout(browser, sessionId);
      const receipt = await finalGeometry(browser, sessionId);
      assert.ok(receipt.reserve > 0, `${mode}: ${JSON.stringify(receipt)}`);
      assert.equal(receipt.legendDockIntersectionArea, 0, `${mode}: ${JSON.stringify(receipt)}`);
      assert.ok(receipt.stageGap >= 9, `${mode}: ${JSON.stringify(receipt)}`);
    }
  } finally {
    await browser.close();
  }
});

test('a real 5px Legend gap keeps the stage rail and Legend clear', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async () => {
  const browser = new ChromeVisualBrowser(chromePath);
  try {
    const sessionId = await load(browser, render('architecture', CASES.architecture));
    await evaluate(browser, sessionId, `(function () {
      var container = document.querySelector('.diagram-container');
      var legend = document.querySelector('[data-legend]');
      var nav = document.querySelector('.diagram-nav');
      var legendRect = legend.getBoundingClientRect();
      var containerRect = container.getBoundingClientRect();
      nav.style.right = 'auto';
      nav.style.left = (legendRect.right - containerRect.left + 5) + 'px';
      nav.style.bottom = Math.max(0, containerRect.bottom - legendRect.bottom) + 'px';
      window.dispatchEvent(new Event('resize'));
    })()`);
    await waitForLayout(browser, sessionId);
    const receipt = await finalGeometry(browser, sessionId);

    assert.equal(receipt.legendDockIntersectionArea, 0, JSON.stringify(receipt));
    assert.ok(receipt.reserve > 0, JSON.stringify(receipt));
    assert.ok(receipt.stageGap >= 9, JSON.stringify(receipt));
  } finally {
    await browser.close();
  }
});

test('Presentation keeps its visible Dock clear of a colliding Legend', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async () => {
  const browser = new ChromeVisualBrowser(chromePath);
  try {
    const sessionId = await load(browser, render('architecture', CASES.architecture), { query: '?present=1' });
    await evaluate(browser, sessionId, `(function () {
      var container = document.querySelector('.diagram-container');
      var legend = document.querySelector('[data-legend]');
      var nav = document.querySelector('.diagram-nav');
      var legendRect = legend.getBoundingClientRect();
      var containerRect = container.getBoundingClientRect();
      nav.style.right = 'auto';
      nav.style.left = Math.max(0, legendRect.left - containerRect.left) + 'px';
      nav.style.bottom = Math.max(0, containerRect.bottom - legendRect.bottom) + 'px';
      nav.style.width = Math.max(240, legendRect.width) + 'px';
      window.dispatchEvent(new Event('resize'));
    })()`);
    await waitForLayout(browser, sessionId);
    const receipt = await finalGeometry(browser, sessionId);

    assert.equal(receipt.legendDockIntersectionArea, 0, JSON.stringify(receipt));
    assert.ok(receipt.reserve > 0, JSON.stringify(receipt));
  } finally {
    await browser.close();
  }
});

test('Semantic Lens protects the final Legend and Dock rectangles', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async () => {
  const browser = new ChromeVisualBrowser(chromePath);
  try {
    const sessionId = await load(browser, render('architecture', CASES.architecture), { width: 1440, height: 900 });
    await evaluate(browser, sessionId, `document.getElementById('btn-semantic-lens').click()`);
    await waitForLayout(browser, sessionId);
    const receipt = await finalGeometry(browser, sessionId);
    assert.equal(receipt.legendLensIntersectionArea, 0, JSON.stringify(receipt));
    assert.equal(receipt.navLensIntersectionArea, 0, JSON.stringify(receipt));
  } finally {
    await browser.close();
  }
});

test('Passport, Legend, and Dock remain mutually clear on desktop and narrow viewports', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async () => {
  const browser = new ChromeVisualBrowser(chromePath);
  try {
    const artifact = render('architecture', CASES.architecture);
    for (const viewport of [
      { width: 1440, height: 900, label: 'desktop' },
      { width: 390, height: 600, label: 'narrow' },
    ]) {
      const sessionId = await load(browser, artifact, viewport);
      await evaluate(browser, sessionId, `(function () {
        var container = document.querySelector('.diagram-container');
        window.scrollTo(0, Math.max(0, container.offsetTop));
        Archify.focus.set('lb', { toggle: false });
        window.dispatchEvent(new Event('resize'));
      })()`);
      await waitForLayout(browser, sessionId);
      const receipt = await finalGeometry(browser, sessionId);
      const message = viewport.label + ': ' + JSON.stringify(receipt);

      assert.equal(receipt.legendDockIntersectionArea, 0, message);
      assert.equal(receipt.legendPassportIntersectionArea, 0, message);
      assert.equal(receipt.navPassportIntersectionArea, 0, message);
    }
  } finally {
    await browser.close();
  }
});

test('mobile, embed, and print keep zero reserve while hidden Legends retain the stage rail', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async () => {
  const browser = new ChromeVisualBrowser(chromePath);
  try {
    let sessionId = await load(browser, render('architecture', CASES.architecture), { width: 720, height: 900 });
    let receipt = await finalGeometry(browser, sessionId);
    assert.equal(receipt.reserve, 0, `mobile: ${JSON.stringify(receipt)}`);
    assert.equal(receipt.legendDockIntersectionArea, 0, `mobile: ${JSON.stringify(receipt)}`);

    sessionId = await load(browser, render('architecture', CASES.architecture), { query: '?embed=1' });
    receipt = await finalGeometry(browser, sessionId);
    assert.equal(receipt.reserve, 0, `embed: ${JSON.stringify(receipt)}`);

    sessionId = await load(browser, render('architecture', CASES.architecture));
    await evaluate(browser, sessionId, `(function () {
      document.querySelector('[data-legend]').hidden = true;
      window.dispatchEvent(new Event('resize'));
    })()`);
    await waitForLayout(browser, sessionId);
    receipt = await finalGeometry(browser, sessionId);
    assert.ok(receipt.reserve > 0, `hidden: ${JSON.stringify(receipt)}`);
    assert.ok(receipt.stageGap >= 9, `hidden: ${JSON.stringify(receipt)}`);

    await browser.cdp.send('Emulation.setEmulatedMedia', { media: 'print' }, sessionId);
    await waitForLayout(browser, sessionId);
    receipt = await finalGeometry(browser, sessionId);
    assert.equal(receipt.reserve, 0, `print: ${JSON.stringify(receipt)}`);
  } finally {
    await browser.close();
  }
});


test('Chrome Layout preserves scheduling, mode restoration and Reader handoffs', {
  skip: chromePath ? false : 'Set ARCHIFY_CHROME to run the real browser regression.',
}, async (t) => {
  const browser = new ChromeVisualBrowser(chromePath);
  t.after(() => browser.close());
  const evidence = process.env.ARCHIFY_CHROME_LAYOUT_EVIDENCE;
  if (evidence) fs.mkdirSync(evidence, { recursive: true });
  const observations = [];
  const file = render('architecture', CASES.architecture);
  const session = await browser.sessionPromise;
  const send = (method, params = {}) => browser.cdp.send(method, params, session);
  const run = (expression, awaitPromise = false) => evaluate(browser, session, expression, awaitPromise);
  await send('Page.addScriptToEvaluateOnNewDocument', {
    source: `window.chromeLayoutErrors = [];
      addEventListener('error', e => chromeLayoutErrors.push(e.message));
      addEventListener('unhandledrejection', e => chromeLayoutErrors.push(String(e.reason)));`,
  });
  async function resize(width, height = 900) {
    await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
    await waitForLayout(browser, session);
  }
  async function state(label) {
    // Read DOM first, without receipt(), measure() or public stability methods.
    // This ensures those methods cannot make a missed automatic event pass.
    const raw = await run(`(() => {
      const html = document.documentElement;
      const container = document.querySelector('.diagram-container');
      const svg = container.querySelector(':scope > svg');
      return {
        reserve: parseFloat(container.style.getPropertyValue('--archify-nav-reserve')) || 0,
        rootRail: html.getAttribute('data-nav-stage-rail'), rail: container.getAttribute('data-nav-stage-rail'),
        reader: html.getAttribute('data-reader-layout'), width: innerWidth,
        viewBox: svg.getAttribute('viewBox'),
        errors: chromeLayoutErrors,
        external: performance.getEntriesByType('resource').map(e => e.name).filter(n => /^https?:/.test(n))
      };
    })()`);
    assert.deepEqual(raw.errors, [], label);
    assert.deepEqual(raw.external, [], label);
    const geometry = await finalGeometry(browser, session);
    assert.equal(raw.reserve, geometry.receiptReserve, `${label}: observation must not repair stale state`);
    assert.equal(raw.rootRail, raw.reserve ? 'true' : null, label);
    assert.equal(raw.rail, raw.rootRail, label);
    observations.push({ label, ...raw, geometry });
    return { ...raw, geometry };
  }
  function zero(value) {
    assert.equal(value.reserve, 0);
    assert.equal(value.rail, null);
    assert.equal(value.rootRail, null);
  }
  function clearStage(value) {
    assert.equal(value.geometry.dockStageIntersectionArea, 0, JSON.stringify(value));
    assert.ok(value.geometry.stageGap >= 9, JSON.stringify(value));
  }
  function variant(name, source) {
    const original = fs.readFileSync(file, 'utf8');
    const html = original.replace('  <script>\n    var Archify = {};',
      () => `  <script>${source}</script>\n  <script>\n    var Archify = {};`);
    assert.notEqual(html, original);
    const output = path.join(tmp, `chrome-${name}.html`);
    fs.writeFileSync(output, html);
    return output;
  }
  try {
    await t.test('automatic resize crosses Chrome and Reader thresholds without reserve accumulation', async () => {
      await load(browser, file);
      const initial = await state('threshold-initial');
      for (const width of [719, 720, 721, 1023, 1024, 1025, 720, 1440]) {
        await resize(width);
        const current = await state(`threshold-${width}-${observations.length}`);
        assert.equal(current.geometry.receiptEligible, width > 720);
        assert.equal(current.reader, width >= 1024 ? 'adaptive' : null);
        assert.equal(current.viewBox, initial.viewBox);
        if (width <= 720) zero(current);
        else clearStage(current);
        if (width === 1440) assert.equal(current.reserve, initial.reserve);
      }
    });

    await t.test('automatic observer callbacks handle navigation size, visibility and content changes', async () => {
      await load(browser, file);
      const initial = await state('observer-initial');
      await run(`document.querySelector('.diagram-nav').style.display = 'none'`);
      await waitForLayout(browser, session);
      zero(await state('observer-nav-hidden'));
      await run(`document.querySelector('.diagram-nav').style.display = ''`);
      await waitForLayout(browser, session);
      const restored = await state('observer-nav-restored');
      assert.equal(restored.reserve, initial.reserve);
      await run(`document.querySelector('.diagram-nav').style.height = '120px'`);
      await waitForLayout(browser, session);
      const taller = await state('observer-nav-taller');
      assert.ok(taller.reserve > initial.reserve);
      clearStage(taller);
      // A legend mutation drives the existing MutationObserver. Font/preset
      // rules remain the production rules; no public layout method is invoked.
      await run(`document.querySelector('[data-legend]').setAttribute('transform', 'translate(0,-20)')`);
      await waitForLayout(browser, session);
      clearStage(await state('observer-legend-changed'));
      await run(`document.querySelector('.diagram-nav').style.height = ''; window.dispatchEvent(new Event('resize'))`);
      await waitForLayout(browser, session);
      assert.equal((await state('observer-returned')).reserve, initial.reserve);
      await run(`document.querySelector('.diagram-nav').style.bottom = '-200px'; window.dispatchEvent(new Event('resize'))`);
      await waitForLayout(browser, session);
      zero(await state('no-reserve-needed'));
    });



  } finally {
    if (evidence) fs.writeFileSync(path.join(evidence, 'observations.json'), JSON.stringify(observations, null, 2) + '\n');
  }
});

process.on('exit', () => fs.rmSync(tmp, { recursive: true, force: true }));
