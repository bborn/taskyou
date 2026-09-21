package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"

	"github.com/bborn/workflow/internal/db"
)

func TestLinkifyURLs(t *testing.T) {
	const url = "https://x.com/paolino/status/2098437083064926438"
	link := ansi.SetHyperlink(url) + url + ansi.ResetHyperlink()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "see " + url + " now", "see " + link + " now"},
		{"styled", "\x1b[38;5;30;4m" + url + "\x1b[0m\x1b[38;5;252m \x1b[0m", "\x1b[38;5;30;4m" + link + "\x1b[0m\x1b[38;5;252m \x1b[0m"},
		{"trailing punctuation", "(" + url + ").", "(" + link + ")."},
		{"end of line", url + "\nnext", link + "\nnext"},
		{"no url", "nothing here", "nothing here"},
		{"bare scheme", "https:// alone", "https:// alone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := linkifyURLs(tt.in); got != tt.want {
				t.Errorf("linkifyURLs(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// Glamour's rendering of a bare URL must come out as one hyperlink whose
// target is exactly the URL, and the link must not change the visible width.
func TestLinkifyURLsGlamourOutput(t *testing.T) {
	const url = "https://x.com/paolino/status/2098437083064926438"
	for _, style := range []string{"dark", "notty"} {
		r, err := glamour.NewTermRenderer(glamour.WithStylePath(style), glamour.WithWordWrap(120))
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := r.Render("Announces RubyLLM 2.0.\n" + url)
		if err != nil {
			t.Fatal(err)
		}
		got := linkifyURLs(rendered)
		if want := ansi.SetHyperlink(url) + url + ansi.ResetHyperlink(); strings.Count(got, want) != 1 {
			t.Errorf("%s: want exactly one hyperlink to %s in %q", style, url, got)
		}
		if ansi.StringWidth(got) != ansi.StringWidth(rendered) {
			t.Errorf("%s: width changed: %d -> %d", style, ansi.StringWidth(rendered), ansi.StringWidth(got))
		}
	}
}

// A URL drawn flush against a box-drawing border, a truncation ellipsis or a
// bullet must not take that character into its target. iTerm2 opened
// ".../pull/732│" — the pane border swallowed into the link — and a hyperlink
// whose target ends in the border is no better than no hyperlink at all.
func TestLinkifyURLsStopsAtNonURLCharacters(t *testing.T) {
	const url = "https://github.com/bborn/taskyou/pull/732"
	link := ansi.SetHyperlink(url) + url + ansi.ResetHyperlink()

	for _, suffix := range []string{"│", "┃", "▏", "…", "→", "•", "║"} {
		t.Run(suffix, func(t *testing.T) {
			if got, want := linkifyURLs(url+suffix), link+suffix; got != want {
				t.Errorf("linkifyURLs(%q)\n got %q\nwant %q", url+suffix, got, want)
			}
		})
	}
}

// Query strings, fragments and percent-escapes are part of the URL and must
// survive into the target.
func TestLinkifyURLsKeepsFullURI(t *testing.T) {
	const url = "https://github.com/bborn/taskyou/pull/732/files?w=1&diff=split#diff-a%2Fb.go"
	want := ansi.SetHyperlink(url) + url + ansi.ResetHyperlink()
	if got := linkifyURLs(url); got != want {
		t.Errorf("linkifyURLs(%q)\n got %q\nwant %q", url, got, want)
	}
}

// link wraps url verbatim in an OSC 8 hyperlink with no trailing prose.
func link(url string) string {
	return ansi.SetHyperlink(url) + url + ansi.ResetHyperlink()
}

// A URL whose path legitimately ends in ')', ']', ';', ':' or "'" must keep that
// character in the OSC 8 target. The unconditional TrimRight that linkifyURLs
// used to run stripped it, so a click opened a truncated (often 404) URL while
// the visible text looked intact — the exact failure linkifyURLs exists to
// prevent. The canonical hit is a Wikipedia/MediaWiki URL ending in ')'.
func TestLinkifyURLsKeepsTrailingURICharacters(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"trailing paren (wikipedia)", "https://en.wikipedia.org/wiki/Foo_(bar)"},
		{"trailing paren (disambiguation)", "https://en.wikipedia.org/wiki/Orange_(disambiguation)"},
		{"trailing bracket", "https://example.com/foo[1]"},
		{"trailing semicolon", "https://example.com/matrix;param;"},
		{"trailing colon", "https://example.com/path:"},
		{"trailing apostrophe", "https://example.com/it's"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linkifyURLs(tt.url)
			if want := link(tt.url); got != want {
				t.Errorf("linkifyURLs(%q)\n got %q\nwant %q", tt.url, got, want)
			}
			if !strings.Contains(got, ansi.SetHyperlink(tt.url)) {
				t.Errorf("linkifyURLs(%q): OSC 8 target is not %q\ngot %q", tt.url, tt.url, got)
			}
			if ansi.StringWidth(got) != ansi.StringWidth(tt.url) {
				t.Errorf("linkifyURLs(%q): width changed: %d -> %d", tt.url, ansi.StringWidth(tt.url), ansi.StringWidth(got))
			}
		})
	}
}

