import { asArray, rectsOverlap, cleanEndpointSideProblems, cleanFlowProblems, cleanCrossingProblems, cleanAmbiguousCorridorProblems, cleanBorderRunProblems, cleanRouteRhythmProblems, cleanLabelRouteClearanceProblems, suggestLabelObstacleFix, labelPoint, segmentIntersectsRect, anchor, automaticPortSpread, automaticPortRhythmBridge, defaultFromSide, defaultToSide, chosenSide, routeHonorsEndpointSides, normalizeRoutePoints, roundedPath } from '../shared/geometry.mjs';
import { textUnits } from '../shared/utils.mjs';

// Connection routing: ports, side-aware doglegs and obstacle checks for a set of measured components.
// Lives apart from the renderer so the three-tier layout search can score candidates with the real router.
export function createRouter(connections, components) {
  function routeClearsComponents(conn, points, clearance = 2) {
    const endpointIds = new Set([conn.from, conn.to]);
    for (const component of components.values()) {
      if (endpointIds.has(component.id)) continue;
      for (let index = 0; index < points.length - 1; index += 1) {
        if (segmentIntersectsRect({ start: points[index], end: points[index + 1] }, component, clearance)) {
          return false;
        }
      }
    }
    return true;
  }

  function routeClearsEndpointComponents(points, from, to) {
    const lastSegment = points.length - 2;
    for (let index = 0; index <= lastSegment; index += 1) {
      const segment = { start: points[index], end: points[index + 1] };
      if (index > 0 && segmentIntersectsRect(segment, from)) return false;
      if (index < lastSegment && segmentIntersectsRect(segment, to)) return false;
    }
    return true;
  }

  const OUTWARD_SIDE_VECTOR = {
    left: [-1, 0],
    right: [1, 0],
    top: [0, -1],
    bottom: [0, 1],
  };

  function outwardStub(point, side, distance = 24) {
    const [dx, dy] = OUTWARD_SIDE_VECTOR[side] || [0, 0];
    return [point[0] + dx * distance, point[1] + dy * distance];
  }

  function collinearBacktrack(a, b, c) {
    const first = [b[0] - a[0], b[1] - a[1]];
    const second = [c[0] - b[0], c[1] - b[1]];
    const cross = first[0] * second[1] - first[1] * second[0];
    const dot = first[0] * second[0] + first[1] * second[1];
    return Math.abs(cross) <= 0.0001 && dot < -0.0001;
  }

  function sideAwareBridgeCandidates(start, end, fromSide, toSide) {
    const startStub = outwardStub(start, fromSide);
    const endStub = outwardStub(end, toSide);
    const rawCandidates = [];
    const minimumBridge = 16;
    const verticalSides = new Set(['top', 'bottom']);
    const horizontalSides = new Set(['left', 'right']);

    // Port spreading can leave parallel-side anchors only a few pixels apart.
    // Route through a bounded outside channel so we keep both endpoint normals
    // without introducing a tiny, noisy connector between the two stubs.
    if (verticalSides.has(fromSide) && verticalSides.has(toSide)
        && Math.abs(start[0] - end[0]) < minimumBridge) {
      for (const channelX of [
        Math.max(start[0], end[0]) + minimumBridge,
        Math.min(start[0], end[0]) - minimumBridge,
      ]) {
        rawCandidates.push([
          startStub,
          [channelX, startStub[1]],
          [channelX, endStub[1]],
          endStub,
        ]);
      }
    }
    if (horizontalSides.has(fromSide) && horizontalSides.has(toSide)
        && Math.abs(start[1] - end[1]) < minimumBridge) {
      for (const channelY of [
        Math.max(start[1], end[1]) + minimumBridge,
        Math.min(start[1], end[1]) - minimumBridge,
      ]) {
        rawCandidates.push([
          startStub,
          [startStub[0], channelY],
          [endStub[0], channelY],
          endStub,
        ]);
      }
    }

    rawCandidates.push(
      [startStub, [endStub[0], startStub[1]], endStub],
      [startStub, [startStub[0], endStub[1]], endStub],
    );
    return rawCandidates.map((candidate) => normalizeRoutePoints([start, ...candidate, end]))
      .filter((points) => points.length >= 2)
      .filter((points) => !collinearBacktrack(points[0], points[1], points[2] || points[1]))
      .filter((points) => !collinearBacktrack(points.at(-3) || points.at(-2), points.at(-2), points.at(-1)))
      .filter((points) => routeHonorsEndpointSides(points, fromSide, toSide))
      .map((points) => points.slice(1, -1));
  }

  const AUTOMATIC_PORT_CORNER_GUTTER = 16;
  const AUTOMATIC_PORT_ALIGNMENT_DELTA = 16;

  function portHasCornerClearance(rect, side, point) {
    if (side === 'left' || side === 'right') {
      const inset = Math.min(AUTOMATIC_PORT_CORNER_GUTTER, rect.height / 2);
      return point[1] >= rect.y + inset && point[1] <= rect.y + rect.height - inset;
    }
    if (side === 'top' || side === 'bottom') {
      const inset = Math.min(AUTOMATIC_PORT_CORNER_GUTTER, rect.width / 2);
      return point[0] >= rect.x + inset && point[0] <= rect.x + rect.width - inset;
    }
    return false;
  }

  function alignFacingPorts(conn, from, to, start, end, fromSide, toSide, ports) {
    const hasExplicitGeometry = (
      conn.via
      || (conn.route && conn.route !== 'auto')
      || conn.channelX !== undefined
      || conn.channelY !== undefined
      || conn.labelAt
    );
    const horizontallyFacing = (
      (fromSide === 'right' && toSide === 'left')
      || (fromSide === 'left' && toSide === 'right')
    );
    const verticallyFacing = (
      (fromSide === 'bottom' && toSide === 'top')
      || (fromSide === 'top' && toSide === 'bottom')
    );
    if (hasExplicitGeometry || (!horizontallyFacing && !verticallyFacing)) return { start, end };

    const fromSpread = Boolean(ports?.from);
    const toSpread = Boolean(ports?.to);
    if (fromSpread && toSpread) return { start, end };
    const hasExplicitSides = (
      (conn.fromSide && conn.fromSide !== 'auto')
      || (conn.toSide && conn.toSide !== 'auto')
    );
    if (!fromSpread && !toSpread && hasExplicitSides) return { start, end };

    const alignmentDelta = horizontallyFacing
      ? Math.abs(start[1] - end[1])
      : Math.abs(start[0] - end[0]);
    if (alignmentDelta >= AUTOMATIC_PORT_ALIGNMENT_DELTA) return { start, end };

    // Keep the shared endpoint's distinct spread slot and move only the
    // relationship's unshared endpoint onto that axis. With no spread endpoint,
    // retain the existing least-movement choice between the two facing sides.
    // If both endpoints are shared, preserve the outside bridge so no competing
    // port is silently collapsed.
    const alignEndToStart = horizontallyFacing
      ? { start, end: [end[0], start[1]] }
      : { start, end: [start[0], end[1]] };
    const alignStartToEnd = horizontallyFacing
      ? { start: [start[0], end[1]], end }
      : { start: [end[0], start[1]], end };
    const candidates = fromSpread
      ? [alignEndToStart]
      : toSpread
        ? [alignStartToEnd]
        : [alignEndToStart, alignStartToEnd];
    for (const candidate of candidates) {
      const points = [candidate.start, candidate.end];
      if (portHasCornerClearance(from, fromSide, candidate.start)
          && portHasCornerClearance(to, toSide, candidate.end)
          && routeHonorsEndpointSides(points, fromSide, toSide)
          && routeClearsEndpointComponents(points, from, to)
          && routeClearsComponents(conn, points)) {
        return candidate;
      }
    }
    return { start, end };
  }

  function routeVia(conn, from, to, start, end, fromSide, toSide) {
    if (conn.via) return conn.via;
    switch (conn.route || 'auto') {
      case 'straight':
        return [];
      case 'orthogonal-h': {
        const midX = (start[0] + end[0]) / 2;
        return [[midX, start[1]], [midX, end[1]]];
      }
      case 'orthogonal-v': {
        const midY = (start[1] + end[1]) / 2;
        return [[start[0], midY], [end[0], midY]];
      }
      case 'auto':
      default: {
        // Direct line unless the anchors are clearly orthogonal-friendly.
        const deltaX = Math.abs(start[0] - end[0]);
        const deltaY = Math.abs(start[1] - end[1]);
        if ((deltaX < 4 || deltaY < 4) && routeHonorsEndpointSides([start, end], fromSide, toSide)) return [];

        const rhythmBridge = automaticPortRhythmBridge(start, end, fromSide, toSide, {
          accept: (points) => (
            routeClearsEndpointComponents(points, from, to)
            && routeClearsComponents(conn, points)
          ),
        });
        if (rhythmBridge) return rhythmBridge.slice(1, -1);

        // Automatic port spreading can leave otherwise aligned endpoints only a
        // few pixels apart. A midpoint route would split that tiny difference
        // into two unreadable endpoint stubs, so take a bounded outside channel
        // when both anchors sit on parallel component sides.
        const minimumStub = 8;
        const fromVerticalSide = start[1] === from.y || start[1] === from.y + from.height;
        const toVerticalSide = end[1] === to.y || end[1] === to.y + to.height;
        if (fromVerticalSide && toVerticalSide && deltaX < minimumStub * 2) {
          const outsideChannels = [
            Math.max(start[0], end[0]) + minimumStub * 2,
            Math.min(start[0], end[0]) - minimumStub * 2,
          ];
          for (const channelX of outsideChannels) {
            const candidate = [[channelX, start[1]], [channelX, end[1]]];
            const points = [start, ...candidate, end];
            if (routeHonorsEndpointSides(points, fromSide, toSide) && routeClearsComponents(conn, points)) return candidate;
          }
        }

        const fromHorizontalSide = start[0] === from.x || start[0] === from.x + from.width;
        const toHorizontalSide = end[0] === to.x || end[0] === to.x + to.width;
        if (fromHorizontalSide && toHorizontalSide && deltaY < minimumStub * 2) {
          const outsideChannels = [
            Math.max(start[1], end[1]) + minimumStub * 2,
            Math.min(start[1], end[1]) - minimumStub * 2,
          ];
          for (const channelY of outsideChannels) {
            const candidate = [[start[0], channelY], [end[0], channelY]];
            const points = [start, ...candidate, end];
            if (routeHonorsEndpointSides(points, fromSide, toSide) && routeClearsComponents(conn, points)) return candidate;
          }
        }

        const midX = (start[0] + end[0]) / 2;
        const horizontalFirst = [[midX, start[1]], [midX, end[1]]];
        const midY = (start[1] + end[1]) / 2;
        const verticalFirst = [[start[0], midY], [end[0], midY]];
        const candidates = [horizontalFirst, verticalFirst];
        const sideSafe = candidates.filter((candidate) => (
          routeHonorsEndpointSides([start, ...candidate, end], fromSide, toSide)
        ));
        const sideAware = sideAwareBridgeCandidates(start, end, fromSide, toSide);
        const nearParallelPorts = (
          ((fromSide === 'top' || fromSide === 'bottom')
            && (toSide === 'top' || toSide === 'bottom')
            && deltaX < minimumStub * 2)
          || ((fromSide === 'left' || fromSide === 'right')
            && (toSide === 'left' || toSide === 'right')
            && deltaY < minimumStub * 2)
        );
        const ordered = [
          ...(nearParallelPorts ? sideAware : sideSafe),
          ...(nearParallelPorts ? sideSafe : sideAware),
          ...candidates.filter((candidate) => !sideSafe.includes(candidate)),
        ];
        for (const candidate of ordered) {
          const points = [start, ...candidate, end];
          if (routeClearsEndpointComponents(points, from, to) && routeClearsComponents(conn, points)) return candidate;
        }

        // Both bounded doglegs are blocked. Keep the best endpoint-safe route
        // when one exists so the universal Clean Flow gate reports the actual
        // obstacle; otherwise preserve the historical deterministic fallback
        // and let the endpoint-direction gate explain the side mismatch.
        return sideSafe[0] || sideAware[0] || horizontalFirst;
      }
    }
  }

  const pathCache = new Map();
  const automaticPorts = automaticPortSpread(connections, components);
  function connectionSides(conn) {
    const from = components.get(conn.from);
    const to = components.get(conn.to);
    return {
      fromSide: chosenSide(conn.fromSide, defaultFromSide(from, to)),
      toSide: chosenSide(conn.toSide, defaultToSide(from, to)),
    };
  }

  function connectionEndpointSide(conn, endpoint) {
    const field = endpoint === 'source' ? 'fromSide' : 'toSide';
    if (conn[field] && conn[field] !== 'auto') return conn[field];
    return connectionSides(conn)[field];
  }

  function pathFor(conn) {
    if (pathCache.has(conn)) return pathCache.get(conn);
    const from = components.get(conn.from);
    const to = components.get(conn.to);
    const ports = automaticPorts.get(conn);
    const { fromSide, toSide } = connectionSides(conn);
    const baseStart = ports?.from || anchor(from, fromSide);
    const baseEnd = ports?.to || anchor(to, toSide);
    const { start, end } = alignFacingPorts(
      conn,
      from,
      to,
      baseStart,
      baseEnd,
      fromSide,
      toSide,
      ports,
    );
    const points = [start, ...routeVia(conn, from, to, start, end, fromSide, toSide), end];
    const routed = { d: roundedPath(points, 8), points };
    pathCache.set(conn, routed);
    return routed;
  }

  return { pathFor, connectionEndpointSide };
}

