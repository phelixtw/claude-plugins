import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { esc, languageRegionLoops, nodeLanguage, renderDefinitions, roundedLoopsPath, renderSemanticSigil, textUnits } from '../shared/utils.mjs';
import { animateAttr, focusEdgeAttrs, focusNodeAttrs, focusNodeTitle, loadDiagram, writeDiagram, svgAccessibleText, svgRootAttrs } from '../shared/cli.mjs';
import { componentBox, boundaryBox, connectionPath } from '../shared/layout-report.mjs';
import { throwDiagnosticError, throwDiagnosticProblems } from '../shared/diagnostics.mjs';
import { legendFootprint, relationshipLegendObstacles, resolveLegend, renderLegend as renderResolvedLegend } from '../shared/legend.mjs';
import { availableNodeTextWidth, fittedNodeFontSize, minimumNodeTextWidth } from '../shared/text-fit.mjs';
import { minimumReadableSourceTextPx, REVIEW_EDGE_LABEL_PX, REVIEW_LABEL_PREFERRED_PX } from '../shared/desktop-readability.mjs';
import { translateMessage as i18nText } from '../shared/i18n.mjs';
import { gridLayout, resolveComponentPos, validateGridPlacement } from './grid.mjs';
import { createRouter, routeProblems } from './routing.mjs';
import { schemesOf, FREEFORM_GEOMETRY, layeredPlans as layeredPlansOf } from './layered.mjs';
import {
  asArray,
  isFinitePoint,
  rectsOverlap,
  suggestComponentSeparation,
  anchor,
  polylinePath,
  routePointsValue,
  labelPoint,
  componentFill,
  componentText,
  arrowClassMap,
  variantAccent,
} from '../shared/geometry.mjs';

const componentTextFit = {
  sublabelPreferred: 9,
  sublabelMinimum: 6,
  tagPreferred: 7,
  tagMinimum: 6,
};

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const layoutJsonMode = process.argv.includes('--layout-json');
const cliArgs = process.argv.filter((arg) => arg !== '--layout-json');
const { diagram: arch, template, outPath } = loadDiagram({
  rendererDir: __dirname,
  diagramType: 'architecture',
  defaultExample: 'web-app.architecture.json',
  argv: cliArgs,
});

const grid = gridLayout(arch);

// Code-review pages also ship one layered geometry per scheme of meta.platform (app: MVVM, plus Flutter layers when meta.framework is flutter and WebView layers when meta.webview is true). A child run of this
// renderer (ARCHIFY_LAYERED=<plan json>) places the nodes in the scheme's bands and prints that svg, so it goes through the same
// routing and validation as the freeform one. The parent searches the plans per scheme, tries them in order and keeps the first
// child that validates.
const layeredMode = process.env.ARCHIFY_LAYERED !== undefined;
const layeredPlans = layeredMode || grid || !asArray(arch.components).some((c) => c.code)
  ? {}
  : Object.fromEntries(schemesOf(arch.meta).map((scheme) => [scheme, layeredPlansOf(asArray(arch.components), asArray(arch.connections), { scheme, profile: arch.meta?.quality_profile, edgeLabelFont: REVIEW_EDGE_LABEL_PX })]));
const layeredSchemes = Object.keys(layeredPlans).filter((scheme) => layeredPlans[scheme].length);
const layeredPlanned = layeredMode ? JSON.parse(process.env.ARCHIFY_LAYERED) : null;
if (layeredMode) {
  for (const c of asArray(arch.components)) c.pos = layeredPlanned.positions[c.id];
  for (const conn of asArray(arch.connections)) {
    for (const key of FREEFORM_GEOMETRY) delete conn[key];
    if (layeredPlanned.sides[conn.id]) [conn.fromSide, conn.toSide] = layeredPlanned.sides[conn.id];
    Object.assign(conn, layeredPlanned.hints[conn.id]);
  }
  arch.boundaries = [];
  delete arch.meta.viewBox;
}

const layout = {
  defaultW: 120,
  defaultH: 60,
  margin: 40,
  // Boundary padding — the 30/50 rule that was a hand-arithmetic footgun
  // (CHANGELOG v2.2.1): 30px on top/left/right, plus 20px extra at the bottom.
  boundaryPad: 30,
  boundaryExtraBottom: 20,
  boundaryLabelBaseline: 18,
  boundaryLabelClearance: 4,
  boundaryLabelFontPreferred: 9,
  boundaryLabelFontMinimum: 6,
  boundaryLabelMaskHeight: 16,
  boundaryLabelRailGap: 2,
  boundaryLabelFrameInset: 4,
  legendH: 28,
};

const LEGEND_CATALOG = [
  'frontend',
  'backend',
  'database',
  'cloud',
  'security',
  'messagebus',
  'external',
].map((kind) => ({ kind, label: i18nText(arch.meta.locale, `legend.architecture.${kind}`) }));

