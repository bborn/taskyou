package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		name    string
		str     string
		pattern string
		want    bool
	}{
		{"empty pattern", "hello world", "", true},
		{"empty string", "", "abc", false},
		{"exact match", "hello", "hello", true},
		{"substring at start", "hello", "hel", true},
		{"substring in middle", "hello", "ell", true},
		{"non-contiguous chars", "hello world", "hwd", true},
		{"non-contiguous chars complex", "implement feature", "ipf", true},
		{"no match", "hello", "xyz", false},
		{"pattern longer than string", "hi", "hello", false},
		{"case sensitive no match", "Hello", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fuzzyMatch(tt.str, tt.pattern)
			if got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.str, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestFuzzyScore(t *testing.T) {
	tests := []struct {
		name        string
		str         string
		pattern     string
		shouldMatch bool
	}{
		{"empty pattern", "hello world", "", true},
		{"empty string", "", "abc", false},
		{"exact match", "hello", "hello", true},
		{"substring at start", "hello", "hel", true},
		{"substring in middle", "hello", "ell", true},
		{"non-contiguous chars", "hello world", "hwd", true},
		{"vscode-style dsno->diseno", "diseno website", "dsno", true},
		{"vscode-style dsnw->diseno website", "diseno website", "dsnw", true},
		{"case insensitive", "Hello World", "hw", true},
		{"case insensitive mixed", "HelloWorld", "helloworld", true},
		{"no match", "hello", "xyz", false},
		{"pattern longer than string", "hi", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := fuzzyScore(tt.str, tt.pattern)
			gotMatch := score >= 0
			if gotMatch != tt.shouldMatch {
				t.Errorf("fuzzyScore(%q, %q) = %d, shouldMatch = %v, got match = %v",
					tt.str, tt.pattern, score, tt.shouldMatch, gotMatch)
			}
		})
	}
}

func TestFuzzyScoreRanking(t *testing.T) {
	// Test that better matches get higher scores
	tests := []struct {
		name       string
		pattern    string
		betterStr  string
		worseStr   string
	}{
		{
			name:       "word boundary match beats random match",
			pattern:    "fb",
			betterStr:  "foo bar",      // matches at word boundaries
			worseStr:   "foooobar",     // matches randomly
		},
		{
			name:       "consecutive match beats scattered",
			pattern:    "hello",
			betterStr:  "hello world",       // consecutive match at start
			worseStr:   "something hxexlxlxo", // scattered 'h', 'e', 'l', 'l', 'o' in word
		},
		{
			name:       "start of string match beats middle",
			pattern:    "foo",
			betterStr:  "foo bar baz",  // matches at start
			worseStr:   "bar foo baz",  // matches in middle
		},
		{
			name:       "camelCase boundary match is good",
			pattern:    "gU",
			betterStr:  "getUser",      // matches at camelCase boundary
			worseStr:   "configure",    // 'g' then 'u' scattered
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			betterScore := fuzzyScore(tt.betterStr, tt.pattern)
			worseScore := fuzzyScore(tt.worseStr, tt.pattern)
			if betterScore <= worseScore {
				t.Errorf("Expected %q (score=%d) to rank higher than %q (score=%d) for pattern %q",
					tt.betterStr, betterScore, tt.worseStr, worseScore, tt.pattern)
			}
		})
	}
}

func TestCommandPaletteFiltering(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Implement login feature", Project: "webapp", Status: db.StatusBacklog},
		{ID: 2, Title: "Fix bug in dashboard", Project: "webapp", Status: db.StatusProcessing},
		{ID: 3, Title: "Add unit tests", Project: "api", Status: db.StatusDone},
		{ID: 42, Title: "Command palette", Project: "workflow", Status: db.StatusQueued},
	}

	tests := []struct {
		name     string
		query    string
		expected int // number of expected results
	}{
		{"empty query shows all", "", 4},
		{"filter by ID", "42", 1},
		{"filter by ID with hash", "#42", 1},
		{"filter by title word", "bug", 1},
		{"filter by project", "webapp", 2},
		{"filter by status", "done", 1},
		{"fuzzy match", "ilf", 1}, // "Implement login feature"
		{"no results", "nonexistent", 0},
		{"partial ID match", "4", 1}, // matches only ID 42 (contains "4")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal command palette model for testing
			m := &CommandPaletteModel{
				allTasks: tasks,
			}
			m.searchInput.SetValue(tt.query)
			m.filterTasks()

			if len(m.filteredTasks) != tt.expected {
				t.Errorf("query %q: got %d results, want %d", tt.query, len(m.filteredTasks), tt.expected)
			}
		})
	}
}

