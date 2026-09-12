package taskref

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// fakeStore mimics the real store: GetTask returns nil for a missing ID, and
// SearchTasks matches pr_url as a case-insensitive substring.
type fakeStore struct{ tasks []*db.Task }

func (f fakeStore) GetTask(id int64) (*db.Task, error) {
	for _, t := range f.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, nil
}

func (f fakeStore) SearchTasks(query string, limit int) ([]*db.Task, error) {
	var out []*db.Task
	for _, t := range f.tasks {
		if strings.Contains(strings.ToLower(t.PRURL), strings.ToLower(query)) {
			out = append(out, t)
		}
	}
	return out, nil
}

func TestResolve(t *testing.T) {
	store := fakeStore{tasks: []*db.Task{
		{ID: 5187, PRURL: "https://github.com/offerlab/offerlab/pull/3482"},
		{ID: 7, PRURL: "https://github.com/other/repo/pull/3482"},
		{ID: 5401, PRURL: "https://github.com/offerlab/offerlab/pull/3652"},
		{ID: 5402, PRURL: "https://github.com/offerlab/offerlab/pull/36520"},
		{ID: 8, PRURL: "https://github.com/offerlab/offerlab/pull/100"},
		{ID: 9, PRURL: "https://github.com/offerlab/offerlab/pull/100"},
	}}

	for _, tt := range []struct {
		ref  string
		want int64 // 0: no single task
	}{
		{"5187", 5187},
		{"#5187", 5187},
		{" #5187 ", 5187},
		{"task/5187-draft-offers", 5187},
		{"https://github.com/offerlab/offerlab/pull/3482", 5187},
		{"https://github.com/OfferLab/offerlab/pull/3482/files?w=1", 5187},
		{"github.com/offerlab/offerlab/pull/3652", 5401},
		{"https://github.com/offerlab/offerlab/pull/100", 0}, // two tasks share it
		{"https://github.com/offerlab/offerlab/pull/4", 0},
		{"99999", 0},
		{"draft offers", 0},
		{"fix 5187-thing", 0},
	} {
		got, err := Resolve(store, tt.ref)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", tt.ref, err)
		}
		var gotID int64
		if got != nil {
			gotID = got.ID
		}
		if gotID != tt.want {
			t.Errorf("Resolve(%q) = task %d, want %d", tt.ref, gotID, tt.want)
		}
	}
}

func TestSearchText(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Org/Repo/pull/12/files#r1": "github.com/org/repo/pull/12",
		"  draft offers ": "draft offers",
		"#5187":           "#5187",
	} {
		if got := SearchText(in); got != want {
			t.Errorf("SearchText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractors(t *testing.T) {
	if got := TaskIDFromBranch("task/1068-description"); got != 1068 {
		t.Errorf("TaskIDFromBranch = %d, want 1068", got)
	}
	if got := PRNumberFromURL("https://github.com/org/repo/pull/123"); got != 123 {
		t.Errorf("PRNumberFromURL = %d, want 123", got)
	}
}