// ---- Measure components from free coordinates --------------------------------
function measureComponent(c) {
  const [x, y] = resolveComponentPos(c, grid);
  const [w, h] = Array.isArray(c.size) ? c.size : [layout.defaultW, layout.defaultH];
  return { ...c, x, y, width: w, height: h, cx: x + w / 2, cy: y + h / 2 };
}

const components = new Map(asArray(arch.components).map((c) => [c.id, measureComponent(c)]));
// Code-review diagrams carry per-node `code`; they render larger type because reviewers read file names.
const isReview = asArray(arch.components).some((c) => c.code);
const edgeLabelFont = isReview ? REVIEW_EDGE_LABEL_PX : 8;
const enforcesBoundaryTitleComposition = Boolean(arch.meta?.quality_profile);
const componentSteps = new Map();
for (const [index, conn] of asArray(arch.connections).entries()) {
  if (!componentSteps.has(conn.from)) componentSteps.set(conn.from, index);
  if (!componentSteps.has(conn.to)) componentSteps.set(conn.to, index + 1);
}
for (const [index, c] of asArray(arch.components).entries()) {
  if (!componentSteps.has(c.id)) componentSteps.set(c.id, index);
}

// ---- Boundaries computed from the `wraps` id list ---------------------------
function boundaryRect(boundary) {
  const members = asArray(boundary.wraps).map((id) => components.get(id)).filter(Boolean);
  if (!members.length) return null;
  const minX = Math.min(...members.map((m) => m.x));
  const minY = Math.min(...members.map((m) => m.y));
  const maxX = Math.max(...members.map((m) => m.x + m.width));
  const maxY = Math.max(...members.map((m) => m.y + m.height));
  const pad = boundary.pad ?? layout.boundaryPad;
  const topPad = Math.max(
    pad,
    layout.boundaryLabelBaseline + layout.boundaryLabelClearance,
  );
  return {
    ...boundary,
    x: minX - pad,
    y: minY - topPad,
    width: maxX - minX + pad * 2,
    height: maxY - minY + topPad + layout.boundaryExtraBottom,
    memberTop: minY,
  };
}

function rectContains(outer, inner) {
  const epsilon = 1e-9;
  return outer.x <= inner.x + epsilon
    && outer.y <= inner.y + epsilon
    && outer.x + outer.width + epsilon >= inner.x + inner.width
    && outer.y + outer.height + epsilon >= inner.y + inner.height;
}

function boundaryLabelWidth(label, fontSize) {
  return Math.max(30, textUnits(label) * fontSize * 0.6 + 10);
}

// Language zones: drawn only when the diagram mixes >= 2 source languages.
const languageRegionStyle = {
  Kotlin: ['#7F52FF', 'Kotlin · Android native'],
  Dart: ['#00B8A9', 'Dart · Flutter'],
  Swift: ['#F05138', 'Swift · iOS native'],
};
const regionStyleFor = (language) => languageRegionStyle[language] || ['#C9871A', language];
const componentLanguages = [...components.values()].map((c) => nodeLanguage(c));
const regionLanguages = !layeredMode && new Set(componentLanguages.filter(Boolean)).size >= 2
  ? [...new Set(componentLanguages.filter(Boolean))]
  : [];

// Layered diagrams explain the layer glyphs and zone colors instead of per-type entries.
const LAYER_LEGEND = [
  ['view', 'View（畫面）'],
  ['service', 'Service（Control、Logic）'],
  ['model', 'Model（Repository）'],
];
const architectureLegendEntries = (() => {
  const hidden = arch.meta?.legend?.mode === 'hidden';
  const layered = [...components.values()].some((c) => c.layer);
  const extra = layered ? [
    ...LAYER_LEGEND
      .filter(([layer]) => [...components.values()].some((c) => c.layer === layer))
      .map(([layer, label]) => ({ kind: `layer-${layer}`, layer, label })),
    // nbsp pad: the legend measures labels narrower than they render
    ...regionLanguages.map((language) => ({ kind: `language-${language}`, language, label: `${regionStyleFor(language)[1]}\u00A0\u00A0\u00A0` })),
  ].map((entry) => ({ ...entry, present: true, interactive: false })) : [];
  if (hidden) return [];
  if (layered) return extra;
  return resolveLegend(
    arch.meta?.legend,
    LEGEND_CATALOG,
    new Set([...components.values()].map((component) => component.type)),
  );
})();

function autoViewBoxFor(candidateBoundaries) {
  // column frames end layeredPlanned.bandX from the right canvas edge, as they start from the left one
  const columnsRight = layeredMode ? Math.max(0, ...layeredPlanned.bands.filter((band) => band.x !== undefined).map((band) => band.x + band.width - (layout.margin - layeredPlanned.bandX))) : 0;
  const maxX = Math.max(
    0,
    ...[...components.values()].map((component) => component.x + component.width),
    ...candidateBoundaries.map((boundary) => boundary.x + boundary.width),
    columnsRight,
  );
  const maxY = Math.max(
    0,
    ...[...components.values()].map((component) => component.y + component.height),
    ...candidateBoundaries.map((boundary) => boundary.y + boundary.height),
    ...(layeredMode ? layeredPlanned.bands.map((band) => band.y + band.height) : []),
  );
  let width = Math.ceil(maxX + layout.margin);
  let footprint = legendFootprint(architectureLegendEntries, {
    width: Math.max(1, width - layout.margin * 2),
  });
  if (footprint.minWidth > width - layout.margin * 2) {
    width = Math.ceil(footprint.minWidth + layout.margin * 2);
    footprint = legendFootprint(architectureLegendEntries, {
      width: width - layout.margin * 2,
    });
  }
  return [
    width,
    Math.ceil(maxY + layout.margin + layout.legendH + footprint.extraHeight),
  ];
}

