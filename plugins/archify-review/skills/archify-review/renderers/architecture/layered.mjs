// Layered layout of a code-review diagram: one frame per layer of a recognised scheme. MVVM is three columns (View | ViewModel | Model,
// nodes stacked, edges running between columns); the Flutter and WebView schemes are three bands, top to bottom.
// Pure geometry: node positions, explicit connection sides and band rectangles. Edge routing and validation stay with the renderer.
// Node order inside each band is searched against a cheap surrogate of the routed edges; the best surrogate candidates are then
// scored with the real router and route checks, and the renderer falls through to the next candidate when validation still fails.
import { withDiagnosticRecordingSuppressed } from '../shared/diagnostics.mjs';
import { createRouter, routeProblems } from './routing.mjs';
import { textUnits } from '../shared/utils.mjs';
import { REVIEW_EDGE_LABEL_PX } from '../shared/desktop-readability.mjs';

// Sources: references/code-review-pipeline.md. `label` is [title, subtitle?], drawn on one line at the frame's top-left.
export const SCHEMES = {
  mvvm: { platform: 'app', columns: true, tiers: [['view', ['View']], ['viewmodel', ['ViewModel']], ['model', ['Model', 'Repository / Service']]] },
  flutter: { platform: 'app', framework: 'flutter', tiers: [['ui', ['UI', 'Presentation']], ['logic', ['Logic', 'Domain, optional']], ['data', ['Data']]] },
  webview: { platform: 'app', flag: 'webview', tiers: [['host', ['Host', 'Native host']], ['bridge', ['Bridge', 'JS bridge']], ['content', ['Content', 'Web content']]] },
};
// Schemes offered for a spec's meta: those of its platform, the Flutter one only when meta.framework matches, the WebView one only when meta.webview is true.
export const schemesOf = (meta) => Object.keys(SCHEMES).filter((k) => SCHEMES[k].platform === meta?.platform && (!SCHEMES[k].framework || SCHEMES[k].framework === meta.framework) && (!SCHEMES[k].flag || meta[SCHEMES[k].flag] === true));

const BAND_X = 16; // band left edge; the right edge mirrors it
const LABEL_H = 22; // tier label line above the frame's rows
const PAD_X = 22;
const PAD_Y = 22;
const BAND_GAP = 18;
const GAP_X = 40; // minimum gap between neighbours in a row
const ROW_GAP = 40;
const MIN_BAND_H = 84;
const EMPTY_BAND_H = 48;
const TOP = 24;
const MIN_COLUMN_W = 120;
const TITLE_PAD = 28; // title text inset on both sides of a column frame
const WIDE_ROW_PX = 1010; // widest row budget; the review canvas stays near 1150px so labels keep 13px at 1440
const EXHAUSTIVE_MAX = 6; // bands up to this size are searched exhaustively; larger ones hill-climb
const SWEEPS = 3;
const EXACT_CANDIDATES = 200; // surrogate-best layouts that are scored with the real router
const TUNED_CANDIDATES = 24;
const CLEAN_CANDIDATES = 4; // best of those that also get per-label placement

const tierOf = (c, scheme) => c.code?.layers?.[scheme];
const sizeOf = (c) => (Array.isArray(c.size) ? c.size : [120, 60]);

function* permutations(items) {
  const a = [...items];
  const n = a.length;
  const c = new Array(n).fill(0);
  yield [...a];
  for (let i = 0; i < n;) {
    if (c[i] < i) {
      const k = i % 2 ? c[i] : 0;
      [a[k], a[i]] = [a[i], a[k]];
      yield [...a];
      c[i] += 1;
      i = 0;
    } else {
      c[i] = 0;
      i += 1;
    }
  }
}

