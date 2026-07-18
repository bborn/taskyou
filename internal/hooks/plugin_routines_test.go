package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

// writePluginRoutine drops a routines/<name>/prompt.md into a plugin dir.
func writePluginRoutine(t *testing.T, pluginDir, name, prompt string) {
	t.Helper()
	rdir := filepath.Join(pluginDir, "routines", name)
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rdir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A plugin that ships ONLY a routine (no hooks/actions/workflows) is still usable.
func TestPlugin_RoutinesOnlyIsUsable(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "linear", "name: linear\ndescription: Linear integration\n", nil)
	writePluginRoutine(t, dir, "linear-poll", "poll linear")

	plugins, warnings := LoadPlugins(root)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(plugins) != 1 {
		t.Fatalf("got %d plugins, want 1 (routines-only plugin must load)", len(plugins))
	}
	p := plugins[0]
	if len(p.Routines) != 1 || p.Routines[0] != "linear-poll" {
		t.Errorf("p.Routines = %v, want [linear-poll]", p.Routines)
	}
	if p.RoutinesDir() != filepath.Join(dir, "routines") {
		t.Errorf("RoutinesDir() = %q, want %q", p.RoutinesDir(), filepath.Join(dir, "routines"))
	}
}

// A routines/ subdir without a prompt.md is not a routine and doesn't count.
func TestPlugin_RoutineWithoutPromptIgnored(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "empty", "name: empty\n", nil)
	if err := os.MkdirAll(filepath.Join(dir, "routines", "nope"), 0o755); err != nil {
		t.Fatal(err)
	}

	plugins, _ := LoadPlugins(root)
	// No prompt.md → no routine → nothing usable → plugin skipped entirely.
	if len(plugins) != 0 {
		t.Fatalf("got %d plugins, want 0 (routine dir without prompt.md is not usable)", len(plugins))
	}
}

// PluginRoutineDirs returns the routines/ subdir of every plugin that ships one.
func TestPluginRoutineDirs(t *testing.T) {
	root := t.TempDir()
	withRt := writePlugin(t, root, "hasrt", "name: hasrt\ndescription: d\n", nil)
	writePluginRoutine(t, withRt, "poll", "do the poll")
	// A hooks-only plugin must NOT contribute a routines dir.
	writePlugin(t, root, "hooksonly", "name: hooksonly\nhooks:\n  task.done: d.sh\n",
		map[string]string{"d.sh": "#!/bin/sh\n"})

	dirs := PluginRoutineDirs(root)
	want := filepath.Join(withRt, "routines")
	if len(dirs) != 1 || dirs[0] != want {
		t.Errorf("PluginRoutineDirs = %v, want [%s]", dirs, want)
	}
}
