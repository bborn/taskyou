package ui

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func hostTask(id int64, title, host string) *db.Task {
	return &db.Task{ID: id, Title: title, Status: db.StatusProcessing, PlacementTarget: host}
}

func TestParseFilterHosts(t *testing.T) {
	cases := []struct {
		query     string
		wantHosts []string
		wantRest  string
	}{
		{"", nil, ""},
		{"rate limiting", nil, "rate limiting"},
		{"@mona", []string{"mona"}, ""},
		{"@Mona", []string{"mona"}, ""},
		// Several hosts are an OR, and the token never reaches the keyword.
		{"@mona @bruce", []string{"mona", "bruce"}, ""},
		{"@mona flaky test", []string{"mona"}, "flaky test"},
		{"flaky test @mona", []string{"mona"}, "flaky test"},
		// A bare "@" is the chip mid-keystroke: every placed task.
		{"@", []string{""}, ""},
		// Project chips and addresses inside a keyword must survive untouched.
		{"[offerlab] @mona bump", []string{"mona"}, "[offerlab] bump"},
		{"user@example.com", nil, "user@example.com"},
	}
	for _, c := range cases {
		hosts, rest := parseFilterHosts(c.query)
		if strings.Join(hosts, ",") != strings.Join(c.wantHosts, ",") || rest != c.wantRest {
			t.Errorf("parseFilterHosts(%q) = (%v,%q), want (%v,%q)", c.query, hosts, rest, c.wantHosts, c.wantRest)
		}
	}
}

func TestFilterTasksByHost(t *testing.T) {
	mona := hostTask(1, "on mona", "mona")
	bruce := hostTask(2, "on bruce", "bruce")
	fqdn := hostTask(3, "on mona.local", "mona.local")
	local := hostTask(4, "ran here", "")

	all := []*db.Task{mona, bruce, fqdn, local}

	ids := func(tasks []*db.Task) string {
		var b strings.Builder
		for _, t := range tasks {
			b.WriteString(string(rune('0' + t.ID)))
		}
		return b.String()
	}

	cases := []struct {
		name      string
		hosts     []string
		localHost string
		want      string
	}{
		{"no host token is a no-op", nil, "spark", "1234"},
		{"one host", []string{"mona"}, "spark", "13"},
		{"several hosts are an OR", []string{"mona", "bruce"}, "spark", "123"},
		{"unknown host matches nothing", []string{"nowhere"}, "spark", ""},
		{"local names the tasks that ran here", []string{"local"}, "spark", "4"},
		{"here and localhost are the same thing", []string{"here"}, "spark", "4"},
		// The local machine is a host like any other: on mona, "@mona" has to find
		// the tasks that ran here, which carry no placement target at all.
		{"this machine answers to its own name", []string{"mona"}, "mona", "134"},
		{"bare @ is every placed task", []string{""}, "spark", "123"},
		// Narrowing while the name is still being typed.
		{"prefix matches", []string{"mon"}, "spark", "13"},
	}
	for _, c := range cases {
		if got := ids(filterTasksByHost(all, c.hosts, c.localHost)); got != c.want {
			t.Errorf("%s: filterTasksByHost(%v, %q) = %q, want %q", c.name, c.hosts, c.localHost, got, c.want)
		}
	}
}

// The whole point: typing "@mona" on the board leaves only mona's tasks.
func TestApplyFilterByHost(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	mona := hostTask(0, "Fix the flaky suite", "mona")
	bruce := hostTask(0, "Fix the flaky suite too", "bruce")
	for _, task := range []*db.Task{mona, bruce} {
		task.Project = "personal"
		if err := database.CreateTask(task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		if err := database.SetTaskPlacement(task.ID, task.PlacementTarget, "test"); err != nil {
			t.Fatalf("set placement: %v", err)
		}
	}

	m := &AppModel{db: database, kanban: NewKanbanBoard(0, 0)}
	m.tasks = []*db.Task{mona, bruce}

	m.filterText = "@mona"
	m.finishBoardFilter(m.applyFilter()().(boardFilterMsg))
	if !kanbanHasTask(m.kanban, mona.ID) || kanbanHasTask(m.kanban, bruce.ID) {
		t.Errorf("@mona should show only mona's task (mona=%v bruce=%v)",
			kanbanHasTask(m.kanban, mona.ID), kanbanHasTask(m.kanban, bruce.ID))
	}

	// The host token must not pollute the keyword search: "flaky" still matches
	// both tasks, and the host is what narrows them to one.
	m.filterText = "@bruce flaky"
	m.finishBoardFilter(m.applyFilter()().(boardFilterMsg))
	if !kanbanHasTask(m.kanban, bruce.ID) || kanbanHasTask(m.kanban, mona.ID) {
		t.Errorf("@bruce flaky should show only bruce's task (bruce=%v mona=%v)",
			kanbanHasTask(m.kanban, bruce.ID), kanbanHasTask(m.kanban, mona.ID))
	}
}

func TestOpenHostToken(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", -1},
		{"@", 0},
		{"@mo", 0},
		{"flaky @mo", 6},
		// Finished chip: a space closed it, so there is nothing to complete.
		{"@mona ", -1},
		{"@mona flaky", -1},
		// Not a chip at all.
		{"user@example.com", -1},
	}
	for _, c := range cases {
		if got := openHostToken(c.text); got != c.want {
			t.Errorf("openHostToken(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}
