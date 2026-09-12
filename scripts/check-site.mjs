#!/usr/bin/env node
// Check the locally served pages, including destinations generated from Markdown.
import { readFile, stat } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const root = fileURLToPath(new URL('../docs/', import.meta.url));
const pages = ['index.html', 'getting-started.html', 'workflows.html', 'reference.html'];
const siteUrl = 'https://taskyou.dev';
const canonicalUrls = new Map(pages.map((page) => [page, page === 'index.html' ? `${siteUrl}/` : `${siteUrl}/${page}`]));
const socialImage = `${siteUrl}/images/taskyou-social.png`;
let checked = 0;
for (const page of pages) {
  const html = await readFile(path.join(root, page), 'utf8');
  const canonical = canonicalUrls.get(page);
  const requiredMetadata = [
    `rel="canonical" href="${canonical}"`,
    'name="robots" content="index,follow,max-image-preview:large"',
    'property="og:site_name" content="TaskYou"',
    `property="og:url" content="${canonical}"`,
    `property="og:image" content="${socialImage}"`,
    'property="og:image:width" content="1200"',
    'property="og:image:height" content="630"',
    'name="twitter:card" content="summary_large_image"',
    `name="twitter:image" content="${socialImage}"`,
  ];
  for (const metadata of requiredMetadata) {
    if (!html.includes(metadata)) throw new Error(`${page}: missing metadata ${metadata}`);
  }
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

const jsonLdMatch = (await readFile(path.join(root, 'index.html'), 'utf8')).match(/<script type="application\/ld\+json">([\s\S]*?)<\/script>/);
if (!jsonLdMatch) throw new Error('index.html: missing JSON-LD');
const jsonLd = JSON.parse(jsonLdMatch[1]);
if (jsonLd['@type'] !== 'SoftwareApplication' || jsonLd.url !== `${siteUrl}/`) {
  throw new Error('index.html: unexpected JSON-LD identity');
}

const socialImageFile = await readFile(path.join(root, 'images/taskyou-social.png'));
if (socialImageFile.toString('ascii', 1, 4) !== 'PNG') throw new Error('Social image must be a PNG');
if (socialImageFile.readUInt32BE(16) !== 1200 || socialImageFile.readUInt32BE(20) !== 630) {
  throw new Error('Social image must be exactly 1200x630');
}

const sitemap = await readFile(path.join(root, 'sitemap.xml'), 'utf8');
const sitemapUrls = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((match) => match[1]);
const canonicalUrlList = [...canonicalUrls.values()];
if (
  sitemapUrls.length !== canonicalUrlList.length ||
  new Set(sitemapUrls).size !== sitemapUrls.length ||
  canonicalUrlList.some((url) => !sitemapUrls.includes(url))
) {
  throw new Error('sitemap.xml must contain each canonical page exactly once');
}
const robots = await readFile(path.join(root, 'robots.txt'), 'utf8');
if (!robots.includes('User-agent: *\nAllow: /') || !robots.includes(`Sitemap: ${siteUrl}/sitemap.xml`)) {
  throw new Error('robots.txt must allow crawling and name the sitemap');
}

console.log(`PASS: ${checked} local links/assets/fragments and discoverability metadata across ${pages.length} pages.`);
