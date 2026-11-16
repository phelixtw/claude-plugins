import {
  escapeHtml as esc,
  resolveLocale,
  translateMessage,
  viewerCatalog,
} from './i18n.mjs';

export { esc };

export function renderDefinitions() {
  return `        <!-- Definitions -->
        <defs>
          <marker id="arrowhead" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
            <polygon points="0 0, 10 3.5, 0 7" class="m-default" />
          </marker>
          <marker id="arrowhead-emphasis" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
            <polygon points="0 0, 10 3.5, 0 7" class="m-emphasis" />
          </marker>
          <marker id="arrowhead-security" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
            <polygon points="0 0, 10 3.5, 0 7" class="m-security" />
          </marker>
          <marker id="arrowhead-dashed" markerWidth="10" markerHeight="7" refX="9" refY="3.5" orient="auto">
            <polygon points="0 0, 10 3.5, 0 7" class="m-dashed" />
          </marker>
          <pattern id="grid" width="40" height="40" patternUnits="userSpaceOnUse">
            <path d="M 40 0 L 0 0 0 40" class="c-grid" stroke-width="0.5"/>
          </pattern>
        </defs>`;
}

const SIGIL_TONE = {
  frontend: 'frontend',
  start: 'frontend',
  backend: 'backend',
  active: 'backend',
  database: 'database',
  success: 'database',
  cloud: 'cloud',
  waiting: 'cloud',
  security: 'security',
  failure: 'security',
  messagebus: 'messagebus',
  external: 'external',
  neutral: 'external',
};

const SIGIL_SHAPE = {
  frontend: `<rect x="2" y="3" width="12" height="10" rx="2"/>
            <path d="M2 6.5h12"/>
            <circle cx="4.1" cy="4.8" r=".7" class="sigil-fill"/>
            <circle cx="6.3" cy="4.8" r=".7" class="sigil-fill"/>`,
  backend: `<path d="M6 3 3 8l3 5M10 3l3 5-3 5"/>`,
  database: `<ellipse cx="8" cy="4" rx="5" ry="2"/>
            <path d="M3 4v8c0 1.1 2.2 2 5 2s5-.9 5-2V4M3 8c0 1.1 2.2 2 5 2s5-.9 5-2"/>`,
  cloud: `<path d="M4.3 12.5h7.3a2.4 2.4 0 0 0 .2-4.8 4 4 0 0 0-7.5-1.3A3.1 3.1 0 0 0 4.3 12.5Z"/>`,
  security: `<path d="M8 2.2 13 4v3.5c0 3.1-1.8 5.4-5 6.5-3.2-1.1-5-3.4-5-6.5V4Z"/>
            <path d="m5.8 8 1.5 1.5 3-3"/>`,
  messagebus: `<path d="M2.5 4.5h11M2.5 8h11M2.5 11.5h11"/>
            <circle cx="5" cy="4.5" r="1" class="sigil-fill"/>
            <circle cx="10.5" cy="8" r="1" class="sigil-fill"/>
            <circle cx="7" cy="11.5" r="1" class="sigil-fill"/>`,
  external: `<rect x="2.5" y="5" width="8.5" height="8" rx="1.5"/>
            <path d="M8 2.5h5.5V8M13.5 2.5 7.5 8.5"/>`,
  start: `<circle cx="8" cy="8" r="5"/>
            <path d="m7 5.4 3.6 2.6L7 10.6Z" class="sigil-fill"/>`,
  active: `<path d="M2 8h3l1.5-3.5L9 12l1.6-4H14"/>`,
  waiting: `<path d="M4 2.5h8M4 13.5h8M5 3c0 2.8 2 3.2 3 5-1 1.8-3 2.2-3 5M11 3c0 2.8-2 3.2-3 5 1 1.8 3 2.2 3 5"/>`,
  success: `<circle cx="8" cy="8" r="5.3"/>
            <path d="m5.2 8 1.8 1.8 3.8-4"/>`,
  failure: `<circle cx="8" cy="8" r="5.3"/>
            <path d="m5.7 5.7 4.6 4.6m0-4.6-4.6 4.6"/>`,
  neutral: `<rect x="3" y="3" width="10" height="10" rx="2"/>
            <circle cx="8" cy="8" r="1.2" class="sigil-fill"/>`,
  // Architecture layer glyphs; tinted by the node's own type via the tone option.
  view: `<rect x="2" y="3" width="12" height="9" rx="1.5"/>
            <path d="M6 14.5h4M8 12v2.5"/>`,
  service: `<circle cx="8" cy="8" r="2.2"/>
            <path d="M8 2v2M8 12v2M2 8h2M12 8h2M3.8 3.8l1.4 1.4M10.8 10.8l1.4 1.4M12.2 3.8l-1.4 1.4M5.2 10.8l-1.4 1.4"/>`,
  model: `<ellipse cx="8" cy="4" rx="5" ry="2"/>
            <path d="M3 4v8c0 1.1 2.2 2 5 2s5-.9 5-2V4M3 8c0 1.1 2.2 2 5 2s5-.9 5-2"/>`,
};