function resolvedViewBoxWidth(candidateBoundaries) {
  if (Array.isArray(arch.meta?.viewBox) && Number.isFinite(arch.meta.viewBox[0])) {
    return arch.meta.viewBox[0];
  }
  return autoViewBoxFor(candidateBoundaries)[0];
}

function expandBoundaryForReadableTitle(boundary, minimumFontSize) {
  if (!enforcesBoundaryTitleComposition) return boundary;
  const requiredWidth = boundaryLabelWidth(boundary.label, minimumFontSize)
    + layout.boundaryLabelFrameInset * 2;
  const extra = Math.max(0, requiredWidth - boundary.width);
  if (!extra) return boundary;
  return {
    ...boundary,
    x: boundary.x - extra / 2,
    width: boundary.width + extra,
  };
}

function measureBoundaryTitle(boundary, minimumFontSize) {
  const availableWidth = Math.max(0, boundary.width - layout.boundaryLabelFrameInset * 2);
  const units = textUnits(boundary.label);
  const fitted = units > 0
    ? (availableWidth - 10) / (units * 0.6)
    : layout.boundaryLabelFontPreferred;
  const preferredFontSize = Math.max(layout.boundaryLabelFontPreferred, minimumFontSize);
  const fontSize = Math.max(
    minimumFontSize,
    Math.min(preferredFontSize, fitted),
  );
  const desiredWidth = boundaryLabelWidth(boundary.label, fontSize);
  const height = Math.max(layout.boundaryLabelMaskHeight, Math.ceil(fontSize + 7));
  return {
    x: boundary.x + layout.boundaryLabelFrameInset,
    y: boundary.memberTop
      - layout.boundaryLabelClearance
      - height,
    width: Math.min(availableWidth, desiredWidth),
    height,
    fontSize,
    minimumFontSize,
    baselineOffset: fontSize + 4,
    availableWidth,
    minimumWidth: boundaryLabelWidth(boundary.label, minimumFontSize),
  };
}

function horizontalOverlap(left, right) {
  return left.x < right.x + right.width && left.x + left.width > right.x;
}

function layoutBoundaryTitles(rawBoundaries, minimumFontSize) {
  const placedTitles = [];
  const measured = new Map();
  const ordered = rawBoundaries
    .map((boundary, index) => ({ boundary, index }))
    .sort((left, right) => {
      const areaDelta = left.boundary.width * left.boundary.height
        - right.boundary.width * right.boundary.height;
      return areaDelta || left.index - right.index;
    });

  for (const entry of ordered) {
    const { index } = entry;
    const boundary = expandBoundaryForReadableTitle(entry.boundary, minimumFontSize);
    const title = measureBoundaryTitle(boundary, minimumFontSize);
    let guard = 0;
    while (guard < rawBoundaries.length + components.size + 1) {
      guard += 1;
      const blockers = [
        ...placedTitles,
        ...components.values(),
      ].filter((candidate) => horizontalOverlap(title, candidate) && rectsOverlap(title, candidate));
      if (!blockers.length) break;
      title.y = Math.min(
        ...blockers.map((blocker) => blocker.y - layout.boundaryLabelRailGap - title.height),
      );
    }
    placedTitles.push(title);
    measured.set(index, { boundary, title });
  }

  return rawBoundaries.map((_boundary, index) => {
    const { boundary, title } = measured.get(index);
    const bottom = boundary.y + boundary.height;
    // Profile-less schema-v1 inputs keep their legacy boundary geometry. A
    // quality profile opts into the stricter title-composition contract and
    // may expand the frame to contain an adapted title rail.
    const y = enforcesBoundaryTitleComposition
      ? Math.min(boundary.y, title.y - layout.boundaryLabelFrameInset)
      : boundary.y;
    return {
      ...boundary,
      y,
      height: bottom - y,
      title,
    };
  });
}