func TestMatchesQuery(t *testing.T) {
	task := &db.Task{
		ID:      123,
		Title:   "Implement search feature",
		Project: "myproject",
		Status:  db.StatusBacklog,
	}

	m := &CommandPaletteModel{}

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"match by ID", "123", true},
		{"match by ID with hash", "#123", true},
		{"partial ID", "12", true},
		{"match by title", "search", true},
		{"match by project", "myproject", true},
		{"match by status", "backlog", true},
		{"fuzzy match title", "isf", true}, // "Implement search feature"
		{"no match", "xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.matchesQuery(task, tt.query)
			if got != tt.want {
				t.Errorf("matchesQuery(%+v, %q) = %v, want %v", task, tt.query, got, tt.want)
			}
		})
	}
}

func TestStatusPriority(t *testing.T) {
	// Test that status priorities are correctly ordered
	tests := []struct {
		name   string
		higher string
		lower  string
	}{
		{"processing before backlog", db.StatusProcessing, db.StatusBacklog},
		{"blocked before backlog", db.StatusBlocked, db.StatusBacklog},
		{"processing before blocked", db.StatusProcessing, db.StatusBlocked},
		{"queued before backlog", db.StatusQueued, db.StatusBacklog},
		{"backlog before done", db.StatusBacklog, db.StatusDone},
		{"done before archived", db.StatusDone, db.StatusArchived},
		{"processing before done", db.StatusProcessing, db.StatusDone},
		{"blocked before done", db.StatusBlocked, db.StatusDone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if statusPriority(tt.higher) >= statusPriority(tt.lower) {
				t.Errorf("Expected %q (priority=%d) to have higher priority than %q (priority=%d)",
					tt.higher, statusPriority(tt.higher), tt.lower, statusPriority(tt.lower))
			}
		})
	}
}

func TestFilterTasksStatusOrdering(t *testing.T) {
	// Test that filtered tasks are ordered by status priority
	tasks := []*db.Task{
		{ID: 1, Title: "dog task backlog", Status: db.StatusBacklog},
		{ID: 2, Title: "dog task done", Status: db.StatusDone},
		{ID: 3, Title: "dog task processing", Status: db.StatusProcessing},
		{ID: 4, Title: "dog task blocked", Status: db.StatusBlocked},
	}

	m := &CommandPaletteModel{
		allTasks: tasks,
	}
	m.searchInput.SetValue("dog")
	m.filterTasks()

	// Verify we got all 4 matching tasks
	if len(m.filteredTasks) != 4 {
		t.Fatalf("Expected 4 results, got %d", len(m.filteredTasks))
	}

	// First should be processing (highest priority)
	if m.filteredTasks[0].Status != db.StatusProcessing {
		t.Errorf("First task should be processing, got %q", m.filteredTasks[0].Status)
	}

	// Second should be blocked
	if m.filteredTasks[1].Status != db.StatusBlocked {
		t.Errorf("Second task should be blocked, got %q", m.filteredTasks[1].Status)
	}

	// Third should be backlog
	if m.filteredTasks[2].Status != db.StatusBacklog {
		t.Errorf("Third task should be backlog, got %q", m.filteredTasks[2].Status)
	}

	// Fourth should be done
	if m.filteredTasks[3].Status != db.StatusDone {
		t.Errorf("Fourth task should be done, got %q", m.filteredTasks[3].Status)
	}
}

