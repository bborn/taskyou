package hooks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These tests pin the rollback contract of a subdir plugin update: a working
// plugin must survive an update whose upstream version turns out to be
// semantically broken (a filesystem-successful copy that loads no usable
// plugin). installSubdir moves the existing plugin aside into a `.old` backup
// before copying the new upstream; Install validates the new copy with
// installedPluginNames and must restore the backup when validation fails.

// makeHookedSubdirRepo builds a local collection repo whose only subdir plugin
// `rpi-go` ships a hook script, so it loads as a usable plugin. Used as the
// clone source (no network needed).
func makeHookedSubdirRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "coll")
	dir := filepath.Join(repo, "rpi-go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"),
		[]byte("name: rpi-go\ndescription: hooked\nhooks:\n  task.done: on-done.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "on-done.sh"),
		[]byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	return repo
}

// commitRepo stages and commits the current state of repo with message msg.
func commitRepo(t *testing.T, repo, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// findPlugin returns the loaded Plugin with the given manifest name, or nil.
func findPlugin(plugins []Plugin, name string) *Plugin {
	for i := range plugins {
		if plugins[i].Name == name {
			return &plugins[i]
		}
	}
	return nil
}

// assertWorkingRpiGo fails the test unless the installed rpi-go plugin is intact
// (its script exists, it loads, and its task.done hook is still declared) and no
// leftover .old backup directory remains.
func assertWorkingRpiGo(t *testing.T, pluginsDir string) {
	t.Helper()
	plugins, _ := LoadPlugins(pluginsDir)
	p := findPlugin(plugins, "rpi-go")
	if p == nil {
		t.Fatalf("LoadPlugins after broken update: rpi-go not found; plugins=%v", plugins)
	}
	if _, ok := p.Hooks["task.done"]; !ok {
		t.Errorf("restored rpi-go is missing its task.done hook; hooks=%v", p.Hooks)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "on-done.sh")); err != nil {
		t.Errorf("original hook script on-done.sh is gone after broken update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "rpi-go.old")); !os.IsNotExist(err) {
		t.Errorf("rpi-go.old backup dir left behind after a failed subdir update: %v", err)
	}
	if _, ok := LoadSources(pluginsDir)["rpi-go"]; !ok {
		t.Error("provenance record for rpi-go was lost after a failed subdir update")
	}
}

// TestUpdate_SubdirBrokenUpstreamRestoresOld reproduces the bug: install a working
// subdir plugin, break the upstream so it loads nothing, then Update. The
// previously working plugin must be restored from the .old backup rather than
// destroyed. The upstream change (manifest declares nothing AND the hook script
// is gone) triggers loadPlugin's "manifest declares no hooks..." site.
func TestUpdate_SubdirBrokenUpstreamRestoresOld(t *testing.T) {
	repo := makeHookedSubdirRepo(t)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")

	req := InstallRequest{ID: "rpi-go", Source: repo, Subdir: "rpi-go"}
	if _, err := Install(context.Background(), pluginsDir, req); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if plugins, _ := LoadPlugins(pluginsDir); len(plugins) != 1 || plugins[0].Name != "rpi-go" {
		t.Fatalf("after install: LoadPlugins = %v, want [rpi-go]", plugins)
	}

	// Break the upstream: strip all hook/action declarations and remove the
	// script, so loadPlugin drops every entry and installedPluginNames returns [].
	if err := os.WriteFile(filepath.Join(repo, "rpi-go", "plugin.yaml"),
		[]byte("name: rpi-go\ndescription: stripped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "rpi-go", "on-done.sh")); err != nil {
		t.Fatal(err)
	}
	commitRepo(t, repo, "break upstream")

	if _, err := Update(context.Background(), pluginsDir, "rpi-go"); err == nil {
		t.Fatal("Update with a broken upstream returned nil error; want a no-usable-plugins error")
	}
	assertWorkingRpiGo(t, pluginsDir)
}

// TestUpdate_SubdirSuccessRemovesBackup verifies the fix does not leak the .old
// backup on the happy path: a successful subdir update must drop the backup once
// the new copy is validated as a usable plugin.
func TestUpdate_SubdirSuccessRemovesBackup(t *testing.T) {
	repo := makeHookedSubdirRepo(t)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	req := InstallRequest{ID: "rpi-go", Source: repo, Subdir: "rpi-go"}
	if _, err := Install(context.Background(), pluginsDir, req); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Add a new upstream file (still a working plugin) so the update succeeds.
	if err := os.WriteFile(filepath.Join(repo, "rpi-go", "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitRepo(t, repo, "add file")

	if _, err := Update(context.Background(), pluginsDir, "rpi-go"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "rpi-go", "NEW.md")); err != nil {
		t.Errorf("update did not bring the new upstream file across: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "rpi-go.old")); !os.IsNotExist(err) {
		t.Errorf("rpi-go.old backup dir left behind after a successful subdir update: %v", err)
	}
}