// A trailing ')' or ']' that belongs to surrounding prose — not to the URL —
// must still be shed, but only when it does not balance an opening '(' or '['
// inside the matched URL. This is what distinguishes "(url)." from
// "https://en.wikipedia.org/wiki/Foo_(bar)".
func TestLinkifyURLsShedsUnbalancedProseBrackets(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"prose paren after balanced url",
			"(https://en.wikipedia.org/wiki/Foo_(bar))",
			"(" + link("https://en.wikipedia.org/wiki/Foo_(bar)") + ")",
		},
		{
			"balanced url paren followed by surplus prose parens",
			"(https://example.com/(x)))",
			"(" + link("https://example.com/(x)") + "))",
		},
		{
			"prose bracket after balanced url",
			"[https://example.com/foo[1]]",
			"[" + link("https://example.com/foo[1]") + "]",
		},
		{
			"unmatched trailing bracket",
			"https://example.com/path]",
			link("https://example.com/path") + "]",
		},
		{
			"only sentence punctuation",
			"https://example.com/path!?.",
			link("https://example.com/path") + "!?.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := linkifyURLs(tt.in); got != tt.want {
				t.Errorf("linkifyURLs(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// End-to-end through the real detail-view render path (DetailModel.renderContent)
// — the same code path at detail.go:3068 that linkifies a glamour-rendered task
// body. A task body whose prose contains a Wikipedia URL ending in ')' must
// produce an OSC 8 hyperlink whose target is the full URL (with the ')'), never
// the truncated form. This is the integration the bug report blames.
func TestLinkifyURLsDetailRenderContentTrailingParen(t *testing.T) {
	const url = "https://en.wikipedia.org/wiki/Foo_(bar)"
	task := &db.Task{
		ID:     1,
		Title:  "Research Foo (bar)",
		Status: db.StatusProcessing,
		Body:   "See " + url + " for context.",
	}
	m := &DetailModel{task: task, width: 160, height: 50, focused: true, ready: true}
	m.viewport.Width = m.width - 4
	m.viewport.Height = m.height - 8
	rendered := m.renderContent()
	if !strings.Contains(rendered, ansi.SetHyperlink(url)) {
		t.Fatalf("renderContent: OSC 8 target is not the full URL %q\ngot %q", url, rendered)
	}
	if strings.Contains(rendered, ansi.SetHyperlink(strings.TrimRight(url, ")"))) {
		t.Fatalf("renderContent: OSC 8 target was truncated (missing trailing ')') in %q", rendered)
	}
	want := ansi.SetHyperlink(url) + url + ansi.ResetHyperlink()
	if strings.Count(rendered, want) != 1 {
		t.Fatalf("renderContent: want exactly one hyperlink to %s in %q", url, rendered)
	}
}
