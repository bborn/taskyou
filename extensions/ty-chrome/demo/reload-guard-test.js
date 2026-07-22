// Verifies the reload guard against a real Chrome: does the bridge actually
// refuse to reload a tab the user is working in, and does it still reload when
// they're not? Asserts on real page state (a nav counter that survives only if
// the page was NOT reloaded).
//
//   NODE_PATH=<dir-with-playwright> node reload-guard-test.js ..
const { chromium } = require('playwright');
const path = require('path');

const EXT = path.resolve(process.argv[2]);
const PROFILE = '/tmp/ty-reload-guard-profile';

// Served over real http: extension host permissions don't cover data: URLs, so
// the probe can't be injected there and the guard would fail open.
const HTML = `<!doctype html><title>app</title>
<h1>app</h1>
<form>
  <input id="name" value="default">
  <textarea id="notes"></textarea>
  <input type="checkbox" id="agree">
  <button type="button" id="btn">click</button>
</form>`;

let pass = 0;
let fail = 0;
const expect = (label, actual, wanted) => {
  const ok = actual === wanted;
  ok ? pass++ : fail++;
  console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${label}\n        got ${JSON.stringify(actual)}${ok ? '' : `, wanted ${JSON.stringify(wanted)}`}`);
};

(async () => {
  const server = require('http').createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/html', 'Cache-Control': 'no-store' });
    res.end(HTML);
  });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const PAGE = `http://127.0.0.1:${server.address().port}/`;

  require('fs').rmSync(PROFILE, { recursive: true, force: true });
  const ctx = await chromium.launchPersistentContext(PROFILE, {
    headless: false,
    args: [...(process.env.HEADED ? [] : ['--headless=new']), `--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`],
  });
  let [sw] = ctx.serviceWorkers();
  if (!sw) sw = await ctx.waitForEvent('serviceworker');

  const page = await ctx.newPage();
  await page.goto(PAGE);
  const tabId = await sw.evaluate(async () => (await chrome.tabs.query({ title: 'app' }))[0]?.id);

  const probe = () => sw.evaluate(async (id) => probeActivity(id), tabId);
  // Marker that only survives if the page was never reloaded.
  const mark = () => page.evaluate(() => (window.__marker = 'alive'));
  // A reload tears down the execution context; retry so we read the new one.
  const marker = async () => {
    for (let i = 0; i < 5; i++) {
      try {
        return await page.evaluate(() => window.__marker || 'GONE (page reloaded)');
      } catch {
        await new Promise((r) => setTimeout(r, 400));
      }
    }
    return 'GONE (page reloaded)';
  };
  // Drive the real bridge entry point the executor uses.
  const bridge = (action, params = {}) =>
    sw.evaluate(async ([id, action, params]) => handlers.browserExec({ tabId: id, command: { action, params } }), [tabId, action, params]);

  console.log('\n=== 1. idle page: reload is allowed ===');
  await mark();
  expect('probe reports not busy', (await probe()).busy, false);
  let r = await bridge('reload');
  await page.waitForLoadState();
  expect('bridge reload succeeded', r.ok, true);
  expect('page actually reloaded', await marker(), 'GONE (page reloaded)');

  console.log('\n=== 2. focused in an input: refused ===');
  await page.click('#name');
  await mark();
  let a = await probe();
  expect('probe reports busy', a.busy, true);
  expect('reason names the input', a.reasons.includes('typing in an input'), true);
  r = await bridge('reload');
  await new Promise((res) => setTimeout(res, 600));
  expect('bridge reload was blocked', r.blocked, true);
  expect('page survived', await marker(), 'alive');
  console.log('        executor was told:', JSON.stringify(r.error));

  console.log('\n=== 3. navigate is guarded too ===');
  r = await bridge('navigate', { url: PAGE + '?navigated=1' });
  await new Promise((res) => setTimeout(res, 600));
  expect('bridge navigate was blocked', r.blocked, true);
  expect('page survived', await marker(), 'alive');

  console.log('\n=== 4. unfocused but dirty form field: still refused ===');
  await page.fill('#notes', 'half-written thought');
  await page.click('#btn'); // move focus off the textarea
  await mark();
  a = await probe();
  expect('probe reports busy', a.busy, true);
  expect('reason names unsaved fields', a.reasons.some((x) => x.includes('unsaved form field')), true);
  r = await bridge('reload');
  await new Promise((res) => setTimeout(res, 600));
  expect('bridge reload was blocked', r.blocked, true);
  expect('page survived', await marker(), 'alive');

  console.log('\n=== 5. mid-annotation: refused ===');
  await page.evaluate(() => {
    document.querySelector('#notes').value = document.querySelector('#notes').defaultValue;
    document.activeElement.blur();
  });
  await sw.evaluate(async (id) => {
    await ensureInjected(id);
    await chrome.tabs.sendMessage(id, { type: 'ty-enter-select' });
  }, tabId);
  await new Promise((res) => setTimeout(res, 500));
  await mark();
  a = await probe();
  expect('probe reports busy', a.busy, true);
  expect('reason names annotate mode', a.reasons.some((x) => x.includes('annotating')), true);
  r = await bridge('reload');
  await new Promise((res) => setTimeout(res, 600));
  expect('bridge reload was blocked', r.blocked, true);
  expect('page survived', await marker(), 'alive');

  console.log('\n=== 6. user clears activity: reload flows again ===');
  await sw.evaluate(async (id) => chrome.tabs.sendMessage(id, { type: 'ty-clear' }), tabId);
  await page.evaluate(() => window.__tyAnnotate && (document.activeElement.blur(), 0));
  await sw.evaluate(async (id) => chrome.tabs.sendMessage(id, { type: 'ty-toast', message: '' }), tabId).catch(() => {});
  // leave select mode
  await page.keyboard.press('Escape');
  await new Promise((res) => setTimeout(res, 400));
  a = await probe();
  console.log('        probe now:', JSON.stringify(a));
  if (!a.busy) {
    r = await bridge('reload');
    await page.waitForLoadState();
    expect('bridge reload succeeded', r.ok, true);
    expect('page reloaded', await marker(), 'GONE (page reloaded)');
  } else {
    expect('activity cleared after Escape + clear', a.busy, false);
  }

  console.log(`\n================ ${pass} passed, ${fail} failed ================`);
  await ctx.close();
  server.close();
  process.exit(fail === 0 ? 0 : 1);
})();
