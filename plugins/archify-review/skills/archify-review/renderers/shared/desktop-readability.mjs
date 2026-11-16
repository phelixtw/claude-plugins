export const DESKTOP_READABILITY_VIEWPORT = Object.freeze({ width: 1440, height: 900 });
export const DESKTOP_READER_MIN_WIDTH = 960;
export const DESKTOP_READER_HORIZONTAL_CHROME = 30;
export const DESKTOP_READER_DIAGRAM_WIDTH = DESKTOP_READER_MIN_WIDTH - DESKTOP_READER_HORIZONTAL_CHROME;
export const MIN_PROJECTED_NODE_TEXT_PX = 6;

export function projectedNodeTextPx(sourceFontPx, viewBoxWidth, diagramWidth = DESKTOP_READER_DIAGRAM_WIDTH) {
  if (![sourceFontPx, viewBoxWidth, diagramWidth].every(Number.isFinite) || viewBoxWidth <= 0 || diagramWidth <= 0) {
    return Number.NaN;
  }
  return sourceFontPx * Math.min(1, diagramWidth / viewBoxWidth);
}

export function minimumReadableSourceTextPx(
  viewBoxWidth,
  diagramWidth = DESKTOP_READER_DIAGRAM_WIDTH,
  minimumProjectedPx = MIN_PROJECTED_NODE_TEXT_PX,
) {
  if (![viewBoxWidth, diagramWidth, minimumProjectedPx].every(Number.isFinite)
    || viewBoxWidth <= 0
    || diagramWidth <= 0
    || minimumProjectedPx <= 0) {
    return Number.NaN;
  }
  return minimumProjectedPx / Math.min(1, diagramWidth / viewBoxWidth);
}

// Code-review diagrams: the reader's diagram column is ~1100px at a 1440px viewport (file tree takes the rest).
export const REVIEW_DIAGRAM_WIDTH_PX = 1100;
export const REVIEW_DIAGRAM_HEIGHT_PX = 570;
export const REVIEW_LABEL_PREFERRED_PX = 15;
export const REVIEW_EDGE_LABEL_PX = 11;
export const REVIEW_MIN_LABEL_PX = 13;
export const REVIEW_MIN_FILL = 0.3;