const rawBoundaries = asArray(arch.boundaries).map(boundaryRect).filter(Boolean);
function resolveBoundaryTitles() {
  if (!enforcesBoundaryTitleComposition || rawBoundaries.length === 0) {
    return {
      boundaries: layoutBoundaryTitles(rawBoundaries, layout.boundaryLabelFontMinimum),
      readabilityProblem: null,
    };
  }

  const maximumIterations = 32;
  let candidateBoundaries = rawBoundaries;
  for (let iteration = 0; iteration < maximumIterations; iteration += 1) {
    const budgetViewBoxWidth = resolvedViewBoxWidth(candidateBoundaries);
    const minimumFontSize = Math.max(
      layout.boundaryLabelFontMinimum,
      minimumReadableSourceTextPx(budgetViewBoxWidth) + 1e-6,
    );
    const nextBoundaries = layoutBoundaryTitles(rawBoundaries, minimumFontSize);
    const finalViewBoxWidth = resolvedViewBoxWidth(nextBoundaries);
    const finalMinimumFontSize = Math.max(
      layout.boundaryLabelFontMinimum,
      minimumReadableSourceTextPx(finalViewBoxWidth),
    );
    if (minimumFontSize >= finalMinimumFontSize) {
      return { boundaries: nextBoundaries, readabilityProblem: null };
    }
    candidateBoundaries = nextBoundaries;
  }

  const finalViewBoxWidth = resolvedViewBoxWidth(candidateBoundaries);
  return {
    boundaries: candidateBoundaries,
    readabilityProblem: `[composition/desktop-readability] Boundary title layout did not converge after ${maximumIterations} iterations for the final ${finalViewBoxWidth}px viewBox — shorten boundary labels, provide a wider authored viewBox, or move wrapped components closer to the left edge.`,
  };
}

const resolvedBoundaryTitles = resolveBoundaryTitles();
const boundaries = resolvedBoundaryTitles.boundaries;
const compositionFrames = boundaries.map((boundary, index) => ({
  ...boundary,
  id: boundary.id || index,
  kind: boundary.kind || 'boundary',
  radius: boundary.kind === 'security-group' ? 8 : 12,
}));

function componentContext(component) {
  const scopes = boundaries
    .filter((boundary) => asArray(boundary.wraps).includes(component.id))
    .sort((a, b) => (b.width * b.height) - (a.width * a.height))
    .map((boundary) => boundary.label);
  return scopes.length ? scopes.join(' › ') : i18nText(arch.meta.locale, 'node.context.architecture');
}

// ---- Auto viewBox: fit all geometry + the measured resolved legend ----------
const viewBox = arch.meta?.viewBox || autoViewBoxFor(boundaries);
const legendY = () => viewBox[1] - 16;

