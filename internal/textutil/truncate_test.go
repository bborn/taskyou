package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateNeverSplitsARune(t *testing.T) {
	// The "·" is two bytes; a byte slice at 41 lands inside it.
	s := "@devdoc83 Claude Code npx allow-list bug · @paolino RubyLLM 2.0 ships"
	for limit := 1; limit <= utf8.RuneCountInString(s)+2; limit++ {
		got := Truncate(s, limit, "...")
		if !utf8.ValidString(got) {
			t.Fatalf("limit %d produced invalid UTF-8: %q", limit, got)
		}
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("limit %d produced U+FFFD: %q", limit, got)
		}
		if n := utf8.RuneCountInString(got); n > limit {
			t.Fatalf("limit %d exceeded: %d runes in %q", limit, n, got)
		}
	}
}

func TestTruncateKeepsShortStrings(t *testing.T) {
	for _, s := range []string{"", "short", "Ünïcödé", "🚀🚀🚀"} {
		if got := Truncate(s, 20, "..."); got != s {
			t.Fatalf("Truncate(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestTruncateAppendsTailOnlyWhenCut(t *testing.T) {
	if got := Truncate("abcdefghij", 5, "..."); got != "ab..." {
		t.Fatalf("got %q, want %q", got, "ab...")
	}
	if got := Truncate("emoji 🚀 tail here", 8, "…"); !utf8.ValidString(got) || utf8.RuneCountInString(got) > 8 {
		t.Fatalf("got %q", got)
	}
}
