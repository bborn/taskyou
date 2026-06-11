// ty-chrome end-to-end demo: loads the unpacked extension in a persistent
// Chromium context, annotates the demo storefront, and sends the bundle to the
// isolated taskyou instance. Screenshots land in /tmp/tychrome-demo/shots/.
const { chromium } = require('playwright');
const fs = require('fs');

const path = require('path');
const EXT = path.resolve(__dirname, '..');
const SHOTS = '/tmp/tychrome-demo/shots';
const SERVER = 'http://127.0.0.1:8765';
const APP = 'http://localhost:3142/';

(async () => {
  fs.mkdirSync(SHOTS, { recursive: true });
  fs.rmSync('/tmp/tychrome-demo/profile', { recursive: true, force: true });

  const ctx = await chromium.launchPersistentContext('/tmp/tychrome-demo/profile', {
    headless: false,
    viewport: { width: 1280, height: 800 },
    args: [`--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`],
  });

  let [sw] = ctx.serviceWorkers();
  if (!sw) sw = await ctx.waitForEvent('serviceworker');
  const extId = new URL(sw.url()).host;
  console.log('extension id:', extId);

  // Demo page
  const page = await ctx.newPage();
  await page.goto(APP);

  // Side panel (as a tab; ?tab pins it to the demo tab)
  const sp = await ctx.newPage();
  await sp.goto(`chrome-extension://${extId}/sidepanel.html`);
  await sp.evaluate((url) => chrome.storage.local.set({ serverUrl: url }), SERVER);
  const tabId = await sp.evaluate(async (appUrl) => {
    const tabs = await chrome.tabs.query({ url: appUrl + '*' });
    return tabs[0]?.id;
  }, APP);
  if (!tabId) throw new Error('demo tab not found');
  await sp.goto(`chrome-extension://${extId}/sidepanel.html?tab=${tabId}`);
  await sp.waitForTimeout(1200);
  await sp.screenshot({ path: `${SHOTS}/1-panel-matched.png` });
  console.log('panel screenshot done');

  // Inject overlay + enter select mode (from extension page context)
  await sp.evaluate(async (tabId) => {
    await chrome.scripting.executeScript({ target: { tabId }, files: ['content.js'] });
    await chrome.tabs.sendMessage(tabId, { type: 'ty-enter-select' });
  }, tabId);

  // Annotate: hover + click the first Add to cart button
  await page.bringToFront();
  await page.hover('#product-1 .buy-btn');
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${SHOTS}/2-select-highlight.png` });
  await page.click('#product-1 .buy-btn'); // intercepted by overlay
  await page.fill('#ty-annotate-host textarea', 'Make all the "Add to cart" buttons teal (#0d9488) with white text');
  await page.screenshot({ path: `${SHOTS}/3-comment-popover.png` });
  await page.click('#ty-annotate-host .popover .save');

  // Region annotation: Box mode, drag over the price of product 2
  await page.click('#ty-annotate-host .toolbar button:nth-of-type(2)'); // Box
  const price = await page.locator('#product-2 .price').boundingBox();
  await page.mouse.move(price.x - 8, price.y - 6);
  await page.mouse.down();
  await page.mouse.move(price.x + price.width + 40, price.y + price.height + 6, { steps: 8 });
  await page.mouse.up();
  await page.fill('#ty-annotate-host textarea', 'Prices feel small — bump to 22px and use the teal accent color');
  await page.click('#ty-annotate-host .popover .save');
  await page.waitForTimeout(300);
  await page.screenshot({ path: `${SHOTS}/4-annotated.png` });
  console.log('annotations placed');

  // Send from the on-page toolbar
  await page.click('#ty-annotate-host .toolbar button.send');
  await page.waitForSelector('#ty-annotate-host .toast', { timeout: 10000 });
  const toast = await page.textContent('#ty-annotate-host .toast');
  console.log('toast:', toast);
  await page.screenshot({ path: `${SHOTS}/5-sent-toast.png` });

  // Panel after send: executor console shows the nudge arriving
  await sp.bringToFront();
  await sp.waitForTimeout(3500);
  await sp.screenshot({ path: `${SHOTS}/6-panel-executor.png` });

  await ctx.close();
  console.log('demo complete');
})().catch((e) => {
  console.error('DEMO FAILED:', e);
  process.exit(1);
});
