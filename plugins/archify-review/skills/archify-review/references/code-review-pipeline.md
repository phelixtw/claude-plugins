# Code review diagram pipeline (multi-agent)

Read this when building a code review diagram. It is written for the main agent. All Chinese text in specs, flows and reports is Traditional Chinese (繁體).

## Rules for every dispatch

- The main agent only orchestrates and assembles: it runs Stage 0, `review-kit`, `assemble`, `review-check`, `deliver` and `visual-check`, merges the sub-agents' output files, and writes the final reply. Reading diffs, writing flows, the semantic review and every review in Stage 4b run in sub-agents, so the main agent's context holds results, not file dumps. Send independent sub-agents in one message so they run in parallel.
- Name the exact files the sub-agent may write. Everything else is read-only; it must never delete, clean up or `rm -rf` anything.
- Each sub-agent writes only its own output file.
- Folder layout, shared by every ticket: `~/archify-review/review.html` (the viewer, the same file for every ticket), `~/archify-review/reports/<TICKET>.js` (one report per ticket, kept), `~/archify-review/work/` (`hunks.json`, `risk.json`, `plan.json`, `arch-review.json`, part files; overwritten every run), `~/archify-review/slices/<base10>..<head10>.json` (the feature plan of a range, written only by `review-kit slice`), `~/archify-review/crg/<repo>-<hash>/` (code-review-graph's graph of a repo, rebuilt by every `review-kit risk`). `ARCHIFY_REVIEW_HOME` moves the whole folder. The skill directory and the reviewed repo stay untouched.

## Prose markup

Every prose field (cards items, `note`, `story`, `action`, tests `given`/`when`/`then`, file `summary`, brief `role`/`task`/`concepts`) uses three styles: `**bold**` for important roles, file names and the sentence subject; `` `code` `` for anything that refers to code (identifiers, methods, event names, GA keys, values); plain text for the rest. A subject that is also code is `**`name`**`. Code spans are not re-parsed; no `**` or backticks may show raw in the viewer.

## Stage 0 (main agent)

1. Read the ticket. For a Story or Epic, expand it to every subtask and child issue first; commits are usually tagged with the subtask key, not the Story key. Collect every commit from the comments of the ticket and of each subtask, plus `git log --all --grep=<key>` for every key, then de-duplicate. When the result would need more than 12 nodes, draw one diagram per subtask, each its own report (`--report <SUBTASK>`), and list their links. A commit range input (`<sha1>..<sha2>`) skips steps 1 and 2: follow "Feature slices (range input)" below.
2. For a Task or Subtask, also read its parent (Story or Epic), and a sub-agent writes `work/context.json`: the parent's goal and acceptance criteria, its linked design frames (Figma), its localization strings or keys, and its analytics events (GA), plus the task's own description. Keep only what concerns this task's change: the parent explains why the task exists, but the report is about the task. Use it to
   - write 這張 Story 的目的 as what this task does for the story and why, not a summary of the whole story or of work other tasks cover;
   - let the reviews (Stage 4b) check the diff against the parts of the story this task covers, for example design values, missing or untranslated strings, or an analytics event the task should send, and raise gaps as TODO items with their source (design frame, string key, event name).
3. `mkdir -p ~/archify-review/work`, delete the files already in `work/` (never `reports/`), and run `bin/review-kit hunks --repo <repo> <sha>... --out hunks.json` (a feature report: `--report <ID>` in place of the shas), then `bin/review-kit risk --repo <repo> --hunks hunks.json --out risk.json`. Report every `note:` line `risk` prints (the working tree is not the reviewed head, or it has uncommitted changes) in the final reply.

## Refreshing a review

Invoked as `/archify-review refresh <TICKET> [--repo <repo>] [--done <n,...>] [--skip <n,...>]` (copied by the Refresh button of the review page). Any run for a ticket that already has `~/archify-review/reports/<TICKET>.js` follows these steps too, without `--done`/`--skip`.

1. Read the previous report: `reports/<TICKET>.js` is `archifyReport(<json>);`; its `spec` is the previous `arch-review.json` (commits in `spec.changes.commits`, TODO items in the TODO card of `spec.cards`). `--done` and `--skip` are 1-based numbers of those TODO items, as the user marked them in the page.
2. Re-collect the ticket's commits exactly like a fresh run (Stage 0), then rebuild `hunks.json`, `plan.json` and the spec, and re-run `review-check`, `/code-review` and `/security-review`. Reuse the previous spec's text where the code did not change; rewrite only what changed.
3. TODO items: drop every `--skip` item (only pages built before 2.8.0 send it; the user decided not to act on it). Re-check every `--done` item (a ticked item): drop it when the new code fixes it, keep it otherwise. Copy every previous item that is still valid with its exact text (severity and tags included): the page matches its Done / Skip state by that text, so a reworded item loses it.
4. In `plan.json` set `"new_commits": [<shas not in the previous spec's commits>]` (prefix match) and `"repo": "<repo>"`; `assemble` writes them as `changes.commits[].new: true` and `changes.repo`. Set `code.commit` on each component whose files come from a new commit (with a single commit every node with files is covered); those nodes get the "new commit" badge.
5. A TODO item whose text (ignoring severity and `[code-review]`/`[security-review]` tags) is not in the previous TODO card is written `[low][new][code-review] ...` (`[new]` right after the severity; R17 accepts it); the viewer shows a NEW tag by the severity tag. Items present before carry no `[new]`.
6. The "這張 Story 的目的" card is regenerated from scratch (re-read the ticket and commits); it is not carried over or diffed and gets no NEW flags.
7. `deliver --report <TICKET>` and `visual-check --report <TICKET>` as usual. The user reloads the page; ticks on unchanged lines and on unchanged TODO items come back from the browser.

## Feature slices (range input)

Invoked as `/archify-review <sha1>..<sha2> [--repo <repo>]`. The range is cut vertically: each report is one end-to-end, user-facing feature (or one batch of it), and shows only the hunks of the final diff `git diff <sha1> <sha2>` that the feature's commits wrote.

1. Group the commits. One sub-agent reads `git log --reverse --format='%H %s' --name-status <sha1>..<sha2>` and the diffs it needs, and writes only `work/features.json`: `{"features": [{"name", "change", "commits": [sha, ...]}]}`. A feature is one behaviour a user sees, like a Scrum story with its tasks; two commits belong together when they change the same user-facing behaviour, not when they touch the same files. `change` is one sentence on what the user sees change. Every non-merge commit of the range goes into exactly one feature (merge commits may be left out). Non-functional changes (build numbers, lockfiles, CI, docs, tooling) form the last feature.
2. Check and show the plan. Run `bin/review-kit slice plan --repo <repo> --range <sha1>..<sha2> --features work/features.json`. It fails on a commit in no feature or in two, and otherwise prints one row per feature (commits, files, hunks, name, change) and notes on hunks that hold lines of several features or of none. Show the user that table and ask them to confirm or edit the grouping; edit `features.json` and rerun until they confirm. Start nothing expensive before the user confirms. Rerunning with the same features changes nothing. After a report is allocated, a changed grouping needs `--replace`, which drops the batches; a plan with a delivered report then takes a new key.
3. How hunks are owned. `slice plan` blames every hunk of the final diff once (`+` lines at `<sha2>`, `-` lines through `git blame --reverse` and the next commit on the ancestry path that touched the file) and keeps, per hunk, how many lines each commit wrote. A hunk belongs to the feature whose commits wrote most of its lines (the earlier feature on a tie), so one file can be split by hunk across features. A hunk that no feature's commit wrote (a merge resolution, or lines older than the range) belongs to the last feature. Code added and later rewritten or removed inside the range is not in the final diff, so no report shows it.
4. Allocate a report. `bin/review-kit slice next --repo <repo> --range <sha1>..<sha2>` returns the first report; its id is `<KEY>-<n>`, where `<KEY>` is the repo's initials plus a plan number (`acme-cart` gives `AC1`, the next range `AC2`), which never collides with a Jira key. A report holds at most 12 files. When the feature has more, `slice next` exits 1 and lists them; pick at most 12 with `--files`: a test file with the file it tests (it is no node, but its tests fill that hop's `tests`), the files of one flow together, fewer when the diagram also needs framework nodes. Run it again with `--files` to re-cut the newest batch while it is undelivered.
5. Run the pipeline for the report (Stages 0 step 3 to 6) with `bin/review-kit hunks --repo <repo> --report <ID> --out hunks.json` in place of the commit collection. `meta.title` is `<ID> <feature>（第 <n> 批）`. The 這張 Story 的目的 card starts from the feature's `change` and says what this batch covers. `review-check` re-derives the same batch from git, so R4, R7 and R21 cover only this batch's hunks and files. `deliver --report <ID>`; deliver refuses another id for this spec.

