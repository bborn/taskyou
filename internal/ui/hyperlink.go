package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderedURLPattern matches a URL in rendered terminal output. It stops at
// whitespace and at ESC, so the style sequences Glamour wraps a link in are
// never swallowed into it.
var renderedURLPattern = regexp.MustCompile(`https?://[^\s\x1b]+`)

// linkifyURLs wraps every URL in s in an OSC 8 hyperlink. Without one the
// terminal has to guess where a URL ends from the characters on screen, and
// iTerm2 guessed wrong in the detail view, opening links with junk appended.
// An explicit hyperlink gives the click its exact target.
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