// Lays the tiers out as columns (left to right, nodes stacked and centred, one shared frame height); `gap` separates the frames.
function buildColumns(rowsOf, byId, tiers, gap) {
  const columns = tiers.map(([tier, label]) => {
    const ids = rowsOf[tier].flat();
    const heights = ids.map((id) => sizeOf(byId.get(id))[1]);
    const inner = heights.reduce((a, b) => a + b, 0) + ROW_GAP * Math.max(0, ids.length - 1);
    const titleW = Math.ceil(textUnits(label.join(" — ")) * 8.2) + TITLE_PAD; // 13px bold title with its subtitle
    const width = Math.max(MIN_COLUMN_W, titleW, ...ids.map((id) => sizeOf(byId.get(id))[0] + PAD_X * 2));
    return { tier, label, ids, heights, inner, width };
  });
  const height = Math.max(MIN_BAND_H, ...columns.map((c) => c.inner + PAD_Y * 2 + LABEL_H));
  const boxes = new Map();
  let x = BAND_X;
  const bands = columns.map((c) => {
    let y = TOP + LABEL_H + (height - LABEL_H - c.inner) / 2;
    c.ids.forEach((id, i) => {
      const [w, h] = sizeOf(byId.get(id));
      boxes.set(id, { x: Math.round(x + (c.width - w) / 2), y: Math.round(y), w, h });
      y += c.heights[i] + ROW_GAP;
    });
    const band = { tier: c.tier, x, y: TOP, width: c.width, height, label: c.label };
    x += c.width + gap;
    return band;
  });
  return { boxes, bands };
}

// Lays each band's rows out centred; returns boxes and bands.
function build(rowsOf, byId, limit, tiers, gapX) {
  const innerLeft = BAND_X + PAD_X;
  const boxes = new Map();
  const bands = [];
  let y = TOP;
  for (const [tier, label] of tiers) {
    const rows = rowsOf[tier];
    const heights = rows.map((r) => Math.max(...r.map((id) => sizeOf(byId.get(id))[1])));
    const inner = heights.reduce((a, b) => a + b, 0) + ROW_GAP * Math.max(0, rows.length - 1);
    const height = rows.length ? Math.max(MIN_BAND_H, inner + PAD_Y * 2 + LABEL_H) : EMPTY_BAND_H;
    let rowY = y + LABEL_H + (height - LABEL_H - inner) / 2;
    rows.forEach((r, index) => {
      const total = r.reduce((a, id) => a + sizeOf(byId.get(id))[0], 0) + gapX * (r.length - 1);
      let x = innerLeft + (limit - total) / 2;
      for (const id of r) {
        const [w, h] = sizeOf(byId.get(id));
        boxes.set(id, { x: Math.round(x), y: Math.round(rowY), w, h });
        x += w + gapX;
      }
      rowY += heights[index] + ROW_GAP;
    });
    bands.push({ tier, y, height, label });
    y += height + BAND_GAP;
  }
  return { boxes, bands };
}

// Sides follow the geometry: vertically separated nodes leave bottom/top, nodes in one row leave right/left.
// In columns, nodes in different columns leave right/left and nodes in one column bottom/top.
function sidesOf(a, b, columns) {
  if (columns && a.x + a.w <= b.x) return ['right', 'left'];
  if (columns && b.x + b.w <= a.x) return ['left', 'right'];
  if (b.y >= a.y + a.h) return ['bottom', 'top'];
  if (a.y >= b.y + b.h) return ['top', 'bottom'];
  return a.x <= b.x ? ['right', 'left'] : ['left', 'right'];
}

const segHitsBox = (x1, y1, x2, y2, box, pad = 2) => !(Math.max(x1, x2) < box.x - pad || Math.min(x1, x2) > box.x + box.w + pad
  || Math.max(y1, y2) < box.y - pad || Math.min(y1, y2) > box.y + box.h + pad);

// Axis-aligned segment intersection that excludes touching ends.
function cross(p, q) {
  const [a, b] = p;
  const [c, d] = q;
  const aH = a[1] === b[1];
  const cH = c[1] === d[1];
  if (aH === cH) return false;
  const [h, v] = aH ? [p, q] : [q, p];
  const hy = h[0][1];
  const vx = v[0][0];
  return vx > Math.min(h[0][0], h[1][0]) + 1 && vx < Math.max(h[0][0], h[1][0]) - 1
    && hy > Math.min(v[0][1], v[1][1]) + 1 && hy < Math.max(v[0][1], v[1][1]) - 1;
}

