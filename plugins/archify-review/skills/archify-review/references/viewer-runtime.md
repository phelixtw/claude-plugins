# Viewer Runtime reference

Read this only when the user asks for a reader-facing capability. Ordinary generation does not require implementing or re-documenting these features; they are already in the generated HTML.

## Exploration

- Semantic Lens summarizes selected node/relationship kinds without changing authored geometry.
- Intent Trace previews a fine-pointer or keyboard target before committed focus.
- Semantic Passport opens on focus, shows authored upstream/downstream facts, supports a copyable deep link, has an explicit close action, closes on true outside activation and Escape, and never enters canonical export.
- Direct Relationship Pin makes a unique compiled relationship operable while preserving the authored line and stable relationship identity. It must fail closed on conflicting source/target/label/ID metadata.
- Route Probe resolves exactly two endpoints over authored directed relationships by clicking nodes. It never infers a route from geometry.

## Motion

`meta.animation: "trace"` enables a finite reader-controlled Live/Still trace. Static is the default. Still, reduced motion, page hiding, and print preserve complete static meaning. This is not a mobile product feature; narrow layouts get containment only.

## Truth boundary

Viewer state (Lens, focus, route, motion ownership, and temporary overlays) is not part of the canonical SVG and does not replace the checked HTML, the deterministic delivery receipt, or a real visual review. Do not add a hosted service, storage surface, dependency, schema branch, or mobile product surface for these viewer-only capabilities.

## Code change panel and path tabs

Optional `code` on nodes and relationships (see `schemas/common.schema.json` `nodeCode` / `relationshipCode`) is embedded as `archify-code-data`. A focused node with `code` shows the Code change panel (path, symbols, summary, before/after, highlighted diff) in place of the passport. When a route or reach trace touches any `code`, "View changes" adds an in-page tab with two independently scrolling columns. Left: one card per hop with only that hop's code (`from_symbol` to `to_symbol`, file:line, optional `diff` snippet, `note`); node-level `code.files` stay in the focus panel and are never dumped here, and the commit shows once in the tab header. Right top: the ordered real operations, one line each from relationship `action` (falls back to the node labels). Right bottom: a mini diagram cloned from the main nodes in path order; clicking a node focuses it. Optional relationship `variants` (`via` node ids plus `action`/`diff`/`note`) override the base fields when the path passes through one of those nodes, so a shared hop can show the code each path uses. Esc returns to the Diagram tab; the tab's close button or Delete removes it. `summary` should say what the code is responsible for; `before`/`after` are optional. `summary` and relationship `note` accept `[text](#node:<id>)` inline links that focus that node (back on the Diagram tab); unknown ids render as plain text and all other text is escaped. Optional component `layer` (`view`, `service`, `model`) swaps the top-left sigil for a layer glyph tinted by the node's `type`. Specs without `code` render exactly as before.