// ---- Validation: mechanical correctness, never layout taste -----------------
function validateArchitecture() {
  const problems = [];
  if (resolvedBoundaryTitles.readabilityProblem) {
    problems.push(resolvedBoundaryTitles.readabilityProblem);
  }
  if (components.size !== asArray(arch.components).length) problems.push('Component ids must be unique.');
  if (grid) {
    validateGridPlacement(arch, grid, problems);
  } else {
    for (const c of asArray(arch.components)) {
      if (!Array.isArray(c.pos) || c.pos.length !== 2) {
        problems.push(`Component "${c.id}" must include pos [x, y] when layout.mode is omitted (free placement).`);
      }
    }
  }

  for (const c of components.values()) {
    if (!isFinitePoint(c.x, c.y, c.width, c.height)) {
      problems.push(`Component "${c.id}" has non-finite pos/size — pos and size must be [number, number].`);
      continue;
    }
    if (c.width <= 0 || c.height <= 0) {
      problems.push(`Component "${c.id}" has invalid size ${c.width}x${c.height} — width and height must be greater than 0.`);
      continue;
    }
    if (c.x < 0 || c.y < 0 || c.x + c.width > viewBox[0] || c.y + c.height > viewBox[1]) {
      problems.push(`Component "${c.id}" falls outside the viewBox ${viewBox[0]}x${viewBox[1]} — adjust pos/size or set a larger meta.viewBox.`);
    }
    const estLabelW = textUnits(c.label) * 6.6;
    if (estLabelW > c.width + 8) {
      problems.push(`Label "${c.label}" (~${Math.round(estLabelW)}px) is wider than component "${c.id}" (${c.width}px) — shorten the label or widen size.`);
    }
    // sublabel and tag render as single unwrapped <text> elements; shrink-to-fit
    // handles the ordinary case, this rejects what it cannot rescue.
    const availableTextW = availableNodeTextWidth(c.width);
    for (const [field, value, minimum] of [
      ['Sublabel', c.sublabel, componentTextFit.sublabelMinimum],
      ['Tag', c.tag, componentTextFit.tagMinimum],
    ]) {
      if (!value) continue;
      const minimumW = minimumNodeTextWidth(value, minimum);
      if (minimumW > availableTextW) {
        problems.push(`${field} "${value}" needs ~${Math.ceil(minimumW)}px at the ${minimum}px legible minimum, but component "${c.id}" provides ${availableTextW}px — shorten the ${field.toLowerCase()} or widen size.`);
      }
    }
  }

  // Component overlap — the highest-traffic hand-placement failure mode.
  const list = [...components.values()];
  for (let i = 0; i < list.length; i += 1) {
    for (let j = i + 1; j < list.length; j += 1) {
      if (rectsOverlap(list[i], list[j], 8)) {
        problems.push({
          message: `Components "${list[i].id}" and "${list[j].id}" are less than 8px apart — move one or shrink its size.\n${suggestComponentSeparation(list[i], list[j], 8)}`,
          subject: { components: [list[i].id, list[j].id] },
          evidence: { rects: [list[i], list[j]].map(({ id, x, y, width, height }) => ({ id, x, y, width, height })) },
          supportedFixes: [`move "${list[j].id}" right to x ${Math.round(list[i].x + list[i].width + 8)} or down to y ${Math.round(list[i].y + list[i].height + 8)}`, 'or shrink one of the two sizes'],
        });
      }
    }
  }

  // Boundaries: every wrapped id must exist; the computed box must stay in view.
  for (const boundary of asArray(arch.boundaries)) {
    for (const id of asArray(boundary.wraps)) {
      if (!components.has(id)) problems.push(`Boundary "${boundary.label}" wraps unknown component "${id}".`);
    }
  }
  const viewBoxRect = { x: 0, y: 0, width: viewBox[0], height: viewBox[1] };
  for (const boundary of boundaries) {
    if (!enforcesBoundaryTitleComposition) continue;
    if (boundary.title.minimumWidth > boundary.title.availableWidth) {
      problems.push(
        `Boundary label "${boundary.label}" needs ~${Math.ceil(boundary.title.minimumWidth)}px to fit at the `
        + `${Number(boundary.title.minimumFontSize.toFixed(2))}px desktop-readable source minimum, but its frame provides ${Math.floor(boundary.title.availableWidth)}px — `
        + 'shorten the boundary label, increase pad, or widen the wrapped component layout.',
      );
    }
    if (!rectContains(boundary, boundary.title)) {
      problems.push(
        `Boundary label "${boundary.label}" extends outside its final frame — shorten the label or increase boundary pad.`,
      );
    }
    if (!rectContains(viewBoxRect, boundary.title)) {
      problems.push(
        `Boundary label "${boundary.label}" extends outside the viewBox — move wrapped components away from the canvas edge, shorten the label, or increase the viewBox.`,
      );
    }
    for (const component of components.values()) {
      if (!rectsOverlap(boundary.title, component)) continue;
      problems.push(
        `Boundary label "${boundary.label}" overlaps component "${component.id}" — move the component, increase boundary title space, or shorten the label.`,
      );
    }
  }
  for (let leftIndex = 0; leftIndex < boundaries.length; leftIndex += 1) {
    const left = boundaries[leftIndex];
    for (let rightIndex = leftIndex + 1; rightIndex < boundaries.length; rightIndex += 1) {
      const right = boundaries[rightIndex];
      if (enforcesBoundaryTitleComposition && rectsOverlap(left.title, right.title)) {
        problems.push(
          `Boundary labels "${left.label}" and "${right.label}" overlap — shorten a label or increase boundary title space.`,
        );
      }
    }
  }
  for (const b of boundaries) {
    if (b.x < 0 || b.y < 0 || b.x + b.width > viewBox[0] || b.y + b.height > viewBox[1]) {
      problems.push(`Boundary "${b.label}" extends outside the viewBox — its members sit too close to the canvas edge; add margin or enlarge meta.viewBox.`);
    }
  }

  problems.push(...routeProblems({
    connections: arch.connections,
    components,
    pathFor,
    connectionEndpointSide,
    profile: arch.meta?.quality_profile,
    frames: compositionFrames,
    boundaries,
    edgeLabelFont,
    enforcesBoundaryTitleComposition,
    requiresAttachedLabels: isReview || layeredMode,
  }));

  if (problems.length) {
    throwDiagnosticProblems(layeredMode ? 'Layered layout validation failed' : 'Architecture layout validation failed', problems, {
      subject: { diagramType: 'architecture', ...(layeredMode ? { layout: 'layered' } : {}) },
    });
  }
}

function buildLayoutReport() {
  const labels = [];
  for (const conn of asArray(arch.connections)) {
    if (!conn.label || !components.has(conn.from) || !components.has(conn.to)) continue;
    const [lx, ly] = labelPoint(conn, pathFor(conn).points);
    const w = Math.max(30, textUnits(conn.label) * 4.8 + 10);
    labels.push({
      text: conn.label,
      x: Math.round(lx - w / 2),
      y: Math.round(ly - 10),
      width: Math.round(w),
      height: 14,
      labelAt: [Math.round(lx), Math.round(ly)],
    });
  }
  return {
    ok: true,
    diagram_type: 'architecture',
    layout: grid ? { mode: 'grid', ...grid } : { mode: 'free' },
    viewBox,
    components: [...components.values()].map(componentBox),
    boundaries: boundaries.map(boundaryBox),
    connections: asArray(arch.connections)
      .filter((conn) => components.has(conn.from) && components.has(conn.to))
      .map((conn) => {
        const routed = pathFor(conn);
        const labelAt = conn.label ? labelPoint(conn, routed.points) : null;
        return connectionPath(conn, routed, labelAt);
      }),
    labels,
  };
}