func TestFilterTasksStatusOrderingWithQueued(t *testing.T) {
	// Test that queued tasks appear after blocked but before backlog
	tasks := []*db.Task{
		{ID: 1, Title: "cat task backlog", Status: db.StatusBacklog},
		{ID: 2, Title: "cat task queued", Status: db.StatusQueued},
		{ID: 3, Title: "cat task blocked", Status: db.StatusBlocked},
		{ID: 4, Title: "cat task done", Status: db.StatusDone},
		{ID: 5, Title: "cat task processing", Status: db.StatusProcessing},
	}

	m := &CommandPaletteModel{
		allTasks: tasks,
	}
	m.searchInput.SetValue("cat")
	m.filterTasks()

	// Verify ordering: processing > blocked > queued > backlog > done
	expectedOrder := []string{
		db.StatusProcessing,
		db.StatusBlocked,
		db.StatusQueued,
		db.StatusBacklog,
		db.StatusDone,
	}

	if len(m.filteredTasks) != len(expectedOrder) {
		t.Fatalf("Expected %d results, got %d", len(expectedOrder), len(m.filteredTasks))
	}

	for i, expected := range expectedOrder {
		if m.filteredTasks[i].Status != expected {
			t.Errorf("Position %d: expected %q, got %q", i, expected, m.filteredTasks[i].Status)
		}
	}
}

func TestFilterTasksScoreWithinSameStatus(t *testing.T) {
	// Test that within the same status, tasks are sorted by fuzzy score
	tasks := []*db.Task{
		{ID: 1, Title: "unrelated dog", Status: db.StatusProcessing},       // "dog" matches later
		{ID: 2, Title: "dog at the start", Status: db.StatusProcessing},    // "dog" matches at start
		{ID: 3, Title: "big dog handler", Status: db.StatusProcessing},     // "dog" matches in middle
	}

	m := &CommandPaletteModel{
		allTasks: tasks,
	}
	m.searchInput.SetValue("dog")
	m.filterTasks()

	if len(m.filteredTasks) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(m.filteredTasks))
	}

	// "dog at the start" should be first (best match - word boundary at start)
	if m.filteredTasks[0].ID != 2 {
		t.Errorf("Expected task 2 (dog at the start) first, got task %d (%s)",
			m.filteredTasks[0].ID, m.filteredTasks[0].Title)
	}
}

func TestMatchesQueryPRSearch(t *testing.T) {
	taskWithPR := &db.Task{
		ID:       42,
		Title:    "Fix authentication bug",
		Project:  "webapp",
		Status:   db.StatusProcessing,
		PRURL:    "https://github.com/offerlab/offerlab/pull/2382",
		PRNumber: 2382,
	}

	taskWithoutPR := &db.Task{
		ID:      43,
		Title:   "Add feature",
		Project: "webapp",
		Status:  db.StatusBacklog,
	}

	m := &CommandPaletteModel{}

	tests := []struct {
		name  string
		task  *db.Task
		query string
		want  bool
	}{
		// Task with PR - should match
		{"match by PR number", taskWithPR, "2382", true},
		{"match by PR number with hash", taskWithPR, "#2382", true},
		{"match by partial PR number", taskWithPR, "238", true},
		{"match by PR URL full", taskWithPR, "https://github.com/offerlab/offerlab/pull/2382", true},
		{"match by PR URL partial", taskWithPR, "offerlab/pull/2382", true},
		{"match by PR URL path", taskWithPR, "pull/2382", true},
		{"match by PR URL repo", taskWithPR, "offerlab", true},
		{"match by PR URL github", taskWithPR, "github.com", true},

		// Task without PR - should not match PR-specific queries
		{"no PR number match when no PR", taskWithoutPR, "2382", false},
		{"no PR URL match when no PR", taskWithoutPR, "pull/", false},

		// Task with PR - should still match other fields
		{"match by task ID", taskWithPR, "42", true},
		{"match by title", taskWithPR, "auth", true},
		{"match by project", taskWithPR, "webapp", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.matchesQuery(tt.task, tt.query)
			if got != tt.want {
				t.Errorf("matchesQuery(%+v, %q) = %v, want %v", tt.task, tt.query, got, tt.want)
			}
		})
	}
}

