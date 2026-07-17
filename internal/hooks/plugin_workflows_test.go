package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

// writePluginWorkflow drops a workflows/<name>.yaml file into a plugin dir.
func writePluginWorkflow(t *testing.T, pluginDir, name, body string) {
	t.Helper()
	wdir := filepath.Join(pluginDir, "workflows")
	if err := os.MkdirAll(wdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wdir, name+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A plugin that ships ONLY workflows (no hooks, no actions) is still usable — a
// plugin can be pure workflow cargo.
func TestPlugin_WorkflowsOnlyIsUsable(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "rpi-pack", "name: rpi-pack\ndescription: RPI variants\n", nil)
	writePluginWorkflow(t, dir, "rpi-go", "name: rpi-go\nsteps:\n  - name: build\n    prompt: x\n")

	plugins, warnings := LoadPlugins(root)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(plugins) != 1 {
		t.Fatalf("got %d plugins, want 1 (workflows-only plugin must load)", len(plugins))
	}
	p := plugins[0]
	if len(p.Workflows) != 1 || p.Workflows[0] != "rpi-go" {
		t.Errorf("p.Workflows = %v, want [rpi-go]", p.Workflows)
	}
	if p.WorkflowsDir() != filepath.Join(dir, "workflows") {
		t.Errorf("WorkflowsDir() = %q, want %q", p.WorkflowsDir(), filepath.Join(dir, "workflows"))
	}
}

// A collection repo — one directory containing several plugin subdirs, none of
// which is a plugin itself — has ALL of its nested plugins discovered. This is how
// `ty plugins add <community-repo>` installs a whole monorepo of plugins at once.
func TestLoadPlugins_DiscoversNestedCollection(t *testing.T) {
	root := t.TempDir()
	// A cloned collection: root/ty-plugins/{rpi-pack, notify}
	coll := filepath.Join(root, "ty-plugins")
	rpi := writePlugin(t, coll, "rpi-pack", "name: rpi-pack\ndescription: d\n", nil)
	writePluginWorkflow(t, rpi, "rpi-go", "name: rpi-go\nsteps:\n  - name: b\n    prompt: x\n")
	writePlugin(t, coll, "notify", "name: notify\nhooks:\n  task.done: d.sh\n",
		map[string]string{"d.sh": "#!/bin/sh\n"})

	plugins, warnings := LoadPlugins(root)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	names := map[string]bool{}
	for _, p := range plugins {
		names[p.Name] = true
	}
	if !names["rpi-pack"] || !names["notify"] {
		t.Fatalf("nested plugins not discovered; got %v", names)
	}
	// The nested plugin's workflow dir is still contributed.
	dirs := PluginWorkflowDirs(root)
	if len(dirs) != 1 || dirs[0] != filepath.Join(rpi, "workflows") {
		t.Errorf("PluginWorkflowDirs = %v, want [%s]", dirs, filepath.Join(rpi, "workflows"))
	}
}

// PluginWorkflowDirs returns the workflows/ subdir of every plugin that ships one,
// for feeding into the pipeline workflow search path.
func TestPluginWorkflowDirs(t *testing.T) {
	root := t.TempDir()
	withWF := writePlugin(t, root, "haswf", "name: haswf\ndescription: d\n", nil)
	writePluginWorkflow(t, withWF, "foo", "name: foo\nsteps:\n  - name: s\n    prompt: p\n")
	// A hooks-only plugin (no workflows) must NOT contribute a dir.
	writePlugin(t, root, "hooksonly", "name: hooksonly\nhooks:\n  task.done: d.sh\n",
		map[string]string{"d.sh": "#!/bin/sh\n"})

	dirs := PluginWorkflowDirs(root)
	want := filepath.Join(withWF, "workflows")
	if len(dirs) != 1 || dirs[0] != want {
		t.Errorf("PluginWorkflowDirs = %v, want [%s]", dirs, want)
	}
}
