package taskfilter

import (
	"reflect"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestParseStatusTokens(t *testing.T) {
	cases := []struct {
		query        string
		wantStatuses []string
		wantKeyword  string
	}{
		{"", nil, ""},
		{"rate limiting", nil, "rate limiting"},
		{"status:blocked", []string{db.StatusBlocked}, ""},
		{"is:blocked", []string{db.StatusBlocked}, ""},
		{"STATUS:Blocked", []string{db.StatusBlocked}, ""},
		// The headline case from the feature request: in progress + blocked.
		{
			"status:in-progress status:blocked",
			[]string{db.StatusQueued, db.StatusProcessing, db.StatusBlocked},
			"",
		},
		// Repeating a token must not duplicate statuses.
		{"is:active status:queued", []string{db.StatusQueued, db.StatusProcessing}, ""},
		// A token must be stripped from the keyword, or it pollutes fuzzy scoring.
		{"status:done lumen", []string{db.StatusDone}, "lumen"},
		{"lumen status:done", []string{db.StatusDone}, "lumen"},
		// An unknown status is not a token: it stays searchable text rather than
		// silently matching nothing.
		{"status:nonsense", nil, "status:nonsense"},
		// A bare colon-less word is never a token.
		{"status", nil, "status"},
	}
	for _, c := range cases {
		q := Parse(c.query)
		if !reflect.DeepEqual(q.Statuses, c.wantStatuses) {
			t.Errorf("Parse(%q).Statuses = %v, want %v", c.query, q.Statuses, c.wantStatuses)
		}
		if q.Keyword != c.wantKeyword {
			t.Errorf("Parse(%q).Keyword = %q, want %q", c.query, q.Keyword, c.wantKeyword)
		}
	}
}

func TestParseKindPinnedAndPR(t *testing.T) {
	for _, c := range []struct {
		query string
		check func(Query) bool
	}{
		{"is:workflow", func(q Query) bool { return q.Kind == KindWorkflow }},
		{"is:wf", func(q Query) bool { return q.Kind == KindWorkflow }},
		{"is:pipeline", func(q Query) bool { return q.Kind == KindWorkflow }},
		{"is:task", func(q Query) bool { return q.Kind == KindTask }},
		{"is:normal", func(q Query) bool { return q.Kind == KindTask }},
		{"is:pinned", func(q Query) bool { return q.Pinned != nil && *q.Pinned }},
		{"is:unpinned", func(q Query) bool { return q.Pinned != nil && !*q.Pinned }},
		{"has:pr", func(q Query) bool { return q.HasPR != nil && *q.HasPR }},
		{"no:pr", func(q Query) bool { return q.HasPR != nil && !*q.HasPR }},
		{"tag:release", func(q Query) bool { return reflect.DeepEqual(q.Tags, []string{"release"}) }},
	} {
		q := Parse(c.query)
		if !c.check(q) {
			t.Errorf("Parse(%q) did not set the expected predicate: %+v", c.query, q)
		}
		if q.Keyword != "" {
			t.Errorf("Parse(%q) left %q in the keyword", c.query, q.Keyword)
		}
	}
}

func TestParseProjects(t *testing.T) {
	q := Parse("[offerlab] [InfluenceKit] bump deps")
	if !reflect.DeepEqual(q.Projects, []string{"offerlab", "influencekit"}) {
		t.Errorf("projects = %v", q.Projects)
	}
	if q.Keyword != "bump deps" {
		t.Errorf("keyword = %q", q.Keyword)
	}

	// A half-typed tag is reported separately so autocomplete can offer names,
	// and must not become a completed project filter.
	partial := Parse("[off")
	if partial.PartialProject != "off" || len(partial.Projects) != 0 {
		t.Errorf("partial = %+v", partial)
	}

	// Project tags survive alongside structured tokens.
	mixed := Parse("is:task [offerlab] bump")
	if mixed.Kind != KindTask || len(mixed.Projects) != 1 || mixed.Keyword != "bump" {
		t.Errorf("mixed = %+v", mixed)
	}
}

func TestIsZeroAndHasStructured(t *testing.T) {
	if !Parse("").IsZero() {
		t.Error("empty query should be zero")
	}
	if Parse("hello").IsZero() {
		t.Error("keyword query is not zero")
	}
	if Parse("hello").HasStructured() {
		t.Error("a bare keyword sets no structured predicate")
	}
	if !Parse("is:pinned").HasStructured() {
		t.Error("is:pinned is structured")
	}
}

func wfStep(id int64, status string) *db.Task {
	return &db.Task{ID: id, Title: "[plan] goal", Status: status, Tags: "pipeline", SourceBranch: "pipeline/1-a"}
}

func TestMatch(t *testing.T) {
	blocked := &db.Task{ID: 1, Status: db.StatusBlocked, Project: "offerlab"}
	running := &db.Task{ID: 2, Status: db.StatusProcessing, Pinned: true}
	donePR := &db.Task{ID: 3, Status: db.StatusDone, PRNumber: 42, Tags: "release,infra"}
	step := wfStep(4, db.StatusQueued)

	cases := []struct {
		query string
		task  *db.Task
		want  bool
	}{
		{"", blocked, true},
		{"status:in-progress status:blocked", blocked, true},
		{"status:in-progress status:blocked", running, true},
		{"status:in-progress status:blocked", donePR, false},
		{"is:pinned", running, true},
		{"is:pinned", blocked, false},
		{"is:unpinned", blocked, true},
		{"has:pr", donePR, true},
		{"has:pr", blocked, false},
		{"no:pr", blocked, true},
		{"tag:release", donePR, true},
		{"tag:release", blocked, false},
		// Two tags narrow: the task must carry both.
		{"tag:release tag:infra", donePR, true},
		{"tag:release tag:missing", donePR, false},
		{"is:workflow", step, true},
		{"is:workflow", blocked, false},
		{"is:task", blocked, true},
		{"is:task", step, false},
		// Predicates combine with AND.
		{"is:pinned status:blocked", running, false},
		// A nil task never matches, so callers can pass raw slice entries.
		{"", nil, false},
	}
	for _, c := range cases {
		if got := Parse(c.query).Match(c.task); got != c.want {
			t.Errorf("Parse(%q).Match(task %v) = %v, want %v", c.query, c.task, got, c.want)
		}
	}
}

// A workflow-tagged task with no shared branch isn't a workflow member, so it
// must fall on the "task" side rather than vanishing from both filters.
func TestKindTreatsBranchlessTaggedTaskAsPlain(t *testing.T) {
	odd := &db.Task{ID: 9, Title: "tagged but no branch", Tags: "pipeline"}
	if Parse("is:workflow").Match(odd) {
		t.Error("branchless task should not count as a workflow step")
	}
	if !Parse("is:task").Match(odd) {
		t.Error("branchless task should count as a plain task")
	}
}

func TestMatchProjectAndKeyword(t *testing.T) {
	task := &db.Task{ID: 77, Title: "Fix the retry banner", Project: "OfferLab", Body: "seen in staging"}

	if !Parse("[offerlab]").MatchProject(task) {
		t.Error("project match should be case-insensitive")
	}
	if Parse("[influencekit]").MatchProject(task) {
		t.Error("wrong project must not match")
	}
	if !Parse("").MatchProject(task) {
		t.Error("no project tag means every project matches")
	}

	if !Parse("retry").MatchKeyword(task) {
		t.Error("title substring should match")
	}
	if !Parse("staging").MatchKeyword(task) {
		t.Error("body substring should match")
	}
	if !Parse("#77").MatchKeyword(task) {
		t.Error("#id should match")
	}
	if Parse("nowhere").MatchKeyword(task) {
		t.Error("unrelated keyword must not match")
	}
}

func TestFilterKeepsOrder(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Status: db.StatusBlocked},
		{ID: 2, Status: db.StatusDone},
		{ID: 3, Status: db.StatusProcessing},
		{ID: 4, Status: db.StatusBlocked},
	}
	got := Parse("status:in-progress status:blocked").Filter(tasks)
	var ids []int64
	for _, task := range got {
		ids = append(ids, task.ID)
	}
	if !reflect.DeepEqual(ids, []int64{1, 3, 4}) {
		t.Errorf("Filter kept %v, want [1 3 4] in input order", ids)
	}
}

func TestResolveProjects(t *testing.T) {
	q := Parse("[ol] [unknown]").ResolveProjects(func(name string) string {
		if name == "ol" {
			return "OfferLab"
		}
		return ""
	})
	if !reflect.DeepEqual(q.Projects, []string{"offerlab", "unknown"}) {
		t.Errorf("resolved projects = %v", q.Projects)
	}

	// An unresolvable alias must keep filtering (to nothing) rather than
	// widening the view back to every project.
	if q.MatchProject(&db.Task{Project: "influencekit"}) {
		t.Error("unresolved project must not match an unrelated project")
	}
}
