package registry

import "testing"

var searchFixture = []Entry{
	{ID: "slack", Name: "Slack notifications", Description: "Post task updates to a channel.",
		Category: "notifications", Tags: []string{"chat", "webhook"}, Provides: []string{"hook"}},
	{ID: "desktop-notify", Name: "Desktop notifications", Description: "Native desktop notifications when a task finishes.",
		Category: "notifications", Tags: []string{"desktop"}, Provides: []string{"hook", "action"}},
	{ID: "plan-code-review", Name: "Plan → Code → Review", Description: "Plan, code, then two reviewers in parallel.",
		Category: "workflows", Tags: []string{"review"}, Provides: []string{"workflow"}},
	{ID: "worktree", Name: "Worktree inspector", Description: "Show a task's diff or run its tests.",
		Category: "development", Provides: []string{"action"}},
}

func ids(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

func TestSearch_ExactIDRanksFirst(t *testing.T) {
	got := ids(Search(searchFixture, "slack"))
	if len(got) == 0 || got[0] != "slack" {
		t.Errorf("Search(slack) = %v, want slack first", got)
	}
}

// A description-only mention must never outrank a name match, or searching
// "notifications" buries the plugin actually called that.
func TestSearch_NameBeatsDescription(t *testing.T) {
	got := ids(Search(searchFixture, "desktop"))
	if len(got) == 0 || got[0] != "desktop-notify" {
		t.Errorf("Search(desktop) = %v, want desktop-notify first", got)
	}
}

// Multiple terms are ANDed: every term has to hit something.
func TestSearch_AllTermsMustMatch(t *testing.T) {
	if got := ids(Search(searchFixture, "slack channel")); len(got) != 1 || got[0] != "slack" {
		t.Errorf("Search(slack channel) = %v, want [slack]", got)
	}
	if got := ids(Search(searchFixture, "slack worktree")); len(got) != 0 {
		t.Errorf("Search(slack worktree) = %v, want no hits (no entry satisfies both)", got)
	}
}

func TestSearch_MatchesTagsCategoriesAndProvides(t *testing.T) {
	if got := ids(Search(searchFixture, "webhook")); len(got) != 1 || got[0] != "slack" {
		t.Errorf("tag search = %v, want [slack]", got)
	}
	if got := ids(Search(searchFixture, "workflow")); len(got) != 1 || got[0] != "plan-code-review" {
		t.Errorf("provides search = %v, want [plan-code-review]", got)
	}
	if got := len(Search(searchFixture, "notifications")); got != 2 {
		t.Errorf("category search returned %d entries, want 2", got)
	}
}

// An initialism should still find a long hyphenated handle.
func TestSearch_Subsequence(t *testing.T) {
	if got := ids(Search(searchFixture, "pcr")); len(got) != 1 || got[0] != "plan-code-review" {
		t.Errorf("Search(pcr) = %v, want [plan-code-review]", got)
	}
}

func TestSearch_EmptyQueryReturnsEverything(t *testing.T) {
	if got := len(Search(searchFixture, "  ")); got != len(searchFixture) {
		t.Errorf("empty query returned %d entries, want all %d", got, len(searchFixture))
	}
}

func TestDidYouMean(t *testing.T) {
	got := DidYouMean(searchFixture, "slak", 3)
	found := false
	for _, id := range got {
		if id == "slack" {
			found = true
		}
	}
	if !found {
		t.Errorf("DidYouMean(slak) = %v, want it to include slack", got)
	}
	if len(DidYouMean(searchFixture, "zzzzzz", 3)) != 0 {
		t.Error("DidYouMean should stay quiet when nothing is close")
	}
}
