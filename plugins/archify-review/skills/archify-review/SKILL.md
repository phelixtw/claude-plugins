---
name: archify-review
description: Review a commit, a Jira ticket's commits, or a commit range one user-facing feature at a time, as an explorable standalone HTML code-review report built on an architecture diagram: a change file tree, net diffs per flow with the test coverage and Given/When/Then test cases of every changed function, a code-review-graph blast-radius risk on every node, reviewed checkboxes, Code Review / Security / Code Smell TODO tabs with copyable AI prompts, and copyable reference links. Use when the user asks to review a commit, a commit range (`<sha1>..<sha2>`), a branch's commits, or a Jira ticket's changes, to continue with the next batch (`next <REPORT>`), or to refresh an existing archify-review report.
license: MIT
metadata:
  version: "2.17"
  author: tt-a1i
  based_on: Cocoon-AI/architecture-diagram-generator (MIT, v1.0)
---

# Archify

Create a self-contained, interactive HTML diagram from a small typed JSON specification. Static output is the default; enable motion only when the user asks for it.

## Fast authoring path

Use this bounded path for ordinary generation. Do not read the optional Viewer Runtime reference unless the user asks about those features.

1. Use `architecture`, the only diagram type.
2. Read `schemas/architecture.schema.json`, `schemas/common.schema.json`, and `examples/web-app.architecture.json`. Read only those files. Fresh authorship means new stable IDs, domain wording, and layout; use the example for field shape, not facts.
3. Artifact first: the next tool action must write the candidate. Write the candidate before inspecting renderer internals. Do not plan exact coordinates in prose. Start with one clear main path, short side branches, sparse labels, and at most 12 primary nodes. Set `meta.quality_profile` to `"showcase"` unless the user explicitly requests a dense `standard` map. Start with automatic routes and labels. Do not add `via`, `channelX`, `channelY`, or `labelAt` before a diagnostic calls for one; apply at most one diagnosed geometry control per repair.
4. Validate after every candidate edit and immediately before handoff:

   ```bash
   node bin/archify.mjs validate <type> <candidate.json> --quality showcase --json
   ```

   A receipt that lists only the basic checks is basic validation, never showcase acceptance. A showcase pass must report every artifact check as passed (the receipt's `checks` list), with 0 composition errors and 0 warnings. If the candidate omits or misspells the exact `meta.quality_profile` field, fix it before geometry. A passing final validation freezes the candidate: never edit it afterward.
5. For a delivered HTML, `deliver` is the final acceptance command:

   ```bash
   node bin/archify.mjs deliver <type> <candidate.json> <output.html> --quality showcase --json
   ```

   A non-zero exit can never be described as success. A failed delivery preserves any previous output, so do not run `visual-check` on that path: it would inspect the stale last-good artifact, not the failed candidate. If validation fails, change only the diagnosed `subject`, verify `evidence`, choose from `supportedFixes`, and rerun. Continue validation correction while the objective error count reaches a new minimum. If two consecutive rounds do not improve that best count, stop and report the unresolved diagnostics truthfully.

## Type router

| Type | Use for |
|---|---|
| `architecture` | Components, services, cloud/security boundaries, infrastructure |

## Authoring invariants

- One obvious main path; side branches leave the nearest main-path node. Remove low-value edges before adding routing controls.
- Omit `meta.subtitle` by default. Never invent a subtitle that restates the title, nodes, or cards; include one short supporting line only when the user explicitly asks for it.
- Treat the standalone desktop viewer as a first-screen artifact by default, not a shallow strip. Generate one responsive artifact for laptops and external displays—never device-specific HTML or alternate topology. The viewer may adapt only the outer reading width from the live viewport height; it must preserve the authored SVG/viewBox, proportions, semantic geometry, and normal document flow. On a wide or tall desktop, use enough authored vertical rhythm that the diagram panel and its necessary conclusion cards occupy the screen as a balanced whole; runtime scaling cannot repair an over-compressed Y layout or an undersized explicit `meta.viewBox`. Before handoff, open the real HTML at 1440×900, 1600×1000, and 1920×1080; additionally check 2048×1320 whenever the composition is intended for a large desktop display. Require `document.documentElement.scrollWidth <= window.innerWidth` and `scrollHeight <= window.innerHeight` at every checked size, while visually checking that the diagram remains comfortably readable and vertically balanced at the largest checked viewport. Repair overflow by removing only genuinely redundant content or compacting spacing before shrinking nodes, labels, or the main panel. If the largest viewport still has a conspicuous empty lower band at the viewer's width cap, redistribute authored Y positions and increase the viewBox height proportionally; do not add filler copy or decorative cards. Never counterfeit a pass with `overflow: hidden`, clipped content, an internal diagram scroller, stretched SVG height, or smaller typography. Narrow/mobile layouts may scroll vertically when containment requires it.
- Omit `meta.legend` for the truthful `auto` default. When needed, use only `mode: auto|all|hidden` and renderer-supported `entries.<kind>.label|visible`; labels never change semantics.
- Choose one primary authored language from an explicit user choice; otherwise follow the request or conversation's dominant language. `meta.locale` controls only renderer-owned Viewer UI: use `"en"`, `"zh-CN"`, or `"zh-TW"` for the corresponding supported primary language. For every other language, omit `meta.locale` and explicitly disclose that the fixed Viewer UI and `<html lang>` fall back to English. The renderer never translates authored content. See `references/authoring-contract.md` for details.
- Preserve exact product names, code identifiers, commands, protocols, API paths, and environment names. They may remain English inside localized copy, but never justify leaving the surrounding explanatory prose in another language.
- Component types are `frontend`, `backend`, `database`, `cloud`, `security`, `messagebus`, and `external`; variants are `default`, `emphasis`, `security`, and `dashed`.
- Relationship labels are semantic data. When one collides, move the label, adjust the route or spacing, then shorten the wording while preserving meaning. Omit only wording that is already fully implied by both endpoints and contains no protocol, action, direction, synchronous/asynchronous behavior, or cross-boundary mechanism. Preserve every meaningful label; deleting it is not a geometry repair. If a relationship starts unlabeled because its endpoints fully imply it, explain why the wording is redundant; this is a semantic authoring choice, not a geometry repair.
- Spacing means clear gap, not center distance. For a relationship label, clear gap must exceed its measured mask width; follow the label-preserving repair order.
- Automatic routes own their endpoint sides. A side is a direction contract: the first and final segment must leave/enter perpendicular to that side.
- Automatic Port Spread is a default renderer behavior. It skips single relationships and explicit `via`, `channelX`, `channelY`, `labelAt`, or non-`auto` routes. Near parallel ports use an outside bridge so automatic routing cannot create a sub-8px segment or sub-16px interior turn. Architecture separately keeps unobstructed facing automatic ports (`left`/`right` or `top`/`bottom`) on one shared axis when their offset is under 16px and both ports retain corner clearance. If exactly one endpoint was spread, only the unshared endpoint may move onto that axis; if both endpoints were spread, keep the outside bridge so competing ports remain distinct.
- Never accept an edge crossing an unrelated opaque node, an ambiguous shared corridor, or a relationship label masking another route.

Read `references/authoring-contract.md` only when you need field enums, spacing math, geometry repair rules, or placement.

## Delivery

Use `validate` during repair and `deliver` once for final acceptance. Delivery freezes the exact specification bytes into a private same-directory snapshot, renders and checks that snapshot, atomically commits the HTML, and reports SHA-256 plus byte counts for both specification and artifact. This is deterministic artifact evidence; it does not exercise the Viewer in a browser.

After delivery, collect bounded desktop evidence without modifying or rerendering the trusted HTML:

```bash
node bin/archify.mjs visual-check <output.html> --json
```

`visual-check` collects automated browser evidence from the exact delivered HTML without modifying or rerendering it. Its machine-readable measurements and screenshots do not approve perceptual polish. Follow `references/delivery-contract.md` for the canonical receipt fields, coverage, sidecars, exit behavior, and supplementary manual-record requirements.

Keep the three claims separate: `deliver` proves deterministic artifact checks, `visual-check` proves bounded behavior in a real browser, and perceptual visual review requires an actual human or image-capable reviewer. Report browser evidence and perceptual review independently. An unconstrained glance can support only perceptual review; use the canonical delivery contract when recording supplementary manual browser work or handling an environmental failure.

Add `--open` only when the user wants an immediate local preview. Read `references/delivery-contract.md` when using visual review or post-commit opening.

## Optional viewer capabilities

Generated HTML already contains relationship tracing, a node passport with authored upstream/downstream reach, Semantic Lens, and Route Probe. These are reader capabilities, not extra authoring work. `meta.animation: "trace"` is opt-in.

Read `references/viewer-runtime.md` only when the user explicitly asks about motion, Route Probe, Semantic Lens, deep links, or another Viewer Runtime feature.

## Code review diagrams

Use `architecture` with `meta.locale` set to the user's language. For a Jira ticket, collect every commit from its comments and `git log --all --grep=<ticket>`; for a Story or Epic, do the same for every subtask key, since commits usually carry the subtask key. For a Task or Subtask, also read its parent Story (goal, acceptance criteria, linked Figma frames, localization strings, analytics events) as context, but keep the report on this task's change: its purpose card and review findings cover only the part of the story this task does. For a commit range `<sha1>..<sha2>`, follow "Reviewing a range by feature" below. Follow `references/code-review-pipeline.md` (multi-agent stages, review, fix rounds). The main agent only orchestrates and assembles; reading diffs, writing flows and every review (code-review, security-review, and the code-smell review for over-engineering by the bundled `references/ponytail-review.md` rules) run in sub-agents, and the TODO card shows them as Code Review, Security Review and Code Smell tabs. For hunks, diffs, `tests` line numbers and caller line numbers use the bundled `bin/review-kit` (`hunks` gives a file changed by several commits one net diff, `diff`, `lines`, `caller`; example: `bin/review-kit lines --hunks hunks.json --file <path> +290..+298,-290`) and never write ad-hoc Python or Node parsing scripts. Keep work files in `~/archify-review/work/` and deliver with `--report <TICKET>` to `~/archify-review/review.html`: one shared viewer, one `reports/<TICKET>.js` per ticket (layout in `references/code-review-pipeline.md`). When the ticket already has a report, continue from it ("Refreshing a review"). Right after `hunks`, run `bin/review-kit risk --repo <repo> --hunks hunks.json --out risk.json` (code-review-graph's blast radius of every changed file). Build the spec by writing a compact `plan.json` (judgment text, layout, `tests`; format and example in `references/code-review-pipeline.md`) and running `bin/review-kit assemble --hunks hunks.json --plan plan.json --risk risk.json --out arch-review.json`; never write your own generator script. Set `meta.platform` to `"app"` (plus `meta.framework: "flutter"` or `meta.webview: true` when they apply) and give every component `layers` in `plan.json` (`{"mvvm": "view"}`, `{"mvvm": "view", "webview": "host"}`, ...; `assemble` writes it to `code.layers`; schemes, tier names and rules in `references/code-review-pipeline.md`; `review-check` R18 errors without them). Then run `node bin/archify.mjs review-check <spec.json> --repo <git repo> [--json]` before `deliver`; it must report 0 errors. Also run the built-in `/code-review` and `/security-review` per `references/code-review-pipeline.md` (Stage 4b) and put their findings in the "Open questions / TODO" card, each item tagged `[low]`/`[medium]`/`[high]` before `[code-review]`/`[security-review]` (R17 enforces it) and sorted most severe first; first switch to the task's repo and branch as that section says.

After a successful `deliver`, always run `node bin/archify.mjs visual-check <out.html> --json` once (add `--report <TICKET>` for a code review report) (required for code review diagrams, even when the request does not mention it) and report its `status`.

Prerequisites (git, the `bin/review-kit` launcher and its platforms, code-review-graph 2.4.x, Chrome for `visual-check`) and the known limits of `review-kit` are listed once, under "review-kit" in `references/code-review-pipeline.md`.

Spec fields:

- Top-level `changes: { repo?, commits?: [{ sha, subject?, new? }], files: [{ path, status: "added"|"modified"|"deleted", additions?, deletions? }], tests?: [path], slice? }` from `git show --numstat`. It pins a Change File Tree left of the diagram and every path tab. `tests` (written only by `assemble`) lists the changed test files, which the report never draws. `slice` (written only by `review-kit hunks --report`) marks a feature report: its id, the range ends, the feature, the batch and what the Next button offers.
- Relationship `code: { from_symbol, to_symbol, path, line, diff, tests, note }`. `diff` is whole `git show -U2` hunks with their `@@ -a,b +c,d @@` headers, never trimmed; `path` is that file; `line` is a new-file line inside the diff.
- Relationship `code.action` (optional, max 80 characters) is a real user operation such as "User presses Home on the game page"; leave it empty for a pure call chain, and take the trigger from `callers.json`. The path tab shows `code.note` and then this text under "What this change does" for the current hop.
- Relationship `code.story` (optional, 1 to 400 characters, `[label](#node:id)` links allowed) is required on every connection without `action`. It states directly what the two files do together in this change, with the real identifiers and values (illustrative only: "`_setOverlayToShowOnLeave` now sends `toolbarExpanded` to the native side, which keeps the overlay when the floating toolbar was never used."). No metaphors or personification. Every fact (values, who calls whom, when) must match the diff and the code. The path tab shows it under "How they work together" when `action` is empty.
- Relationship `code.tests: { functions, cases?, suggested? }` is shown on the hop's diff, in place of per-line explanations. `functions: [{ symbol, why, lines, status, by, via?, hunk? }]` lists every function, method or property the hop's diff changes, in diff order (a hop that changes none, such as an import, export or manifest entry, names the changed declaration); `lines` uses the ref-link form (`+196..+199,-143`) of lines shown in this diff, and `hunk` (1-based index or array of the diff's `@@` hunks) is required only when a number falls in more than one hunk. `status` is `new` (a test added or changed in the reviewed commits calls it), `existing` (only unchanged tests call it), `none` (no test calls it) or `removed` (the commits delete it; its old tests may stay in `by`; not counted in coverage). `by` names the calling tests (`TypeName.testMethod`, or the test function name), empty for `none`; `via` (new or existing only, at most 120 characters) names the symbol the tests reach it through when they do not call it directly, and reads as weaker coverage. `why` (at most 80 characters, in the report language) says which behaviour of this code matters: what would break for a user or caller if it were wrong, with facts only from the code, its comments, the commit message or the tests; for `removed`, what the deleted code did and where it went, if it moved. `cases: [{ test, covers, given, when, then }]` has one entry per test in the `by` of a new or existing function; `suggested: [{ covers, given, when, then }]` has at least one per `none` function. Given, When and Then are one short sentence each (at most 60 characters) in the report language, with facts taken from the test code, told as the situation a person meets (dates, items, what they do and see), not the test's inputs such as internal ids, keys or arrays; more than two code spans in a case is a `review-check` R24 warning. The viewer marks each function's diff lines with its status (a green gutter bar when tested, red and hatched when untested, grey when removed) and puts a chip on its first line (`✓ N tests`, `✗ No test`, `Removed`); the chip opens a popover with the `why`, each test as Given / When / Then with `via`, or the suggested cases. The hop's file name row shows the coverage N / M (removed functions not counted, none for a pure deletion).
- Node `code: { commit?, files?, brief?, risk? }` with at least one of `files` or `brief`. `files: [{ path, summary, diff? }]` feeds the file tree, seen watermark and path tab titles. `brief: { role, task, concepts?: [{ term, text }] }` (max 6 concepts, `[label](#node:id)` links allowed) is what the node popup shows under its title, the file path(s): the commit, role, task and concepts to know. Without `brief` the popup shows the Code change diff.
- Node `code.risk` is written only by `assemble --risk` from `bin/review-kit risk` (code-review-graph), never by hand; `review-check` R23 requires it on every node with `files`. It gives the node a risk badge and splits its popup into a 說明 tab (the brief) and a 爆炸半徑 tab (score breakdown, impacted callers, functions without tests).

Authoring rules:

- Every changed file is its own node labeled with its file name, except test files: `assemble` moves them to `changes.tests` and fails on a component or connection that points at one, and no node, tree entry, hop or diff shows them (`review-check` R21). Their tests show in the `tests` of the hops they cover; findings about test code stay in the TODO card.
- Every flow is a real change in the commits. Unchanged code (engine, framework, root cause) is not a flow; put causes in a `cards` card titled 這張 Story 的目的 ("Story purpose", rose dot), and only items that need a do-or-skip decision in a card titled 待確認問題 / TODO ("Open questions / TODO", amber dot); no other card (test runs, device logs, accounts; `review-check` R22). The viewer turns the TODO card into a checklist with Code Review, Security Review and Code Smell tabs split by the `[security-review]` and `[code-smell]` tags. A framework node may point at a changed override it calls, with that override's diff.
- Give each relationship the diff of its target node's file, so no two connections run between the same pair of nodes.
- Every hunk is covered by at least one connection.
- Find a hop's tests by reading, never by running them: first the tests added or changed in the reviewed commits (`changes.tests`; their hunks stay in `hunks.json`, so `bin/review-kit diff --file <test file>` prints them), then `grep` for the symbol in the test targets (`test/`, `*Tests/`, `androidTest/`, `__tests__/`, ...), then the file's `untested` list in `risk.json` (code-review-graph's functions with no test). A test that reaches the function only through another symbol is `via` that symbol. Reasons cite only the commit message, code comments or verified callers; never infer one.
- Callers and triggers (file:line and the user action or framework timing) are verified with grep before they are written.
- `note` is one summary sentence; the diff, `story` and `tests` carry the details.
- Prose markup in code-review fields: `**bold**` (roles, file names, sentence subject), `` `code` `` (anything that refers to code), plain text otherwise; `**`name`**` combines both. See `references/code-review-pipeline.md`.

Viewer behaviour you get for free:

- Each path tab shows one hop at a time, with ▲/▼, keyboard and badge-click switching, and a reviewed checkbox per hop; each hop's diff carries its test coverage (see `code.tests`) and nothing is listed under it. Review state (hop and file ticks, TODO item ticks) is kept in the browser's localStorage per report, so it survives reloads, new tabs and switching tickets, and comes back on a new report version for unchanged lines and items. A standalone page (no `--report`) keeps it for the open page only.
- Ticks are kept per changed line (file, `+`/`-` side, line number and text), so a flow showing the same lines as another is ticked with it; a flow box is checked, indeterminate or unchecked by its lines, and files turn seen, partial or unseen from the flows whose hop shows their code; uncited files get their own checkbox. A node whose files are all seen (or, without files, whose connected flows are all ticked) gets a faint ✓ watermark, and a fully ticked path tab and its hop badges get a ✓.
- On a review page the toolbar carries a "Refresh" button (zh-TW 刷新), which copies the refresh command (see "Refreshing a review") because a `file://` page cannot run git. Nodes of a new commit (`changes.commits[].new`) carry a "new commit" badge on the top-right corner, left of the risk badge (高/中/低 and the score), and TODO items marked `[new]` carry a NEW tag next to the severity tag. A feature report's toolbar adds 下一批 (files left in this feature) or 下一個 Feature, which copies `/archify-review next <ID>`.
- The review toolbar has no Path, Map or Lens buttons (and no R / M / L shortcuts); it carries a layout switch instead. The authored node positions are only the starting geometry; the page opens in "MVVM" and has no Freeform button. The buttons rearrange the same nodes into the bands of a recognised layering scheme, top to bottom, with every edge re-routed and validated at `deliver` time: for `meta.platform: "app"` (Flutter, Android, iOS) "MVVM" (three columns: View | ViewModel | Model) and, when `meta.framework` is `"flutter"` (pubspec.yaml with a flutter dependency and Dart/Flutter changes; native Android or iOS code leaves it out), "Flutter" (bands UI / Logic / Data from the Flutter architecture docs); and, when `meta.webview` is `true` (the project itself uses a WebView, judged from the repo and not from the diff; any one of: `pubspec.yaml` depends on `webview_flutter` or `flutter_inappwebview`, Android code uses `android.webkit.WebView` or depends on `androidx.webkit`, iOS code uses `WKWebView`), "WebView" (bands Host / Bridge / Content from the Apple, Android and Flutter WebView docs; only files changed in the commits are drawn). Set `meta.platform: "app"` and give every component `layers` for each scheme that applies (`mvvm`, `flutter`, `webview`; names and sources in `references/code-review-pipeline.md`, R18). Edge labels, badges, popups, path tabs, highlighting and ticks work in every mode. The mode is not stored: the page always opens in MVVM. A layout that cannot be routed cleanly fails `deliver` with a diagnostic; fix it by moving the named nodes to another layer of that scheme.
- Every tab and code block has a "Copy link" button, and a code selection shows one; links deep-link back to that hop and highlight the lines (see below).
- The path graph normalizes node boxes to their text, so a tall fan-out node in the main diagram stays compact there.

## Refreshing a review

`/archify-review refresh <TICKET> [--repo <repo>] [--done <n,...>] [--skip <n,...>]` (the command the Refresh button copies; `--done` lists the TODO items the user ticked; `--skip` comes only from pages built before 2.8.0). Run it like a fresh code-review run (same commit collection, `bin/review-kit hunks`/`assemble`, `review-check`, `/code-review`, `/security-review`, `deliver --report <TICKET>`), with these differences, all in `references/code-review-pipeline.md` ("Refreshing a review"): read the previous spec from `~/archify-review/reports/<TICKET>.js`, drop Skip items, re-check Done items, keep still-valid items word for word, mark commits missing from the previous spec as new (`new_commits` in `plan.json`), and mark TODO items absent from the previous report `[new]`. The "這張 Story 的目的" card is regenerated from scratch, never carried over or diffed, and gets no NEW flags. A feature report refreshes from its plan: `bin/review-kit hunks --report <ID>` replaces the commit collection.

## Reviewing a range by feature

`/archify-review <sha1>..<sha2> [--repo <repo>]` reviews a range one end-to-end, user-facing feature at a time, and shows only code that survives to `<sha2>`. A sub-agent reads every commit of the range and writes `~/archify-review/work/features.json`: features in review order, grouped by the behaviour a user sees (not by shared files), each with its name, its user-facing change and its commits, and non-functional changes (build numbers, lockfiles, CI, docs) as the last feature. Run `bin/review-kit slice plan --repo <repo> --range <sha1>..<sha2> --features features.json`, show the user its table (order, feature, change, commits, files), and edit and rerun until the user confirms it; do nothing expensive before that.
Then `bin/review-kit slice next --repo <repo> --range <sha1>..<sha2>` allocates the first report, `<KEY>-<n>` such as `AC1-1`; when a feature has more than 12 files it asks for `--files` (at most 12, a test with its tested file, one flow's files together). Run the code-review pipeline with `bin/review-kit hunks --repo <repo> --report <ID> --out hunks.json` in place of the commit collection, and `deliver --report <ID>`.
`/archify-review next <ID>` (copied by the page) runs `bin/review-kit slice next --report <ID>`, which is idempotent, and the same pipeline for the report it names. Details: "Feature slices (range input)" in `references/code-review-pipeline.md`.

## Reading a pasted reference link

The viewer copies `file:///.../review.html?report=<TICKET>#ref=hop|node|tab&tab=<path-tab>&edge=<connection id>&node=<component id>&file=<repo path>&lines=-a..-b,+c..+d&commit=<sha>`.
`-` lines are old-file numbers (deleted lines), `+` lines are new-file numbers (added and context lines).
Get them with `git show <commit>:<file>` for `+` and `git show <commit>^:<file>` for `-`, then sed the range. In a feature report (`changes.slice`), read `+` lines at `changes.slice.head` and `-` lines at `changes.slice.base`.
Match `edge` to a spec `connections[].id` and `node` to a `components[].id` for what the code does.
`ref=tab` names only a tab; its key is `path-` plus the node ids joined by `-`.

## Setup and fallback

Install code-review-graph 2.4.x once with `uv tool install code-review-graph` (CLI only; never run its `install` subcommand, which rewrites the target repo's `.mcp.json`, `.claude/` and `CLAUDE.md`). Verify with:

```bash
node bin/archify.mjs doctor
node bin/archify.mjs demo <output-directory>
```

When shell access is unavailable, hand-place architecture SVG into `assets/template.html`, use CSS semantic classes rather than inline colors, and follow the visual review contract in `references/delivery-contract.md`.

## Output

Return the checked HTML path, diagram type, validation summary, specification/artifact receipt, browser-evidence status, and truthful visual-review status. Do not claim success for a non-zero command or claim visual inspection you did not perform.

## Gotchas

- Non-zero exit is never success. After a failed `deliver`, do not run `visual-check`: it inspects the stale last-good artifact.
