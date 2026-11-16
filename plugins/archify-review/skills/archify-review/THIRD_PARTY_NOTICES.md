# Third-party notices

The Archify MIT license applies to Archify's own code and content.

## JetBrains Mono

Delivered Archify viewer artifacts embed the JetBrains Mono variable font
subsets served by Google Fonts. For characters covered by these subsets, font
selection does not depend on a network request or a locally installed copy.
Uncovered characters (including CJK) still use the system fallback stack;
browser and operating-system rasterization can differ.
JetBrains Mono is maintained at
[github.com/JetBrains/JetBrainsMono](https://github.com/JetBrains/JetBrainsMono)
and is distributed under the SIL Open Font License 1.1. The complete license
text is preserved in `assets/JetBrainsMono-OFL.txt` in the packaged Skill and
in the font CSS carried by standalone HTML and SVG exports.

## flutter/packages (test fixtures and README screenshots)

The code-review test fixtures (`test/keep/fixtures/PR-*`, `test/fixtures/review-kit-plan.json`,
`test/fixtures/review-kit-spec.json`, `test/fixtures/overlapping-components.architecture.json`)
and the README images (`readme-assets/`, including `readme-assets/en/`) quote diffs from
[flutter/packages](https://github.com/flutter/packages), Copyright 2013 The Flutter Authors,
distributed under the BSD 3-Clause License
(https://github.com/flutter/packages/blob/main/LICENSE). Commits used:

- `12b43a192e1f5fa5141181a72d681a15abd003c1` (PR #10922, webview_flutter_wkwebview)
- `58d401608590d46f6f2abe83cb51a165f8222c34` (PR #9151, webview_flutter_android)
- `3094f96130ad6682200616c6c42f10e5b6a5ef23` (PR #9248, webview_flutter_platform_interface)
- `44536a0bdf789994121b387aa17bf120e26d0435` (PR #11066) and `f3ce6cbfa7677fa6ea093982c5c7c4194976d339` (PR #11122), webview_flutter_android example app;
  `test/review-kit-golden.test.mjs` runs against them when `ARCHIFY_GOLDEN_REPO` points to a
  flutter/packages checkout.