// ---- Connection routing ------------------------------------------------------
const { pathFor, connectionEndpointSide } = createRouter(arch.connections, components);

// ---- Rendering ---------------------------------------------------------------
function renderLanguageRegions() {
  const nodes = [...components.values()].map((c, i) => ({ x: c.x, y: c.y, width: c.width, height: c.height, language: componentLanguages[i] }));
  // Oversized bounds (painted via overflow: visible on the root svg) tint the container padding too;
  // the diagram container clips them.
  const bleed = 100;
  return languageRegionLoops(nodes, [-bleed, -bleed, viewBox[0] + bleed, viewBox[1] + bleed]).map(({ language, loops }) => {
    const [color, label] = regionStyleFor(language);
    // Label sits in the first visible canvas corner the zone owns.
    const inside = (x, y) => loops.reduce((n, pts) => n + pts.filter((p, k) => {
      const q = pts[(k + 1) % pts.length];
      return p[0] === q[0] && p[0] > x && y >= Math.min(p[1], q[1]) && y < Math.max(p[1], q[1]);
    }).length, 0) % 2 === 1;
    const inset = 14;
    const corners = [[inset, 20, 'start'], [viewBox[0] - inset, 20, 'end'], [inset, viewBox[1] - 8, 'start'], [viewBox[0] - inset, viewBox[1] - 8, 'end']];
    // A zone that owns no canvas corner takes the leftmost point it owns along the bottom or top canvas edge,
    // instead of falling back to a corner another zone's label already uses.
    const edge = [viewBox[1] - 8, 20].flatMap((y) => {
      for (let x = inset; x < viewBox[0] - inset; x += 10) if (inside(x, y - 6)) return [[x + inset, y, 'start']];
      return [];
    })[0];
    const [lx, ly, anchor] = corners.find(([x, y]) => inside(x, y - 6)) || edge || corners[0];
    return `        <g data-graph-role="language-region" data-language="${esc(language)}" aria-hidden="true" pointer-events="none">
          <path d="${roundedLoopsPath(loops)}" fill="${color}" fill-opacity="0.22" stroke="${color}" stroke-opacity="0.6" stroke-width="1" stroke-dasharray="6 4"/>
          <text x="${lx}" y="${ly}" text-anchor="${anchor}" class="t-muted" font-size="11" font-weight="700">${esc(label)}</text>
        </g>`;
  }).join('\n');
}

function renderBoundaryFrame(b, index) {
  const cls = b.kind === 'security-group' ? 'c-security-group' : 'c-region';
  const rx = b.kind === 'security-group' ? 8 : 12;
  return `        <rect data-graph-role="structural-frame" data-composition-frame-kind="${esc(b.kind || 'boundary')}" data-composition-frame-id="${index}" data-composition-frame-label="${esc(b.label)}" x="${b.x}" y="${b.y}" width="${b.width}" height="${b.height}" rx="${rx}" class="${cls}" stroke-width="1"/>`;
}

function renderBoundaryLabel(b, index) {
  const labelCls = b.kind === 'security-group' ? 't-security' : 't-cloud';
  return `        <g data-graph-role="structural-frame-label" data-composition-frame-id="${index}" data-composition-frame-kind="${esc(b.kind || 'boundary')}" data-composition-frame-label="${esc(b.label)}">
          <rect data-graph-role="structural-frame-label-mask" x="${b.title.x}" y="${b.title.y}" width="${b.title.width}" height="${b.title.height}" rx="3" class="c-mask"/>
          <text data-boundary-label="" x="${b.title.x + 4}" y="${b.title.y + b.title.baselineOffset}" class="${labelCls}" font-size="${b.title.fontSize}" font-weight="600">${esc(b.label)}</text>
        </g>`;
}

function renderConnectionPath(conn, index) {
  if (conn.kind === 'counterpart') { // same component on two platforms: dashed, no arrowhead, no focus/path attributes
    const routed = pathFor(conn);
    const story = conn.code?.story ? `<title>${esc(conn.code.story)}</title>` : '';
    return `        <path data-edge-kind="counterpart" data-composition-points="${routePointsValue(routed.points)}" d="${routed.d}" class="a-dashed" stroke-width="${conn.width || 1.5}">${story}</path>`;
  }
  const [cls, marker] = arrowClassMap[conn.variant || 'default'] || arrowClassMap.default;
  const routed = pathFor(conn);
  const strokeWidth = conn.width || (conn.variant === 'emphasis' ? 1.8 : 1.5);
  return `        <path ${focusEdgeAttrs(conn.from, conn.to, conn.label, index, conn.id)} data-composition-points="${routePointsValue(routed.points)}" d="${routed.d}" class="${cls}"${animateAttr(arch.meta, 'edge', index)} stroke-width="${strokeWidth}" marker-end="url(#${marker})"/>`;
}

