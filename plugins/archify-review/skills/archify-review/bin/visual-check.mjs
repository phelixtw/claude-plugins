import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import {
  DESKTOP_READABILITY_VIEWPORT,
  MIN_PROJECTED_NODE_TEXT_PX,
} from '../renderers/shared/desktop-readability.mjs';

export const VISUAL_CHECK_VIEWPORTS = Object.freeze([
  DESKTOP_READABILITY_VIEWPORT,
  Object.freeze({ width: 1600, height: 1000 }),
  Object.freeze({ width: 1920, height: 1080 }),
  Object.freeze({ width: 2048, height: 1320 }),
]);

const CAPTURE_VIEWPORTS = Object.freeze([
  VISUAL_CHECK_VIEWPORTS[0],
  VISUAL_CHECK_VIEWPORTS[VISUAL_CHECK_VIEWPORTS.length - 1],
]);
const THEMES = Object.freeze(['light', 'dark']);
const EXIT = Object.freeze({ pass: 0, fail: 1, skipped: 2 });
export const CHROME_NO_SANDBOX_ENV = 'ARCHIFY_CHROME_NO_SANDBOX';

function sha256(buffer) {
  return createHash('sha256').update(buffer).digest('hex');
}

function htmlEscape(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[char]);
}

function safeUnlink(file) {
  try {
    fs.rmSync(file, { force: true });
  } catch {
    // A stale optional sidecar must never make the delivered HTML mutable.
  }
}

function writeAtomic(file, contents) {
  const temporary = `${file}.tmp-${process.pid}`;
  try {
    fs.writeFileSync(temporary, contents, { flag: 'w' });
    fs.renameSync(temporary, file);
  } finally {
    safeUnlink(temporary);
  }
}

function screenshotKey(width, height, theme) {
  return `${width}x${height}:${theme}`;
}

// A report viewer is shared by every ticket, so its sidecars are named after the report id.
export function sidecarPaths(artifactPath, report = null) {
  const artifact = path.resolve(artifactPath);
  const stem = report ? path.join(path.dirname(artifact), report) : artifact.replace(/\.html?$/i, '');
  const base = `${stem}.visual-check`;
  const screenshots = CAPTURE_VIEWPORTS.flatMap(({ width, height }) => THEMES.map((theme) => ({
    width,
    height,
    theme,
    path: `${base}.${width}x${height}.${theme}.png`,
  })));
  return {
    base,
    receipt: `${base}.json`,
    contactSheet: `${base}.html`,
    screenshots,
  };
}

function cleanupCaptureSidecars(paths) {
  safeUnlink(paths.contactSheet);
  for (const screenshot of paths.screenshots) safeUnlink(screenshot.path);
}

function executable(file, platform = process.platform) {
  if (!file) return null;
  try {
    fs.accessSync(file, platform === 'win32' ? fs.constants.F_OK : fs.constants.X_OK);
    return path.resolve(file);
  } catch {
    return null;
  }
}

function findOnPath(command, env, platform) {
  const directories = String(env.PATH || '').split(path.delimiter).filter(Boolean);
  const extensions = platform === 'win32'
    ? String(env.PATHEXT || '.EXE;.CMD;.BAT;.COM').split(';').filter(Boolean)
    : [''];
  for (const directory of directories) {
    for (const extension of extensions) {
      const candidate = path.join(directory, `${command}${extension}`);
      const resolved = executable(candidate, platform);
      if (resolved) return resolved;
    }
  }
  return null;
}

export function findChrome({ env = process.env, platform = process.platform } = {}) {
  if (Object.prototype.hasOwnProperty.call(env, 'ARCHIFY_CHROME')) {
    return executable(env.ARCHIFY_CHROME, platform);
  }

  const fixed = [];
  const commands = [];
  if (platform === 'darwin') {
    fixed.push(
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      '/Applications/Chromium.app/Contents/MacOS/Chromium',
    );
  } else if (platform === 'win32') {
    for (const root of [env.PROGRAMFILES, env['PROGRAMFILES(X86)'], env.LOCALAPPDATA].filter(Boolean)) {
      fixed.push(
        path.join(root, 'Google', 'Chrome', 'Application', 'chrome.exe'),
        path.join(root, 'Chromium', 'Application', 'chrome.exe'),
      );
    }
  } else {
    commands.push('google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser');
  }

  for (const candidate of fixed) {
    const resolved = executable(candidate, platform);
    if (resolved) return resolved;
  }
  for (const command of commands) {
    const resolved = findOnPath(command, env, platform);
    if (resolved) return resolved;
  }
  return null;
}

class PipeCdp {
  constructor(child, { failureDetails = () => '' } = {}) {
    this.child = child;
    this.failureDetails = failureDetails;
    this.nextId = 1;
    this.buffer = '';
    this.pending = new Map();
    this.waiters = [];
    this.writePipe = child.stdio[3];
    this.readPipe = child.stdio[4];
    this.readPipe.setEncoding('utf8');
    this.readPipe.on('data', (chunk) => this.consume(chunk));
    this.writePipe.on('error', (error) => this.failAll(this.failure('write pipe', error)));
    this.readPipe.on('error', (error) => this.failAll(this.failure('read pipe', error)));
    child.once('error', (error) => this.failAll(this.failure('process launch', error)));
    child.once('close', (code, signal) => {
      const ending = signal ? `signal ${signal}` : `exit code ${code}`;
      this.failAll(this.failure('process exit', new Error(`Chrome closed with ${ending}`)));
    });
  }

  failure(stage, error) {
    const code = error?.code ? ` [${error.code}]` : '';
    const details = this.failureDetails();
    return new Error([
      `Chrome DevTools ${stage} failed: ${error?.message || String(error)}${code}`,
      details,
    ].filter(Boolean).join('\n'));
  }

  consume(chunk) {
    this.buffer += chunk;
    let boundary;
    while ((boundary = this.buffer.indexOf('\0')) >= 0) {
      const raw = this.buffer.slice(0, boundary);
      this.buffer = this.buffer.slice(boundary + 1);
      if (!raw) continue;
      let message;
      try {
        message = JSON.parse(raw);
      } catch (error) {
        this.failAll(new Error(`Chrome DevTools returned invalid JSON: ${error.message}`));
        continue;
      }
      if (message.id) {
        const pending = this.pending.get(message.id);
        if (!pending) continue;
        clearTimeout(pending.timer);
        this.pending.delete(message.id);
        if (message.error) pending.reject(new Error(`${pending.method}: ${message.error.message}`));
        else pending.resolve(message.result || {});
        continue;
      }
      for (const waiter of [...this.waiters]) {
        if (waiter.method !== message.method) continue;
        if (waiter.sessionId && waiter.sessionId !== message.sessionId) continue;
        clearTimeout(waiter.timer);
        this.waiters.splice(this.waiters.indexOf(waiter), 1);
        waiter.resolve(message.params || {});
      }
    }
  }

