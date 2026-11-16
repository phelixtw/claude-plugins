# Authoring contract

Read this reference only after the Fast authoring path calls for more detail. The schemas and examples remain authoritative.

## Schema lookup

Read both the mode schema and `schemas/common.schema.json`. The mode schemas use `$ref`, so the common file is where shared enums live.

- `componentType`: `frontend`, `backend`, `database`, `cloud`, `security`, `messagebus`, `external`
- `variant`: `default`, `emphasis`, `security`, `dashed`
- Relationship IDs use the shared identifier pattern and must be unique in their collection.

Do not invent fields. Use the nearest matching example for structure, then author fresh IDs, wording, facts, and layout.

## Legend contract

Omit `meta.legend` for the truthful default: `auto` lists only semantic kinds
present in typed IR. Use `mode: "all"` for a renderer reference or
`mode: "hidden"` to remove the full legend. Under `entries`, only keys listed
by the selected mode schema are valid; each key accepts `label`, `visible`, or
both. `visible: true` may show an unused supported convention, while
`visible: false` hides it. `hidden` cannot be overridden.

A label override changes reader wording only. Never infer a kind from prose or
use the legend to compensate for missing nodes or relationships.
Long labels are measured and wrap into deterministic rows. Architecture's
implicit automatic viewBox grows from that same measured footprint. For
backwards compatibility, a legacy document with no `meta.legend` may omit an
implicit auto legend that cannot fit its explicit viewBox; this never changes
its typed topology. Adding `meta.legend` makes the presentation intentional and
strict: if its resolved labels cannot fit the authored viewBox, shorten or hide
them, or widen the viewBox using the emitted diagnostic.

## Language consistency

Choose one primary authored language. An explicit user choice wins; otherwise
use the language of the request, or the conversation's dominant language when
the request itself is language-neutral. Separately choose the Viewer locale.
For supported languages, always write the matching `meta.locale`: `"en"` for
English, `"zh-CN"` for Simplified Chinese, or `"zh-TW"` for Traditional
Chinese (Taiwan). The renderer consumes the authored
locale without inferring language from diagram strings. Documents that omit it
remain valid and default to English.

`meta.locale` controls only renderer-owned reader surfaces: `<html lang>`, the
document-title suffix, default SVG description and focus labels, default legend
labels, and fixed Viewer controls, statuses, accessibility names, and errors.
It never translates authored content. Apply the primary language separately to
titles, subtitles, node and relationship copy, boundaries, groups,
legend label overrides, and cards. A bilingual diagram still
chooses one primary locale for the Viewer; follow an explicit primary-language
request, then prompt order or conversation dominance.

For a requested language outside `en`, `zh-CN`, and `zh-TW`, do not write an unsupported
locale. Keep every reader-facing authored string in the requested language,
omit `meta.locale` so the renderer safely uses English, and explicitly tell the
user that fixed Viewer UI and `<html lang>` remain English and the artifact is
not fully localized. The fallback applies only to renderer-owned surfaces; it
never permits authored copy to fall back to English. Do not silently substitute
`zh-CN` for another language or Chinese locale.

Keep exact product names, code identifiers, commands, protocols, API paths, and
environment names intact. Those terms may remain English inside localized copy,
but surrounding explanatory prose must still use the selected language.
Renderer-owned default legend labels follow `meta.locale`; author a
`meta.legend.entries.*.label` override only when the diagram needs different
domain wording, and keep that authored override in the primary language.

## Title hierarchy

Use one concise title and let the diagram carry the explanation. Omit
`meta.subtitle` by default, and never use it to restate the title, nodes, edges,
or cards. Include one short supporting line only when the user explicitly asks
for a subtitle; an omitted or blank subtitle must not leave an empty visual row
in the generated viewer.

## Executable geometry rules

- Node anchors start at side midpoints. `left`/`right` change the horizontal endpoint; `top`/`bottom` change the vertical endpoint. For an automatic Architecture relationship, unobstructed facing ports whose axis offset is under 16px may share one horizontal or vertical axis when both endpoints retain the 16px corner gutter. If exactly one endpoint belongs to a spread group, only its unshared counterpart moves; relationships spread at both endpoints keep their distinct ports and outside bridge.
- A side is a direction contract. The first and final route segment must be perpendicular and outward/inward in the named direction.
- Automatic Port Spread is a default renderer behavior. Shared automatic endpoints spread deterministically and symmetrically with a 16px corner gutter. It does not apply to single relationships or explicit `via`, `channelX`, `channelY`, `labelAt`, or non-`auto` routes.
- Showcase route rhythm: every nonzero segment must be at least 8px; every interior segment must be at least 16px. When spread ports are nearly parallel, the router uses a 24px endpoint stub and a 16px outside bridge instead of manufacturing a tiny dogleg.
- Shared endpoint corridors are allowed only when they remain semantically unambiguous. Unrelated collinear overlap of 8px or more fails showcase.
- Container borders are intentional pass-through geometry, but a long edge running along a structural border is not.
- An edge crossing an unrelated opaque node is always a hard failure, independent of quality profile.

### Spacing and labels

Spacing recommendations mean clear gap between boxes, not center distance. A 200px center distance between 165px-wide nodes leaves only 35px of clear gap.

For a relationship label, require:

```text
clear gap > label mask width + 8px breathing room
label mask width ≈ 6.5px × ASCII units + 13px
CJK characters count as two units
```

Relationship labels are semantic data. If the gap is too small, move the label,
adjust the route or spacing, then shorten the wording while preserving meaning.
Omit only wording already fully implied by both endpoints and carrying no
protocol, action, direction, synchronous/asynchronous behavior, or
cross-boundary mechanism. Preserve every meaningful label.
Deleting it is not a spacing repair. If a relationship starts unlabeled because
its endpoints fully imply it, explain why the wording is redundant; this is a
semantic authoring choice, not a spacing repair. Apply one diagnosed geometry
control (`labelAt`, `labelDx`/`labelDy`, or `labelSegment`) at a time.

### Repair order

1. Fix missing/invalid `meta.quality_profile` and schema errors.
2. Fix node overlap or out-of-range placement.
3. Fix edge-through-node and endpoint-direction errors.
4. Fix crossings, ambiguous corridors, border runs, and route rhythm.
5. Fix label-to-node, label-to-label, then label-to-route clearance.

Run `validate` after every edit. Consume `diagnostics[]` by stable `code`, exact `subject`, measured `evidence`, and `supportedFixes`. If the diagnostic gives `labelAt`, use that point instead of estimating another offset.

## Mode placement

### Architecture

Use one left-to-right spine with short vertical branches. Prefer 6–12 primary components and group only real ownership, trust, process, or deployment boundaries. Boundaries do not replace relationships.

Grid placement is preferred when the schema supports it. Free positions are appropriate for a bounded exception, not for prose-level coordinate planning. Keep external actors outside the system boundary when that is factually true.

## Hand-placed fallback

Use only when no renderer can run. Start from `assets/template.html`, keep semantic CSS classes, preserve the inline SVG/accessibility structure, and run the delivery visual checklist. Never introduce inline literal colors.

## Language zones (architecture)

When nodes carry `code.files[].path` in two or more languages (by extension, majority per node), the architecture renderer tints the whole canvas into full-bleed zones, one per language, split along a stepped line derived from node positions (nearest node wins). Nodes without code data take the zone they sit in. Single-language diagrams draw nothing. Diagrams whose nodes use `layer` get a legend of only the layer glyphs plus one swatch per zone; diagrams without `layer` keep the type-based legend.
