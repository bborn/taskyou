#!/usr/bin/env python3
"""Render docs/marketing/compare/*.md drafts into styled HTML under docs/compare/.

Source of truth for copy is the Markdown under docs/marketing/compare/. This
generator wraps each page in the taskyou.dev shell (nav/footer + compare.css)
so the /compare section matches the landing page. Re-run after editing copy:

    python3 scripts/gen-compare-pages.py
"""
import re, html, sys, pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
SRC = ROOT / "docs/marketing/compare"
OUT = ROOT / "docs/compare"
OUT.mkdir(parents=True, exist_ok=True)

PAGES = {
    "index":                                ("Compare", "Compare"),
    "taskyou-vs-tmux":                      ("TaskYou vs tmux / Zellij", "vs tmux"),
    "taskyou-vs-conductor-emdash-superset": ("TaskYou vs Conductor / Emdash / Superset", "vs Conductor"),
    "taskyou-vs-cmux-warp":                 ("TaskYou vs cmux / Warp", "vs cmux / Warp"),
}


def rewrite_link(href):
    if "README.md" in href:
        return "https://github.com/bborn/taskyou#readme"
    if href == "../index.html":
        return "/"
    href = re.sub(r"\.md(#.*)?$", lambda m: ".html" + (m.group(1) or ""), href)
    if href == "index.html":
        return "./"
    return href


def inline(text):
    spans = []

    def stash(m):
        spans.append(m.group(1))
        return f"\x00{len(spans) - 1}\x00"

    text = re.sub(r"`([^`]+)`", stash, text)
    text = html.escape(text, quote=False)
    text = re.sub(
        r"\[([^\]]+)\]\(([^)]+)\)",
        lambda m: f'<a href="{html.escape(rewrite_link(m.group(2)), quote=True)}">{m.group(1)}</a>',
        text,
    )
    text = re.sub(r"\*\*(.+?)\*\*", r"<strong>\1</strong>", text)
    text = re.sub(r"(?<![\w*])\*([^*]+)\*(?![\w*])", r"<em>\1</em>", text)
    text = re.sub(r"\x00(\d+)\x00", lambda m: f"<code>{html.escape(spans[int(m.group(1))], quote=False)}</code>", text)
    # Map status emoji to crisp text glyphs (U+FE0E forces text, not emoji,
    # presentation) so they match the terminal aesthetic instead of bolting on
    # OS color emoji. Applied globally so table cells AND the legend line agree.
    text = text.replace("✅", '<span class="mark yes">✓︎</span>')
    text = text.replace("◐", '<span class="mark partial">◐︎</span>')
    text = text.replace("❌", '<span class="mark no">✕︎</span>')
    return text


def style_cell(cell):
    return inline(cell.strip())


def value_cell(cell):
    """Format a comparison-column cell as a centered status glyph with the
    annotation (if any) as a small caption beneath it. Keeps every glyph on a
    consistent baseline so rows don't read ragged when annotations vary in
    length. Cells with no leading glyph (prose like 'A task board + queue',
    'n/a') render as a plain centered caption."""
    h = inline(cell.strip())
    m = re.match(r'^(<span class="mark [a-z]+">[^<]*</span>)\s*(.*)$', h)
    if m:
        mark, rest = m.group(1), m.group(2).strip()
        return mark + (f'<span class="anno">{rest}</span>' if rest else "")
    if not h:
        return ""
    return f'<span class="anno plain">{h}</span>'


