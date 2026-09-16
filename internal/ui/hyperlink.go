package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderedURLPattern matches a URL in rendered terminal output. It accepts only
// the characters RFC 3986 allows in a URI, so the match stops at whitespace, at
// ESC (the style sequences Glamour wraps a link in), and at anything a terminal
// UI may draw flush against the link — a box-drawing border, a truncation
// ellipsis, a bullet. Those characters cannot be part of a URL, and letting one
// into the match would put it into the hyperlink's target.
var renderedURLPattern = regexp.MustCompile(`https?://[A-Za-z0-9\-._~%!$&'()*+,;=:/?#\[\]@]+`)

// linkifyURLs wraps every URL in s in an OSC 8 hyperlink. Without one the
// terminal has to guess where a URL ends from the characters on screen, and
// iTerm2 guessed wrong in the detail view, opening links with junk appended.
// An explicit hyperlink gives the click its exact target.
//
// Call it on already-rendered text: it is ANSI-aware and leaves the visible
// width unchanged, so a linkified line still aligns and truncates the same way.
func linkifyURLs(s string) string {
	return renderedURLPattern.ReplaceAllStringFunc(s, func(url string) string {
		trimmed := strings.TrimRight(url, ".,;:!?)]}'\"")
		rest := url[len(trimmed):]
		if trimmed == "" || strings.HasSuffix(trimmed, "://") {
			return url
		}
		return ansi.SetHyperlink(trimmed) + trimmed + ansi.ResetHyperlink() + rest
	})
}
