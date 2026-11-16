import { test } from 'node:test';
import assert from 'node:assert/strict';
import { deliverSpec, openPage, readFixture, scratchDir, skipChrome } from './helpers/review-page.mjs';

// Without MutationObserver the review page degrades but stays usable.
test('review page loads and works with no MutationObserver', { skip: skipChrome, timeout: 90000 }, async (t) => {
  const spec = readFixture('PR-10922');
  const p = await openPage(t, deliverSpec(scratchDir(t), spec), '#ref=tab&tab=path-menu-ctrl', 'delete window.MutationObserver;');
  assert.equal(await p.run('typeof MutationObserver'), 'undefined');
  assert.equal(await p.run(`document.querySelectorAll('.review-file').length`), spec.changes.files.length);
  assert.ok(await p.run(`document.querySelectorAll('.path-panel:not([hidden]) .path-hop.is-current').length`) === 1, 'hop opens');
  assert.ok(await p.run(`document.querySelectorAll('.path-panel:not([hidden]) .path-hop.is-current pre, .path-panel:not([hidden]) .path-hop.is-current .diff, .path-panel:not([hidden]) .path-hop.is-current [class*=excerpt]').length`) > 0, 'diff renders');
  await p.run(`document.querySelector('#layout-switch button[data-layout="mvvm"]').click(); Archify.viewerChromeLayout.whenStable()`);
  assert.equal(await p.run('document.documentElement.dataset.layout'), 'mvvm');
});
