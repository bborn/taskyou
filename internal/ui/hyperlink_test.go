package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
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