func TestFilterTasksSortsByLastAccessedWhenNoQuery(t *testing.T) {
	// Create test tasks with different last_accessed_at times
	now := time.Now()
	oldAccess := db.LocalTime{Time: now.Add(-2 * time.Hour)}
	recentAccess := db.LocalTime{Time: now.Add(-1 * time.Hour)}
	mostRecentAccess := db.LocalTime{Time: now.Add(-30 * time.Minute)}

	tasks := []*db.Task{
		{ID: 1, Title: "Old task", CreatedAt: db.LocalTime{Time: now.Add(-3 * time.Hour)}, LastAccessedAt: &oldAccess},
		{ID: 2, Title: "Recent task", CreatedAt: db.LocalTime{Time: now.Add(-2 * time.Hour)}, LastAccessedAt: &recentAccess},
		{ID: 3, Title: "Most recent task", CreatedAt: db.LocalTime{Time: now.Add(-1 * time.Hour)}, LastAccessedAt: &mostRecentAccess},
		{ID: 4, Title: "Never accessed", CreatedAt: db.LocalTime{Time: now.Add(-30 * time.Minute)}, LastAccessedAt: nil},
	}

	m := &CommandPaletteModel{
		allTasks: tasks,
	}
	// Empty query - should sort by last_accessed_at
	m.searchInput.SetValue("")
	m.filterTasks()

	if len(m.filteredTasks) != 4 {
		t.Fatalf("Expected 4 tasks, got %d", len(m.filteredTasks))
	}

	// First should be most recently accessed (ID 3)
	if m.filteredTasks[0].ID != 3 {
		t.Errorf("First task should be ID 3 (most recently accessed), got ID %d", m.filteredTasks[0].ID)
	}

	// Second should be recently accessed (ID 2)
	if m.filteredTasks[1].ID != 2 {
		t.Errorf("Second task should be ID 2 (recently accessed), got ID %d", m.filteredTasks[1].ID)
	}

	// Third should be old accessed (ID 1)
	if m.filteredTasks[2].ID != 1 {
		t.Errorf("Third task should be ID 1 (old accessed), got ID %d", m.filteredTasks[2].ID)
	}

	// Fourth should be never accessed (ID 4) - uses created_at as fallback
	if m.filteredTasks[3].ID != 4 {
		t.Errorf("Fourth task should be ID 4 (never accessed), got ID %d", m.filteredTasks[3].ID)
	}
}

func TestFilterTasksNeverAccessedSortsByCreatedAt(t *testing.T) {
	// Test that tasks that have never been accessed are sorted by created_at
	now := time.Now()

	tasks := []*db.Task{
		{ID: 1, Title: "Oldest", CreatedAt: db.LocalTime{Time: now.Add(-3 * time.Hour)}, LastAccessedAt: nil},
		{ID: 2, Title: "Middle", CreatedAt: db.LocalTime{Time: now.Add(-2 * time.Hour)}, LastAccessedAt: nil},
		{ID: 3, Title: "Newest", CreatedAt: db.LocalTime{Time: now.Add(-1 * time.Hour)}, LastAccessedAt: nil},
	}

	m := &CommandPaletteModel{
		allTasks: tasks,
	}
	// Empty query - should sort by created_at (newest first) when no access times
	m.searchInput.SetValue("")
	m.filterTasks()

	if len(m.filteredTasks) != 3 {
		t.Fatalf("Expected 3 tasks, got %d", len(m.filteredTasks))
	}

	// Should be sorted by created_at descending (newest first)
	if m.filteredTasks[0].ID != 3 {
		t.Errorf("First task should be ID 3 (newest created), got ID %d", m.filteredTasks[0].ID)
	}
	if m.filteredTasks[1].ID != 2 {
		t.Errorf("Second task should be ID 2 (middle created), got ID %d", m.filteredTasks[1].ID)
	}
	if m.filteredTasks[2].ID != 1 {
		t.Errorf("Third task should be ID 1 (oldest created), got ID %d", m.filteredTasks[2].ID)
	}
}

