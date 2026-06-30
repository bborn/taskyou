package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/bborn/workflow/internal/db"
)

// writePlugin creates a plugin dir with the given manifest and scripts.
// scripts maps a relative filename to its body; each is made executable.
func writePlugin(t *testing.T, root, name, manifest string, scripts map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for rel, body := range scripts {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadPlugins_DiscoversValidPlugins(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "zeta", "name: zeta\nversion: 1.0.0\nhooks:\n  task.done: done.sh\n",
		map[string]string{"done.sh": "#!/bin/sh\n"})
	writePlugin(t, root, "alpha", "name: alpha\nhooks:\n  task.blocked: b.sh\n  task.done: d.sh\n",
		map[string]string{"b.sh": "#!/bin/sh\n", "d.sh": "#!/bin/sh\n"})

	plugins, warnings := LoadPlugins(root)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(plugins) != 2 {
		t.Fatalf("got %d plugins, want 2", len(plugins))
	}
	// Sorted by name for deterministic fan-out.
	if plugins[0].Name != "alpha" || plugins[1].Name != "zeta" {
		t.Errorf("plugins not sorted by name: %q, %q", plugins[0].Name, plugins[1].Name)
	}
	if got, ok := plugins[0].ScriptFor("task.done"); !ok || filepath.Base(got) != "d.sh" {
		t.Errorf("alpha task.done script = %q (ok=%v)", got, ok)
	}
}

func TestLoadPlugins_MissingDirIsNotAnError(t *testing.T) {
	plugins, warnings := LoadPlugins(filepath.Join(t.TempDir(), "does-not-exist"))
	if plugins != nil || warnings != nil {
		t.Errorf("got plugins=%v warnings=%v, want nil/nil", plugins, warnings)
	}
}

func TestLoadPlugins_SkipsInvalidAndWarns(t *testing.T) {
	root := t.TempDir()
	// Loose file (not a dir) is ignored entirely.
	if err := os.WriteFile(filepath.Join(root, "loose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Dir without a manifest is silently ignored (no warning).
	writePlugin(t, root, "notaplugin", "", map[string]string{"x.sh": "#!/bin/sh\n"})
	// Manifest missing a name -> skipped with warning.
	writePlugin(t, root, "noname", "hooks:\n  task.done: d.sh\n", map[string]string{"d.sh": "#!/bin/sh\n"})
	// Hook references a script that doesn't exist -> skipped with warning.
	writePlugin(t, root, "broken", "name: broken\nhooks:\n  task.done: missing.sh\n", nil)
	// One good plugin survives.
	writePlugin(t, root, "good", "name: good\nhooks:\n  task.done: d.sh\n", map[string]string{"d.sh": "#!/bin/sh\n"})

	plugins, warnings := LoadPlugins(root)
	if len(plugins) != 1 || plugins[0].Name != "good" {
		t.Fatalf("got %d plugins (%v), want only 'good'", len(plugins), plugins)
	}
	if len(warnings) != 2 {
		t.Errorf("got %d warnings, want 2 (noname, broken): %v", len(warnings), warnings)
	}
}

func TestLoadPlugins_DropsMissingHookButKeepsRest(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "partial",
		"name: partial\nhooks:\n  task.done: ok.sh\n  task.blocked: gone.sh\n",
		map[string]string{"ok.sh": "#!/bin/sh\n"})

	plugins, warnings := LoadPlugins(root)
	if len(plugins) != 1 {
		t.Fatalf("got %d plugins, want 1", len(plugins))
	}
	if _, ok := plugins[0].ScriptFor("task.done"); !ok {
		t.Error("task.done hook should survive")
	}
	if _, ok := plugins[0].ScriptFor("task.blocked"); ok {
		t.Error("task.blocked hook should have been dropped")
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning about dropped hook, got %v", warnings)
	}
}

func TestRunner_FansOutToPluginsAndLegacyHook(t *testing.T) {
	hooksDir := t.TempDir()
	pluginsDir := t.TempDir()
	out := t.TempDir() // each hook touches a marker file here

	// Legacy single-script hook.
	legacy := "#!/bin/sh\necho legacy > \"" + filepath.Join(out, "legacy") + "\"\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "task.done"), []byte(legacy), 0o755); err != nil {
		t.Fatal(err)
	}

	// Two plugins both handling task.done — neither collides.
	for _, name := range []string{"one", "two"} {
		marker := filepath.Join(out, name)
		body := "#!/bin/sh\necho \"$TASK_PLUGIN_NAME:$TASK_ID\" > \"" + marker + "\"\n"
		writePlugin(t, pluginsDir, name,
			"name: "+name+"\nhooks:\n  task.done: run.sh\n",
			map[string]string{"run.sh": body})
	}

	r := newRunner(hooksDir, pluginsDir, log.NewWithOptions(os.Stderr, log.Options{Level: log.FatalLevel}))
	if len(r.Plugins()) != 2 {
		t.Fatalf("loaded %d plugins, want 2", len(r.Plugins()))
	}

	r.Run("task.done", &db.Task{ID: 42, Title: "t"}, "done")

	// Hooks run in background goroutines; wait for all three markers.
	for _, f := range []string{"legacy", "one", "two"} {
		waitForFile(t, filepath.Join(out, f))
	}

	// Plugin env was injected.
	if got := readFile(t, filepath.Join(out, "one")); got != "one:42\n" {
		t.Errorf("plugin 'one' marker = %q, want %q", got, "one:42\n")
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hook marker %q never appeared", filepath.Base(path))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