// Surrogate of the routed edges: a straight run in one row, otherwise down/up, along a mid channel, down/up.
function surrogate(edge, boxes, columns) {
  const a = boxes.get(edge.from);
  const b = boxes.get(edge.to);
  const [fs, ts] = sidesOf(a, b, columns);
  if (fs === 'left' || fs === 'right') {
    const x1 = fs === 'right' ? a.x + a.w : a.x;
    const x2 = ts === 'left' ? b.x : b.x + b.w;
    const ay = a.y + a.h / 2;
    const by = b.y + b.h / 2;
    if (columns) { // out, along the channel between the columns, in
      const mx = (x1 + x2) / 2;
      return [[[x1, ay], [mx, ay]], [[mx, ay], [mx, by]], [[mx, by], [x2, by]]];
    }
    const y = (ay + by) / 2;
    return [[[x1, y], [x2, y]]];
  }
  const y1 = fs === 'bottom' ? a.y + a.h : a.y;
  const y2 = ts === 'top' ? b.y : b.y + b.h;
  const ax = a.x + a.w / 2;
  const bx = b.x + b.w / 2;
  const my = (y1 + y2) / 2;
  return [[[ax, y1], [ax, my]], [[ax, my], [bx, my]], [[bx, my], [bx, y2]]];
}

function cost(layout, edges, columns) {
  const { boxes } = layout;
  const routed = edges.map((e) => ({ e, segs: surrogate(e, boxes, columns) }));
  let value = 0;
  for (const { e, segs } of routed) {
    for (const [[x1, y1], [x2, y2]] of segs) {
      for (const [id, box] of boxes) {
        if (id !== e.from && id !== e.to && segHitsBox(x1, y1, x2, y2, box)) value += 1000;
      }
      value += (Math.abs(x2 - x1) + Math.abs(y2 - y1)) * 0.05;
    }
  }
  for (let i = 0; i < routed.length; i += 1) {
    for (let j = i + 1; j < routed.length; j += 1) {
      const shared = routed[i].e.from === routed[j].e.from || routed[i].e.to === routed[j].e.to
        || routed[i].e.from === routed[j].e.to || routed[i].e.to === routed[j].e.from;
      for (const p of routed[i].segs) {
        for (const q of routed[j].segs) {
          if (cross(p, q)) value += 100;
          // two horizontal runs on one channel read as a single corridor
          if (!shared && p[0][1] === p[1][1] && q[0][1] === q[1][1] && Math.abs(p[0][1] - q[0][1]) < 8
            && Math.min(Math.max(p[0][0], p[1][0]), Math.max(q[0][0], q[1][0])) - Math.max(Math.min(p[0][0], p[1][0]), Math.min(q[0][0], q[1][0])) > 4) value += 60;
        }
      }
    }
  }
  return value;
}

// Every way to split `ids` into ordered rows that each fit (ordered set partitions, rows permuted).
function* rowLayouts(ids, fits) {
  if (!ids.length) { yield []; return; }
  const n = ids.length;
  for (let mask = 1; mask < 1 << n; mask += 1) {
    const chosen = ids.filter((_, i) => mask & (1 << i));
    if (!fits(chosen)) continue;
    const rest = ids.filter((_, i) => !(mask & (1 << i)));
    for (const row of permutations(chosen)) {
      for (const tail of rowLayouts(rest, fits)) yield [row, ...tail];
    }
  }
}

const rowsKey = (rows) => rows.map((r) => r.join(',')).join('/');

// Up to `count` candidate layouts for one scheme, best surrogate cost first; [] unless every component carries a layer of that scheme.
// Route and label hints authored for the freeform layout point at freeform coordinates; a layered layout drops them.
export const FREEFORM_GEOMETRY = ['fromSide', 'toSide', 'route', 'via', 'channelX', 'channelY', 'labelAt', 'labelDx', 'labelDy', 'labelSegment'];
export const withoutFreeformGeometry = (conn) => Object.fromEntries(Object.entries(conn).filter(([key]) => !FREEFORM_GEOMETRY.includes(key)));