func TestFilterTasksAccessedBeforeNeverAccessed(t *testing.T) {
	// Test that accessed tasks always come before never-accessed tasks
	now := time.Now()
	oldAccess := db.LocalTime{Time: now.Add(-24 * time.Hour)} // Accessed long ago

	tasks := []*db.Task{
		// Never accessed but created very recently
		{ID: 1, Title: "Never accessed new", CreatedAt: db.LocalTime{Time: now.Add(-1 * time.Minute)}, LastAccessedAt: nil},
		// Accessed long ago
		{ID: 2, Title: "Accessed old", CreatedAt: db.LocalTime{Time: now.Add(-48 * time.Hour)}, LastAccessedAt: &oldAccess},
	}

	m := &CommandPaletteModel{
		allTasks: tasks,
	}
	// Empty query
	m.searchInput.SetValue("")
	m.filterTasks()

	if len(m.filteredTasks) != 2 {
		t.Fatalf("Expected 2 tasks, got %d", len(m.filteredTasks))
	}

	// Accessed task (even if old) should come before never-accessed task
	if m.filteredTasks[0].ID != 2 {
		t.Errorf("First task should be ID 2 (has been accessed), got ID %d", m.filteredTasks[0].ID)
	}
	if m.filteredTasks[1].ID != 1 {
		t.Errorf("Second task should be ID 1 (never accessed), got ID %d", m.filteredTasks[1].ID)
	}
}