function renderConnectionLabel(conn, index) {
  if (!conn.label) return '';
  const [lx, ly] = labelPoint(conn, pathFor(conn).points);
  const w = Math.max(30, textUnits(conn.label) * 4.8 + 10);
  const focus = conn.kind === 'counterpart' ? 'data-edge-kind="counterpart"' : focusEdgeAttrs(conn.from, conn.to, conn.label, index, conn.id);
  return `        <g data-detail="context" ${focus}>
          <rect x="${lx - w / 2}" y="${ly - edgeLabelFont - 2}" width="${w}" height="${edgeLabelFont + 6}" rx="3" class="c-mask"/>
          <text x="${lx}" y="${ly}" class="${variantAccent(conn.variant)}" font-size="${edgeLabelFont}" text-anchor="middle">${esc(conn.label)}</text>
        </g>`;
}

function renderComponent(c) {
  const fill = componentFill[c.type] || 'c-external';
  const accent = componentText[c.type] || 't-muted';
  const cx = c.cx;
  const hasSub = c.sublabel != null && c.sublabel !== '';
  // Layered (code-map) nodes with label, sublabel and tag fill the box as
  // three evenly spaced lines with larger type; other nodes keep the compact
  // centred layout.
  const threeLine = Boolean(c.layer) && hasSub && Boolean(c.tag);
  const lineFont = Math.min(12, c.height * 0.17);
  const labelY = threeLine ? c.y + c.height * 0.4 : hasSub ? c.y + c.height / 2 - 2 : c.y + c.height / 2 + 4;
  const subY = threeLine ? c.y + c.height * 0.66 : c.y + c.height / 2 + 14;
  const tagY = threeLine ? c.y + c.height * 0.89 : c.y + c.height - 8;
  const subPreferred = threeLine ? lineFont : isReview ? 11 : componentTextFit.sublabelPreferred;
  const sub = hasSub
    ? `\n        <text data-detail="context" x="${cx}" y="${subY}" class="t-muted" font-size="${fittedNodeFontSize(c.sublabel, threeLine ? c.width - 24 : c.width, subPreferred, componentTextFit.sublabelMinimum)}" text-anchor="middle">${esc(c.sublabel)}</text>`
    : '';
  const tag = c.tag
    ? `\n        <text data-detail="context" x="${cx}" y="${tagY}" class="${accent}" font-size="${fittedNodeFontSize(c.tag, c.width, subPreferred, componentTextFit.tagMinimum)}" text-anchor="middle"${threeLine ? ' font-weight="600"' : ''}>${esc(c.tag)}</text>`
    : '';
  const labelFontSize = fittedNodeFontSize(c.label, (threeLine ? c.width - 40 : c.width), threeLine ? Math.min(15, c.height * 0.22) : isReview ? REVIEW_LABEL_PREFERRED_PX : 11, 8);
  const passport = { kind: c.type, sublabel: c.sublabel, tag: c.tag, context: componentContext(c) };
  return `        <g ${focusNodeAttrs(c.id, c.label, passport, arch.meta.locale)}>
          ${focusNodeTitle(c.label, passport)}
          <rect x="${c.x}" y="${c.y}" width="${c.width}" height="${c.height}" rx="6" class="c-mask"/>
          <rect x="${c.x}" y="${c.y}" width="${c.width}" height="${c.height}" rx="6" class="${fill}"${animateAttr(arch.meta, 'node', componentSteps.get(c.id))} stroke-width="1.5"/>
          ${renderSemanticSigil(c.layer || c.type, { x: c.x + 6, y: c.y + 6, toneOf: c.type })}
          <text data-node-label=""${hasSub ? ' data-detail-anchor=""' : ''} x="${cx}" y="${labelY}" class="t-primary" font-size="${labelFontSize}" font-weight="600" text-anchor="middle">${esc(c.label)}</text>${sub}${tag}
        </g>`;
}

function renderLegend() {
  const entries = architectureLegendEntries;
  const relationshipObstacles = relationshipLegendObstacles(arch.connections, {
    pointsFor: (connection) => pathFor(connection).points,
    labelRectFor: (connection) => {
      if (!connection.label) return null;
      const [x, y] = labelPoint(connection, pathFor(connection).points);
      const width = Math.max(30, textUnits(connection.label) * 4.8 + 10);
      return { x: x - width / 2, y: y - 10, width, height: 14 };
    },
  });
  const contentBottom = Math.max(
    0,
    ...[...components.values()].map((component) => component.y + component.height),
    ...boundaries.map((boundary) => boundary.y + boundary.height),
  );
  return renderResolvedLegend({
    entries,
    locale: arch.meta.locale,
    layout: {
      x: layout.margin,
      baselineY: legendY(),
      width: viewBox[0] - layout.margin * 2,
      minTitleY: contentBottom + 8,
      obstacles: relationshipObstacles,
      unfit: arch.meta?.legend === undefined ? 'hide' : 'error',
      diagramType: 'architecture',
    },
    renderSwatch: (entry) => entry.layer
      ? renderSemanticSigil(entry.layer, { x: entry.x + 1, y: entry.baseline - 10, size: 12 })
      : entry.language
        ? `<rect x="${entry.x}" y="${entry.baseline - 9}" width="16" height="10" rx="2.5" fill="${regionStyleFor(entry.language)[0]}" fill-opacity="0.35" stroke="${regionStyleFor(entry.language)[0]}" stroke-width="1"/>`
        : `<rect x="${entry.x}" y="${entry.baseline - 9}" width="16" height="10" rx="2.5" class="${componentFill[entry.kind] || 'c-external'}" stroke-width="1"/>`,
  });
}