// Route-level layout problems (endpoint sides, obstacles, crossings, corridors, border runs, rhythm, label overlaps).
// Width of the mask the renderer draws behind a connection label.
export const edgeLabelWidth = (label, edgeLabelFont) => Math.max(30, textUnits(label) * 0.6 * edgeLabelFont + 10);

// Label over a node: name the node and the connection, and offer the repairs (move the label, or widen the gap it sits in).
function labelOverlapProblem(rect, c, components, edgeLabelFont) {
  const { from, to } = rect.relation;
  const [a, b] = [components.get(from), components.get(to)];
  const gap = a && b ? Math.max(a.x - (b.x + b.width), b.x - (a.x + a.width)) : 0;
  const need = Math.ceil(edgeLabelWidth(rect.label, edgeLabelFont)) + 8;
  const lx = Math.round(rect.lx), ly = Math.round(rect.ly);
  const fixes = [
    `labelAt [${lx}, ${Math.round(c.y + c.height + 14)}] (below ${c.id})`,
    `labelAt [${lx}, ${Math.round(c.y - 4)}] (above ${c.id})`,
    ...(gap > 0 && gap < need ? [`move ${from} and ${to} apart: the gap is ${Math.round(gap)}px, a gap of at least ${need}px fits the label`] : []),
  ];
  const box = (r) => ({ x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height) });
  return {
    message: `Label "${rect.label}" overlaps component "${c.id}" — adjust labelDx/labelDy/labelSegment or set labelAt.\n${suggestLabelObstacleFix(rect, rect.lx, rect.ly, c)}`,
    subject: { component: c.id, connection: rect.relation.id ?? `${from}->${to}`, label: rect.label },
    evidence: { labelWidth: Math.round(rect.width), labelRect: box(rect), componentRect: box(c), ...(gap > 0 ? { gapBetweenEndpoints: Math.round(gap) } : {}) },
    supportedFixes: fixes,
  };
}