// A quiet, renderer-owned role stamp. It is authored SVG content rather than a
// viewer overlay, so it survives canonical export while adding no focus target,
// accessible name, layout box, or interaction state of its own.
export function renderSemanticSigil(kind, { x, y, size = 11, toneOf } = {}) {
  const normalized = Object.hasOwn(SIGIL_SHAPE, kind) ? kind : 'neutral';
  const tone = SIGIL_TONE[toneOf ?? normalized] || 'external';
  const scale = size / 16;
  return `<g aria-hidden="true" data-semantic-sigil="${esc(normalized)}" class="semantic-sigil s-${tone}" transform="translate(${x} ${y}) scale(${scale})">
            ${SIGIL_SHAPE[normalized]}
          </g>`;
}

// Card items accept `**bold**` and `code` spans; escaped first, code contents stay literal.
function cardMarkup(text) {
  return esc(text)
    .replace(/`([^`]+)`/g, '<code class="inline-code">$1</code>')
    .replace(/\*\*([^*]+?)\*\*/g, '<strong>$1</strong>');
}

export function renderCards(cards) {
  const list = Array.isArray(cards) ? cards : [];
  return `    <!-- Info Cards -->
    <div class="cards">
${list.map((card) => `      <div class="card">
        <div class="card-header">
          <div class="card-dot ${esc(card.dot)}"></div>
          <h3>${esc(card.title)}</h3>
        </div>
        <ul>
${card.items.map((item) => `          <li>&bull; ${cardMarkup(item)}</li>`).join('\n')}
        </ul>
      </div>`).join('\n\n')}
    </div>`;
}

const SVG_SLOT_RE = /      <!-- ARCHIFY:SVG_SLOT_START -->[\s\S]*?      <!-- ARCHIFY:SVG_SLOT_END -->/;
const CARDS_SLOT_RE = /    <!-- ARCHIFY:CARDS_SLOT_START -->[\s\S]*?    <!-- ARCHIFY:CARDS_SLOT_END -->/;
const SUBTITLE_SLOT_RE = /^([ \t]*)<p class="subtitle">\[Subtitle description\]<\/p>[ \t]*(\r?\n)?/m;
const CODE_DATA_PLACEHOLDER = '<!-- ARCHIFY:CODE_DATA -->';

export function serializeScriptJson(value) {
  return JSON.stringify(value)
    .replaceAll('<', '\\u003c')
    .replaceAll('>', '\\u003e')
    .replaceAll('&', '\\u0026');
}

const TEMPLATE_PLACEHOLDERS = [
  '<html lang="en" data-theme="dark">',
  '<title>[PROJECT NAME] Architecture Diagram</title>',
  '<h1>[PROJECT NAME] Architecture</h1>',
  CODE_DATA_PLACEHOLDER,
];

// Everything a page needs beyond the template; the same object is a report's `page`.
export function templateParts({ title, subtitle, svg, layeredSvg = null, cards, locale, codeData = null }) {
  const resolvedLocale = resolveLocale(locale);
  return {
    documentTitle: translateMessage(resolvedLocale, 'page.title', { title }),
    title,
    subtitle: typeof subtitle === 'string' ? subtitle : null,
    locale: resolvedLocale,
    messages: viewerCatalog(resolvedLocale),
    svg,
    layeredSvg,
    cards,
    codeData,
  };
}

// Fills the template body (`<body>` to `</body>`) from templateParts output.
// Self-contained: the report viewer runs this exact source in the browser.
export function fillBody(body, parts) {
  var escape = function (value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  };
  var json = function (value) {
    return JSON.stringify(value).replace(/</g, '\\u003c').replace(/>/g, '\\u003e').replace(/&/g, '\\u0026');
  };
  var codeDataPlaceholder = '<!-- ARCHIFY:CODE_DATA -->';
  var i18nPlaceholder = '    <!-- ARCHIFY:I18N_DATA -->';
  var i18nData = '    <script id="archify-i18n-data" type="application/json">' + json({ locale: parts.locale, messages: parts.messages }) + '</script>';
  var localized = body.replace(/\{\{i18n:([a-zA-Z0-9_.-]+)\}\}/g, function (_match, key) {
    if (!Object.prototype.hasOwnProperty.call(parts.messages, key)) throw new Error('Missing Archify i18n message ' + JSON.stringify(key) + ' for ' + parts.locale);
    return escape(parts.messages[key]);
  });
  var withI18n = localized.indexOf(i18nPlaceholder) >= 0
    ? localized.replace(i18nPlaceholder, function () { return i18nData; })
    : localized.replace(codeDataPlaceholder, function () { return i18nData + '\n    ' + codeDataPlaceholder; });
  var subtitle = parts.subtitle && parts.subtitle.trim() ? '<p class="subtitle">' + escape(parts.subtitle) + '</p>' : '';
  return withI18n
    .replace('<h1>[PROJECT NAME] Architecture</h1>', function () { return '<h1>' + escape(parts.title) + '</h1>'; })
    .replace(/^([ \t]*)<p class="subtitle">\[Subtitle description\]<\/p>[ \t]*(\r?\n)?/m, function (_match, indent, newline) {
      return subtitle ? indent + subtitle + (newline || '') : '';
    })
    .replace(/      <!-- ARCHIFY:SVG_SLOT_START -->[\s\S]*?      <!-- ARCHIFY:SVG_SLOT_END -->/, function () { return parts.svg; })
    .replace(/    <!-- ARCHIFY:CARDS_SLOT_START -->[\s\S]*?    <!-- ARCHIFY:CARDS_SLOT_END -->/, function () { return parts.cards; })
    .replace(codeDataPlaceholder, function () {
      return (parts.codeData ? '<script id="archify-code-data" type="application/json">' + json(parts.codeData) + '</script>' : '')
        + (parts.layeredSvg ? '\n    <script id="archify-layered-svg" type="application/json">' + json(parts.layeredSvg) + '</script>' : '');
    });
}

// Splits a template into the part before `<body>`, the body, and the rest.
export function splitTemplate(template) {
  const start = template.indexOf('<body>');
  const end = template.lastIndexOf('</body>') + '</body>'.length;
  if (start < 0 || end < start) throw new Error('template missing <body>');
  return { head: template.slice(0, start), body: template.slice(start, end), tail: template.slice(end) };
}

export function fillTemplate(template, parts) {
  const { head, body, tail } = splitTemplate(template);
  return head
    .replace(TEMPLATE_PLACEHOLDERS[0], () => `<html lang="${esc(parts.locale)}" data-theme="dark"${parts.codeData?.changes ? ' data-review' : ''}>`)
    .replace(TEMPLATE_PLACEHOLDERS[1], () => `<title>${esc(parts.documentTitle)}</title>`)
    + fillBody(body, parts) + tail;
}

export function applyTemplate(template, input) {
  if (!SVG_SLOT_RE.test(template)) {
    throw new Error('applyTemplate: template missing ARCHIFY:SVG_SLOT sentinel');
  }
  if (!CARDS_SLOT_RE.test(template)) {
    throw new Error('applyTemplate: template missing ARCHIFY:CARDS_SLOT sentinel');
  }
  if (!SUBTITLE_SLOT_RE.test(template)) {
    throw new Error('applyTemplate: template missing subtitle placeholder');
  }
  for (const ph of TEMPLATE_PLACEHOLDERS) {
    if (!template.includes(ph)) {
      throw new Error(`applyTemplate: template missing placeholder ${JSON.stringify(ph)}`);
    }
  }
  return fillTemplate(template, templateParts(input));
}

// CJK and other wide/fullwidth glyphs render at roughly twice the advance
// width of ASCII in the monospace stacks the template uses. Keep halfwidth
// forms (notably U+FF61–U+FF9F Katakana) out of this set. The explicit ranges
// also cover vertical punctuation and supplementary East Asian scripts that
// literal glyph ranges made difficult to audit.
// Code points that take two columns of advance width: East Asian Wide and
// Fullwidth per UAX #11, tracking Unicode 17.0. That takes in the BMP symbols
// carrying emoji presentation (U+2705, U+2B50, U+26A1, U+231B, ...), which
// render at the same square advance as the supplementary-plane emoji already
// listed here, and Hangul Jamo Extended-A. Two boundary calls worth naming:
// Unicode 16.0 reclassified the trigrams (U+2630-U+2637) and the monogram /
// digram symbols (U+268A-U+268F) from Neutral to Wide, so both are in; and
// Hangul Jamo Extended-A stops at U+A97C, its last assigned jamo, because
// U+A97D-U+A97F are unassigned, and unassigned code points outside the CJK
// ranges UAX #11 names default to Neutral rather than Wide. Spelled out as
// ranges because V8 has no \p{East_Asian_Width=W} property escape.
const FULLWIDTH_RE = /[\u1100-\u115F\u231A-\u231B\u2329-\u232A\u23E9-\u23EC\u23F0\u23F3\u25FD-\u25FE\u2614-\u2615\u2630-\u2637\u2648-\u2653\u267F\u268A-\u268F\u2693\u26A1\u26AA-\u26AB\u26BD-\u26BE\u26C4-\u26C5\u26CE\u26D4\u26EA\u26F2-\u26F3\u26F5\u26FA\u26FD\u2705\u270A-\u270B\u2728\u274C\u274E\u2753-\u2755\u2757\u2795-\u2797\u27B0\u27BF\u2B1B-\u2B1C\u2B50\u2B55\u2E80-\uA4CF\uA960-\uA97C\uAC00-\uD7A3\uF900-\uFAFF\uFE10-\uFE19\uFE30-\uFE6F\uFF01-\uFF60\uFFE0-\uFFE6\u{16FE0}-\u{18DFF}\u{1AFF0}-\u{1AFFF}\u{1B000}-\u{1B2FF}\u{1F000}-\u{1FAFF}\u{20000}-\u{3FFFD}]/u;

// A variation selector (U+FE00-U+FE0F) carries no advance of its own: it
// re-presents the character before it. VS15 (U+FE0E) asks for text
// presentation, which renders narrow; VS16 (U+FE0F) asks for emoji
// presentation, which renders at the square emoji advance. So a base plus a
// selector is measured from the selector, not from the base -- otherwise
// widening the emoji-presentation bases above turns U+2B50 U+FE0F from two
// units into three while the glyph on screen stays one square, and leaves
// U+2708 U+FE0F at two only because its base happens to be narrow.
//
// A selector following a base that cannot take emoji presentation is
// malformed input; measuring it wide is the safe direction here, since
// over-measuring pads a box while under-measuring spills the label out of it.
const VARIATION_SELECTOR_FIRST = 0xfe00;
const VARIATION_SELECTOR_LAST = 0xfe0f;
const VARIATION_SELECTOR_TEXT = 0xfe0e;
const VARIATION_SELECTOR_EMOJI = 0xfe0f;

export function textUnits(text) {
  const chars = Array.from(String(text ?? ''));
  let units = 0;
  for (let i = 0; i < chars.length; i += 1) {
    const codePoint = chars[i].codePointAt(0);
    if (codePoint >= VARIATION_SELECTOR_FIRST && codePoint <= VARIATION_SELECTOR_LAST) continue;
    const next = i + 1 < chars.length ? chars[i + 1].codePointAt(0) : -1;
    if (next === VARIATION_SELECTOR_EMOJI) units += 2;
    else if (next === VARIATION_SELECTOR_TEXT) units += 1;
    else units += FULLWIDTH_RE.test(chars[i]) ? 2 : 1;
  }
  return units;
}

// ---- Language regions (architecture background tints) -----------------------
const LANGUAGES = {
  '.kt': 'Kotlin', '.kts': 'Kotlin', '.dart': 'Dart', '.swift': 'Swift', '.java': 'Java',
  '.ts': 'TypeScript', '.tsx': 'TypeScript', '.js': 'JavaScript', '.jsx': 'JavaScript',
  '.mjs': 'JavaScript', '.py': 'Python', '.go': 'Go', '.rs': 'Rust', '.cs': 'C#', '.rb': 'Ruby',
};

// Majority language of a node's code.files extensions; ties go to the first seen.
export function nodeLanguage(component) {
  const counts = new Map();
  for (const file of component?.code?.files ?? []) {
    const filePath = String(file?.path ?? '');
    const ext = filePath.match(/\.[^./\\]+$/)?.[0]?.toLowerCase();
    // Android resources (res/ xml, drawables, manifest) belong to the native Android side.
    const language = LANGUAGES[ext] ?? (/(^|\/)(res\/|AndroidManifest\.xml$)/.test(filePath) ? 'Kotlin' : null);
    if (language) counts.set(language, (counts.get(language) ?? 0) + 1);
  }
  return [...counts].sort((a, b) => b[1] - a[1])[0]?.[0] ?? null;
}

// Full-bleed language zones: every cell of `bounds` ([x0, y0, x1, y1]) goes to
// the language of the nearest node, so zones tile the canvas without overlap
// and each node sits inside its own zone. Cell edges are cut on node edges,
// long gaps are subdivided to keep the dividing line close to the midline.
// Nodes without a language never own a zone; they take whatever zone they sit in.
// ponytail: O(cells * nodes) scan, fine for diagrams below a few hundred nodes.
export function languageRegionLoops(nodes, bounds, maxCell = 60) {
  const languages = [...new Set(nodes.map((n) => n.language).filter(Boolean))];
  if (languages.length < 2) return [];
  const [bx0, by0, bx1, by1] = bounds;
  const cuts = (lo, hi, edges) => {
    const base = [...new Set([lo, hi, ...edges.filter((e) => e > lo && e < hi)])].sort((a, b) => a - b);
    return base.flatMap((v, k) => {
      if (k === base.length - 1) return [v];
      const parts = Math.ceil((base[k + 1] - v) / maxCell);
      return Array.from({ length: parts }, (_p, q) => v + ((base[k + 1] - v) * q) / parts);
    });
  };
  const xs = cuts(bx0, bx1, nodes.flatMap((n) => [n.x, n.x + n.width]));
  const ys = cuts(by0, by1, nodes.flatMap((n) => [n.y, n.y + n.height]));
  const dist = (r, cx, cy) => Math.hypot(Math.max(r.x - cx, 0, cx - r.x - r.width), Math.max(r.y - cy, 0, cy - r.y - r.height));
  const owner = (i, j) => {
    const cx = (xs[i] + xs[i + 1]) / 2;
    const cy = (ys[j] + ys[j + 1]) / 2;
    let best = null;
    for (const r of nodes) {
      if (!r.language) continue;
      const d = dist(r, cx, cy);
      if (!best || d < best.d) best = { d, language: r.language };
    }
    return best.language;
  };
  const grid = xs.slice(0, -1).map((_x, i) => ys.slice(0, -1).map((_y, j) => owner(i, j)));
  const at = (i, j) => grid[i]?.[j] ?? null;
  return languages.map((language) => {
    // Directed boundary edges with the region on the right (clockwise on screen).
    const out = new Map();
    const add = (a, b) => { const k = `${a}`; (out.get(k) ?? out.set(k, []).get(k)).push({ a, b }); };
    for (let i = 0; i < xs.length - 1; i += 1) {
      for (let j = 0; j < ys.length - 1; j += 1) {
        if (at(i, j) !== language) continue;
        const [x0, x1, y0, y1] = [xs[i], xs[i + 1], ys[j], ys[j + 1]];
        if (at(i, j - 1) !== language) add([x0, y0], [x1, y0]);
        if (at(i + 1, j) !== language) add([x1, y0], [x1, y1]);
        if (at(i, j + 1) !== language) add([x1, y1], [x0, y1]);
        if (at(i - 1, j) !== language) add([x0, y1], [x0, y0]);
      }
    }
    const loops = [];
    for (const list of out.values()) {
      for (const start of list) {
        if (start.used) continue;
        const points = [];
        for (let edge = start; edge && !edge.used; edge = out.get(`${edge.b}`)?.find((e) => !e.used)) {
          edge.used = true;
          points.push(edge.a);
        }
        // Drop collinear vertices so each corner is a real turn.
        const corners = points.filter((p, k) => {
          const prev = points[(k + points.length - 1) % points.length];
          const next = points[(k + 1) % points.length];
          return (p[0] - prev[0]) * (next[1] - p[1]) !== (p[1] - prev[1]) * (next[0] - p[0]);
        });
        if (corners.length >= 4) loops.push(corners);
      }
    }
    return { language, loops };
  });
}

// Rounded rectilinear path through the corner points of one or more loops.
export function roundedLoopsPath(loops, radius = 8) {
  return loops.map((pts) => pts.map((p, k) => {
    const prev = pts[(k + pts.length - 1) % pts.length];
    const next = pts[(k + 1) % pts.length];
    const lenIn = Math.hypot(p[0] - prev[0], p[1] - prev[1]);
    const lenOut = Math.hypot(next[0] - p[0], next[1] - p[1]);
    const r = Math.min(radius, lenIn / 2, lenOut / 2);
    const a = [p[0] + ((prev[0] - p[0]) / lenIn) * r, p[1] + ((prev[1] - p[1]) / lenIn) * r];
    const b = [p[0] + ((next[0] - p[0]) / lenOut) * r, p[1] + ((next[1] - p[1]) / lenOut) * r];
    return `${k ? 'L' : 'M'}${a[0]} ${a[1]}Q${p[0]} ${p[1]} ${b[0]} ${b[1]}`;
  }).join('') + 'Z').join('');
}
