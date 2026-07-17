package hooks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestLoadPlugins_ActionsValidatedAndKept(t *testing.T) {
	root := t.TempDir()
	manifest := "name: acty\n" +
		"actions:\n" +
		"  - id: greet\n" +
		"    label: Say hi\n" +
		"    command: greet.sh\n" +
		"  - id: broken\n" + // script missing -> dropped
		"    command: missing.sh\n" +
		"  - label: no-id\n" + // no id -> dropped
		"    command: greet.sh\n"
	writePlugin(t, root, "acty", manifest, map[string]string{"greet.sh": "#!/bin/sh\necho hi\n"})

	plugins, warnings := LoadPlugins(root)
	if len(plugins) != 1 {
		t.Fatalf("got %d plugins, want 1", len(plugins))
	}
	if len(plugins[0].Actions) != 1 || plugins[0].Actions[0].ID != "greet" {
		t.Fatalf("kept actions = %+v, want only 'greet'", plugins[0].Actions)
	}
	if plugins[0].Actions[0].DisplayLabel() != "Say hi" {
		t.Errorf("DisplayLabel = %q", plugins[0].Actions[0].DisplayLabel())
	}
	if len(warnings) != 1 {
		t.Errorf("want 1 warning about dropped actions, got %v", warnings)
	}
}

func TestLoadPlugins_ActionsOnlyPluginIsValid(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "actiononly",
		"name: actiononly\nactions:\n  - id: go\n    command: go.sh\n",
		map[string]string{"go.sh": "#!/bin/sh\n"})

	plugins, _ := LoadPlugins(root)
	if len(plugins) != 1 {
		t.Fatalf("actions-only plugin should load; got %d", len(plugins))
	}
	if len(plugins[0].Hooks) != 0 {
		t.Errorf("expected no hooks")
	}
}

func TestRunAction_InjectsTaskAndPluginEnv(t *testing.T) {
	root := t.TempDir()
	script := "#!/bin/sh\necho \"$TASK_PLUGIN_NAME|$TASK_ID|$TASK_TITLE\"\n"
	dir := writePlugin(t, root, "p", "name: p\nactions:\n  - id: a\n    command: a.sh\n",
		map[string]string{"a.sh": script})

	p := Plugin{Name: "p", Dir: dir, Actions: []Action{{ID: "a", Command: "a.sh"}}}
	task := &db.Task{ID: 7, Title: "hello"}

	out, err := RunAction(context.Background(), p, p.Actions[0], task)
	if err != nil {
		t.Fatalf("RunAction: %v (%s)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "p|7|hello" {
		t.Errorf("action output = %q, want %q", got, "p|7|hello")
	}
}

func TestRunAction_NilTaskOmitsTaskVars(t *testing.T) {
	root := t.TempDir()
	// set -u would fail on unset TASK_ID; use default-expansion to prove it's unset.
	script := "#!/bin/sh\nset -u\necho \"plugin=$TASK_PLUGIN_NAME id=${TASK_ID:-none}\"\n"
	dir := writePlugin(t, root, "p", "name: p\nactions:\n  - id: a\n    command: a.sh\n",
		map[string]string{"a.sh": script})

	p := Plugin{Name: "p", Dir: dir, Actions: []Action{{ID: "a", Command: "a.sh"}}}
	out, err := RunAction(context.Background(), p, p.Actions[0], nil)
	if err != nil {
		t.Fatalf("RunAction: %v (%s)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "plugin=p id=none" {
		t.Errorf("action output = %q, want %q", got, "plugin=p id=none")
	}
}

func TestFindAction(t *testing.T) {
	plugins := []Plugin{{Name: "p", Actions: []Action{{ID: "a"}}}}

	if _, a, err := FindAction(plugins, "p", "a"); err != nil || a.ID != "a" {
		t.Errorf("FindAction(p,a) = %+v, %v", a, err)
	}
	if _, _, err := FindAction(plugins, "p", "nope"); err == nil {
		t.Error("expected error for unknown action")
	}
	if _, _, err := FindAction(plugins, "nope", "a"); err == nil {
		t.Error("expected error for unknown plugin")
	}
}

func TestRunAction_MissingScriptErrors(t *testing.T) {
	p := Plugin{Name: "p", Dir: filepath.Join(t.TempDir(), "p")}
	_, err := RunAction(context.Background(), p, Action{ID: "x", Command: "nope.sh"}, nil)
	if err == nil {
		t.Error("expected error for missing action script")
	}
}