export function routeProblems({ connections, components, pathFor, connectionEndpointSide, profile, frames = [], boundaries = [], edgeLabelFont, enforcesBoundaryTitleComposition = false, requiresAttachedLabels = false }) {
  const problems = [];
  const compositionFrames = frames;
  for (const conn of asArray(connections)) {
    if (!components.has(conn.from)) problems.push(`Connection "${conn.label || conn.from}" references unknown source "${conn.from}".`);
    if (!components.has(conn.to)) problems.push(`Connection "${conn.label || conn.to}" references unknown target "${conn.to}".`);
    if (components.has(conn.from) && components.has(conn.to)) {
      const routed = pathFor(conn);
      const [start, end] = [routed.points[0], routed.points[routed.points.length - 1]];
      const distance = Math.hypot(end[0] - start[0], end[1] - start[1]);
      if (distance < 24) problems.push(`Connection "${conn.label || `${conn.from}->${conn.to}`}" is too short (${Math.round(distance)}px; minimum 24px) — place its components farther apart.`);
    }
  }

  problems.push(...cleanEndpointSideProblems({
    relations: connections,
    endpointIds: new Set(components.keys()),
    pathFor,
    diagramType: 'architecture',
    relationCollection: 'connections',
    fromSideFor: (conn) => connectionEndpointSide(conn, 'source'),
    toSideFor: (conn) => connectionEndpointSide(conn, 'target'),
    routeHint: 'keep automatic routing so the renderer can use a side-aware bridge, or set truthful fromSide/toSide with perpendicular via segments',
  }));
  problems.push(...cleanFlowProblems({
    relations: connections,
    obstacles: components.values(),
    pathFor,
    diagramType: 'architecture',
    relationCollection: 'connections',
    obstacleKind: 'component',
    routeHint: 'adjust fromSide/toSide, set route/via, or move the component'
  }));
  problems.push(...cleanCrossingProblems({
    relations: connections,
    endpointIds: new Set(components.keys()),
    pathFor,
    diagramType: 'architecture',
    relationCollection: 'connections',
    profile: profile,
    routeHint: 'adjust route/via or fromSide/toSide so the connections use separate corridors'
  }));
  problems.push(...cleanAmbiguousCorridorProblems({
    relations: connections,
    endpointIds: new Set(components.keys()),
    pathFor,
    diagramType: 'architecture',
    relationCollection: 'connections',
    profile: profile,
    routeHint: 'adjust route/via or fromSide/toSide so unrelated connections do not visually merge'
  }));
  problems.push(...cleanBorderRunProblems({
    relations: connections,
    endpointIds: new Set(components.keys()),
    frames: compositionFrames,
    pathFor,
    diagramType: 'architecture',
    relationCollection: 'connections',
    profile: profile,
    routeHint: 'adjust route/via or fromSide/toSide so the connection crosses the boundary perpendicularly instead of following its border'
  }));
  problems.push(...cleanRouteRhythmProblems({
    relations: connections,
    endpointIds: new Set(components.keys()),
    pathFor,
    diagramType: 'architecture',
    relationCollection: 'connections',
    profile: profile,
    routeHint: 'move route/via points into a wider corridor or move the component so every turn has room to read'
  }));

  // Connection labels must not land on top of components.
  const labelRects = [];
  for (const [connectionIndex, conn] of asArray(connections).entries()) {
    if (!conn.label || !components.has(conn.from) || !components.has(conn.to)) continue;
    const [lx, ly] = labelPoint(conn, pathFor(conn).points);
    const w = edgeLabelWidth(conn.label, edgeLabelFont);
    labelRects.push({ relation: conn, relationIndex: connectionIndex, label: conn.label, x: lx - w / 2, y: ly - 10, width: w, height: 14, lx, ly });
  }
  // A label reads as its connection's only when it sits on that route (code-review and layered layouts).
  const LABEL_DETACH_PX = 32;
  const segmentDistance = ([px, py], [ax, ay], [bx, by]) => {
    const dx = bx - ax, dy = by - ay;
    const t = dx || dy ? Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy))) : 0;
    return Math.hypot(px - ax - t * dx, py - ay - t * dy);
  };
  for (const rect of requiresAttachedLabels ? labelRects : []) {
    const points = pathFor(rect.relation).points;
    const center = [rect.x + rect.width / 2, rect.y + rect.height / 2];
    const away = Math.min(...points.slice(1).map((p, i) => segmentDistance(center, points[i], p)));
    if (away > LABEL_DETACH_PX) {
      problems.push(`Label "${rect.label}" sits ${Math.round(away)}px from its own connection (max ${LABEL_DETACH_PX}px) — remove a stale labelAt or move it onto the route.`);
    }
  }
  for (const rect of labelRects) {
    for (const c of components.values()) {
      if (rectsOverlap(rect, c, -2)) {
        problems.push(labelOverlapProblem(rect, c, components, edgeLabelFont));
      }
    }
    if (enforcesBoundaryTitleComposition) {
      for (const boundary of boundaries) {
        if (!rectsOverlap(boundary.title, rect)) continue;
        problems.push(
          `Boundary label "${boundary.label}" overlaps connection label "${rect.label}" — move the boundary title rail by adjusting wrapped component positions, or move the connection label with labelAt/labelDx/labelDy/labelSegment.`,
        );
      }
    }
  }
  problems.push(...cleanLabelRouteClearanceProblems({
    relations: connections,
    labels: labelRects,
    endpointIds: new Set(components.keys()),
    pathFor,
    diagramType: 'architecture',
    relationCollection: 'connections',
    profile: profile,
  }));
  return problems;
}
