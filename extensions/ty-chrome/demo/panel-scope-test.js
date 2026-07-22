// Verifies side-panel scoping against a real Chrome by reading Chrome's own
// per-tab state via chrome.sidePanel.getOptions().
//
//   NODE_PATH=<dir-with-playwright> node panel-scope-test.js ..
//
// Scenarios, each asking "which tabs does the panel show on?":
//   1. baseline      3 tabs, panel opened on tab 3
//   2. new tab       a 4th tab created in the same window
//   3. NEW WINDOW    a second browser window with its own tabs
//   4. worker wiped  in-memory worker state cleared (what MV3 teardown does),
//                    then another tab created
const { chromium } = require('playwright');
const path = require('path');

const EXT = path.resolve(process.argv[2]);
const PROFILE = '/tmp/ty-panel-scope-test-profile';

const SNAPSHOT = async () => {
  const tabs = await chrome.tabs.query({});
  const out = [];
  for (const tab of tabs) {
    let o = {};
    try {
      o = await chrome.sidePanel.getOptions({ tabId: tab.id });
    } catch (e) {
      o = { error: e.message };
    }
    out.push({
      tabId: tab.id,
      tag: (tab.title || tab.url).slice(0, 20),
      win: tab.windowId,
      groupId: tab.groupId,
      enabled: o.enabled,
    });
  }
  return out;
};

(async () => {
  require('fs').rmSync(PROFILE, { recursive: true, force: true });
  const ctx = await chromium.launchPersistentContext(PROFILE, {
    // MV3 extensions need Chrome's *new* headless; Playwright's `headless: true`
    // still uses the old one, which loads no extensions at all. Pass the flag
    // ourselves so no window ever steals focus.
    headless: false,
    args: [
      ...(process.env.HEADED ? [] : ['--headless=new']),
      `--disable-extensions-except=${EXT}`,
      `--load-extension=${EXT}`,
    ],
  });
  let [sw] = ctx.serviceWorkers();
  if (!sw) sw = await ctx.waitForEvent('serviceworker');

  const results = [];
  const check = async (label, targetGetter) => {
    const rows = await sw.evaluate(SNAPSHOT);
    const target = targetGetter();
    const on = rows.filter((r) => r.enabled === true);
    const leaked = on.filter((r) => r.tabId !== target && r.groupId === -1);
    console.log(`\n--- ${label}`);
    for (const r of rows) {
      const mark = r.tabId === target ? '<= panel opened here' : r.enabled ? '<= LEAK' : '';
      console.log(
        `  win ${String(r.win).padEnd(11)} tab ${String(r.tabId).padEnd(11)} ${r.tag.padEnd(20)} enabled=${String(r.enabled).padEnd(9)} group=${String(r.groupId).padEnd(11)} ${mark}`,
      );
    }
    results.push({ label, leaked: leaked.length });
    return rows;
  };

  const newTab = async (title, opts = {}) => {
    const p = await ctx.newPage(opts);
    await p.goto(`data:text/html,<title>${title}</title><h1>${title}</h1>`);
    return p;
  };

  for (let i = 1; i <= 3; i++) await newTab(`tab ${i}`);
  const tabIds = await sw.evaluate(async () => {
    const t = await chrome.tabs.query({});
    return t.filter((x) => x.url.startsWith('data:')).map((x) => x.id);
  });
  const target = tabIds[2];
  console.log('opening panel on tab', target);

  // Emulate the toolbar click: chrome.action.onClicked cannot be dispatched
  // programmatically, so run the same worker-side path the listener runs.
  // (sidePanel.open() is skipped — it requires a real user gesture.)
  await sw.evaluate(async (tabId) => {
    if (typeof scopeTabToGroup === 'function') {
      chrome.sidePanel.setOptions({ tabId, path: `sidepanel.html?tab=${tabId}`, enabled: true });
      await scopeTabToGroup(tabId); // new model
    } else {
      await handlers.panelOpened({ tabId }); // old model
    }
  }, target);
  await new Promise((r) => setTimeout(r, 900));
  await check('1. baseline — panel opened on tab 3', () => target);

  await newTab('tab 4 (same window)');
  await new Promise((r) => setTimeout(r, 900));
  await check('2. new tab in the same window', () => target);

  // Second browser window.
  const w2 = await ctx.newPage();
  await w2.evaluate(() => window.open('about:blank', '_blank', 'width=900,height=700')).catch(() => {});
  await sw.evaluate(async () => {
    const win = await chrome.windows.create({ url: 'data:text/html,<title>other window</title><h1>other window</h1>' });
    return win.id;
  });
  await new Promise((r) => setTimeout(r, 1200));
  await check('3. a SECOND browser window', () => target);

  // What MV3 service-worker teardown does to worker-held state.
  const wiped = await sw.evaluate(() => {
    if (typeof panelScope !== 'undefined') {
      panelScope.clear();
      return 'panelScope (in-memory Map) cleared';
    }
    return 'no in-memory scope state to clear (scope lives in chrome.storage.session)';
  });
  console.log(`\n[teardown emulation] ${wiped}`);
  await newTab('tab 5 (post teardown)');
  await new Promise((r) => setTimeout(r, 900));
  await check('4. new tab after worker state loss', () => target);

  console.log('\n================ SUMMARY ================');
  for (const r of results) {
    console.log(`  ${r.leaked === 0 ? 'PASS' : 'FAIL'}  ${r.label.padEnd(42)} leaked onto ${r.leaked} tab(s)`);
  }
  const failed = results.filter((r) => r.leaked > 0).length;
  console.log(`\n${failed === 0 ? 'ALL SCENARIOS SCOPED CORRECTLY' : failed + ' SCENARIO(S) LEAKED'}`);
  await ctx.close();
  process.exit(failed === 0 ? 0 : 1);
})();
