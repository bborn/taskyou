#!/usr/bin/env node
// Render the public guides from Markdown. The committed HTML needs no build at deploy time.
// Uses the Markdown renderer already installed by `npm ci --prefix desktop`.
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { Marked } from "../desktop/node_modules/marked/lib/marked.esm.js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const guides = ["getting-started", "workflows", "reference"];
const titles = {
  "getting-started": "Your first task",
  workflows: "Workflow recipes",
  reference: "Full reference",
};
const descriptions = {
  "getting-started": "Install TaskYou, add a project, run one coding-agent task in an isolated git worktree, and review the result.",
  workflows: "Download tested TaskYou workflow recipes for bug fixes, features, and refactors with parallel review steps and pull-request handoffs.",
  reference: "TaskYou reference for CLI commands, terminal and desktop interfaces, task executors, workflows, plugins, hooks, SSH access, and configuration.",
};
const siteUrl = "https://taskyou.dev";
const socialImage = `${siteUrl}/images/taskyou-social.png`;
const socialImageAlt = "TaskYou — Kanban for code agents, with a pixel coffee mug and illustrated terminal Kanban board";
const escape = (text) =>
  text
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
let stale = false;
for (const guide of guides) {
  const counts = new Map();
  const markdown = new Marked({
    renderer: {
      heading({ tokens, depth, text }) {
        const base = text
          .toLowerCase()
          .replace(/<[^>]*>/g, "")
          .replace(/[^\p{L}\p{N}_\-\s]/gu, "")
          .replace(/\s/g, "-");
        const count = counts.get(base) || 0;
        counts.set(base, count + 1);
        return `<h${depth} id="${escape(base + (count ? `-${count}` : ""))}">${this.parser.parseInline(tokens)}</h${depth}>\n`;
      },
    },
  });
  const source = await readFile(path.join(root, "docs", `${guide}.md`), "utf8");
  const content = markdown
    .parse(source)
    .replace(/href="([^"#][^"]*)"/g, (all, href) => {
      if (/^(https?:|mailto:)/.test(href)) return all;
      const [target, hash] = href.split("#");
      if (guides.some((name) => target === `${name}.md`))
        return `href="${target.replace(/\.md$/, ".html")}${hash ? `#${hash}` : ""}"`;
      if (target.endsWith(".yaml") || target.startsWith("downloads/") || /\.(png|jpe?g|webp|svg|mp4)$/.test(target)) return all;
      const relative = path.posix.normalize(`docs/${target}`);
      return `href="https://github.com/bborn/taskyou/blob/main/${relative}${hash ? `#${hash}` : ""}"`;
    });
  const html = `<!doctype html>
<!-- Generated from ${guide}.md by scripts/build-site.mjs. Edit the Markdown source. -->
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>${titles[guide]} — TaskYou</title><meta name="description" content="${escape(descriptions[guide])}"><meta name="robots" content="index,follow,max-image-preview:large">
<link rel="canonical" href="${siteUrl}/${guide}.html"><link rel="icon" href="images/logo.webp">
<meta property="og:title" content="${titles[guide]} — TaskYou"><meta property="og:description" content="${escape(descriptions[guide])}"><meta property="og:type" content="website"><meta property="og:site_name" content="TaskYou"><meta property="og:locale" content="en_US"><meta property="og:url" content="${siteUrl}/${guide}.html">
<meta property="og:image" content="${socialImage}"><meta property="og:image:secure_url" content="${socialImage}"><meta property="og:image:type" content="image/png"><meta property="og:image:width" content="1200"><meta property="og:image:height" content="630"><meta property="og:image:alt" content="${socialImageAlt}">
<meta name="twitter:card" content="summary_large_image"><meta name="twitter:title" content="${titles[guide]} — TaskYou"><meta name="twitter:description" content="${escape(descriptions[guide])}"><meta name="twitter:image" content="${socialImage}"><meta name="twitter:image:alt" content="${socialImageAlt}">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet"><link rel="stylesheet" href="assets/site.css"></head>
<body><a class="skip" href="#main">Skip to content</a><nav class="nav" aria-label="Main navigation"><div class="wrap"><a class="brand" href="./"><img src="images/logo.webp" width="40" height="40" alt="">taskyou</a><div class="navlinks"><a href="workflows.html">Workflows</a><a href="https://github.com/bborn/taskyou">GitHub</a></div></div></nav>
<div class="guide-layout"><aside class="guide-nav" aria-label="Documentation"><p>TaskYou docs</p>${guides.map((name) => `<a href="${name}.html"${name === guide ? ' aria-current="page"' : ""}>${titles[name]}</a>`).join("")}<a href="./#demo">Product tour</a></aside>
<main id="main" class="prose">${content}<p class="source"><a href="https://github.com/bborn/taskyou/blob/main/docs/${guide}.md">View this guide on GitHub</a></p></main></div>
<footer class="footer"><div class="wrap"><span>TaskYou. An agent for every task.</span><a href="./">Back to TaskYou</a></div></footer></body></html>
`;
  const dest = path.join(root, "docs", `${guide}.html`);
  if (process.argv.includes("--check")) {
    const existing = await readFile(dest, "utf8").catch(() => "");
    if (existing !== html) {
      console.error(`Stale generated guide: docs/${guide}.html`);
      stale = true;
    }
  } else {
    await writeFile(dest, html);
    console.log(`Rendered docs/${guide}.html`);
  }
}
if (stale) process.exitCode = 1;
