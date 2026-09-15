package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

// hostsTestDB opens an empty database with one project, and points the plugin
// loader at a temp dir so no test can see the developer's own plugins.
func hostsTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.CreateProject(&db.Project{Name: "taskyou", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TY_PLUGINS_DIR", t.TempDir())
	return database
}

// installHostsPlugin writes a task.hosts handler that answers with body.
func installHostsPlugin(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ty-on")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: ty-on\nhooks:\n  task.hosts: hosts.sh\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat <<'JSON'\n" + body + "\nJSON\n"
	if err := os.WriteFile(filepath.Join(dir, "hosts.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TY_PLUGINS_DIR", filepath.Dir(dir))
}

func newTask(t *testing.T, database *db.DB, executorName string) *db.Task {
	t.Helper()
	task := &db.Task{Title: "a task", Project: "taskyou", Executor: executorName}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPlacementChoicesListsWhatThePluginOffers(t *testing.T) {
	database := hostsTestDB(t)
	installHostsPlugin(t, `{"hosts":[{"name":"mona","target":"mona","workdir":"~/Projects/taskyou"}]}`)

	got := PlacementChoices(context.Background(), database, "taskyou", "claude")

	if len(got) != 1 || got[0].Target != "mona" {
		t.Fatalf("PlacementChoices() = %+v, want the one offered host", got)
	}
	if got[0].WorkDir != "~/Projects/taskyou" {
		t.Errorf("workdir = %q, want the project's checkout on that host", got[0].WorkDir)
	}
}

// A choice that cannot be honoured is not a choice: the remote adapters only
// cover Claude and Codex, so no picker is offered for the other executors, and
// a project nobody named has nothing to match a fleet against.
func TestPlacementChoicesOffersNothingItCannotHonour(t *testing.T) {
	database := hostsTestDB(t)
	installHostsPlugin(t, `{"hosts":[{"name":"mona"}]}`)

	if got := PlacementChoices(context.Background(), database, "taskyou", "gemini"); len(got) != 0 {
		t.Errorf("PlacementChoices(gemini) = %+v, want none: it cannot be launched remotely", got)
	}
	if got := PlacementChoices(context.Background(), database, "", "claude"); len(got) != 0 {
		t.Errorf("PlacementChoices(no project) = %+v, want none", got)
	}
}

// The plain case: pick a host in the form, and the task is placed there before
// it has ever spawned, so the resolver is never asked.
func TestChoosePlacementRecordsTheChosenHost(t *testing.T) {
	database := hostsTestDB(t)
	installHostsPlugin(t, `{"hosts":[{"name":"mona","target":"mona","workdir":"~/Projects/taskyou"}]}`)
	task := newTask(t, database, "claude")

	if err := ChoosePlacement(context.Background(), database, task, "mona", ""); err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}

	got, err := database.GetTaskPlacementDecision(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Decided || got.Target != "mona" {
		t.Fatalf("placement = %+v, want a decided placement on mona", got)
	}
	// The directory was not typed in the form: it comes from the same plugin
	// that offered the host, so picking a name is the whole interaction.
	if got.WorkDir != "~/Projects/taskyou" {
		t.Errorf("workdir = %q, want the checkout the host list named", got.WorkDir)
	}
	if got.Reason != PlacementChoiceReason {
		t.Errorf("reason = %q, want it to say a person chose this", got.Reason)
	}
}

// "Automatic" is the default and must write nothing at all: a recorded decision
// — even an empty one — would stop the resolver from ever being asked.
func TestChoosePlacementAutomaticDecidesNothing(t *testing.T) {
	database := hostsTestDB(t)
	task := newTask(t, database, "claude")

	for _, target := range []string{"", "  ", "auto"} {
		if err := ChoosePlacement(context.Background(), database, task, target, ""); err != nil {
			t.Fatalf("ChoosePlacement(%q): %v", target, err)
		}
		got, err := database.GetTaskPlacementDecision(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Decided {
			t.Errorf("ChoosePlacement(%q) decided %+v, want the resolver left to answer", target, got)
		}
	}
}

// "This machine" is a decision, not the absence of one: it pins the task here,
// so a resolver installed later does not quietly ship it off.
func TestChoosePlacementLocalIsADecision(t *testing.T) {
	database := hostsTestDB(t)
	task := newTask(t, database, "claude")

	for _, target := range []string{"local", "here", "localhost", "Local"} {
		if err := database.ClearTaskPlacement(task.ID); err != nil {
			t.Fatal(err)
		}
		if err := ChoosePlacement(context.Background(), database, task, target, ""); err != nil {
			t.Fatalf("ChoosePlacement(%q): %v", target, err)
		}
		got, err := database.GetTaskPlacementDecision(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Decided || got.Target != "" {
			t.Errorf("ChoosePlacement(%q) = %+v, want a decided local placement", target, got)
		}
	}
}

// A host that nothing can tell us the directory of is refused rather than
// recorded: a placement with no workdir fails at spawn, hours later.
func TestChoosePlacementNeedsADirectoryForARemoteHost(t *testing.T) {
	database := hostsTestDB(t)
	task := newTask(t, database, "claude")

	err := ChoosePlacement(context.Background(), database, task, "unknown-host", "")
	if err == nil {
		t.Fatal("ChoosePlacement() = nil, want an error naming the missing directory")
	}
	got, _ := database.GetTaskPlacementDecision(task.ID)
	if got.Decided {
		t.Errorf("placement = %+v, want nothing recorded for a refused choice", got)
	}
}

// A directory given by hand is used as-is, so a host ty has never heard of can
// still be chosen.
func TestChoosePlacementAcceptsADirectoryGivenByHand(t *testing.T) {
	database := hostsTestDB(t)
	task := newTask(t, database, "claude")

	if err := ChoosePlacement(context.Background(), database, task, "new-box", "/srv/taskyou"); err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}

	got, _ := database.GetTaskPlacementDecision(task.ID)
	if got.Target != "new-box" || got.WorkDir != "/srv/taskyou" {
		t.Errorf("placement = %+v, want the host and directory that were given", got)
	}
}

// An executor that cannot be launched over SSH is refused with the reason,
// rather than being recorded and failing at spawn.
func TestChoosePlacementRefusesAnExecutorThatCannotRunRemotely(t *testing.T) {
	database := hostsTestDB(t)
	task := newTask(t, database, "gemini")

	err := ChoosePlacement(context.Background(), database, task, "mona", "/srv/taskyou")
	if err == nil {
		t.Fatal("ChoosePlacement() = nil, want gemini refused")
	}
	got, _ := database.GetTaskPlacementDecision(task.ID)
	if got.Decided {
		t.Errorf("placement = %+v, want nothing recorded", got)
	}
}

// A host can be named by the label a person reads rather than by its ssh
// destination — `ty create --host mona` when the fleet reaches it at
// mona.example. Recording the label would fail at launch, so the destination is
// what gets written down.
func TestChoosePlacementRecordsTheDestinationBehindAName(t *testing.T) {
	database := hostsTestDB(t)
	installHostsPlugin(t, `{"hosts":[{"name":"mona","target":"mona.example","workdir":"~/Projects/taskyou"}]}`)
	task := newTask(t, database, "claude")

	if err := ChoosePlacement(context.Background(), database, task, "mona", ""); err != nil {
		t.Fatalf("ChoosePlacement: %v", err)
	}

	got, _ := database.GetTaskPlacementDecision(task.ID)
	if got.Target != "mona.example" {
		t.Fatalf("target = %q, want the ssh destination the name stands for", got.Target)
	}
}
