package executor

import (
	"testing"

	"github.com/bborn/workflow/internal/github"
)

func TestShouldPromoteReviewTask(t *testing.T) {
	cases := []struct {
		name string
		info *github.PRInfo
		want bool
	}{
		{"nil (no PR / lookup failed)", nil, false},
		{"open PR still awaiting review", &github.PRInfo{State: github.PRStateOpen}, false},
		{"draft PR", &github.PRInfo{State: github.PRStateDraft}, false},
		{"merged PR", &github.PRInfo{State: github.PRStateMerged}, true},
		{"closed PR (abandoned counts as done)", &github.PRInfo{State: github.PRStateClosed}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldPromoteReviewTask(c.info); got != c.want {
				t.Fatalf("shouldPromoteReviewTask(%+v) = %v, want %v", c.info, got, c.want)
			}
		})
	}
}
