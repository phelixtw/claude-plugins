# Archify JSON IR Schemas

The renderer consumes a JSON intermediate representation (IR) validated
against the architecture schema in this folder before any layout work happens.

## Files

| Schema | Governs | Structural arrays |
|--------|---------|-------------------|
| `architecture.schema.json` | `diagram_type: "architecture"` | `components`, `boundaries`, `connections` |
| `common.schema.json` | shared `$defs` only (no top-level document) | — |

The architecture schema requires `schema_version`, `diagram_type`, `meta` (with
`title`), and its structural arrays — except `cards`, which is optional — and sets `additionalProperties: false` at every
level, so unknown fields are rejected rather than silently ignored.

Components accept an optional
`code` (`nodeCode`) and connections an optional `code` (`relationshipCode`) for the viewer's
Code change panel and in-page path tabs.

Every `meta` object also accepts `animation: "trace"` for opt-in SVG/CSS motion
in generated HTML. Omit it, or set `"none"`, for the default static output.
It also accepts `locale: "en" | "zh-CN"`. The field selects the fixed Viewer
UI, renderer-owned default legend and accessibility copy, document-title
suffix, and `<html lang>` value; it does not translate authored strings.
Omitting it preserves legacy behavior and resolves to English. Unsupported
locale values fail schema validation instead of being guessed or silently
rewritten.
### Legend presentation contract

Every `meta` object accepts an optional legend shape:

```json
"legend": {
  "mode": "auto",
  "entries": {
    "security": { "label": "restricted data", "visible": true }
  }
}
```

`mode` is `auto` (the default), `all`, or `hidden`. `auto` includes only kinds
present in typed IR; `all` includes the renderer's full stable catalog;
`hidden` removes the complete legend and takes precedence over entry overrides.
Architecture documents that omit an explicit `viewBox` size that automatic
viewBox from the same measured resolved legend footprint used for final SVG
layout. Legacy documents that omit `meta.legend` use a
compatibility-safe implicit `auto`: if the resolved legend cannot fit an
explicit authored viewBox without overlap, Archify omits the complete legend
instead of turning a previously valid schema-v1 document into a hard failure.
Once an author adds `meta.legend` (including explicit `mode: "auto"`), the
layout is intentional and unfit labels or bands fail with a path-prefixed
diagnostic. An entry may set a non-empty, bounded `label`, boolean `visible`,
or both.
`visible: false` removes a resolved entry and `visible: true` forces a supported
but unused kind into the visual legend. Unknown kinds and properties fail
strict validation.

Supported keys are renderer-owned:

| Renderer | `meta.legend.entries` keys |
|---|---|
| Architecture | `frontend`, `backend`, `database`, `cloud`, `security`, `messagebus`, `external` |

Labels are presentation only: they do not rename the stable kind, change
nodes/relationships, or create Semantic Lens edge facts. Component entries
backed by exact compiled node facts receive the interactive Semantic Legend
bridge.

`connections` accept an optional author-controlled `id` using the shared ID
pattern. The renderer keeps its source-order runtime key separately, while the
authored ID enables a stable `#relation=<id>` viewer link that survives array
reordering. ID-less documents remain valid and their relationship pins stay
local to the current page.

## schema_version policy

The architecture schema keeps `schema_version` pinned to `1`.

A file that validates today must keep validating and rendering within its
declared version throughout the 2.x release line. Additive viewer,
accessibility, and presentation improvements may enhance generated HTML, but
they must not reinterpret authored IR or turn a previously valid profile-less
v1 file into a new hard layout failure. Breaking IR changes require a new
version; additive, backwards-compatible fields do not.

## Shared definitions (common.schema.json)

The architecture schema references `common.schema.json#/$defs/...`:

- `id` — element identifiers, pattern `^[a-zA-Z][a-zA-Z0-9_-]*$`
- `point` — an `[x, y]` pair of numbers (used by `via` and `labelAt`)
- `componentType` — `frontend`, `backend`, `database`, `cloud`, `security`,
  `messagebus`, `external`
- `locale` — the bounded renderer locale, `en` or `zh-CN`
- `variant` — `default`, `emphasis`, `security`, `dashed`
- `legendMode` and `legendEntry` — the shared strict mode and label/visibility
  override shapes used by each renderer-owned key map
- `cards` — the summary-card blocks rendered below the SVG

## Runtime validation

At development time, `scripts/generate-validators.mjs` compiles the
schemas with ajv's draft 2020-12 standalone generator using `strict: true` and
`allErrors: true`. The generated `renderers/shared/generated-validators.mjs`
is committed and shipped with the skill, so runtime validation has no npm or
network dependency. `renderers/shared/validator.mjs` applies the matching
standalone validator before the renderer's own layout checks.
The shared loader then checks one cross-collection fact that JSON Schema cannot
express cleanly here: duplicate authored relationship IDs.

## Visual quality

`meta.quality_profile` controls how strictly Archify judges composition.

`npm test` runs the generator in check mode and fails when the committed
validators drift from their schemas.

## Error format

Schema violations exit non-zero. Each ajv error is reported on its own line as
the instance path — annotated with the nearest enclosing element's `id` or
`label` — followed by the message and parameters:

```text
architecture schema validation failed:
  /components/3 (id/label: "Consent Gate") must NOT have additional properties {"additionalProperty":"colour"}
```

Schemas catch shape errors (types, enums, ranges, unknown fields); geometry
problems such as overlaps and label collisions are the renderers' job.
