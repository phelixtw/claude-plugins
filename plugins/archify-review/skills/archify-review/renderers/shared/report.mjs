import fs from 'node:fs';
import path from 'node:path';
import { esc, fillBody, serializeScriptJson, splitTemplate } from './utils.mjs';
import { SUPPORTED_LOCALES, viewerCatalog } from './i18n.mjs';

// A report file is `archifyReport(<json>);` so a file:// viewer can load it with <script src>.
const PREFIX = 'archifyReport(';
const SUFFIX = ');\n';

export const REPORT_ID_RE = /^[A-Za-z][A-Za-z0-9]*-\d+$/;

export function reportPayload({ id, parts, spec }) {
  return { format: 'archify-report', version: 1, id, generatedAt: new Date().toISOString(), page: parts, spec };
}

export function reportSource(payload) {
  return PREFIX + serializeScriptJson(payload) + SUFFIX;
}

export function readReport(file) {
  const text = fs.readFileSync(file, 'utf8');
  if (!text.startsWith(PREFIX) || !text.endsWith(SUFFIX)) throw new Error(`${file} is not an archify report`);
  return JSON.parse(text.slice(PREFIX.length, -SUFFIX.length));
}

// reports/index.js lists every report so a viewer opened without ?report= can offer them; rebuilt on each deliver.
export function writeReportIndex(dir) {
  const list = fs.readdirSync(dir).filter((f) => f.endsWith('.js') && REPORT_ID_RE.test(f.slice(0, -3))).flatMap((f) => {
    try {
      const p = readReport(path.join(dir, f));
      return [{ id: p.id, title: p.page?.title || p.id, generatedAt: p.generatedAt || '' }];
    } catch { return []; }
  }).sort((a, b) => b.generatedAt.localeCompare(a.generatedAt));
  const staged = path.join(dir, `.index.${process.pid}.js`);
  fs.writeFileSync(staged, `archifyReportIndex(${serializeScriptJson(list)});\n`);
  fs.renameSync(staged, path.join(dir, 'index.js'));
  return list;
}

// Runs in the viewer: picks the report from ?report=<ID> (or #report=<ID>), loads reports/<ID>.js
// and fills the page with the same fillBody the standalone renderer uses.
function bootstrap(fillBody, idPattern, catalogs) {
  var html = document.documentElement;
  var status = document.getElementById('archify-report-status');
  function fail(message) { html.setAttribute('data-report-state', 'error'); status.textContent = message; }
  function fromHash() {
    var m = /(?:^#|&)report=([^&]*)/.exec(location.hash);
    if (!m) return false;
    var params = new URLSearchParams(location.search);
    params.set('report', decodeURIComponent(m[1]));
    var hash = location.hash.replace(/(^#|&)report=[^&]*/, '$1').replace(/^#&/, '#');
    location.replace(location.pathname + '?' + params + (hash === '#' ? '' : hash));
    return true;
  }
  addEventListener('hashchange', fromHash);
  if (fromHash()) return;
  var id = new URLSearchParams(location.search).get('report') || '';
  if (!id) return pick();
  if (!idPattern.test(id)) return fail('Open this viewer as review.html?report=<ID>.');
  window.archifyReport = function (payload) {
    if (!payload || payload.id !== id) return fail('reports/' + id + '.js does not hold report ' + id + '.');
    var page = payload.page;
    // Viewer wording comes from this viewer's catalog, so an older report shows the current labels and gets
    // messages added since; keys the catalog does not know keep the report's own text.
    page.messages = Object.assign({}, page.messages, catalogs[page.locale] || catalogs.en);
    var filled = fillBody(JSON.parse(document.getElementById('archify-viewer-body').textContent), page);
    html.lang = page.locale;
    if (page.codeData && page.codeData.changes) html.setAttribute('data-review', '');
    document.title = page.documentTitle;
    window.archifyReportPayload = payload;
    document.body.innerHTML = filled.slice(filled.indexOf('>') + 1, filled.lastIndexOf('</body>'));
    // innerHTML does not run scripts; re-create them in order so each runs synchronously as during parsing.
    Array.prototype.forEach.call(document.body.querySelectorAll('script'), function (old) {
      var script = document.createElement('script');
      Array.prototype.forEach.call(old.attributes, function (a) { script.setAttribute(a.name, a.value); });
      script.text = old.text;
      old.replaceWith(script);
    });
    html.setAttribute('data-report-state', 'ready');
  };
  load('reports/' + id + '.js', function () { fail('Report not found: reports/' + id + '.js'); });
  function load(src, onerror) {
    var loader = document.createElement('script');
    loader.src = src;
    loader.onerror = onerror;
    document.body.appendChild(loader);
  }
  // No ?report=: list reports/index.js as one-click links, newest first.
  function pick() {
    window.archifyReportIndex = function (list) {
      html.setAttribute('data-report-state', 'index');
      status.textContent = list.length ? 'Pick a report:' : 'No reports yet.';
      var ul = document.createElement('ul');
      ul.id = 'archify-report-index';
      ul.style.cssText = 'font-family: monospace; line-height: 2; padding: 0 24px 24px 48px';
      list.forEach(function (r) {
        var li = document.createElement('li');
        var a = document.createElement('a');
        a.href = '?report=' + encodeURIComponent(r.id);
        a.textContent = r.id;
        li.appendChild(a);
        li.appendChild(document.createTextNode('  ' + (r.title === r.id ? '' : r.title.replace(r.id, '').trim()) + '  ' + r.generatedAt.slice(0, 10)));
        ul.appendChild(li);
      });
      status.after(ul);
    };
    load('reports/index.js', function () { fail('Open this viewer as review.html?report=<ID>.'); });
  }
}

// The viewer page: the template head, the raw template body as JSON, and the bootstrap.
export function buildViewerShell(template, version = '0.0.0') {
  const { head, body, tail } = splitTemplate(template);
  return head.replace('<title>[PROJECT NAME] Architecture Diagram</title>', `<title>${esc('Archify Review')}</title>\n  <meta name="archify-viewer-version" content="${esc(version)}">`)
    + '<body>\n'
    + '  <p id="archify-report-status" style="font-family: monospace; padding: 24px">Loading report…</p>\n'
    + `  <script id="archify-viewer-body" type="application/json">${serializeScriptJson(body)}</script>\n`
    // The bootstrap source holds `<script` and `<!--` literals, so it travels as JSON, never as raw script text.
    + `  <script id="archify-viewer-boot" type="application/json">${serializeScriptJson(`(${bootstrap})(${fillBody}, ${REPORT_ID_RE}, ${JSON.stringify(Object.fromEntries(SUPPORTED_LOCALES.map((l) => [l, viewerCatalog(l)])))});`)}</script>\n`
    + `  <script>new Function(JSON.parse(document.getElementById('archify-viewer-boot').textContent))();</script>\n`
    + '</body>' + tail;
}

// The version a viewer shell was built by, from its <meta name="archify-viewer-version">; '0.0.0' when absent.
export function viewerVersion(html) {
  const match = /<meta name="archify-viewer-version" content="([^"]*)">/.exec(html || '');
  return match ? match[1] : '0.0.0';
}

// Numeric x.y.z comparison: negative when a < b.
export function compareVersions(a, b) {
  const pa = String(a).split('.').map((n) => parseInt(n, 10) || 0);
  const pb = String(b).split('.').map((n) => parseInt(n, 10) || 0);
  for (let i = 0; i < 3; i += 1) if ((pa[i] || 0) !== (pb[i] || 0)) return (pa[i] || 0) - (pb[i] || 0);
  return 0;
}
