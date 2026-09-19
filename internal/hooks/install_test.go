package hooks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/registry"
)

func gitInit(t *testing.T, repo string) {
	t.Helper()
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
}

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
	gitInit(t, repo)
	return repo
}

// makeCollectionRepo builds a local git repo holding several plugins in
// subdirectories, with no plugin at its root.
func makeCollectionRepo(t *testing.T, names ...string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "ty-plugins")
	for _, name := range names {
		dir := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Join(dir, "workflows"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"),
			[]byte("name: "+name+"\ndescription: d\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workflows", name+".yaml"),
			[]byte("name: "+name+"\nsteps:\n  - name: b\n    prompt: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, repo)
	return repo
}

func TestInstall_ClonesAndUpdates(t *testing.T) {
	source := makeSourcePluginRepo(t, "rpi-pack")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")

	res, err := Install(context.Background(), pluginsDir, InstallRequest{Source: source})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Plugins) != 1 || res.Plugins[0] != "rpi-pack" {
		t.Errorf("installed = %v, want [rpi-pack]", res.Plugins)
	}
	if res.Updated {
		t.Error("first install reported updated=true, want false (fresh clone)")
	}

	// The cloned plugin loads, and its workflow is discoverable.
	plugins, _ := LoadPlugins(pluginsDir)
	var got *Plugin
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

	// Re-installing the same source updates in place (git pull) rather than erroring.
	res, err = Install(context.Background(), pluginsDir, InstallRequest{Source: source})
	if err != nil {
		t.Fatalf("second Install (update): %v", err)
	}
	if !res.Updated {
		t.Error("second install reported updated=false, want true (git pull)")
	}
}

// A collection repo (many plugin subdirs, none a plugin at its root) installs all
// of its plugins with a single install.
func TestInstall_ClonesCollection(t *testing.T) {
	repo := makeCollectionRepo(t, "rpi-go", "rpi-rails")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")

	res, err := Install(context.Background(), pluginsDir, InstallRequest{Source: repo})
	if err != nil {
		t.Fatalf("Install collection: %v", err)
	}
	if len(res.Plugins) != 2 || res.Plugins[0] != "rpi-go" || res.Plugins[1] != "rpi-rails" {
		t.Errorf("installed = %v, want [rpi-go rpi-rails]", res.Plugins)
	}
}

// Installing by subdir takes ONE plugin out of a collection — the behaviour a
// catalog entry needs, since every entry in a collection repo shares one source.
func TestInstall_SubdirTakesOnePlugin(t *testing.T) {
	repo := makeCollectionRepo(t, "rpi-go", "rpi-rails")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")

	res, err := Install(context.Background(), pluginsDir, InstallRequest{
		ID: "rpi-go", Source: repo, Subdir: "rpi-go",
	})
	if err != nil {
		t.Fatalf("Install subdir: %v", err)
	}
	if len(res.Plugins) != 1 || res.Plugins[0] != "rpi-go" {
		t.Fatalf("installed = %v, want [rpi-go] only", res.Plugins)
	}
	if res.Name != "rpi-go" {
		t.Errorf("install dir name = %q, want rpi-go", res.Name)
	}
	// The sibling must NOT have come along.
	plugins, _ := LoadPlugins(pluginsDir)
	if len(plugins) != 1 {
		t.Errorf("plugins dir holds %d plugins, want 1: %v", len(plugins), plugins)
	}
	// It is a plain directory, not a git checkout: nothing depends on the
	// collection repo sticking around.
	if _, err := os.Stat(filepath.Join(res.Dir, ".git")); !os.IsNotExist(err) {
		t.Error("subdir install left a .git dir behind; want a plain copy")
	}
	// And the install is attributed to its catalog ID.
	if src, ok := LoadSources(pluginsDir)["rpi-go"]; !ok || src.ID != "rpi-go" || src.Subdir != "rpi-go" {
		t.Errorf("source record = %+v, want id/subdir rpi-go", src)
	}
}

// Re-installing a subdir plugin replaces it in place and reports an update.
func TestInstall_SubdirReinstallUpdates(t *testing.T) {
	repo := makeCollectionRepo(t, "rpi-go")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	req := InstallRequest{ID: "rpi-go", Source: repo, Subdir: "rpi-go"}
	if _, err := Install(context.Background(), pluginsDir, req); err != nil {
		t.Fatalf("first install: %v", err)
	}
	res, err := Install(context.Background(), pluginsDir, req)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !res.Updated {
		t.Error("re-installing a subdir plugin reported updated=false, want true")
	}
	if len(res.Plugins) != 1 {
		t.Errorf("plugins = %v, want one", res.Plugins)
	}
}

// Install preserves the executable bit — a hook script copied without +x is a
// plugin that silently does nothing.
func TestInstall_SubdirPreservesExecutableBit(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "coll")
	dir := filepath.Join(repo, "notify")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"),
		[]byte("name: notify\ndescription: d\nhooks:\n  task.done: on-done.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "on-done.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)

	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	res, err := Install(context.Background(), pluginsDir, InstallRequest{ID: "notify", Source: repo, Subdir: "notify"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	fi, err := os.Stat(filepath.Join(res.Dir, "on-done.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("hook script mode = %v, want executable", fi.Mode().Perm())
	}
}

func TestInstall_RejectsNonPluginRepo(t *testing.T) {
	// A git repo with no plugin.yaml at its root is not installable as a plugin.
	repo := filepath.Join(t.TempDir(), "not-a-plugin")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")

	if _, err := Install(context.Background(), pluginsDir, InstallRequest{Source: repo}); err == nil {
		t.Error("expected an error installing a repo with no plugin.yaml, got nil")
	}
	// And it must not leave a stray dir behind.
	if _, err := os.Stat(filepath.Join(pluginsDir, "not-a-plugin")); !os.IsNotExist(err) {
		t.Error("failed clone left a stray plugin dir behind")
	}
}

func TestInstall_RejectsEscapingSubdir(t *testing.T) {
	repo := makeCollectionRepo(t, "rpi-go")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if _, err := Install(context.Background(), pluginsDir, InstallRequest{
		Source: repo, Subdir: "../../etc", Name: "evil",
	}); err == nil {
		t.Error("expected an error for a subdir escaping the checkout, got nil")
	}
}

// TestRemove_DeletesSingleRepo installs a single-plugin repo, then removes it: the
// whole checkout is deleted and it's no longer discoverable.
func TestRemove_DeletesSingleRepo(t *testing.T) {
	source := makeSourcePluginRepo(t, "rpi-pack")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if _, err := Install(context.Background(), pluginsDir, InstallRequest{Source: source}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	dir, inCheckout, err := Remove("rpi-pack", pluginsDir)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if inCheckout {
		t.Error("inCheckout = true, want false (its own single-plugin checkout)")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin dir %s still exists after remove", dir)
	}
	plugins, _ := LoadPlugins(pluginsDir)
	for _, p := range plugins {
		if p.Name == "rpi-pack" {
			t.Fatalf("rpi-pack still discoverable after remove: %v", plugins)
		}
	}
	// Its provenance record goes with it, so it no longer reads as installed.
	if _, ok := LoadSources(pluginsDir)["rpi-pack"]; ok {
		t.Error("source record survived the remove")
	}
}

// TestRemove_CollectionLeavesSiblings removes one plugin from a multi-plugin
// collection checkout: only its subdir goes, the sibling stays, and the caller is
// told it lived inside a shared checkout.
func TestRemove_CollectionLeavesSiblings(t *testing.T) {
	repo := makeCollectionRepo(t, "rpi-go", "rpi-rails")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if _, err := Install(context.Background(), pluginsDir, InstallRequest{Source: repo}); err != nil {
		t.Fatalf("Install collection: %v", err)
	}

	_, inCheckout, err := Remove("rpi-go", pluginsDir)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !inCheckout {
		t.Error("inCheckout = false, want true (nested in a shared collection checkout)")
	}

	plugins, _ := LoadPlugins(pluginsDir)
	var names []string
	for _, p := range plugins {
		names = append(names, p.Name)
	}
	if len(names) != 1 || names[0] != "rpi-rails" {
		t.Errorf("after removing rpi-go, remaining plugins = %v, want [rpi-rails]", names)
	}
}

// TestRemove_MatchesByDirName removes a plugin whose directory name differs from
// its manifest name, using the directory name.
func TestRemove_MatchesByDirName(t *testing.T) {
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	dir := filepath.Join(pluginsDir, "my-dir")
	if err := os.MkdirAll(filepath.Join(dir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("name: fancy-name\ndescription: d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflows", "w.yaml"), []byte("name: w\nsteps:\n  - name: b\n    prompt: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Remove("my-dir", pluginsDir); err != nil {
		t.Fatalf("Remove by dir name: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("plugin dir %s still exists after remove", dir)
	}
}

// TestRemove_UnknownErrors errors (rather than deleting anything) when no plugin
// matches the given name.
func TestRemove_UnknownErrors(t *testing.T) {
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Remove("nope", pluginsDir); err == nil {
		t.Error("expected an error removing an unknown plugin, got nil")
	}
}

// Update re-pulls a git checkout and re-copies a subdir install, using the
// recorded provenance in the latter case (there is no remote to ask).
func TestUpdate_SubdirUsesRecordedSource(t *testing.T) {
	repo := makeCollectionRepo(t, "rpi-go")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if _, err := Install(context.Background(), pluginsDir, InstallRequest{
		ID: "rpi-go", Source: repo, Subdir: "rpi-go",
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Change the upstream copy; the update must bring the new file across.
	if err := os.WriteFile(filepath.Join(repo, "rpi-go", "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "add file"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	res, err := Update(context.Background(), pluginsDir, "rpi-go")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !res.Updated {
		t.Error("Update reported updated=false")
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "rpi-go", "NEW.md")); err != nil {
		t.Errorf("update did not bring the new upstream file across: %v", err)
	}
}

func TestUpdate_UnknownSourceErrors(t *testing.T) {
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	dir := filepath.Join(pluginsDir, "hand-made")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"),
		[]byte("name: hand-made\ndescription: d\nservices:\n  - name: s\n    command: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(context.Background(), pluginsDir, "hand-made"); err == nil {
		t.Error("expected an error updating a hand-copied plugin with no recorded source")
	}
}

func TestResolve(t *testing.T) {
	entries := []registry.Entry{
		{ID: "slack", Name: "Slack", Description: "d", Source: "https://github.com/x/plugins", Subdir: "slack"},
		{ID: "rpi", Name: "RPI", Description: "d", Source: "https://github.com/x/plugins", Subdir: "rpi"},
	}
	ctx := context.Background()

	t.Run("catalog id", func(t *testing.T) {
		req, err := Resolve(ctx, "slack", entries)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if req.ID != "slack" || req.Subdir != "slack" || req.Name != "slack" {
			t.Errorf("req = %+v, want the slack entry", req)
		}
	})

	t.Run("git url wins over catalog lookup", func(t *testing.T) {
		req, err := Resolve(ctx, "https://github.com/y/z", entries)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if req.Source != "https://github.com/y/z" || req.Subdir != "" {
			t.Errorf("req = %+v, want the literal URL", req)
		}
	})

	t.Run("owner/repo shorthand", func(t *testing.T) {
		req, err := Resolve(ctx, "taskyou/plugins", entries)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if req.Source != "https://github.com/taskyou/plugins" {
			t.Errorf("source = %q, want the expanded GitHub URL", req.Source)
		}
	})

	t.Run("local path", func(t *testing.T) {
		dir := t.TempDir()
		req, err := Resolve(ctx, dir, entries)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if req.Source != dir {
			t.Errorf("source = %q, want %q", req.Source, dir)
		}
	})

	t.Run("typo suggests", func(t *testing.T) {
		_, err := Resolve(ctx, "slak", entries)
		if err == nil {
			t.Fatal("expected an error for an unknown handle")
		}
		if !strings.Contains(err.Error(), "slack") {
			t.Errorf("error %q should suggest slack", err)
		}
	})
}

func TestIsInstalled(t *testing.T) {
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	dir := filepath.Join(pluginsDir, "notify-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"),
		[]byte("name: desktop-notify\ndescription: d\nservices:\n  - name: s\n    command: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plugins, _ := LoadPlugins(pluginsDir)

	// Matched by manifest name even though the directory is named differently —
	// a hand-copied plugin must not read as "available" in the browser.
	if _, ok := IsInstalled(pluginsDir, "desktop-notify", plugins); !ok {
		t.Error("IsInstalled(desktop-notify) = false, want true (manifest name match)")
	}
	if _, ok := IsInstalled(pluginsDir, "notify-dir", plugins); !ok {
		t.Error("IsInstalled(notify-dir) = false, want true (directory name match)")
	}
	if _, ok := IsInstalled(pluginsDir, "slack", plugins); ok {
		t.Error("IsInstalled(slack) = true, want false")
	}

	// A provenance record is authoritative even when neither name matches.
	if err := saveSource(pluginsDir, "notify-dir", InstalledSource{ID: "fancy-id", Source: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := IsInstalled(pluginsDir, "fancy-id", plugins); !ok {
		t.Error("IsInstalled(fancy-id) = false, want true (recorded source ID)")
	}
}

// Updating a plugin that lives inside a collection checkout pulls the checkout.
// Looking only at the plugin's own directory finds neither a .git nor a source
// record — every plugin in a collection was reporting "don't know where this
// came from".
func TestUpdate_CollectionMemberPullsTheCheckout(t *testing.T) {
	repo := makeCollectionRepo(t, "rpi-go", "rpi-rails")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if _, err := Install(context.Background(), pluginsDir, InstallRequest{Source: repo}); err != nil {
		t.Fatalf("Install collection: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "rpi-go", "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "add file"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	res, err := Update(context.Background(), pluginsDir, "rpi-go")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Name != "ty-plugins" {
		t.Errorf("updated %q, want the collection checkout ty-plugins", res.Name)
	}
	if len(res.Plugins) != 2 {
		t.Errorf("update reported %v, want both plugins in the checkout", res.Plugins)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "ty-plugins", "rpi-go", "NEW.md")); err != nil {
		t.Errorf("pull did not bring the new upstream file across: %v", err)
	}
}

func TestInstallRoot(t *testing.T) {
	repo := makeCollectionRepo(t, "rpi-go")
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	if _, err := Install(context.Background(), pluginsDir, InstallRequest{Source: repo}); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(pluginsDir, "ty-plugins", "rpi-go")
	root, _, ok := InstallRoot(pluginsDir, nested)
	if !ok || root != filepath.Join(pluginsDir, "ty-plugins") {
		t.Errorf("InstallRoot(%s) = %q, %v; want the checkout", nested, root, ok)
	}

	// A hand-copied plugin has no install root at all.
	hand := filepath.Join(pluginsDir, "hand")
	if err := os.MkdirAll(hand, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := InstallRoot(pluginsDir, hand); ok {
		t.Error("a hand-copied plugin should have no install root")
	}
}