  send(method, params = {}, sessionId = undefined, timeoutMs = 15000) {
    const id = this.nextId++;
    const message = { id, method, params };
    if (sessionId) message.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`${method}: timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      this.pending.set(id, { method, resolve, reject, timer });
      try {
        this.writePipe.write(`${JSON.stringify(message)}\0`, (error) => {
          if (error) this.failAll(this.failure('write pipe', error));
        });
      } catch (error) {
        this.failAll(this.failure('write pipe', error));
      }
    });
  }

  waitFor(method, sessionId, timeoutMs = 15000) {
    return new Promise((resolve, reject) => {
      const waiter = { method, sessionId, resolve, reject, timer: null };
      waiter.timer = setTimeout(() => {
        this.waiters.splice(this.waiters.indexOf(waiter), 1);
        reject(new Error(`${method}: event timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      this.waiters.push(waiter);
    });
  }

  failAll(error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    for (const waiter of this.waiters) {
      clearTimeout(waiter.timer);
      waiter.reject(error);
    }
    this.pending.clear();
    this.waiters = [];
  }
}

export function chromeVisualBrowserArgs(profileRoot, {
  env = process.env,
  getuid = typeof process.getuid === 'function' ? () => process.getuid() : null,
} = {}) {
  const args = [
    '--headless=new',
    '--remote-debugging-pipe',
    '--disable-gpu',
    '--hide-scrollbars',
    '--disable-background-networking',
    '--disable-component-update',
    '--disable-default-apps',
    '--disable-sync',
    '--metrics-recording-only',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-background-timer-throttling',
    '--disable-backgrounding-occluded-windows',
    '--disable-renderer-backgrounding',
    '--force-device-scale-factor=1',
    `--user-data-dir=${profileRoot}`,
    'about:blank',
  ];
  const rootUser = typeof getuid === 'function' && getuid() === 0;
  const sandboxOptOut = env?.[CHROME_NO_SANDBOX_ENV] === '1';
  if (rootUser || sandboxOptOut) args.unshift('--no-sandbox');
  return args;
}

