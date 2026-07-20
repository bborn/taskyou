package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/hooks"
)

// TestRemovePlugin_DeletesSingleRepo installs a single-plugin repo, then removes
// it: the whole checkout is deleted and it's no longer discoverable.
func TestRemovePlugin_DeletesSingleRepo(t *testing.T) {
	source := makeSourcePluginRepo(t, "rpi-pack")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if _, _, err := addPlugin(source, pluginsDir, ""); err != nil {
		t.Fatalf("addPlugin: %v", err)
	}

	dir, inCheckout, err := removePlugin("rpi-pack", pluginsDir)
	if err != nil {
		t.Fatalf("removePlugin: %v", err)
	}
	if inCheckout {
		t.Error("inCheckout = true, want false (its own single-plugin checkout)")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin dir %s still exists after remove", dir)
	}
	plugins, _ := hooks.LoadPlugins(pluginsDir)
	for _, p := range plugins {
		if p.Name == "rpi-pack" {
			t.Fatalf("rpi-pack still discoverable after remove: %v", plugins)
		}
	}
}

// TestRemovePlugin_CollectionLeavesSiblings removes one plugin from a multi-plugin
// collection checkout: only its subdir goes, the sibling stays, and the caller is
// told it lived inside a shared checkout.
func TestRemovePlugin_CollectionLeavesSiblings(t *testing.T) {
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
	if _, _, err := addPlugin(repo, pluginsDir, ""); err != nil {
		t.Fatalf("addPlugin collection: %v", err)
	}

	_, inCheckout, err := removePlugin("rpi-go", pluginsDir)
	if err != nil {
		t.Fatalf("removePlugin: %v", err)
	}
	if !inCheckout {
		t.Error("inCheckout = false, want true (nested in a shared collection checkout)")
	}

	plugins, _ := hooks.LoadPlugins(pluginsDir)
	var names []string
	for _, p := range plugins {
		names = append(names, p.Name)
	}
	if len(names) != 1 || names[0] != "rpi-rails" {
		t.Errorf("after removing rpi-go, remaining plugins = %v, want [rpi-rails]", names)
	}
}

// TestRemovePlugin_MatchesByDirName removes a plugin whose directory name differs
// from its manifest name, using the directory name.
func TestRemovePlugin_MatchesByDirName(t *testing.T) {
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	dir := filepath.Join(pluginsDir, "my-dir")
	if err := os.MkdirAll(filepath.Join(dir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("name: fancy-name\ndescription: d\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "workflows", "w.yaml"), []byte("name: w\nsteps:\n  - name: b\n    prompt: x\n"), 0o644)

	if _, _, err := removePlugin("my-dir", pluginsDir); err != nil {
		t.Fatalf("removePlugin by dir name: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin dir %s still exists after remove", dir)
	}
}

// TestRemovePlugin_UnknownErrors errors (rather than deleting anything) when no
// plugin matches the given name.
func TestRemovePlugin_UnknownErrors(t *testing.T) {
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := removePlugin("nope", pluginsDir); err == nil {
		t.Error("expected an error removing an unknown plugin, got nil")
	}
}