def convert(md):
    lines = md.split("\n")
    out, i, n = [], 0, len(md.split("\n"))
    while i < n:
        line = lines[i]
        if line.startswith("```"):
            buf = []
            i += 1
            while i < n and not lines[i].startswith("```"):
                buf.append(html.escape(lines[i], quote=False))
                i += 1
            i += 1
            out.append("<pre><code>" + "\n".join(buf) + "</code></pre>")
            continue
        if re.match(r"^---+\s*$", line):
            out.append("<hr>")
            i += 1
            continue
        m = re.match(r"^(#{1,4})\s+(.*)$", line)
        if m:
            lvl, txt = len(m.group(1)), inline(m.group(2).strip())
            out.append(f'<h1 class="headline">{txt}</h1>' if lvl == 1 else f"<h{lvl}>{txt}</h{lvl}>")
            i += 1
            continue
        if line.strip().startswith("|") and i + 1 < n and re.match(r"^\s*\|[\s:|-]+\|\s*$", lines[i + 1]):
            header = line.strip().strip("|").split("|")
            i += 2
            rows = []
            while i < n and lines[i].strip().startswith("|"):
                rows.append(lines[i].strip().strip("|").split("|"))
                i += 1
            th = "".join(
                (f'<th class="c">{inline(h.strip())}</th>' if k > 0 else f"<th>{inline(h.strip())}</th>")
                for k, h in enumerate(header)
            )
            body = []
            for r in rows:
                tds = "".join(
                    (f'<td class="val">{value_cell(cell)}</td>' if k > 0 else f"<td>{style_cell(cell)}</td>")
                    for k, cell in enumerate(r)
                )
                body.append("<tr>" + tds + "</tr>")
            out.append(
                '<div class="tablewrap"><table><thead><tr>' + th + "</tr></thead><tbody>" + "".join(body) + "</tbody></table></div>"
            )
            continue
        if line.startswith(">"):
            buf = []
            while i < n and lines[i].startswith(">"):
                buf.append(lines[i][1:].strip())
                i += 1
            out.append("<blockquote>" + "".join(f"<p>{inline(b)}</p>" for b in buf if b) + "</blockquote>")
            continue
        if re.match(r"^\s*[-*]\s+", line):
            buf = []
            while i < n and re.match(r"^\s*[-*]\s+", lines[i]):
                buf.append(inline(re.sub(r"^\s*[-*]\s+", "", lines[i])))
                i += 1
            out.append("<ul>" + "".join(f"<li>{b}</li>" for b in buf) + "</ul>")
            continue
        if re.match(r"^\s*\d+\.\s+", line):
            buf = []
            while i < n and re.match(r"^\s*\d+\.\s+", lines[i]):
                buf.append(inline(re.sub(r"^\s*\d+\.\s+", "", lines[i])))
                i += 1
            out.append("<ol>" + "".join(f"<li>{b}</li>" for b in buf) + "</ol>")
            continue
        if not line.strip():
            i += 1
            continue
        buf = [line]
        i += 1
        while (
            i < n
            and lines[i].strip()
            and not re.match(r"^(#{1,4}\s|>|\s*[-*]\s|\s*\d+\.\s|```|---+\s*$)", lines[i])
            and not lines[i].strip().startswith("|")
        ):
            buf.append(lines[i])
            i += 1
        para = inline(" ".join(b.strip() for b in buf))
        cls = ' class="note"' if para.startswith("<em>Accuracy note") or para.startswith("<em>(") else ""
        out.append(f"<p{cls}>{para}</p>")
    return "\n".join(out)


NAV = '''<nav>
  <div class="wrap">
    <a class="brand" href="/"><img src="../images/logo.webp" alt="">taskyou</a>
    <div class="navlinks">
      <a class="desktop-only" href="/#how">How it works</a>
      <a class="desktop-only" href="/#features">Features</a>
      <a class="active" href="/compare/">Compare</a>
      <a href="https://github.com/bborn/taskyou">GitHub</a>
      <a href="/#install" class="btn btn-primary">Install ↓</a>
    </div>
  </div>
</nav>'''

FOOTER = '''<footer>
  <div class="wrap">
    <span class="fbrand"><img src="../images/logo.webp" alt="">taskyou</span>
    <span class="flinks">
      <a href="/">Home</a>
      <a href="/compare/">Compare</a>
      <a href="https://github.com/bborn/taskyou">GitHub</a>
      <a href="https://github.com/bborn/taskyou#readme">Docs</a>
    </span>
  </div>
</footer>'''

TEMPLATE = '''<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{title} — TaskYou</title>
<meta name="description" content="{desc}">
<link rel="icon" href="../images/logo.webp">
<meta property="og:title" content="{title} — TaskYou">
<meta property="og:description" content="{desc}">
<meta property="og:type" content="website">
<meta name="twitter:card" content="summary_large_image">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;600;700&display=swap" rel="stylesheet">
<link rel="stylesheet" href="compare.css">
</head>
<body>
<div class="grid-bg"></div>
{nav}
<section class="body">
  <div class="wrap prose">
{content}
{pager}
  </div>
</section>
{footer}
</body>
</html>
'''


def pager(slug):
    links = ['<a href="/compare/">← All comparisons</a>']
    if slug != "index":
        links.append('<a href="/">taskyou.dev →</a>')
    return '<div class="pager">' + "".join(links) + "</div>"


def main():
    for slug, (title, _short) in PAGES.items():
        md = (SRC / f"{slug}.md").read_text()
        dm = re.search(r"\*\*One line:\*\*\s*(.+)", md)
        desc = re.sub(r"[*_`]", "", dm.group(1)).strip() if dm else "TaskYou is an autonomy-first task orchestrator for coding agents."
        desc = html.escape(desc[:180], quote=True)
        page = TEMPLATE.format(
            title=html.escape(title), desc=desc, nav=NAV, content=convert(md), pager=pager(slug), footer=FOOTER
        )
        (OUT / f"{slug}.html").write_text(page)
        print("wrote", (OUT / f"{slug}.html").relative_to(ROOT))
    print("done")


if __name__ == "__main__":
    main()
