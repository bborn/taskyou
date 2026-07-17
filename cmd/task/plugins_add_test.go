package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/hooks"
)

// makeSourcePluginRepo builds a local git repo that is a single plugin shipping a
// workflow, and returns its path. Used as the clone source (no network needed).
func makeSourcePluginRepo(t *testing.T, name string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), name)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(repo, "workflows"), 0o755))
	must(os.WriteFile(filepath.Join(repo, "plugin.yaml"),
		[]byte("name: "+name+"\ndescription: rpi variants\n"), 0o644))
	must(os.WriteFile(filepath.Join(repo, "workflows", "rpi-go.yaml"),
		[]byte("name: rpi-go\nsteps:\n  - name: build\n    prompt: x\n"), 0o644))
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t.local"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func TestAddPlugin_ClonesAndUpdates(t *testing.T) {
	source := makeSourcePluginRepo(t, "rpi-pack")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")

	installed, updated, err := addPlugin(source, pluginsDir, "")
	if err != nil {
		t.Fatalf("addPlugin: %v", err)
	}
	if len(installed) != 1 || installed[0] != "rpi-pack" {
		t.Errorf("installed = %v, want [rpi-pack]", installed)
	}
	if updated {
		t.Error("first add reported updated=true, want false (fresh clone)")
	}

	// The cloned plugin loads, and its workflow is discoverable.
	plugins, _ := hooks.LoadPlugins(pluginsDir)
	var got *hooks.Plugin
	for i := range plugins {
		if plugins[i].Name == "rpi-pack" {
			got = &plugins[i]
		}
	}
	if got == nil {
		t.Fatalf("cloned plugin not found by LoadPlugins; got %v", plugins)
	}
	if len(got.Workflows) != 1 || got.Workflows[0] != "rpi-go" {
		t.Errorf("cloned plugin workflows = %v, want [rpi-go]", got.Workflows)
	}

	// Re-adding the same source updates in place (git pull) rather than erroring.
	_, updated, err = addPlugin(source, pluginsDir, "")
	if err != nil {
		t.Fatalf("second addPlugin (update): %v", err)
	}
	if !updated {
		t.Error("second add reported updated=false, want true (git pull)")
	}
}

// A collection repo (many plugin subdirs, none a plugin at its root) installs all
// of its plugins with a single `ty plugins add`.
func TestAddPlugin_ClonesCollection(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "ty-plugins")
	mk := func(rel, body string) {
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("rpi-go/plugin.yaml", "name: rpi-go\ndescription: d\n")
	mk("rpi-go/workflows/rpi-go.yaml", "name: rpi-go\nsteps:\n  - name: b\n    prompt: x\n")
	mk("rpi-rails/plugin.yaml", "name: rpi-rails\ndescription: d\n")
	mk("rpi-rails/workflows/rpi-rails.yaml", "name: rpi-rails\nsteps:\n  - name: b\n    prompt: x\n")
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t.local"}, {"config", "user.name", "t"}, {"add", "-A"}, {"commit", "-qm", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	pluginsDir := filepath.Join(t.TempDir(), "plugins")

	installed, _, err := addPlugin(repo, pluginsDir, "")
	if err != nil {
		t.Fatalf("addPlugin collection: %v", err)
	}
	if len(installed) != 2 || installed[0] != "rpi-go" || installed[1] != "rpi-rails" {
		t.Errorf("installed = %v, want [rpi-go rpi-rails]", installed)
	}
}

func TestAddPlugin_RejectsNonPluginRepo(t *testing.T) {
	// A git repo with no plugin.yaml at its root is not installable as a plugin.
	repo := filepath.Join(t.TempDir(), "not-a-plugin")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t.local"}, {"config", "user.name", "t"}, {"add", "-A"}, {"commit", "-qm", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.CombinedOutput()
	}
	pluginsDir := filepath.Join(t.TempDir(), "plugins")

	if _, _, err := addPlugin(repo, pluginsDir, ""); err == nil {
		t.Error("expected an error installing a repo with no plugin.yaml, got nil")
	}
	// And it must not leave a stray dir behind.
	if _, err := os.Stat(filepath.Join(pluginsDir, "not-a-plugin")); !os.IsNotExist(err) {
		t.Error("failed clone left a stray plugin dir behind")
	}
}