func TestExtractTaskID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{"task branch with description", "task/1068-i-should-be-able-to-paste", 1068},
		{"task branch ID only", "task/1068", 1068},
		{"feature branch", "feature/42-add-login", 42},
		{"fix branch", "fix/999-bug-fix", 999},
		{"just ID with dash", "1068-description", 1068},
		{"no ID", "feature/no-id-here", 0},
		{"empty string", "", 0},
		{"plain number", "1068", 1068},
		{"nested path", "refs/heads/task/1068-description", 1068},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTaskID(tt.input)
			if got != tt.want {
				t.Errorf("extractTaskID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractPRNumber(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"full github URL", "https://github.com/offerlab/offerlab/pull/2382", 2382},
		{"github URL without https", "github.com/org/repo/pull/123", 123},
		{"github URL with www", "https://www.github.com/org/repo/pull/456", 456},
		{"not a PR URL", "https://github.com/org/repo/issues/789", 0},
		{"empty string", "", 0},
		{"random text", "some random text", 0},
		{"just a number", "123", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPRNumber(tt.input)
			if got != tt.want {
				t.Errorf("extractPRNumber(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestMatchesQueryBranchName(t *testing.T) {
	taskWithBranch := &db.Task{
		ID:         1068,
		Title:      "Enable paste in quick select",
		Project:    "workflow",
		Status:     db.StatusProcessing,
		BranchName: "task/1068-i-should-be-able-to-paste-in-a-git-branc",
	}

	taskWithSourceBranch := &db.Task{
		ID:           42,
		Title:        "Fix UI overflow",
		Project:      "webapp",
		Status:       db.StatusQueued,
		SourceBranch: "fix/ui-overflow",
	}

	m := &CommandPaletteModel{}

	tests := []struct {
		name  string
		task  *db.Task
		query string
		want  bool
	}{
		// Branch name matching
		{"match full branch name", taskWithBranch, "task/1068-i-should-be-able-to-paste-in-a-git-branc", true},
		{"match partial branch name", taskWithBranch, "task/1068", true},
		{"extract task ID from branch", taskWithBranch, "task/1068-some-other-text", true}, // extracts 1068
		{"match by branch substring", taskWithBranch, "paste-in-a-git", true},

		// Source branch matching
		{"match source branch", taskWithSourceBranch, "fix/ui-overflow", true},
		{"match source branch partial", taskWithSourceBranch, "ui-overflow", true},

		// Branch name with ID extraction for different task
		{"branch ID doesn't match other task", taskWithSourceBranch, "task/1068-description", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.matchesQuery(tt.task, tt.query)
			if got != tt.want {
				t.Errorf("matchesQuery(%+v, %q) = %v, want %v", tt.task, tt.query, got, tt.want)
			}
		})
	}
}

func TestScoreTaskBranchAndPR(t *testing.T) {
	taskWithBranch := &db.Task{
		ID:         1068,
		Title:      "Enable paste in quick select",
		Project:    "workflow",
		Status:     db.StatusProcessing,
		BranchName: "task/1068-i-should-be-able-to-paste-in-a-git-branc",
		PRNumber:   2382,
		PRURL:      "https://github.com/bborn/workflow/pull/2382",
	}

	otherTask := &db.Task{
		ID:      999,
		Title:   "Some other task",
		Project: "workflow",
		Status:  db.StatusBacklog,
	}

	m := &CommandPaletteModel{}

	tests := []struct {
		name      string
		task      *db.Task
		query     string
		wantScore int // minimum expected score (-1 means no match)
	}{
		// Branch name pasted - should extract ID and match
		{"pasted branch name extracts ID", taskWithBranch, "task/1068-i-should-be-able-to-paste-in-a-git-branc", 1000},
		{"pasted different branch with same ID", taskWithBranch, "task/1068-something-else", 1000},

		// GitHub PR URL pasted - should extract PR number and match
		{"pasted github PR URL", taskWithBranch, "https://github.com/bborn/workflow/pull/2382", 900},
		{"pasted github PR URL different repo", taskWithBranch, "https://github.com/other/repo/pull/2382", 900},

		// Branch name substring match
		{"branch name substring", taskWithBranch, "paste-in-a-git", 850},

		// Other task shouldn't match branch queries
		{"other task no branch match", otherTask, "task/1068-description", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := strings.ToLower(tt.query)
			got := m.scoreTask(tt.task, query)
			if tt.wantScore == -1 {
				if got >= 0 {
					t.Errorf("scoreTask(%+v, %q) = %d, expected no match (<0)", tt.task, tt.query, got)
				}
			} else if got < tt.wantScore {
				t.Errorf("scoreTask(%+v, %q) = %d, want >= %d", tt.task, tt.query, got, tt.wantScore)
			}
		})
	}
}

func TestFilterTasksFindsByBranchName(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Unrelated task", Status: db.StatusBacklog},
		{ID: 1068, Title: "Enable paste in quick select", Status: db.StatusProcessing,
			BranchName: "task/1068-i-should-be-able-to-paste-in-a-git-branc"},
		{ID: 3, Title: "Another task", Status: db.StatusQueued},
	}

	m := &CommandPaletteModel{allTasks: tasks}

	// Paste a branch name - should find the right task
	m.searchInput.SetValue("task/1068-i-should-be-able-to-paste-in-a-git-branc")
	m.filterTasks()

	if len(m.filteredTasks) == 0 {
		t.Fatal("Expected at least 1 result when pasting branch name")
	}
	if m.filteredTasks[0].ID != 1068 {
		t.Errorf("First result should be task 1068, got %d", m.filteredTasks[0].ID)
	}
}

func TestFilterTasksFindsByGitHubPRURL(t *testing.T) {
	tasks := []*db.Task{
		{ID: 1, Title: "Unrelated task", Status: db.StatusBacklog},
		{ID: 42, Title: "Fix auth bug", Status: db.StatusProcessing,
			PRNumber: 2382, PRURL: "https://github.com/offerlab/offerlab/pull/2382"},
		{ID: 3, Title: "Another task", Status: db.StatusQueued},
	}

	m := &CommandPaletteModel{allTasks: tasks}

	// Paste a GitHub PR URL - should find the right task
	m.searchInput.SetValue("https://github.com/offerlab/offerlab/pull/2382")
	m.filterTasks()

	if len(m.filteredTasks) == 0 {
		t.Fatal("Expected at least 1 result when pasting PR URL")
	}
	if m.filteredTasks[0].ID != 42 {
		t.Errorf("First result should be task 42, got %d", m.filteredTasks[0].ID)
	}
}