## Next batch

Invoked as `/archify-review next <ID>` (copied by the page's 下一批 or 下一個 Feature button). Run `bin/review-kit slice next --report <ID>`; it finds the plan by the id and returns the report after `<ID>`:

- the next batch of the same feature, or the first batch of the next feature with code, allocated only once, so pasting the command twice gives the same id;
- `<next> is already delivered`: open `review.html?report=<next>`, or refresh it;
- `nothing left after <ID>`: the range is fully reviewed; tell the user.

Then run step 5 above for the report it names. `/archify-review refresh <ID>` of a feature report works like any refresh, with `hunks --report <ID>` in place of the commit collection.

## review-kit (Go CLI, mandatory for git data)

Use `bin/review-kit` for hunks, diffs, `tests` line numbers and commit-version caller lines. Never write your own Python or Node scripts to parse `git show`, number lines, compute `lines` strings or copy hunk text. 

### Prerequisites

- `git` must be on PATH, and the target repo must be a normal git checkout.
- `bin/review-kit` is a launcher script. It runs the prebuilt binary for this machine
  (darwin-arm64, darwin-amd64, linux-amd64, linux-arm64). On any other platform it builds
  `tools/review-kit` with Go on the fly, so install Go (https://go.dev/dl/) there.
  Windows has no prebuilt binary; use WSL or Git Bash with Go installed.
- `review-kit hunks` rejects merge commits (exit 1). Pass the non-merge commits the merge
  contains instead.
- `review-kit hunks` ignores the user's git diff settings (`core.quotepath`, `diff.noprefix`,
  `diff.mnemonicPrefix`); non-ASCII file names are reported as-is.
- Known limitation: `review-kit caller --symbol` never matches a symbol that starts with a
  non-word character (for example `.foo`); pass the bare identifier (`foo`).
- `visual-check` needs Chrome or Chromium; set `ARCHIFY_CHROME` to its executable path when it is not found automatically.
- code-review-graph 2.4.x (tirth8205/code-review-graph, MIT) must be on PATH: `uv tool install code-review-graph`, checked with `code-review-graph --version` and by `node bin/archify.mjs doctor`. Install only the CLI. Never run its `install` or `init` subcommand: they rewrite the target repo's `.mcp.json`, `.claude/`, `CLAUDE.md` and git hooks. `review-kit risk` runs only `build`, `detect-changes` and `impact`, analyses the current working tree of the repo (no checkout), and keeps the graph in `CRG_DATA_DIR`, by default `~/archify-review/crg/<repo>-<hash>/`, never in the repo.

### Commands

- `bin/review-kit hunks --repo <repo> --report <ID> --out hunks.json`: the hunks and files a feature report owes (Feature slices), with `changes.slice` for the page's Next button. Line numbers and hunk text are those of `git diff <base> <head>`; `Hunk.commit` is the head.
- `bin/review-kit slice plan --repo <repo> --range <base>..<head> --features features.json [--replace]` and `bin/review-kit slice next (--repo <repo> --range <base>..<head> | --report <ID>) [--files <path>...]`: see Feature slices.
- `bin/review-kit risk --repo <repo> --hunks hunks.json --out risk.json`: code-review-graph's blast radius per changed file: the score (0 to 1) of its riskiest changed function, that function's risk terms (flows, callers from other modules, tests, security-looking name, callers, churn), direct callers with `file:line`, and changed functions without a test. The base is the range base of a feature report, else the parent of the oldest commit. It fails when code-review-graph is missing, is not 2.4.x, or caps its function list. `risk --version` prints the version it found.
- `bin/review-kit hunks --repo <repo> <sha>... --out hunks.json`: all hunks (numbered, with per-line sign/oldNo/newNo) plus `changes` (commits, files with status and numstat) to paste into the spec. A file changed by several of the commits gets one net diff (the first commit's parent to the last commit), so the review shows its final change only; a commit of another ticket in between that also changed the file does not split it: its lines are part of the final change, and a `note: <file> net diff includes commits outside the selection: <sha>, ...` line names it; say so in the Story purpose card. Per-commit hunks stay (with a `note:` line) only when the commits are not on one line of history. Never paste per-commit diffs of a net-diffed file: `review-check` compares every diff with these same net hunks (R2/R4/R7), and `visual-check` fails a path-tab diff whose hunks overlap. Both are blocking; fix the spec and rerun before delivering.
- `bin/review-kit diff --hunks hunks.json --file <path> [--hunk 1 3]`: verbatim hunk text for `code.diff` or a node `diff`. `--hunk N` is the Nth hunk of that file in commit order; a tests function's `hunk` is the position inside the printed diff.
- `bin/review-kit lines --hunks hunks.json --file <path> [--hunk N] +290..+298,-290`: prints the canonical `lines` string; exit 1 if a number is missing from the hunk, has the wrong sign, or sits in several hunks (then pass `--hunk`).
- `bin/review-kit caller --repo <repo> --sha <sha> --file <path> --symbol <name>`: `path:line` of every whole-word match in the commit version, not HEAD.
- `bin/review-kit assemble --hunks hunks.json --plan plan.json --risk risk.json --out arch-review.json`: builds the whole spec from a compact `plan.json`. Write `plan.json` (judgment content only) and run this; never write a generator script. It moves test files from `changes.files` to `changes.tests`, so they are never nodes, tree entries or hops. `--risk` writes `code.risk` on every component with `files` (or labeled like one changed file): the riskiest file's score, symbol and terms, plus the callers and untested functions of all its files.

### plan.json

Top level: any spec keys (`meta`, `cards`, ...), passed through untouched; `schema_version` and `diagram_type` default to 1 and `"architecture"`. `changes` comes from `hunks.json`.

`meta.title` starts with the Jira key (`PROJ-123 ...`). The report id is that key; a feature report's id is its `<KEY>-<n>` and its title `<ID> <feature>（第 <n> 批）`. Review ticks and TODO item ticks are kept in the browser's localStorage per report (`archify-review-state:<TICKET>`), never in the report file.

- Component: `id`, `label`, `sublabel`, `type`, `layers`, `pos`, `size` (and any other key, passed through; `type` follows where the code runs, not its role: every file in an app is `frontend`, including helpers, native plugins and state classes; `backend` is server-side code only), `files`: a path string, or `{path, summary, hunks}`. `hunks` is `"all"` or a list of per-file hunk numbers (the `--hunk N` numbering); it fills that file's `diff`, and without `hunks` the file gets no diff. A `code` object (for example `brief`) is kept.
- `layers` is required on every component (`review-check` R18): `{mvvm: ...}` when `meta.platform` is `"app"` (`{mvvm: ..., flutter: ...}` when `meta.framework` is also `"flutter"`; add `webview: ...` when `meta.webview` is `true`). `assemble` moves it to `code.layers`, so `code: {layers: ...}` works too. `meta.platform` is always `"app"` (Flutter, Android, iOS); a `"web"` value or a `layers.web` key fails R18. Set `meta.framework: "flutter"` only when the repo has a `pubspec.yaml` with a `flutter` dependency (`sdk: flutter`) and the changed files are Dart/Flutter code; native Android (Kotlin/Java) or iOS (Swift/Objective-C) code, even inside a Flutter repo, leaves it out and gets MVVM only. The viewer opens in MVVM and offers one layout button per scheme (MVVM; Flutter with `meta.framework`; WebView with `meta.webview`). MVVM is drawn as three columns, left to right; the other schemes as bands, top to bottom. Every tier is a dashed frame with its title at the top-left, and an unused tier keeps its (narrow or flat) frame. Assign each layer from the node's role in the change, read in the code (not from its file path); a node can sit in different bands in different schemes. Tiers:
  - MVVM (`mvvm`): `view` (widgets, screens, dialogs, layouts), `viewmodel` (logic that turns app data into UI state and handles UI actions: ViewModel, ChangeNotifier, controller, presenter), `model` (Repository and Service: data sources, state of record, persistence, API clients, platform channels).
  - Flutter (`flutter`, only with `meta.framework: "flutter"`): `ui` (widgets, screens, dialogs and the helpers that build them, plus their view models: Cubit, Bloc, ViewModel, ChangeNotifier, controller; the Flutter guide puts a view model in the UI layer: "Views and view models make up the UI layer of an application"), `logic` (only the optional domain layer between UI and data, such as use cases or interactors that combine repositories; most changes have none, and Cubit, Bloc and ViewModel are not logic), `data` (repositories, services, data sources, platform plugins).
  - WebView (`webview`, only with `meta.webview: true`): `host` (native app code that creates, places and configures the WebView: widget, controller, WebViewClient or navigation delegate, keep-alive, size and lifecycle, and the state that decides when it is shown), `bridge` (code that lets app and page call each other: JS handler, channel or interface registration and handling, scripts injected into the page), `content` (the HTML, CSS and JS that run inside the WebView and the page-side calls into the bridge). Only files changed in the commits are nodes; web code the commits do not touch is not drawn, so a tier without changes stays an empty frame.
  - `meta.webview: true` marks a WebView change. Judge the diff, not the project: an app that depends on a WebView library but whose reviewed commits do not change a JS bridge or web content is an app change (MVVM, plus Flutter for Flutter code), and `review-check` R18 fails a spec whose components are all `host`. The project has a WebView when any one of these holds: `pubspec.yaml` depends on `webview_flutter` or `flutter_inappwebview`; Android code uses `android.webkit.WebView` or depends on `androidx.webkit`; iOS code uses `WKWebView`. Then every component needs `layers.webview` (`host`, `bridge` or `content`) next to its `mvvm` (and `flutter`) layer.
  - Framework, engine and system nodes go to the band they act at; when unsure, use the band of the node they talk to most. A node that talks to several bands sits best in the middle one. A node that holds no state and only draws is `view`/`ui`; a node that holds state and reacts to the UI is `viewmodel` in MVVM and still `ui` in Flutter (a view model belongs to the UI layer there; `logic` is only for a domain layer).
  - `deliver` routes and validates one geometry per scheme; when it fails with "validation failed" and a `code.layers.<scheme>` hint, change that scheme's layer of the nodes named in the diagnostic (or drop a connection) and run `deliver` again. Do not skip it: a failing layout means edges would overlap.

  Sources (layer names are taken from these; do not invent others):
  - MVVM, Flutter app architecture guide, https://docs.flutter.dev/app-architecture/guide: "The UI layer is made up of two architectural components, based on the MVVM design pattern: Views and View models" and "Views and view models make up the UI layer of an application. Repositories and services represent the data of an application, or the model layer of MVVM." Bands: View / ViewModel / Model (Repository, Service). The optional domain layer is not a band.
  - Flutter, https://docs.flutter.dev/app-architecture/concepts ("Layered architecture"): "Typically, applications are separated into 2 to 3 layers, depending on complexity." UI layer: "Displays data to the user that is exposed by the business logic layer, and handles user interaction. This is also commonly referred to as the "presentation layer"." Logic layer: "Implements core business logic, and facilitates interaction between the data layer and UI layer. Commonly known as the "domain layer". The logic layer is optional, and only needs to be implemented if your application has complex business logic that happens on the client." Data layer: "Manages interactions with data sources, such as databases or platform plugins. Exposes data and methods to the business logic layer." And: "each layer can only communicate with the layers directly below or above it." Bands: UI (Presentation) / Logic (Domain, optional) / Data.
  - WebView (no official "three layers" page; each name comes from the sentences below). Apple, https://developer.apple.com/documentation/webkit/wkusercontentcontroller: "A WKUserContentController object provides a bridge between your app and the JavaScript code running in the web view." Host: Android, https://developer.android.com/develop/ui/views/layout/webapps/webview: "The WebView class is an extension of Android's View class that lets you display web pages as a part of your activity layout."; Apple, https://developer.apple.com/documentation/webkit/wkwebview: "A WKWebView object is a platform-native view that you use to incorporate web content seamlessly into your app's UI."; flutter_inappwebview, https://inappwebview.dev/docs/webview/in-app-webview: "InAppWebView is a Flutter Widget for adding an inline native WebView integrated into the flutter widget tree." Bridge: Android (same page): "To bind a new interface between your JavaScript and Android code, call addJavascriptInterface, passing it a class instance to bind to your JavaScript and an interface name that your JavaScript can call to access the class."; Apple, https://developer.apple.com/documentation/webkit/wkscriptmessagehandler: "An interface for receiving messages from JavaScript code running in a webpage."; flutter_inappwebview, https://inappwebview.dev/docs/webview/javascript/communication: "To add a JavaScript handler, you can use InAppWebViewController.addJavaScriptHandler method, where you define the handlerName and a callback to be invoked when it is called by the JavaScript side."; webview_flutter, https://pub.dev/documentation/webview_flutter/latest/webview_flutter/WebViewController/addJavaScriptChannel.html: "Adds a new JavaScript channel to the set of enabled channels." Content: Android (same page): "There's no need to initialize the Android interface from JavaScript. The WebView automatically makes it available to your web page."; Apple (WKScriptMessageHandler page): "To call your message handler from JavaScript, call the function window.webkit.messageHandlers.<messageHandlerName>.postMessage(<messageBody>) in your code."; flutter_inappwebview (communication page): "window.flutter_inappwebview.callHandler returns a JavaScript Promise that can be used to get the json result returned by the callback."
- Connection: `id`, `from`, `to`, `label`, `kind`, `story` or `action`, `note`, `from_symbol`, `to_symbol`, `file` plus optional `hunks` (default: every hunk of the file), `tests`, optional `line`; every other key passes through. `file` is never a test file. `story`, `action`, `note`, `from_symbol`, `to_symbol`, `line` move into `code`. `kind: "counterpart"` has no `file` and needs a `story`.
- Tests: `{functions: [{symbol, why, lines: "+11..+15,-10", status, by, via?, hunk?}], cases: [{test, covers, given, when, then}], suggested: [{covers, given, when, then}]}`, the same shape as the spec's `code.tests` (fields in SKILL.md). `hunk` is the position inside the connection diff. Assemble checks every `lines` against the diff and writes it in canonical form.
- Filled by assemble: `code.path`, `code.diff` (exact hunk text), canonical `tests.functions[].lines`, `code.line` (default: first `+` line of the first function), file `diff`, `changes` (with `changes.tests`). Errors (named by connection and function): a number not in the diff with that sign, a number in several hunks without `hunk`, a hunk used by no connection or file diff (test files' hunks excepted), a component or connection on a test file. Output is one summary line, or only the error list with exit 1.

```json
{
  "meta": {"title": "PROJ-1 toolbar restyle", "locale": "en", "quality_profile": "showcase", "platform": "app"},
  "cards": [{"dot": "rose", "title": "Story purpose", "items": ["The bar did not match Figma"]},
            {"dot": "amber", "title": "Open questions / TODO", "items": ["Dart unit tests requested on the ticket are missing", "[low][security-review] no findings in the reviewed hunks"]}],
  "components": [
    {"id": "kt", "type": "frontend", "label": "Overlay.kt", "sublabel": "drag handle", "layers": {"mvvm": "viewmodel"}, "pos": [40, 164], "size": [250, 70],
     "files": [{"path": "app/Overlay.kt", "summary": "binds the bar"}], "code": {"brief": {"role": "controller", "task": "binds buttons"}}},
    {"id": "xml", "type": "frontend", "label": "overlay.xml", "layers": {"mvvm": "view"}, "pos": [340, 164], "size": [250, 70],
     "files": [{"path": "res/overlay.xml", "summary": "layout", "hunks": "all"}]}
  ],
  "connections": [
    {"id": "kt-xml", "from": "kt", "to": "xml", "label": "drag", "file": "app/Overlay.kt", "hunks": [1, 3],
     "from_symbol": "Overlay.show", "to_symbol": "floating_toolbar", "action": "User drags the bar", "note": "The whole bar is the handle.",
     "tests": {"functions": [{"symbol": "Overlay.show", "why": "The bar must appear when the overlay opens, or the user has nothing to drag.", "lines": "+174,-174", "status": "existing", "by": ["OverlayTest.showsBar"]},
                           {"symbol": "Overlay.title", "why": "Set the bar's title text; the title is gone from the bar.", "lines": "-185", "hunk": 2, "status": "removed", "by": []}],
               "cases": [{"test": "OverlayTest.showsBar", "covers": ["Overlay.show"], "given": "The overlay is hidden", "when": "show() runs", "then": "The bar is visible"}]}}
  ]
}
```

Output is one summary line or only failures. Test statuses and cases, stories, node layout and screenshots still need judgment; `review-check` stays the checker.

## Stage 1 (parallel, fact gathering)

- Stage 1 agents must be a sub-agent type that can write files (for example general-purpose); a read-only search type such as Explore cannot write `callers.json`.
- `hunks.json` comes from `bin/review-kit hunks`; Agent A only adds a one-sentence description per hunk (separate file) when needed.
- Agent B writes only `callers.json`: for every changed method or class, callers as `file:line` from `bin/review-kit caller` (commit version; a feature report uses `--sha <changes.slice.head>`), and the user action or framework timing that triggers it.

## Stage 2 (main agent)

Layout budget (enforced by `review-check` R15/R16/R19): the viewer fits the diagram to a ~1100x570 panel at 1440x900, so file names must render at 13px or more there. Size every node to its label (about 9px per character + 16, labels render at 15px), use at most 4 columns, rows 114px apart, and keep the whole viewBox within about 1150x610. Wide gaps and empty bands shrink the type; pack nodes tight and let edge labels sit in the narrow gaps.

Write `plan.json` (`meta.platform`, `meta.framework` for Flutter code, `meta.webview` for a WebView project, and every component's `layers`, see the layer rules under plan.json): `components`, `connections` (`id`, `from`, `to`, `label`, `file`, `hunks`). Every hunk is assigned to at least one connection. Run `bin/review-kit assemble --hunks hunks.json --plan plan.json --risk risk.json --out arch-review.json` and fix layout before moving on; `changes` comes from `hunks.json`.

## Stage 3 (parallel, flow writing)

Group connections; one agent per group. Each writes the judgment content of its connections into `plan.json` (or a part file the main agent pastes in): `tests` (every changed function with its `lines`, checked by `assemble`, its status and covering tests, its `why`, one Given / When / Then case per covering test and a suggested case per untested function; R24), one-sentence `note`. `why` is the reason this code is worth a test: the behaviour a user or caller relies on, said as what breaks if it is wrong ("an old save without `edited` must decode to an empty map, or the whole list fails to load"), not a restatement of the code. Take it only from the code, its comments, the commit message or the tests; for a removed function, say what it did and where that moved. To find the tests, read the tests added or changed in the commits (`changes.tests`; `bin/review-kit diff --hunks hunks.json --file <test file>`), `grep` for the symbol in the test targets, and check the file's `untested` list in `risk.json`; never run the tests. Given / When / Then come from the test code, not from the test name, and read as the situation a person meets: the dates, items and actions a user would recognise, and what they would see ("the first-week list marks Monday, Tuesday and Friday" / "grouped on Monday" / "today, the next workday, after 10/30"), not the test's inputs (internal ids such as `m1#0`, keys, arrays, enum values, empty fixtures). Name a function only as the trigger when the scenario needs it; at most two code spans per case (an R24 warning; code no user meets, such as a plugin API, may need more). `code.action` is set only when `callers.json` shows a real user operation (a tap, a key press, leaving the app) that triggers the connection; a pure call chain leaves it empty. Every connection without `action` gets `code.story`: a direct statement of what the two files do together in this change, with the real identifiers and values (illustrative only: "`styles.xml` gives the three buttons the same 32×36 size"). No metaphors or personification. Every fact (values, who calls whom, when) must match the diff and the code. Each agent writes only its own part file. When a `note` or `story` states a reason, it may only quote the commit message, a code comment or `callers.json`; never infer one. A net diff hides intermediate commits: mention what changed on the way (for example a method added and then renamed, or code added and then removed) in the `note` or `story` only when it matters to the reviewer; when the end result is the same, leave it out.

Then run `bin/review-kit assemble` again to build `arch-review.json`.

One more agent writes `briefs.json` with `code.brief` (`role`, `task`, up to 6 `concepts`) for every node, from `hunks.json` and `callers.json`. `concepts` are the platform or framework concepts needed to read the file (for example MeasureSpec, clipToOutline, vector drawable viewport), each explained in one or two sentences of general knowledge. `role` and `task` may only come from the code, comments and commit message; never infer.

## Stage 4 (review)

1. Mechanical (main agent): `node bin/archify.mjs review-check arch-review.json --repo <repo> --json`.
2. Semantic checklist (one sub-agent that reads `arch-review.json`, `hunks.json` and `callers.json` and writes only `findings.json`):
   - Do the test statuses, `by` tests and Given / When / Then match the test code, and does each `why` state a behaviour the code, comments, commit message or tests show?
   - Do caller claims agree with `callers.json`?
   - Does one fact contradict itself across connections or cards?
   - Is anything written as done that the commit did not do?
   - Does the plain language read as "before, after, why" in one or two short sentences, or as short bullets?
3. Write all findings to `findings.json`.

## Stage 4b (three review sub-agents, main agent merges)

Run Claude Code's built-in reviews and the code-smell review on the same change set and turn their findings into TODO items. The main agent switches the branch, then sends three sub-agents in one message, each writing only its own file in `work/`: `todo-code-review.json`, `todo-security-review.json`, `todo-code-smell.json` (a JSON array of finished TODO strings, steps 3–6 and 8 below already applied). The main agent waits for all three, merges and sorts them (step 5), and switches the branch back. A sub-agent that fails or cannot run its skill writes one `[skipped] <review>: <reason>` item.

1. Switch to the task's repo and branch first (main agent). The reviews read only the cwd repo's current branch, and `security-review` takes no target argument.
   - Record the original branch (`git branch --show-current`) before anything else.
   - `cd` to the task's project repo and check out the task's branch (the branch holding the reviewed commits). Verify with `git status` that the branch is right.
   - A feature report reviews the range `<base>..<head>` (from `changes.slice`) limited to the batch's files; step 3 then keeps only findings in this batch's hunks.
   - Dirty working tree: do NOT stash, reset, checkout over it or discard anything. Ask the user how to proceed. When no one can answer (non-interactive run), skip `security-review` and add the TODO item `[skipped] security-review: working tree of <repo> is dirty`; `code-review` accepts a free-text target, so run it with the commit range instead of switching.
   - Each sub-agent runs its review from that repo: `code-review`, `security-review`, or the code-smell review (Stage 4c), one Skill call each. Never `--fix` or `--comment`; this stage only reads.
   - When all three are done, switch back to the recorded original branch.
2. `code-review` may run as a background task. Wait for its result notification before building the card; do not finish with the card still unfilled.
3. Keep only findings whose `file:line` falls inside a reviewed hunk (look it up with `bin/review-kit lines`); drop the rest.
4. Map each kept finding to one TODO string. `code-review` returns JSON findings (most severe first) with `file`, `line`, `summary`, `failure_scenario`, `category` and, after a verify pass, `verdict` (`CONFIRMED` or `PLAUSIBLE`); `security-review` reports its findings with a severity and confidence. Write the question from `summary` (add the `failure_scenario` consequence when short), and the location from `file`/`line`.
5. Severity: the format is `[<severity>][code-review] <finding> (<path>:<line>, <connection-id>)` or `[<severity>][security-review] ...`, with `<severity>` exactly one of `low`, `medium`, `high` (the viewer shows it as a badge; `review-check` R17 errors on a missing or unknown severity or an unsorted list; map any `critical` to `high`). Each item has a checkbox the user ticks to mark it handled. The viewer adds an "AI Fix" button (copies a fix prompt) to each `low` item, an "Ask AI" button (copies a prompt that asks the AI to judge the item without editing) to each `medium`/`high` item, and a copy-all button on the card, so write each `low` item as a self-contained fix with its `(<path>:<line>)`. Use the severity `security-review` reports (HIGH/MEDIUM/LOW). `code-review` has no severity field, so rate it yourself: `high` for a `CONFIRMED` correctness bug or data loss, `medium` for a `PLAUSIBLE` bug, a behavior question for the PO or a missing test of the new logic, `low` for `simplification`/`efficiency` cleanups and style. Sort the whole list by severity, most severe first; within one level keep the review's own order and put `security-review`, then `code-review`, then `code-smell`.
6. The connection id comes from the connection whose diff holds that line; omit it when no connection does (a line of a test file, for example). Use the file name plus line, not the full repo path, so items stay short.
7. Add the strings of all three reviews to the `Open questions / TODO` card (the `cards` entry with `"dot": "amber"` in `plan.json`). The viewer splits that card into Code Review, Security Review and Code Smell tabs by the `[security-review]` and `[code-smell]` tags (their `[skipped]` items included); items without either go to Code Review. "Copy all LOW" copies the open tab's `low` items only.
8. A review that ran and found nothing adds one item `[low][<review>] <sentence>`, where `<sentence>` is the report language's line: `no findings in the reviewed hunks` for `en` (or no `meta.locale`), `审查的 hunk 没有发现问题` for `zh-CN`, `審查的 hunk 沒有發現問題` for `zh-TW` (the viewer catalog key `viewer.todo.noFindings`; `review-check` R20 warns on another language's sentence, and the viewer shows it as one plain line, without badge, tag, checkbox or button, and leaves it out of the counts; it hides the line when the same tab also holds other items, such as an untagged open question). A review that was unavailable (Codex or another harness) or skipped adds one `[skipped] <review>: <reason>` item instead.

The card is the purpose of this stage: it collects pending questions for a human to decide, so write findings as questions or TODOs, not verdicts.

## Stage 4c (code-smell sub-agent)

The third Stage 4b sub-agent reviews only the reviewed hunks for over-engineering with the bundled rules in `references/ponytail-review.md` (ponytail-review, MIT, github.com/DietrichGebert/ponytail). Read that file first and follow its tags and boundaries; no other skill is needed. Keep only findings whose `file:line` falls inside a reviewed hunk.

1. Format: `[<risk>][code-smell] <tag>: <what to cut>. <replacement>. (<file>:<line>, <connection-id>)`. `<tag>` is one of the ponytail-review tags `delete`, `stdlib`, `native`, `yagni`, `shrink`; `<replacement>` names the standard-library function, platform feature or shorter form, or says `Nothing replaces it.` Write one finding per item.
2. `<risk>` is the risk of making the cut:
   - `low`: inside the change and one file, no signature change.
   - `medium`: a signature change or a removal that touches other callers in this repo, or code with no test yet.
   - `high`: a published interface (an API other teams or packages call) or a persisted data format.
3. The viewer gives each `low` item an "AI Fix" button whose prompt asks to cut the code or swap in the named replacement with behavior unchanged and tests run before and after, so a `low` item must be self-contained.
4. Leave out ponytail-review's closing `net: -<N> lines possible.` and `Lean already. Ship.` lines. No findings: one `[low][code-smell] <no-findings sentence>` item (step 8). A sub-agent that could not run the review: `[skipped] code-smell: <reason>`.

## Stage 5 (parallel fix)

Group findings by connection; one agent per group, writing only its own part file. Re-run `assemble` and return to stage 4. At most two rounds; report whatever is still failing as it is.

## Stage 6 (main agent)

Run `node bin/archify.mjs deliver architecture work/arch-review.json ~/archify-review/review.html --quality showcase --report <TICKET> --json` (a feature report: `--report <ID>`). It writes `reports/<TICKET>.js` and (when it changed) the viewer; the page is `~/archify-review/review.html?report=<TICKET>`. Then `visual-check` is required, not optional: run it once after a successful deliver and print only `status` and `diagnostics` (the full receipt stays in the sidecar). The diagram is not finished until it has run; report its status in the final reply, and if it cannot run, say so and why.

```
node bin/archify.mjs visual-check ~/archify-review/review.html --report <TICKET> --json | node -e 'const d=JSON.parse(require("fs").readFileSync(0));console.log(d.status,JSON.stringify(d.diagnostics))'
```

Never run `bin/visual-check.mjs` directly: it is a library with no entry point.

## Where the review-check rules come from

- R1: a flow for something the commit did not touch, or without diff, path and line, misled the reader.
- R2 (also applied to node `code.files` diffs): hunks were trimmed, cherry-picked or shortened, so gutter line numbers no longer matched git.
- R3 (also applied to node `code.files` diffs): a hunk was shown under a connection pointing at a different file.
- R4: hunks of the commit were left out of every flow, so reviewers never saw them. Hunks of the test files in `changes.tests` need no flow.
- R5: a `tests.functions[].lines` cited line numbers absent from the diff, or used the wrong old/new sign.
- R6: the connection `line` pointed outside the diff, so the file link landed on the wrong line.
- R7: the Change File Tree listed numbers or statuses that disagreed with `git show --numstat` (test files in `changes.tests` are not listed).
- R8 (warning): long multi-sentence notes buried the summary; detail belongs in `story`.
- R9 (warning): several connections between one from/to pair made the diagram ambiguous.
- R10: a tests function's line numbers matched lines in several hunks of one diff (commits overlap in new-file numbers), so they named the wrong code; set `hunk` on the function.
- R11: no Story purpose card in `cards` (title 這張 Story 的目的 or "Story purpose"), so reviewers saw what changed but not why.
- R13: a connection whose diff has only comment changes drew a runtime relationship that does not exist (two files that merely mention each other in comments); drop the connection and put the hunk in that node's `code.files[].diff`, which still counts for R4 coverage. If the two files are the same component on two platforms, use `kind: counterpart` instead (below).
- R14: a connection had neither `code.action` nor `code.story`, so its "View changes" panel showed only the no-interaction placeholder. A `kind: counterpart` connection may use `code.note` instead.
- R15: node labels would render below 13px at 1440x900 because the viewBox is too large for the panel; pack and widen nodes to their labels (see the layout budget in stage 2).
- R16 (warning): nodes cover under 30% of the canvas, i.e. empty bands.
- R17: a TODO item from `/code-review`, `/security-review` or the code-smell review had no `[low]`/`[medium]`/`[high]` severity before the `[code-review]`/`[security-review]`/`[code-smell]` tag, or the list was not sorted most severe first, so the viewer could not badge or order it.
- R18: `meta.platform` is missing or not `app`, a component lacks a layer for a scheme of that platform (`code.layers.mvvm`, `.flutter` (only with `meta.framework: "flutter"`) or `.webview` (only with `meta.webview: true`; `host`, `bridge` or `content`)), a component carries `layers.web`, or a WebView component is in no diff (no `code.files` and no connection), so the layout switch could not place it or the page would draw code the commits do not touch; set them (see the plan.json notes above).
- R19: an edge label is wider than the horizontal gap between its two nodes, so `deliver` would draw it over a node; the message gives the gap the label needs. When fixing R16, never shrink a gap below that.
- R20 (warning): the "no findings" TODO item is written in a different language than `meta.locale`.
- R21: a file in `changes.files` has no component labeled with its file name, or a test file of `changes.tests` is drawn: a component labeled with its name, a node `files[].path` or a connection `path` names it. Test files are never nodes, tree entries or hops; their tests go in the `tests` of the hops they cover.
- R22: a card other than 這張 Story 的目的 and 待確認問題 / TODO (for example a device test log or test accounts). The report is shared; put only what changed, why, and open questions in it.
- R23: a component with `code.files` has no `code.risk`, so its badge and 爆炸半徑 tab would be missing. Run `bin/review-kit risk` and `assemble --risk risk.json`; never write `code.risk` by hand.
- R24: a hop's `tests` were missing or did not hold together: every non-counterpart connection needs `tests.functions` (at least one), each with status `new`, `existing`, `none` or `removed`; a `new` or `existing` function names its tests in `by` and each of them has a `cases` entry; a `none` function has an empty `by` and a `suggested` entry covering it; `via` appears only on a `new` or `existing` function; every function has a `why` of at most 80 visible characters; every Given, When and Then is non-empty and at most 60 visible characters (a code span counts 20 at most), and more than two code spans in a case is a warning.

## Design counterpart connections

- `"kind": "counterpart"` links two files that implement the same component on two platforms (for example a Flutter widget and the native layout that draws it) without calling each other. It renders dashed, with no arrowhead, and is not a hop in "View changes".
- Use it only when the code or its comments state that the two implement the same thing. Link to the top-level native counterpart (here the layout), not to every sub-resource that also mentions it.
- Give it a `label` and `code.story` (or `code.note`) stating the correspondence; no `diff`, `tests`, `path` or `line`. It is exempt from R1, R5, R6, R10, R13, R24 and does not count for R4: the comment-only hunk stays in the node's `code.files[].diff`.
