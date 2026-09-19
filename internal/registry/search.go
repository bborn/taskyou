package registry

import (
	"sort"
	"strings"
)

// Match is one search hit: the entry plus why it ranked where it did.
type Match struct {
	Entry Entry
	Score int
}

// Search ranks entries against a free-text query.
//
// The query is split on whitespace and every term must hit *something* (AND), so
// "slack notify" doesn't return every plugin that merely mentions Slack. Within
// a term the strongest field wins — an ID match outranks a description mention —
// and scores are summed across terms. An empty query returns everything in
// catalog order, which is what a browser wants when the search box is empty.
func Search(entries []Entry, query string) []Entry {
	matches := SearchRanked(entries, query)
	out := make([]Entry, len(matches))
	for i, m := range matches {
		out[i] = m.Entry
	}
	return out
}

// SearchRanked is Search, keeping the scores.
func SearchRanked(entries []Entry, query string) []Match {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		out := make([]Match, len(entries))
		for i, e := range entries {
			out[i] = Match{Entry: e}
		}
		return out
	}

	var matches []Match
	for _, e := range entries {
		total := 0
		hitAll := true
		for _, term := range terms {
			s := scoreTerm(e, term)
			if s == 0 {
				hitAll = false
				break
			}
			total += s
		}
		if hitAll {
			matches = append(matches, Match{Entry: e, Score: total})
		}
	}
	// Ties keep the catalog's own order (category, then name) — stable sort over
	// input that sortEntries already grouped.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	return matches
}

// scoreTerm returns how strongly one lowercase term matches an entry, 0 for no
// match. The ladder is deliberate: what the user typed is most likely the handle
// or the name, and a description hit should never outrank either.
func scoreTerm(e Entry, term string) int {
	id := strings.ToLower(e.ID)
	name := strings.ToLower(e.DisplayName())

	switch {
	case id == term:
		return 1000
	case name == term:
		return 800
	case strings.HasPrefix(id, term):
		return 600
	case strings.HasPrefix(name, term):
		return 500
	}
	for _, tag := range e.Tags {
		if strings.EqualFold(tag, term) {
			return 450
		}
	}
	for _, p := range e.Provides {
		if strings.EqualFold(p, term) {
			return 300
		}
	}
	switch {
	case strings.Contains(id, term):
		return 280
	case strings.Contains(name, term):
		return 260
	case strings.EqualFold(e.Category, term):
		return 240
	}
	for _, tag := range e.Tags {
		if strings.Contains(strings.ToLower(tag), term) {
			return 180
		}
	}
	switch {
	case strings.Contains(strings.ToLower(e.Description), term):
		return 120
	case strings.Contains(strings.ToLower(e.Category), term):
		return 90
	case strings.Contains(strings.ToLower(e.Author), term):
		return 60
	}
	// Last resort: a subsequence of the *handle*, so "pcr" finds
	// "plan-code-review". Deliberately not the name or description — a prose
	// sentence contains almost any short subsequence, which turns this from a
	// helpful fallback into noise that matches everything.
	if subsequence(id, term) {
		return 30
	}
	return 0
}

// subsequence reports whether pattern's runes appear in s in order.
func subsequence(s, pattern string) bool {
	if pattern == "" {
		return true
	}
	pr := []rune(pattern)
	i := 0
	for _, c := range s {
		if c == pr[i] {
			if i++; i == len(pr) {
				return true
			}
		}
	}
	return false
}

// DidYouMean returns up to limit entry IDs closest to what the user typed, for
// the "no plugin named X" error. Cheap on purpose: the catalog is small and the
// goal is a hint, not spell-check.
func DidYouMean(entries []Entry, query string, limit int) []string {
	ranked := SearchRanked(entries, query)
	if len(ranked) == 0 {
		// Nothing matched as a whole term; fall back to shared-prefix guesses.
		q := strings.ToLower(query)
		for _, e := range entries {
			if len(q) >= 3 && strings.HasPrefix(strings.ToLower(e.ID), q[:3]) {
				ranked = append(ranked, Match{Entry: e})
			}
		}
	}
	var out []string
	for _, m := range ranked {
		if len(out) >= limit {
			break
		}
		out = append(out, m.Entry.ID)
	}
	return out
}
