#!/usr/bin/env node
// Check the locally served pages, including destinations generated from Markdown.
import { readFile, stat } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const root = fileURLToPath(new URL('../docs/', import.meta.url));
const pages = ['index.html', 'getting-started.html', 'workflows.html', 'reference.html'];
let checked = 0;
for (const page of pages) {
  const html = await readFile(path.join(root, page), 'utf8');
  for (const [, raw] of html.matchAll(/(?:href|src)="([^"]+)"/g)) {
    if (/^(?:https?:|mailto:|data:|\/\/)/.test(raw)) continue;
    const url = new URL(raw, `http://site.local/${page}`);
    let dest = path.join(root, decodeURIComponent(url.pathname));
    if ((await stat(dest)).isDirectory()) dest = path.join(dest, 'index.html');
    await stat(dest);
    if (url.hash && dest.endsWith('.html')) {
      const target = await readFile(dest, 'utf8');
      const id = decodeURIComponent(url.hash.slice(1));
      if (!target.includes(`id="${id}"`)) throw new Error(`${page}: missing fragment ${raw}`);
    }
    checked++;
  }
  if (page === 'getting-started.html' && !html.includes('href="downloads/taskyou-storefront.zip"')) {
    throw new Error('Practice-project link must download the local archive, not open its source on GitHub');
  }
}
console.log(`PASS: ${checked} local links/assets/fragments across ${pages.length} pages.`);