export function layeredPlans(components, connections, { scheme, count = 6, profile, edgeLabelFont = REVIEW_EDGE_LABEL_PX } = {}) {
  connections = (connections || []).map(withoutFreeformGeometry);
  const { tiers, columns = false } = SCHEMES[scheme];
  const TIERS = tiers.map(([id]) => id);
  if (!components.length || !components.every((c) => TIERS.includes(tierOf(c, scheme)))) return [];
  const byId = new Map(components.map((c) => [c.id, c]));
  const widthOf = (id) => sizeOf(byId.get(id))[0];
  const edges = (connections || []).filter((e) => e.id && byId.has(e.from) && byId.has(e.to) && e.from !== e.to);
  const freeCx = (c) => (Array.isArray(c.pos) ? c.pos[0] + sizeOf(c)[0] / 2 : 0);
  const freeRight = Math.max(...components.map((c) => (Array.isArray(c.pos) ? c.pos[0] : 0) + sizeOf(c)[0]));
  const baseLimit = Math.max(freeRight - 40, ...components.map((c) => sizeOf(c)[0])); // same row budget as the freeform canvas
  const wideLimit = Math.max(baseLimit, WIDE_ROW_PX); // fewer rows: a flatter diagram reads larger
  const labelW = (e) => Math.max(30, textUnits(e.label) * 4.8 + 10); // as drawn by the renderer
  const gap = Math.min(170, Math.max(80, 36 + Math.max(0, ...edges.filter((e) => e.label).map(labelW)))); // room for an edge label between columns
  const gapX = Math.min(120, Math.max(GAP_X, gap)); // neighbours in a row leave room for an edge label between them
  // A row (a column's stack holds one node per row) fits when it has room and, in columns, one node.
  const fits = (limit) => (ids) => (!columns || ids.length <= 1) && ids.reduce((a, id) => a + widthOf(id), 0) + gapX * (ids.length - 1) <= limit;
  const sidesFor = (layout, e) => sidesOf(layout.boxes.get(e.from), layout.boxes.get(e.to), columns);
  // Layouts for one row budget, as { layout, cost } entries.
  const search = (limit) => {
    // Start: freeform left-to-right order, greedy-wrapped into rows.
    const current = Object.fromEntries(TIERS.map((tier) => {
      const ids = components.filter((c) => tierOf(c, scheme) === tier).sort((a, b) => freeCx(a) - freeCx(b) || a.id.localeCompare(b.id)).map((c) => c.id);
      const rows = [];
      let row = [];
      for (const id of ids) {
        if (row.length && !fits(limit)([...row, id])) { rows.push(row); row = []; }
        row.push(id);
      }
      if (row.length) rows.push(row);
      return [tier, rows];
    }));
    const seen = new Map();
    const evaluate = (candidate) => {
      const key = TIERS.map((tier) => rowsKey(candidate[tier])).join('|');
      if (!seen.has(key)) {
        const layout = columns ? buildColumns(candidate, byId, tiers, gap) : build(candidate, byId, limit, tiers, gapX);
        seen.set(key, { key: `${limit}|${key}`, layout, cost: cost(layout, edges, columns) });
      }
      return seen.get(key).cost;
    };

    // Neighbours of a row layout for the hill-climb: swap two nodes, or move one node to the end/start of another row or a new row.
    function* moves(rows) {
      const flat = rows.flatMap((r, ri) => r.map((id, ci) => [ri, ci, id]));
      for (const [r1, c1] of flat) {
        for (const [r2, c2] of flat) {
          if ((r1 === r2 && c2 <= c1) || (r1 > r2)) continue;
          const next = rows.map((r) => [...r]);
          [next[r1][c1], next[r2][c2]] = [next[r2][c2], next[r1][c1]];
          if (next.every(fits(limit))) yield next;
        }
        for (let target = 0; target <= rows.length; target += 1) {
          if (target === r1 && rows[r1].length === 1) continue;
          for (const atStart of [false, true]) {
            const next = rows.map((r) => [...r]);
            const [id] = next[r1].splice(c1, 1);
            if (target === rows.length) next.push([id]);
            else if (atStart) next[target].unshift(id); else next[target].push(id);
            const pruned = next.filter((r) => r.length);
            if (pruned.every(fits(limit))) yield pruned;
          }
        }
      }
    }

    let best = evaluate(current);
    for (let sweep = 0; sweep < SWEEPS; sweep += 1) {
      let improved = false;
      for (const tier of TIERS) {
        const ids = current[tier].flat();
        if (ids.length < 2) continue;
        let winner = current[tier];
        const candidates = ids.length <= EXHAUSTIVE_MAX ? rowLayouts(ids, fits(limit)) : moves(winner);
        for (const rows of candidates) {
          const value = evaluate({ ...current, [tier]: rows });
          if (value < best) { best = value; winner = rows; }
        }
        // hill-climb larger bands until no neighbour improves
        while (ids.length > EXHAUSTIVE_MAX) {
          let stepped = false;
          for (const rows of moves(winner)) {
            const value = evaluate({ ...current, [tier]: rows });
            if (value < best) { best = value; winner = rows; stepped = true; break; }
          }
          if (!stepped) break;
        }
        if (winner !== current[tier]) { current[tier] = winner; improved = true; }
      }
      if (!improved) break;
    }
    return [...seen.values()];
  };
  const pool = new Map();
  for (const limit of new Set(columns ? [baseLimit] : [baseLimit, wideLimit])) {
    for (const entry of search(limit)) pool.set(entry.key, entry);
  }

  // Real routes and route checks for the surrogate-best layouts.
  const LABEL_VARIANTS = [{}, { labelSegment: 0 }, { labelSegment: 2 }, { labelDy: 24 }, { labelDy: -14 }];
  const score = (entry, hints, sides = new Map(edges.map((e) => [e.id, sidesFor(entry.layout, e)]))) => {
    const placed = new Map(components.map((c) => {
      const { x, y, w, h } = entry.layout.boxes.get(c.id);
      return [c.id, { ...c, x, y, width: w, height: h, cx: x + w / 2, cy: y + h / 2 }];
    }));
    const routed = (connections || []).map((e) => ({ ...e, ...(sides.has(e.id) ? { fromSide: sides.get(e.id)[0], toSide: sides.get(e.id)[1] } : {}), ...(hints.get(e.id) || {}) }));
    const { pathFor, connectionEndpointSide } = createRouter(routed, placed);
    return {
      // candidates are scored, not reported: keep their problems out of the diagnostic record
      problems: withDiagnosticRecordingSuppressed(() => routeProblems({ connections: routed, components: placed, pathFor, connectionEndpointSide, profile, edgeLabelFont, requiresAttachedLabels: true })).length,
      pointsOf: (id) => pathFor(routed.find((e) => e.id === id)).points,
    };
  };
  // Greedy per-edge tuning: other side pairs (the router bridges same-side pairs around the nodes), then label placement.
  const SIDES = ['top', 'bottom', 'left', 'right'];
  const SIDE_PAIRS = SIDES.flatMap((from) => SIDES.map((to) => [from, to]));
  const tune = (entry) => {
    const hints = new Map();
    const sides = new Map(edges.map((e) => [e.id, sidesFor(entry.layout, e)]));
    let result = score(entry, hints, sides);
    for (const e of edges) {
      if (!result.problems) break;
      for (const pair of SIDE_PAIRS) {
        const trial = new Map(sides).set(e.id, pair);
        const next = score(entry, hints, trial);
        if (next.problems < result.problems) { result = next; sides.set(e.id, pair); }
      }
    }
    for (const e of edges.filter((x) => x.label)) {
      if (!result.problems) break;
      const points = result.pointsOf(e.id);
      // try the label at several spots along the routed edge
      const spots = points.slice(0, -1).flatMap((p, i) => [0.5, 0.25, 0.75].map((t) => [p[0] + (points[i + 1][0] - p[0]) * t, p[1] + (points[i + 1][1] - p[1]) * t - 10]));
      for (const variant of [...LABEL_VARIANTS.slice(1), ...spots.map((labelAt) => ({ labelAt }))]) {
        const trial = new Map(hints).set(e.id, variant);
        const next = score(entry, trial, sides);
        if (next.problems < result.problems) { result = next; hints.set(e.id, variant); }
        if (!result.problems) break;
      }
    }
    return { ...entry, ...result, sides, hints };
  };
  const exact = [...pool.values()].sort((a, b) => a.cost - b.cost).slice(0, EXACT_CANDIDATES)
    .map((entry) => ({ ...entry, ...score(entry, new Map()) })).sort((a, b) => a.problems - b.problems || a.cost - b.cost);
  const heightOf = (entry) => Math.max(...entry.layout.bands.map((b) => b.y + b.height));
  const results = [];
  let clean = 0;
  for (const entry of exact.slice(0, TUNED_CANDIDATES)) {
    const done = entry.problems ? tune(entry) : { ...entry, sides: new Map(edges.map((e) => [e.id, sidesFor(entry.layout, e)])), hints: new Map() };
    results.push(done);
    if (!done.problems && (clean += 1) >= CLEAN_CANDIDATES) break; // a few clean candidates are enough; the flattest wins
  }
  return results.sort((a, b) => a.problems - b.problems || heightOf(a) - heightOf(b) || a.cost - b.cost).slice(0, count).map(({ layout, sides, problems, hints }) => ({
    positions: Object.fromEntries([...layout.boxes].map(([id, box]) => [id, [box.x, box.y]])),
    sides: Object.fromEntries(sides),
    hints: Object.fromEntries(hints),
    bands: layout.bands,
    bandX: BAND_X,
    problems,
  }));
}
