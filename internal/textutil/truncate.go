// Package textutil holds small string helpers shared across ty.
package textutil

import "unicode/utf8"

// Truncate shortens s to at most limit runes, appending tail when it does.
//
// It exists because the obvious form, s[:limit] + tail, slices BYTES. Any
// multi-byte character straddling the cut is split in half, leaving a dangling
// byte and invalid UTF-8 — which corrupts terminal layout (the renderer and the
// terminal disagree on the width), HTML, and any text handed to a model. Titles
// routinely carry a "·", an accent or an emoji, so this is not a rare case.
//
// limit counts runes, not columns: callers here are bounding text size, not
// laying out a terminal. For width-sensitive rendering use ansi.Truncate, which
// accounts for double-width characters.
//
// A limit at or below the length of tail returns the leading runes alone, so the
// result never grows past the caller's budget.
func Truncate(s string, limit int, tail string) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	keep := limit - utf8.RuneCountInString(tail)
	if keep <= 0 {
		return string([]rune(s)[:limit])
	}
	return string([]rune(s)[:keep]) + tail
}