// One dashed boundary frame per tier of a layered layout (the boundary look), titled at its top-left; columns carry their own x and width.
function renderTierBands() {
  return layeredPlanned.bands.map((band) => {
    const x = band.x ?? layeredPlanned.bandX;
    return `        <g data-graph-role="tier-band" data-tier="${band.tier}" aria-hidden="true" pointer-events="none">
          <rect class="c-region" x="${x}" y="${band.y}" width="${band.width ?? viewBox[0] - layeredPlanned.bandX * 2}" height="${band.height}" rx="12" stroke-width="1"/>
          <text class="t-cloud" x="${x + 14}" y="${band.y + 18}" font-size="13"><tspan font-weight="700">${esc(band.label[0])}</tspan>${band.label[1] ? `<tspan class="t-tier-sub" dx="6">— ${esc(band.label[1])}</tspan>` : ''}</text>
        </g>`;
  }).join('\n');
}

// Slots hold what differs between the two layouts; the viewer swaps them and moves nodes and routes in place.
const slot = (name, content) => (layeredMode || layeredSchemes.length ? `        <g data-layout-slot="${name}">\n${content}\n        </g>` : content);

function renderSvg() {
  return `      <svg viewBox="0 0 ${viewBox[0]} ${viewBox[1]}" ${svgRootAttrs(arch.meta)}${regionLanguages.length ? ' style="overflow: visible"' : ''}>
${svgAccessibleText(arch.meta, 'architecture')}
${renderDefinitions()}

        <!-- Background Grid -->
        <rect width="100%" height="100%" fill="url(#grid)" />

${slot('back', `        <!-- Language zones (full-bleed background partition per source language) -->
${renderLanguageRegions()}

        <!-- Boundaries (behind everything) -->
${boundaries.map(renderBoundaryFrame).join('\n\n')}${layeredMode ? `\n\n        <!-- Tier bands -->\n${renderTierBands()}` : ''}`)}

        <!-- Connection paths (before components for correct z-order) -->
${asArray(arch.connections).map(renderConnectionPath).join('\n')}

        <!-- Components -->
${[...components.values()].map(renderComponent).join('\n\n')}

        <!-- Connection labels -->
${asArray(arch.connections).map(renderConnectionLabel).join('\n')}

${slot('front', `        <!-- Boundary labels (foreground masks keep routes out of titles) -->
${boundaries.map(renderBoundaryLabel).join('\n\n')}`)}

        <!-- Legend -->
${slot('legend', renderLegend())}
      </svg>`;
}

// Each scheme's layered svg comes from child runs (see top of file); when no candidate of a scheme validates, deliver aborts with the first diagnostic.
function renderLayeredSvg(scheme) {
  let firstFailure = null;
  for (const plan of layeredPlans[scheme]) {
    const child = spawnSync(process.execPath, [fileURLToPath(import.meta.url), ...process.argv.slice(2)], {
      encoding: 'utf8',
      maxBuffer: 1 << 28,
      env: { ...process.env, ARCHIFY_LAYERED: JSON.stringify(plan) },
    });
    if (child.status === 0) return child.stdout.trim();
    firstFailure ||= child;
  }
  const hint = `change code.layers.${scheme} of the nodes named in the diagnostic (a node that talks to several layers sits best in the middle one) or drop a connection, then deliver again`;
  let payload = null;
  try { payload = JSON.parse((firstFailure.stderr || '').trim()); } catch { /* plain stack trace outside diagnostic mode */ }
  if (payload?.diagnostics?.length) {
    throwDiagnosticError(payload.error || `Layered layout (${scheme}) validation failed`, payload.diagnostics.map((d) => ({ ...d, supportedFixes: [...(d.supportedFixes || []), hint] })));
  }
  process.stderr.write(`${firstFailure.stderr || `Layered layout (${scheme}) run failed (${firstFailure.signal || firstFailure.status}).\n`}\nLayered layout (${scheme}): ${hint}.\n`);
  process.exit(firstFailure.status || 1);
}

validateArchitecture();
if (layoutJsonMode) {
  console.log(JSON.stringify(buildLayoutReport(), null, 2));
  process.exit(0);
}
if (layeredMode) {
  process.stdout.write(renderSvg());
} else {
  writeDiagram({
    outPath,
    template,
    diagramType: 'architecture',
    meta: arch.meta,
    svg: renderSvg(),
    layeredSvg: layeredSchemes.length ? Object.fromEntries(layeredSchemes.map((scheme) => [scheme, renderLayeredSvg(scheme)])) : null,
    cards: arch.cards,
  });
}