async function evaluate(cdp, sessionId, expression, awaitPromise = false) {
  const response = await cdp.send('Runtime.evaluate', {
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

export class ChromeVisualBrowser {
  constructor(chromePath, {
    env = process.env,
    getuid = typeof process.getuid === 'function' ? () => process.getuid() : null,
    spawnImpl = spawn,
  } = {}) {
    this.profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'archify-visual-check-profile-'));
    this.stderr = '';
    const args = chromeVisualBrowserArgs(this.profileRoot, { env, getuid });
    this.child = spawnImpl(chromePath, args, { stdio: ['ignore', 'ignore', 'pipe', 'pipe', 'pipe'] });
    this.child.stderr.setEncoding('utf8');
    this.child.stderr.on('data', (chunk) => {
      this.stderr = `${this.stderr}${chunk}`.slice(-8000);
    });
    this.child.stderr.on('error', (error) => {
      this.stderr = `${this.stderr}\nChrome stderr stream failed: ${error.message}`.trim().slice(-8000);
    });
    this.cdp = new PipeCdp(this.child, {
      failureDetails: () => {
        const exit = this.child.signalCode
          ? `signal ${this.child.signalCode}`
          : this.child.exitCode == null ? 'still running' : `exit code ${this.child.exitCode}`;
        const stderr = this.stderr.trim();
        return [
          `Chrome process: ${exit}.`,
          stderr ? `Chrome stderr:\n${stderr}` : '',
        ].filter(Boolean).join('\n');
      },
    });
    this.sessionPromise = this.attach();
  }

  async attach() {
    const targets = await this.cdp.send('Target.getTargets');
    let target = targets.targetInfos?.find((item) => item.type === 'page');
    if (!target) {
      const created = await this.cdp.send('Target.createTarget', { url: 'about:blank' });
      target = { targetId: created.targetId };
    }
    const attached = await this.cdp.send('Target.attachToTarget', {
      targetId: target.targetId,
      flatten: true,
    });
    await this.cdp.send('Page.enable', {}, attached.sessionId);
    await this.cdp.send('Runtime.enable', {}, attached.sessionId);
    return attached.sessionId;
  }

  async inspect({ artifactPath, width, height, theme, screenshotPath, report = null }) {
    const sessionId = await this.sessionPromise;
    await this.cdp.send('Emulation.setDeviceMetricsOverride', {
      width,
      height,
      deviceScaleFactor: 1,
      mobile: false,
    }, sessionId);

    const url = new URL(pathToFileURL(artifactPath).href);
    url.searchParams.set('theme', theme);
    if (report) url.searchParams.set('report', report);
    const loaded = this.cdp.waitFor('Page.loadEventFired', sessionId);
    const navigation = await this.cdp.send('Page.navigate', { url: url.href }, sessionId);
    if (navigation.errorText) throw new Error(`Chrome navigation failed: ${navigation.errorText}`);
    await loaded;
    if (report) {
      const state = await evaluate(this.cdp, sessionId, 'document.documentElement.getAttribute("data-report-state")');
      if (state !== 'ready') throw new Error(`Report ${report} did not load (state ${state}).`);
    }
    await evaluate(this.cdp, sessionId, `(function () {
      document.documentElement.setAttribute('data-motion', 'still');
      var panel = document.querySelector('.diagram-container');
      if (panel) panel.setAttribute('data-detail-level', 'read');
      var fontsReady = document.fonts && document.fonts.ready
        ? document.fonts.ready.catch(function () {})
        : Promise.resolve();
      return fontsReady.then(function () {
        if (window.Archify && Archify.readerLayout && typeof Archify.readerLayout.whenStable === 'function') {
          return Archify.readerLayout.whenStable();
        }
      }).then(function () {
        if (window.Archify && Archify.viewerChromeLayout && typeof Archify.viewerChromeLayout.whenStable === 'function') {
          return Archify.viewerChromeLayout.whenStable();
        }
      }).then(function () {
        if (window.Archify && Archify.readerLayout && typeof Archify.readerLayout.whenStable === 'function') {
          return Archify.readerLayout.whenStable();
        }
      }).then(function () {
        if (window.Archify && Archify.viewerChromeLayout && typeof Archify.viewerChromeLayout.whenStable === 'function') {
          return Archify.viewerChromeLayout.whenStable();
        }
        return new Promise(function (resolve) {
          requestAnimationFrame(function () { requestAnimationFrame(resolve); });
        });
      });
    })()`, true);

    const metrics = await evaluate(this.cdp, sessionId, `(function () {
      var reader = document.querySelector('.container');
      var diagram = document.querySelector('.diagram-container');
      var svg = diagram && (
        diagram.querySelector(':scope > svg') ||
        diagram.querySelector(':scope > .diagram-stage > svg')
      );
      var stage = diagram && (diagram.querySelector(':scope > .diagram-stage') || svg);
      var legend = svg && svg.querySelector('[data-legend]');
      var navigationDock = diagram && diagram.querySelector('.diagram-nav');
      var viewBox = svg && svg.viewBox && svg.viewBox.baseVal;
      var diagramWidth = svg ? svg.getBoundingClientRect().width : 0;
      var viewBoxWidth = viewBox ? viewBox.width : 0;
      var scale = viewBoxWidth > 0 ? Math.min(1, diagramWidth / viewBoxWidth) : 0;
      var minimum = null;
      if (svg && scale > 0) {
        Array.from(svg.querySelectorAll('text[data-node-label], text[data-boundary-label], text[data-detail="context"]')).forEach(function (text) {
          var detail = text.hasAttribute('data-node-label')
            ? 'primary'
            : text.hasAttribute('data-boundary-label') ? 'boundary' : 'context';
          if (detail === 'context' && !text.closest('[data-node-id]')) return;
          var sourceFontPx = parseFloat(text.getAttribute('font-size') || '');
          if (!Number.isFinite(sourceFontPx)) return;
          var projectedFontPx = sourceFontPx * scale;
          if (!minimum || projectedFontPx < minimum.projectedFontPx) {
            minimum = {
              text: (text.textContent || '').trim(),
              detail: detail,
              sourceFontPx: sourceFontPx,
              projectedFontPx: projectedFontPx
            };
          }
        });
      }
      function intersectionArea(a, b) {
        if (!a || !b || !a.width || !a.height || !b.width || !b.height) return 0;
        var width = Math.max(0, Math.min(a.right, b.right) - Math.max(a.left, b.left));
        var height = Math.max(0, Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top));
        return width * height;
      }
      var legendRect = legend ? legend.getBoundingClientRect() : null;
      var stageRect = window.Archify && Archify.viewerChromeLayout
        && typeof Archify.viewerChromeLayout.stageRect === 'function'
        ? Archify.viewerChromeLayout.stageRect()
        : (stage ? stage.getBoundingClientRect() : null);
      var navigationDockRect = navigationDock ? navigationDock.getBoundingClientRect() : null;
      var stageDockIntersectionArea = intersectionArea(stageRect, navigationDockRect);
      var viewerChromeReceipt = window.Archify && Archify.viewerChromeLayout
        && typeof Archify.viewerChromeLayout.receipt === 'function'
        ? Archify.viewerChromeLayout.receipt()
        : null;
      return {
        innerWidth: window.innerWidth,
        innerHeight: window.innerHeight,
        scrollWidth: Math.ceil(document.documentElement.scrollWidth),
        scrollHeight: Math.ceil(document.documentElement.scrollHeight),
        resolvedTheme: document.documentElement.getAttribute('data-theme') || '',
        readerWidth: reader ? reader.getBoundingClientRect().width : 0,
        diagramWidth: diagramWidth,
        // Review layout: cards sit below the diagram, so page scroll is allowed while the diagram panel is >= 70vh.
        reviewDiagramRatio: document.documentElement.hasAttribute('data-review') && document.querySelector('.diagram-container')
          ? document.querySelector('.diagram-container').getBoundingClientRect().height / window.innerHeight
          : null,
        missingReviewFeatures: document.documentElement.hasAttribute('data-review')
          ? ${JSON.stringify(REVIEW_FEATURES)}.filter(function (f) { return !document.querySelector(f.selector); }).map(function (f) { return f.name; })
          : [],
        diagramHeight: diagram ? diagram.getBoundingClientRect().height : 0,
        viewBoxWidth: viewBoxWidth,
        minimumProjectedNodeTextPx: minimum ? minimum.projectedFontPx : null,
        minimumProjectedNodeText: minimum ? minimum.text : null,
        minimumProjectedNodeTextDetail: minimum ? minimum.detail : null,
        hasLegend: Boolean(legendRect && legendRect.width && legendRect.height),
        hasNavigationDock: Boolean(navigationDockRect && navigationDockRect.width && navigationDockRect.height),
        legendDockIntersectionArea: stageDockIntersectionArea > 0
          ? intersectionArea(legendRect, navigationDockRect)
          : 0,
        dockStageIntersectionArea: stageDockIntersectionArea,
        dockStageGap: stageRect && navigationDockRect ? navigationDockRect.top - stageRect.bottom : null,
        viewerChromeRequiredGap: viewerChromeReceipt ? viewerChromeReceipt.gap : null,
        viewerChromeReserve: viewerChromeReceipt ? viewerChromeReceipt.reserve : 0,
        viewerChromeActive: viewerChromeReceipt ? viewerChromeReceipt.active : false
      };
    })()`);
    if (!metrics || !Number.isFinite(metrics.scrollWidth) || !Number.isFinite(metrics.scrollHeight)) {
      throw new Error('Chrome returned incomplete containment metrics.');
    }

    if (screenshotPath) {
      const capture = await this.cdp.send('Page.captureScreenshot', {
        format: 'png',
        fromSurface: true,
        captureBeyondViewport: false,
      }, sessionId, 20000);
      if (!capture.data) throw new Error('Chrome returned an empty screenshot.');
      fs.writeFileSync(screenshotPath, Buffer.from(capture.data, 'base64'));
    }
    // After the screenshot: opens every path tab (a one-node tab for each unconnected node); lists changed files no tab shows a diff for,
    // diffs without a reviewed checkbox, files whose hunks overlap (stacked per-commit diffs), and a missing side column toggle or side-by-side mode.
    metrics.filesWithoutPathDiff = await evaluate(this.cdp, sessionId, `(function () {
      if (!document.documentElement.hasAttribute('data-review') || !window.Archify || !Archify.pathTabs) return [];
      var changes = Archify.codeData.changes();
      var seen = {};
      var unticked = {};
      var stacked = {};
      var missing = [];
      // Every connection's tab, plus a one-node tab for each node with no connection.
      var keys = Archify.codeData.edges().map(function (edge) { return 'path-' + edge.from + '-' + edge.to; });
      var linked = {};
      Archify.codeData.edges().forEach(function (edge) { linked[edge.from] = linked[edge.to] = true; });
      document.querySelectorAll('.diagram-container > svg [data-node-id]').forEach(function (g) {
        var id = g.getAttribute('data-node-id');
        if (!linked[id] && Archify.codeData.node(id)) keys.push('path-' + id);
      });
      var diffKey = null;
      keys.forEach(function (key) {
        if (!Archify.pathTabs.restore(key)) return;
        if (!diffKey && document.querySelector('.path-panel:not([hidden]) .code-diff')) diffKey = key;
        // A reviewed checkbox in view must be the element a click lands on (nothing drawn over it).
        Array.prototype.forEach.call(document.querySelectorAll('.path-panel:not([hidden]) .path-hop input[type=checkbox]'), function (box) {
          var r = box.getBoundingClientRect();
          if (!r.width || r.top < 0 || r.bottom > window.innerHeight || r.left < 0 || r.right > window.innerWidth) return;
          var hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
          if (hit !== box && missing.indexOf('reviewed checkbox takes a click') < 0) missing.push('reviewed checkbox takes a click');
        });
        // The first hop card does not touch the top edge of its column.
        var col = document.querySelector('.path-panel:not([hidden]) .path-col');
        var first = col && Array.prototype.filter.call(col.children, function (c) { return c.offsetHeight > 0; })[0];
        if (first && first.getBoundingClientRect().top - col.getBoundingClientRect().top < 8 && missing.indexOf('space above the first hop card') < 0) missing.push('space above the first hop card');
        // Every hop card names its file in its head (the badge letter or the coverage ratio alone is not enough).
        Array.prototype.forEach.call(document.querySelectorAll('.path-panel:not([hidden]) .path-hop-head'), function (head) {
          var name = Array.prototype.filter.call(head.children, function (c) { return !c.matches('.path-hop-n, .ct-ratio'); }).map(function (c) { return c.textContent.trim(); }).join('');
          if (!name && missing.indexOf('file name on every hop card') < 0) missing.push('file name on every hop card');
        });
        document.querySelectorAll('.path-panel:not([hidden]) .code-diff[data-file]').forEach(function (pre) {
          var file = pre.getAttribute('data-file');
          seen[file] = true;
          var card = pre.closest('.path-hop');
          if (!card || !card.querySelector('input[type=checkbox]')) unticked[file] = true;
          // Hunks of one file must not overlap: overlapping ranges mean per-commit diffs stacked on each other.
          var spans = [];
          pre.querySelectorAll('.cd-hunk').forEach(function (row) {
            var m = /@@ -(\\d+)(?:,(\\d+))? \\+(\\d+)(?:,(\\d+))? @@/.exec(row.textContent);
            if (m) spans.push([[+m[1], m[2] == null ? 1 : +m[2]], [+m[3], m[4] == null ? 1 : +m[4]]]);
          });
          spans.forEach(function (x, i) {
            spans.slice(i + 1).forEach(function (y) {
              [0, 1].forEach(function (side) {
                var p = x[side], q = y[side];
                if (p[1] && q[1] && p[0] < q[0] + q[1] && q[0] < p[0] + p[1]) stacked[file] = true;
              });
            });
          });
        });
      });
      // Side column toggle, and side-by-side diffs while both the file tree and the side column are collapsed.
      var html = document.documentElement;
      if (diffKey) Archify.pathTabs.restore(diffKey);
      // Contrast in this theme, with a diff open so diff lines are in the page.
      missing = missing.concat((${reviewContrastIssues})(${JSON.stringify(CONTRAST_CHECKS)}));
      // On a path tab the toolbar's cards button collapses the side column; #btn-tree collapses the file tree.
      var sideToggle = document.querySelector('.path-panel:not([hidden])') && document.querySelector('#btn-cards:not([hidden])');
      var treeToggle = document.querySelector('#btn-tree:not([hidden])');
      if (!sideToggle) missing.push('path side column collapse toggle');
      else if (treeToggle && !html.hasAttribute('data-review-collapsed') && !html.hasAttribute('data-review-side-collapsed')) {
        var modes = function () { return Array.prototype.map.call(document.querySelectorAll('.path-panel:not([hidden]) .code-diff'), function (pre) { return pre.getAttribute('data-diff-mode') || 'unified'; }); };
        treeToggle.click();
        sideToggle.click();
        var split = modes();
        // Both columns must stay inside the diff box: the new side may not be pushed out of view.
        Array.prototype.forEach.call(document.querySelectorAll('.path-panel:not([hidden]) .code-diff[data-diff-mode="split"]'), function (pre) {
          var box = pre.getBoundingClientRect();
          var right = Array.prototype.filter.call(pre.querySelectorAll('.cd-line'), function (line) { return line.style.gridColumn === '2'; })[0];
          if (pre.scrollWidth > pre.clientWidth + 1 || (right && right.getBoundingClientRect().right > box.right + 1)) {
            missing.push('side-by-side diff fits its box (' + pre.getAttribute('data-file') + ')');
          }
        });
        treeToggle.click();
        sideToggle.click();
        var back = modes();
        if (!split.length || split.some(function (m) { return m !== 'split'; })) missing.push('side-by-side diff with both sides collapsed');
        if (back.some(function (m) { return m !== 'unified'; })) missing.push('unified diff after expanding');
      }
      Archify.pathTabs.showDiagram();
      return missing.concat( ((changes && changes.files) || []).filter(function (file) {
        return (file.additions || file.deletions) && !seen[file.path];
      }).map(function (file) { return 'path-tab diff for ' + file.path; }))
        .concat(Object.keys(unticked).map(function (file) { return 'reviewed checkbox on the path-tab diff of ' + file; }))
        .concat(Object.keys(stacked).map(function (file) { return 'one net diff instead of stacked diff hunks in ' + file; }));
    })()`);
    // Interactions: collapsing a rail card gives the other card its room, the toolbar's cards button hides the rail and
    // widens the diagram, and a TODO prompt button shows the copy toast. The clipboard is stubbed so the check never
    // touches the real one.
    metrics.interactionIssues = await evaluate(this.cdp, sessionId, `(async function () {
      if (!document.documentElement.hasAttribute('data-review')) return [];
      var issues = [];
      var frame = function () { return new Promise(function (r) { requestAnimationFrame(function () { requestAnimationFrame(r); }); }); };
      var rail = document.getElementById('btn-cards'), cards = document.querySelector('.cards');
      if (rail && !rail.hidden && cards && getComputedStyle(cards).position === 'fixed') {
        var list = cards.querySelectorAll('.card'), toggles = cards.querySelectorAll('.card-collapse');
        if (list.length > 1 && toggles.length === list.length) {
          var h1 = list[1].getBoundingClientRect().height;
          toggles[0].click();
          await frame();
          if (list[1].getBoundingClientRect().height < h1 + 50) issues.push('collapsing a card gives the other card its room');
          toggles[0].click();
          await frame();
        }
        var diagram = document.querySelector('.diagram-container'), w0 = diagram.getBoundingClientRect().width;
        rail.click();
        await frame();
        if (cards.offsetHeight > 0 || diagram.getBoundingClientRect().width < w0 + 100) issues.push('the cards button hides the rail and widens the diagram');
        rail.click();
        await frame();
      }
      // Each TODO tab shows only its own items: a hidden item must be out of view, whatever CSS it has.
      var tabs = document.querySelectorAll('.todo-tabs [role=tab]');
      Array.prototype.forEach.call(tabs, function (tab) {
        tab.click();
        var leak = Array.prototype.some.call(document.querySelectorAll('.todo-list > li[hidden]'), function (li) { return li.offsetHeight > 0; });
        if (leak && issues.indexOf('TODO items of other tabs stay out of view') < 0) issues.push('TODO items of other tabs stay out of view');
        var shown = Array.prototype.filter.call(document.querySelectorAll('.todo-list > li'), function (li) { return li.offsetHeight > 0; });
        var noneShown = shown.some(function (li) { return li.classList.contains('todo-none'); });
        if (noneShown && shown.length > 1 && issues.indexOf('no "no findings" line on a tab with items') < 0) issues.push('no "no findings" line on a tab with items');
      });
      if (tabs.length) tabs[0].click();
      var ask = document.querySelector('.todo-list .todo-ask'), fix = document.querySelector('.todo-list .todo-fix');
      if (ask && fix) {
        var as = getComputedStyle(ask), fs = getComputedStyle(fix);
        if (['fontSize', 'backgroundColor', 'borderTopColor', 'borderRadius', 'color'].some(function (k) { return as[k] !== fs[k]; })) issues.push('Ask AI and AI Fix buttons share one style');
      }
      var btn = document.querySelector('.todo-list .todo-ask, .todo-list .todo-fix');
      if (btn) {
        Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: function () { return Promise.resolve(); } } });
        btn.click();
        for (var i = 0; i < 20 && !document.querySelector('.archify-toast.show'); i++) await new Promise(function (r) { setTimeout(r, 50); });
        if (!document.querySelector('.archify-toast.show')) issues.push('copy toast');
      }
      // The diagram zoom: the slider scales the diagram, a trackpad pinch (ctrl+wheel) is taken by the diagram, reset restores it.
      var zoomSlider = document.querySelector('.diagram-nav input.zoom-slider'), zoomSvg = document.querySelector('.diagram-container > svg');
      if (zoomSlider && zoomSvg && window.Archify && Archify.zoom) {
        var relayout = async function () { window.dispatchEvent(new Event('resize')); await new Promise(function (r) { setTimeout(r, 200); }); await frame(); };
        await relayout();
        var fittedH = parseFloat(getComputedStyle(zoomSvg).height);
        var w0 = zoomSvg.getBoundingClientRect().width;
        zoomSlider.value = 200; zoomSlider.dispatchEvent(new Event('input', { bubbles: true }));
        await frame();
        if (Math.abs(zoomSvg.getBoundingClientRect().width / w0 - 2) > 0.05) issues.push('zoom slider scales the diagram');
        await relayout();
        if (Math.abs(parseFloat(getComputedStyle(zoomSvg).height) - fittedH) > 2) issues.push('a zoomed diagram keeps its fitted size on relayout');
        Archify.zoom.reset();
        var pinch = new WheelEvent('wheel', { ctrlKey: true, deltaY: -50, cancelable: true, bubbles: true, clientX: zoomSvg.getBoundingClientRect().left + 20, clientY: zoomSvg.getBoundingClientRect().top + 20 });
        document.querySelector('.diagram-container').dispatchEvent(pinch);
        await frame();
        if (!pinch.defaultPrevented || Number(zoomSlider.value) <= 100) issues.push('pinch zoom on the diagram');
        Archify.zoom.reset();
        await frame();
        if (getComputedStyle(zoomSvg).transform !== 'none') issues.push('zoom reset restores the fitted diagram');
      }
      // Layout: the review chrome keeps its shape. The side rails line up with the main panel, the title row stays one row
      // even with every path tab open, the toolbar shows icons only and rail card headings line up. On a path tab the
      // notes sit in the right rail, the path title is one row and the path diagram's badges sit on their node's corner.
      var near = function (a, b, d) { return Math.abs(a - b) <= (d || 2); };
      var box = function (el) { return el.getBoundingClientRect(); };
      var mid = function (r) { return (r.top + r.bottom) / 2; };
      var flag = function (what) { if (issues.indexOf(what) < 0) issues.push(what); };
      var lined = function (rails, panel, what) {
        if (!panel) return;
        var p = box(panel), top = Math.max(p.top, 8), bottom = Math.min(p.bottom, innerHeight - 8);
        rails.forEach(function (r) {
          if (!r || !r.offsetHeight || getComputedStyle(r).position !== 'fixed') return;
          if (!near(box(r).top, top) || !near(box(r).bottom, bottom)) flag(what);
        });
      };
      var oneRow = function (strip, items) {
        var tallest = Math.max.apply(null, Array.prototype.map.call(items, function (item) { return box(item).height; }).concat([0]));
        return !strip || box(strip).height <= tallest + 14;
      };
      if (window.Archify && Archify.pathTabs) Archify.pathTabs.showDiagram();
      await frame();
      var tree = document.querySelector('.review-tree');
      lined([tree, cards], document.querySelector('.diagram-container'), 'side rails line up with the diagram panel');
      var titleRow = document.querySelector('.header .header-row'), tabRow = document.querySelector('.header .header-row2');
      var h1 = titleRow && titleRow.querySelector('h1');
      if (titleRow && tabRow && innerWidth > 720) {
        if (!near(mid(box(titleRow)), mid(box(tabRow)), 4) || (h1 && box(h1).height > parseFloat(getComputedStyle(h1).fontSize) * 2)) flag('title, view tabs and toolbar share one row');
        if (!oneRow(document.querySelector('.view-tabs'), document.querySelectorAll('.view-tabs > .view-tab'))) flag('view tabs scroll in one row');
      }
      if (['theme-label', 'refresh-label', 'next-label'].some(function (id) { var el = document.getElementById(id); return el && el.offsetWidth > 0; })) flag('toolbar shows icons only');
      if (cards && getComputedStyle(cards).position === 'fixed') {
        var chevrons = Array.prototype.filter.call(cards.querySelectorAll('.card-collapse'), function (c) { return c.offsetWidth > 0; });
        chevrons.forEach(function (c) {
          var title = c.closest('.card-header').querySelector('h3');
          if (!near(box(c).left, box(chevrons[0]).left) || !title || !title.offsetWidth || !near(mid(box(c)), mid(box(title)), 4)) flag('rail card headings line up');
        });
        if (!oneRow(cards.querySelector('.todo-tabs'), cards.querySelectorAll('.todo-tabs [role=tab]'))) flag('TODO tabs stay in one row');
      }
      var edge = window.Archify && Archify.codeData.edges()[0];
      if (edge && Archify.pathTabs.restore('path-' + edge.from + '-' + edge.to)) {
        await frame();
        await new Promise(function (r) { setTimeout(r, 100); });
        var panel = document.querySelector('.path-panel:not([hidden])'), side = panel && panel.querySelector('.path-side');
        if (side && innerWidth >= 1100 && side.offsetHeight && (getComputedStyle(side).position !== 'fixed' || box(side).left < box(panel).right - 1)) flag('path notes sit in the right rail');
        lined([tree, side], panel, 'side rails line up with the path panel');
        var head = panel && panel.querySelector('.path-head'), eyebrow = head && head.querySelector('.path-eyebrow'), heading = head && head.querySelector('h2');
        if (eyebrow && heading && (!near(mid(box(eyebrow)), mid(box(heading)), 4) || box(head).height > parseFloat(getComputedStyle(heading).fontSize) * 2.2)) flag('path title stays one row');
        Array.prototype.forEach.call(panel ? panel.querySelectorAll('.path-graph [data-path-node]') : [], function (node) {
          var body = node.querySelector('rect'), badge = node.querySelector('.review-risk-badge');
          if (!body || !badge) return;
          var b = box(body), r = box(badge.querySelector('rect')), t = box(badge.querySelector('text'));
          if (!near(r.right, b.right, 3) || !near(mid(r), b.top, 3) || !near((t.left + t.right) / 2, (r.left + r.right) / 2, 3)) flag('path diagram badges sit on their node corner');
        });
        Archify.pathTabs.showDiagram();
        await frame();
      }
      return issues;
    })()`, true);
    return metrics;
  }

  async close() {
    this.cdp.failAll(new Error('visual-check finished'));
    if (this.child.exitCode === null && this.child.signalCode === null) {
      this.child.kill('SIGTERM');
      await new Promise((resolve) => {
        const timer = setTimeout(() => {
          if (this.child.exitCode === null && this.child.signalCode === null) this.child.kill('SIGKILL');
          resolve();
        }, 1500);
        this.child.once('exit', () => {
          clearTimeout(timer);
          resolve();
        });
      });
    }
    try {
      fs.rmSync(this.profileRoot, { recursive: true, force: true });
    } catch {
      // Chrome may briefly retain profile files on Windows; evidence is done.
    }
  }
}

function observation({ width, height, theme, metrics }) {
  const innerWidth = Number(metrics.innerWidth);
  const innerHeight = Number(metrics.innerHeight);
  const scrollWidth = Number(metrics.scrollWidth);
  const scrollHeight = Number(metrics.scrollHeight);
  const overflowX = scrollWidth > innerWidth;
  const reviewScroll = metrics.reviewDiagramRatio != null && Number(metrics.reviewDiagramRatio) >= 0.7;
  const overflowY = scrollHeight > innerHeight && !reviewScroll;
  const minimumProjectedNodeTextPx = metrics.minimumProjectedNodeTextPx == null
    ? null
    : Number(metrics.minimumProjectedNodeTextPx);
  const readabilityOk = minimumProjectedNodeTextPx == null
    || minimumProjectedNodeTextPx >= MIN_PROJECTED_NODE_TEXT_PX;
  const legendDockIntersectionArea = Number(metrics.legendDockIntersectionArea) || 0;
  const dockStageIntersectionArea = Number(metrics.dockStageIntersectionArea) || 0;
  const dockStageGap = metrics.dockStageGap == null ? null : Number(metrics.dockStageGap);
  const receiptDockStageGap = metrics.viewerChromeRequiredGap == null
    ? null
    : Number(metrics.viewerChromeRequiredGap);
  const requiredDockStageGap = Number.isFinite(receiptDockStageGap) ? receiptDockStageGap : 0;
  const viewerChromeStageOk = !metrics.hasNavigationDock || (
    Number.isFinite(dockStageGap)
    && dockStageIntersectionArea <= 0.5
    && dockStageGap >= requiredDockStageGap - 1
  );
  const diagramHeight = Number(metrics.diagramHeight) || null;
  const diagramHeightOk = diagramHeight == null || diagramHeight >= innerHeight * MIN_DIAGRAM_HEIGHT_RATIO;
  const viewerChromeOk = legendDockIntersectionArea <= 0.5 && viewerChromeStageOk && diagramHeightOk;
  return {
    width,
    height,
    theme,
    innerWidth,
    innerHeight,
    scrollWidth,
    scrollHeight,
    overflowX,
    overflowY,
    ok: !overflowX && !overflowY,
    readerWidth: Number(metrics.readerWidth) || null,
    diagramWidth: Number(metrics.diagramWidth) || null,
    viewBoxWidth: Number(metrics.viewBoxWidth) || null,
    minimumProjectedNodeTextPx,
    minimumProjectedNodeText: metrics.minimumProjectedNodeText || null,
    minimumProjectedNodeTextDetail: metrics.minimumProjectedNodeTextDetail || null,
    minimumRequiredNodeTextPx: MIN_PROJECTED_NODE_TEXT_PX,
    readabilityOk,
    hasLegend: Boolean(metrics.hasLegend),
    hasNavigationDock: Boolean(metrics.hasNavigationDock),
    legendDockIntersectionArea,
    dockStageIntersectionArea,
    dockStageGap,
    requiredDockStageGap,
    viewerChromeStageOk,
    diagramHeight,
    diagramHeightOk,
    viewerChromeReserve: Number(metrics.viewerChromeReserve) || 0,
    viewerChromeActive: Boolean(metrics.viewerChromeActive),
    viewerChromeOk,
    resolvedTheme: metrics.resolvedTheme || theme,
    missingReviewFeatures: [
      ...(Array.isArray(metrics.missingReviewFeatures) ? metrics.missingReviewFeatures : []),
      ...(Array.isArray(metrics.filesWithoutPathDiff) ? metrics.filesWithoutPathDiff : []),
      ...(Array.isArray(metrics.interactionIssues) ? metrics.interactionIssues : []),
      ...(metrics.resolvedTheme && metrics.resolvedTheme !== theme ? [`${theme} color theme`] : []),
    ],
  };
}

function contactSheetHtml({ artifactPath, receipt, screenshots }) {
  const cards = screenshots.map((entry) => `
      <figure>
        <img src="${htmlEscape(entry.file)}" alt="${htmlEscape(`${entry.theme} ${entry.width} by ${entry.height}`)}">
        <figcaption><strong>${htmlEscape(entry.theme.toUpperCase())}</strong> · ${entry.width}×${entry.height} · containment ${entry.ok ? 'pass' : 'fail'}</figcaption>
      </figure>`).join('');
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Archify automated browser evidence · ${htmlEscape(path.basename(artifactPath))}</title>
<style>
*{box-sizing:border-box}body{margin:0;padding:24px;background:#e9eef5;color:#172033;font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}header{max-width:1500px;margin:0 auto 18px}h1{margin:0 0 6px;font-size:20px}p{margin:0;color:#526176}.grid{max-width:1500px;margin:auto;display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}figure{margin:0;padding:10px;background:white;border:1px solid #c9d4e3;border-radius:12px;box-shadow:0 10px 30px rgba(15,23,42,.08)}img{display:block;width:100%;height:auto;border:1px solid #e2e8f0}figcaption{padding:9px 4px 2px;color:#526176}@media(max-width:900px){.grid{grid-template-columns:1fr}}
</style>
</head>
<body>
<header><h1>Automated browser evidence</h1><p>${htmlEscape(path.basename(artifactPath))} · visual-check containment ${htmlEscape(receipt.containment.status)} · perceptual visual review pending</p></header>
<main class="grid">${cards}
</main>
</body>
</html>
`;
}

function viewportSubject(artifact, entry) {
  return {
    artifact,
    viewport: { width: entry.width, height: entry.height, theme: entry.theme },
  };
}

const MIN_DIAGRAM_HEIGHT_RATIO = 0.35;

// Review-page features that must survive every change; add each new review feature here.
export const REVIEW_FEATURES = [
  { name: 'file tree collapse toggle', selector: '#btn-tree:not([hidden])' },
  { name: 'file reviewed checkboxes', selector: '.review-tree input' },
  { name: 'TODO tabs', selector: '.todo-tabs [role=tab]' },
  { name: 'TODO item checkboxes', selector: '.todo-list > li > input.todo-check' },
  { name: 'Ask AI or AI Fix buttons', selector: '.todo-list .todo-ask, .todo-list .todo-fix' },
  { name: 'cards rail button', selector: '#btn-cards:not([hidden])' },
  { name: 'card collapse toggles', selector: '.cards .card-collapse' },
  { name: 'diagram zoom slider', selector: '.diagram-nav input.zoom-slider[type=range]' },
  { name: 'Refresh button', selector: '#btn-refresh:not([hidden])' },
  { name: 'color theme button', selector: '#btn-theme' },
  { name: 'Code Smell tab', selector: '.todo-tabs [data-tab=smell]' },
  { name: 'MVVM layout on open', selector: 'html[data-layout="mvvm"] #layout-switch button[data-layout="mvvm"][aria-pressed="true"]' },
];

// WCAG contrast floors checked in both themes: text 4.5, the reviewed tick 3 (non-text, 1.4.11),
// and a diff highlight must stand out from the diff background by `highlight`.
export const CONTRAST_CHECKS = {
  text: ['h1', '.subtitle', '.review-name', '.review-add', '.review-del', '.todo-list > li', '#btn-refresh', '#btn-theme',
    '.cd-line', '.cd-add', '.cd-del', '.cd-add .cd-mark', '.cd-del .cd-mark', '.tk-c', '.tk-s', '.tk-n', '.tk-k',
    '.ct-chip[data-cover="new"]', '.ct-chip[data-cover="existing"]', '.ct-chip[data-cover="none"]', '.ct-chip[data-cover="removed"]', '.ct-ratio'],
  textMin: 4.5,
  tickMin: 3,
  highlight: ['.cd-add', '.cd-del'],
  highlightMin: 1.15,
};

// Runs in the page. Colors are normalized through a canvas so color-mix() and rgba() resolve the same way.
function reviewContrastIssues(checks) {
  var ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
  function rgba(css) {
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = '#000';
    ctx.fillStyle = css;
    ctx.fillRect(0, 0, 1, 1);
    var d = ctx.getImageData(0, 0, 1, 1).data;
    return [d[0], d[1], d[2], d[3] / 255];
  }
  function over(top, under) {
    var a = top[3];
    return [top[0] * a + under[0] * (1 - a), top[1] * a + under[1] * (1 - a), top[2] * a + under[2] * (1 - a), 1];
  }
  function backdrop(el) {
    var chain = [];
    for (var n = el; n && n.nodeType === 1; n = n.parentElement) chain.push(getComputedStyle(n).backgroundColor);
    chain.push(getComputedStyle(document.documentElement).backgroundColor);
    var color = [255, 255, 255, 1];
    if (document.documentElement.getAttribute('data-theme') === 'dark') color = [0, 0, 0, 1];
    for (var i = chain.length - 1; i >= 0; i -= 1) color = over(rgba(chain[i]), color);
    return color;
  }
  function lum(c) {
    var v = c.slice(0, 3).map(function (x) { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); });
    return 0.2126 * v[0] + 0.7152 * v[1] + 0.0722 * v[2];
  }
  function ratio(a, b) { var x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); }
  function fixed(n) { return Math.round(n * 100) / 100; }
  var issues = [];
  checks.text.forEach(function (selector) {
    var el = document.querySelector(selector);
    if (!el) return;
    var bg = backdrop(el);
    var r = ratio(over(rgba(getComputedStyle(el).color), bg), bg);
    if (r < checks.textMin) issues.push('readable text contrast for ' + selector + ' (' + fixed(r) + ' < ' + checks.textMin + ')');
  });
  checks.highlight.forEach(function (selector) {
    var el = document.querySelector(selector);
    if (!el) return;
    var r = ratio(backdrop(el), backdrop(el.parentElement));
    if (r < checks.highlightMin) issues.push('visible highlight for ' + selector + ' (' + fixed(r) + ' < ' + checks.highlightMin + ')');
  });
  // The reviewed tick: a probe mark inside the first diagram node, composited over the node fill.
  var node = document.querySelector('.diagram-container svg [data-node-id][data-node-kind]');
  var rect = node && node.querySelector('rect:not(.c-mask)');
  if (rect) {
    var mark = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    mark.setAttribute('class', 'review-seen-mark');
    node.appendChild(mark);
    var ms = getComputedStyle(mark), rs = getComputedStyle(rect);
    var fill = rgba(ms.fill);
    fill[3] *= parseFloat(ms.fillOpacity) * parseFloat(ms.opacity);
    mark.remove();
    var nodeFill = rgba(rs.fill);
    nodeFill[3] *= parseFloat(rs.fillOpacity) * parseFloat(rs.opacity);
    var under = over(nodeFill, backdrop(node.closest('.diagram-container')));
    var t = ratio(over(fill, under), under);
    if (t < checks.tickMin) issues.push('visible reviewed tick on diagram nodes (' + fixed(t) + ' < ' + checks.tickMin + ')');
  }
  return issues;
}

function failureDiagnostic({ code, message, subject, evidence, supportedFixes, severity = 'error' }) {
  return { code, severity, message, subject, evidence, supportedFixes };
}

function observationDiagnostics({ artifact, allObservations, readabilityObservations }) {
  const diagnostics = [];
  for (const entry of allObservations) {
    if (entry.missingReviewFeatures?.length) {
      diagnostics.push(failureDiagnostic({
        code: 'viewer/review-feature-missing',
        message: `Review page is missing: ${entry.missingReviewFeatures.join(', ')} at ${entry.width}x${entry.height} (${entry.theme}).`,
        subject: viewportSubject(artifact, entry),
        evidence: { missingReviewFeatures: entry.missingReviewFeatures },
        supportedFixes: ['restore the missing review feature in assets/template.html, then rerun visual-check'],
      }));
    }
    if (!entry.ok) {
      diagnostics.push(failureDiagnostic({
        code: 'viewer/viewport-overflow',
        message: `The rendered artifact overflows the ${entry.width}x${entry.height} ${entry.theme} viewport.`,
        subject: viewportSubject(artifact, entry),
        evidence: {
          innerWidth: entry.innerWidth,
          innerHeight: entry.innerHeight,
          scrollWidth: entry.scrollWidth,
          scrollHeight: entry.scrollHeight,
          overflowX: entry.overflowX,
          overflowY: entry.overflowY,
        },
        supportedFixes: [
          `contain the rendered layout within ${entry.width}x${entry.height}, then rerun visual-check`,
        ],
      }));
    }
    if (entry.legendDockIntersectionArea > 0.5) {
      diagnostics.push(failureDiagnostic({
        code: 'viewer/chrome-legend-clearance',
        message: `The navigation Dock obscures the SVG Legend at ${entry.width}x${entry.height} (${entry.theme}).`,
        subject: viewportSubject(artifact, entry),
        evidence: { legendDockIntersectionArea: entry.legendDockIntersectionArea },
        supportedFixes: [
          'move the SVG Legend or Viewer Dock until legendDockIntersectionArea is 0, then rerun visual-check',
        ],
      }));
    }
    if (!entry.diagramHeightOk) {
      diagnostics.push(failureDiagnostic({
        code: 'viewer/diagram-squeezed',
        message: `The diagram panel is squeezed to ${Math.round(entry.diagramHeight)}px at ${entry.width}x${entry.height} (${entry.theme}).`,
        subject: viewportSubject(artifact, entry),
        evidence: { diagramHeight: entry.diagramHeight, innerHeight: entry.innerHeight },
        supportedFixes: [
          'shorten the cards below the diagram (fewer or shorter TODO items), then rerun visual-check',
        ],
      }));
    }
    if (!entry.viewerChromeStageOk) {
      const stageOverlapsDock = entry.dockStageIntersectionArea > 0.5;
      diagnostics.push(failureDiagnostic({
        code: 'viewer/chrome-stage-clearance',
        message: stageOverlapsDock
          ? `Navigation Dock enters the protected SVG stage at ${entry.width}x${entry.height} (${entry.theme}).`
          : `Navigation Dock clearance from the protected SVG stage is below the required gap at ${entry.width}x${entry.height} (${entry.theme}).`,
        subject: viewportSubject(artifact, entry),
        evidence: {
          dockStageIntersectionArea: entry.dockStageIntersectionArea,
          dockStageGap: entry.dockStageGap,
          requiredDockStageGap: entry.requiredDockStageGap,
        },
        supportedFixes: [
          `adjust Viewer stage reservation or clipping until dockStageGap is at least ${entry.requiredDockStageGap} and dockStageIntersectionArea is 0, then rerun visual-check`,
        ],
      }));
    }
  }
  for (const entry of readabilityObservations) {
    if (entry.readabilityOk) continue;
    diagnostics.push(failureDiagnostic({
      code: 'viewer/projected-text-readability',
      message: `Projected ${entry.minimumProjectedNodeTextDetail || 'node'} text is below the readability floor at ${entry.width}x${entry.height}.`,
      subject: viewportSubject(artifact, entry),
      evidence: {
        text: entry.minimumProjectedNodeText,
        detail: entry.minimumProjectedNodeTextDetail,
        minimumProjectedNodeTextPx: entry.minimumProjectedNodeTextPx,
        minimumRequiredNodeTextPx: entry.minimumRequiredNodeTextPx,
      },
      supportedFixes: [
        `increase projected node text to at least ${entry.minimumRequiredNodeTextPx}px at ${entry.width}x${entry.height}, then rerun visual-check`,
      ],
    }));
  }
  return diagnostics;
}

function baseReceipt({ artifactPath, artifact, outputs, chrome }) {
  return {
    schemaVersion: 1,
    ok: false,
    command: 'visual-check',
    evidenceKind: 'automated-browser',
    status: 'fail',
    visualReview: 'pending',
    artifact: {
      path: artifactPath,
      sha256: sha256(artifact),
      bytes: artifact.byteLength,
    },
    state: { detail: 'read', motion: 'still' },
    chrome,
    diagnostics: [],
    containment: { status: 'fail', viewports: [] },
    readability: { status: 'fail', minimumProjectedNodeTextPx: MIN_PROJECTED_NODE_TEXT_PX, viewports: [] },
    viewerChrome: { status: 'fail', viewports: [] },
    captures: { status: 'fail', screenshots: [], contactSheet: null },
    sidecars: {
      receipt: path.basename(outputs.receipt),
      contactSheet: path.basename(outputs.contactSheet),
    },
  };
}

function persistReceipt(outputs, receipt) {
  writeAtomic(outputs.receipt, `${JSON.stringify(receipt, null, 2)}\n`);
}

export async function runVisualCheck({
  artifactPath,
  report = null,
  chromePath,
  resolveChrome = findChrome,
  browserFactory = async (resolvedChrome) => new ChromeVisualBrowser(resolvedChrome),
} = {}) {
  if (!artifactPath) throw new Error('visual-check requires one delivered HTML artifact.');
  const artifact = path.resolve(artifactPath);
  if (!/\.html?$/i.test(artifact)) throw new Error('visual-check requires an .html artifact.');
  const artifactBytes = fs.readFileSync(artifact);
  const outputs = sidecarPaths(artifact, report);
  const reportPath = report ? path.join(path.dirname(artifact), 'reports', `${report}.js`) : null;
  const reportBytes = reportPath ? fs.readFileSync(reportPath) : null;
  cleanupCaptureSidecars(outputs);
  safeUnlink(outputs.receipt);

  const resolvedChrome = chromePath || resolveChrome();
  const receipt = baseReceipt({
    artifactPath: artifact,
    artifact: artifactBytes,
    outputs,
    chrome: resolvedChrome
      ? { status: 'available', executable: resolvedChrome }
      : { status: 'unavailable', executable: null },
  });

  if (!resolvedChrome) {
    receipt.status = 'skipped';
    receipt.containment.status = 'skipped';
    receipt.readability.status = 'skipped';
    receipt.viewerChrome.status = 'skipped';
    receipt.captures.status = 'skipped';
    receipt.error = 'Chrome or Chromium is unavailable. Set ARCHIFY_CHROME to its executable path.';
    receipt.diagnostics = [failureDiagnostic({
      code: 'viewer/chrome-unavailable',
      severity: 'warning',
      message: receipt.error,
      subject: { artifact },
      evidence: { executable: null },
      supportedFixes: ['set ARCHIFY_CHROME to a Chrome or Chromium executable and rerun visual-check'],
    })];
    persistReceipt(outputs, receipt);
    return { exitCode: EXIT.skipped, receipt };
  }

  let browser;
  try {
    browser = await browserFactory(resolvedChrome);
    const observations = new Map();
    const screenshotsByKey = new Map(outputs.screenshots.map((entry) => [
      screenshotKey(entry.width, entry.height, entry.theme),
      entry,
    ]));

    for (const viewport of VISUAL_CHECK_VIEWPORTS) {
      const key = screenshotKey(viewport.width, viewport.height, 'light');
      const screenshot = screenshotsByKey.get(key);
      const metrics = await browser.inspect({
        artifactPath: artifact,
        report,
        ...viewport,
        theme: 'light',
        ...(screenshot ? { screenshotPath: screenshot.path } : {}),
      });
      observations.set(key, observation({ ...viewport, theme: 'light', metrics }));
    }
    for (const viewport of CAPTURE_VIEWPORTS) {
      const key = screenshotKey(viewport.width, viewport.height, 'dark');
      const screenshot = screenshotsByKey.get(key);
      const metrics = await browser.inspect({
        artifactPath: artifact,
        report,
        ...viewport,
        theme: 'dark',
        screenshotPath: screenshot.path,
      });
      observations.set(key, observation({ ...viewport, theme: 'dark', metrics }));
    }

    const afterBytes = fs.readFileSync(artifact);
    if (sha256(afterBytes) !== receipt.artifact.sha256 || afterBytes.byteLength !== receipt.artifact.bytes) {
      throw new Error('The delivered artifact changed while visual-check was running.');
    }
    if (reportPath && !fs.readFileSync(reportPath).equals(reportBytes)) {
      throw new Error(`The report ${report} changed while visual-check was running.`);
    }

    receipt.containment.viewports = VISUAL_CHECK_VIEWPORTS.map(({ width, height }) => (
      observations.get(screenshotKey(width, height, 'light'))
    ));
    receipt.readability.viewports = receipt.containment.viewports.map((entry) => ({ ...entry }));
    receipt.viewerChrome.viewports = receipt.containment.viewports.map((entry) => ({ ...entry }));
    receipt.captures.screenshots = outputs.screenshots.map((entry) => ({
      ...observations.get(screenshotKey(entry.width, entry.height, entry.theme)),
      file: path.basename(entry.path),
    }));
    const allObservations = [...observations.values()];
    const containmentPass = allObservations.every((entry) => entry.ok);
    const readabilityPass = receipt.readability.viewports.every((entry) => entry.readabilityOk);
    const viewerChromePass = allObservations.every((entry) => entry.viewerChromeOk);
    const featuresPass = allObservations.every((entry) => !entry.missingReviewFeatures.length);
    receipt.diagnostics = observationDiagnostics({
      artifact,
      allObservations,
      readabilityObservations: receipt.readability.viewports,
    });
    receipt.containment.status = containmentPass ? 'pass' : 'fail';
    receipt.readability.status = readabilityPass ? 'pass' : 'fail';
    receipt.viewerChrome.status = viewerChromePass ? 'pass' : 'fail';
    receipt.captures.status = 'pass';
    receipt.captures.contactSheet = path.basename(outputs.contactSheet);
    receipt.ok = containmentPass && readabilityPass && viewerChromePass && featuresPass;
    receipt.status = receipt.ok ? 'pass' : 'fail';
    writeAtomic(outputs.contactSheet, contactSheetHtml({
      artifactPath: artifact,
      receipt,
      screenshots: receipt.captures.screenshots,
    }));
    persistReceipt(outputs, receipt);
    return { exitCode: receipt.ok ? EXIT.pass : EXIT.fail, receipt };
  } catch (error) {
    cleanupCaptureSidecars(outputs);
    receipt.status = 'fail';
    receipt.ok = false;
    receipt.error = error.message;
    receipt.containment.status = 'fail';
    receipt.readability.status = 'fail';
    receipt.viewerChrome.status = 'fail';
    receipt.captures.status = 'fail';
    receipt.captures.screenshots = [];
    receipt.captures.contactSheet = null;
    receipt.diagnostics = [failureDiagnostic({
      code: 'viewer/visual-check-runtime',
      message: 'visual-check could not complete its Chrome inspection.',
      subject: { artifact },
      evidence: { reason: error.message },
      supportedFixes: ['resolve the reported Chrome inspection error, then rerun visual-check'],
    })];
    persistReceipt(outputs, receipt);
    return { exitCode: EXIT.fail, receipt };
  } finally {
    if (browser?.close) await browser.close();
  }
}
