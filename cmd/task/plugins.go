package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
)

// newPluginsCmd returns the `ty plugins` command group for inspecting installed
// task plugins (self-contained hook bundles under ~/.config/task/plugins/).
func newPluginsCmd() *cobra.Command {
	pluginsCmd := &cobra.Command{
		Use:   "plugins",
		Short: "List and inspect installed task plugins",
		Long: `Task plugins are self-contained directories under ~/.config/task/plugins/
that react to task events. Each plugin has a plugin.yaml manifest declaring
which events it handles; drop a plugin directory in and it is active. Any number
of plugins may handle the same event.`,
		Run: func(cmd *cobra.Command, args []string) {
			listPlugins()
		},
	}

	pluginsCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List installed plugins and the events they handle",
		Run:   func(cmd *cobra.Command, args []string) { listPlugins() },
	})

	pluginsCmd.AddCommand(&cobra.Command{
		Use:   "dir",
		Short: "Print the plugins directory path",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(hooks.DefaultPluginsDir())
		},
	})

	addCmd := &cobra.Command{
		Use:   "add <git-url|path> [--name N]",
		Short: "Install a plugin from a git repo (or update it if already installed)",
		Long: `Clone a plugin repository into the plugins dir. The repo root must be a
plugin (a plugin.yaml, optionally with a workflows/ subdir). Re-running add on the
same plugin updates it in place with git pull.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			installed, updated, err := addPlugin(args[0], hooks.DefaultPluginsDir(), name)
			if err != nil {
				return err
			}
			verb := "Installed"
			if updated {
				verb = "Updated"
			}
			fmt.Printf("%s %d plugin(s): %s. Run `ty plugins list` to see what they provide.\n",
				verb, len(installed), strings.Join(installed, ", "))
			return nil
		},
	}
	addCmd.Flags().String("name", "", "Install under this directory name (default: derived from the source)")
	pluginsCmd.AddCommand(addCmd)

	removeCmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm", "uninstall"},
		Short:   "Uninstall a plugin by deleting its directory",
		Long: `Delete an installed plugin's directory. The name is the one shown by
` + "`ty plugins list`" + ` (the manifest name), even when it differs from the
directory name. When the plugin lives inside a multi-plugin collection checkout,
only its own subdirectory is removed; the shared git checkout and its sibling
plugins are left in place (and re-running ` + "`ty plugins add`" + ` on the
source may restore it).`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, inCheckout, err := removePlugin(args[0], hooks.DefaultPluginsDir())
			if err != nil {
				return err
			}
			fmt.Printf("Removed plugin %q (%s).\n", args[0], dir)
			if inCheckout {
				fmt.Println("Note: it was part of a shared git checkout; its sibling plugins remain, " +
					"and re-adding that source may restore it.")
			}
			return nil
		},
	}
	pluginsCmd.AddCommand(removeCmd)

	runCmd := &cobra.Command{
		Use:          "run <plugin> <action> [task-id]",
		Short:        "Run a plugin action, optionally in the context of a task",
		Args:         cobra.RangeArgs(2, 3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var taskID int64
			if len(args) == 3 {
				id, err := strconv.ParseInt(args[2], 10, 64)
				if err != nil {
					return fmt.Errorf("invalid task id %q: %w", args[2], err)
				}
				taskID = id
			}
			return runPluginAction(cmd.Context(), args[0], args[1], taskID)
		},
	}
	pluginsCmd.AddCommand(runCmd)

	return pluginsCmd
}

// addPlugin installs (or updates) a plugin from a git source into pluginsDir. A
// fresh install is a shallow clone into pluginsDir/<name>; an existing install of
// the same name is updated in place with `git pull`. It returns the installed
// directory name, whether it was an update, and validates that what landed is a
// real plugin (rolling back a bad fresh clone so no stray dir is left behind).
func addPlugin(source, pluginsDir, name string) (installed []string, updated bool, err error) {
	if name == "" {
		name = derivePluginName(source)
	}
	if name == "" {
		return nil, false, fmt.Errorf("could not derive a plugin name from %q; pass --name", source)
	}
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return nil, false, fmt.Errorf("create plugins dir: %w", err)
	}
	target := filepath.Join(pluginsDir, name)

	switch {
	case isGitCheckout(target):
		if out, err := runGit(target, "pull", "--ff-only"); err != nil {
			return nil, false, fmt.Errorf("update %s: %w\n%s", name, err, out)
		}
		updated = true
	case dirExists(target):
		return nil, false, fmt.Errorf("%s already exists and is not a git checkout; remove it or use --name", target)
	default:
		if out, err := runGit("", "clone", "--depth", "1", source, target); err != nil {
			return nil, false, fmt.Errorf("clone %s: %w\n%s", source, err, out)
		}
	}

	// Validate what landed contains at least one usable plugin — a single plugin at
	// the root, or several nested in a collection. Roll back a bad FRESH clone so no
	// stray dir is left behind.
	installed = installedPluginNames(pluginsDir, target)
	if len(installed) == 0 {
		if !updated {
			os.RemoveAll(target)
		}
		return nil, false, fmt.Errorf("%s contains no usable plugins (need a plugin.yaml with a hook, action, or workflow)", source)
	}
	return installed, updated, nil
}

// removePlugin uninstalls the plugin named name by deleting its directory. The
// name is matched against the loaded plugins' manifest names (what `plugins list`
// shows), falling back to the directory's base name, so it works even when a
// plugin's directory name differs from its manifest name.
//
// It returns the removed directory and whether that directory sat inside a
// multi-plugin collection checkout — in which case only the plugin's own subdir is
// deleted (leaving the shared git checkout and its siblings), and a later `add` of
// the source may restore it. The plugin dir is verified to live strictly inside
// pluginsDir before anything is deleted, so a corrupt manifest can't point removal
// at an arbitrary path.
func removePlugin(name, pluginsDir string) (dir string, inCheckout bool, err error) {
	if pluginsDir == "" {
		return "", false, fmt.Errorf("no plugins directory configured")
	}
	plugins, _ := hooks.LoadPlugins(pluginsDir)
	var match *hooks.Plugin
	for i := range plugins {
		if plugins[i].Name == name || filepath.Base(plugins[i].Dir) == name {
			match = &plugins[i]
			break
		}
	}
	if match == nil {
		return "", false, fmt.Errorf("no plugin named %q installed in %s; run `ty plugins list` to see what's installed", name, pluginsDir)
	}

	dir = match.Dir
	// Safety: refuse to delete anything that is not strictly under pluginsDir.
	rel, relErr := filepath.Rel(pluginsDir, dir)
	if relErr != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false, fmt.Errorf("refusing to remove %s: not inside plugins dir %s", dir, pluginsDir)
	}

	inCheckout = insideCollectionCheckout(dir, pluginsDir)
	if err := os.RemoveAll(dir); err != nil {
		return "", false, fmt.Errorf("remove %s: %w", dir, err)
	}
	return dir, inCheckout, nil
}

// insideCollectionCheckout reports whether dir is a plugin nested inside a shared
// git checkout (a collection repo cloned by `plugins add`), rather than being its
// own checkout. It's true when dir itself is not a git checkout but an ancestor
// strictly between it and pluginsDir is.
func insideCollectionCheckout(dir, pluginsDir string) bool {
	if isGitCheckout(dir) {
		return false
	}
	for parent := filepath.Dir(dir); len(parent) > len(pluginsDir); parent = filepath.Dir(parent) {
		if isGitCheckout(parent) {
			return true
		}
	}
	return false
}

// derivePluginName turns a git URL or path into a plugin directory name.
func derivePluginName(source string) string {
	s := strings.TrimSuffix(strings.TrimRight(source, "/"), ".git")
	return filepath.Base(s)
}

func isGitCheckout(dir string) bool { return dirExists(filepath.Join(dir, ".git")) }

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func runGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

// installedPluginNames returns the names of every plugin LoadPlugins discovers at
// or under target — one for a single-plugin repo, several for a collection repo.
// Matched by Dir prefix since a plugin's manifest name may differ from its dir.
func installedPluginNames(pluginsDir, target string) []string {
	plugins, _ := hooks.LoadPlugins(pluginsDir)
	var names []string
	for _, p := range plugins {
		if p.Dir == target || strings.HasPrefix(p.Dir, target+string(os.PathSeparator)) {
			names = append(names, p.Name)
		}
	}
	sort.Strings(names)
	return names
}

func runPluginAction(ctx context.Context, pluginName, actionID string, taskID int64) error {
	plugins, warnings := hooks.LoadPlugins(hooks.DefaultPluginsDir())
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}

	plugin, action, err := hooks.FindAction(plugins, pluginName, actionID)
	if err != nil {
		return err
	}

	// Load the task for context, if one was named.
	var task *db.Task
	if taskID != 0 {
		database, dberr := openTaskDB(db.DefaultPath())
		if dberr != nil {
			return dberr
		}
		defer database.Close()
		task, err = database.GetTask(taskID)
		if err != nil {
			return fmt.Errorf("task #%d: %w", taskID, err)
		}
	}

	out, runErr := hooks.RunAction(ctx, plugin, action, task)
	if len(out) > 0 {
		fmt.Print(string(out))
		if out[len(out)-1] != '\n' {
			fmt.Println()
		}
	}
	if runErr != nil {
		return fmt.Errorf("action %s/%s failed: %w", pluginName, actionID, runErr)
	}
	return nil
}

func listPlugins() {
	dir := hooks.DefaultPluginsDir()
	plugins, warnings := hooks.LoadPlugins(dir)

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}

	if len(plugins) == 0 {
		fmt.Printf("No plugins installed in %s\n", dir)
		fmt.Println("Add one by creating <name>/plugin.yaml there. See docs/plugins.md.")
		return
	}

	fmt.Printf("Plugins in %s:\n\n", dir)
	for _, p := range plugins {
		ver := p.Version
		if ver == "" {
			ver = "—"
		}
		fmt.Printf("  %s (%s)\n", p.Name, ver)
		if p.Description != "" {
			fmt.Printf("    %s\n", p.Description)
		}
		events := make([]string, 0, len(p.Hooks))
		for e := range p.Hooks {
			events = append(events, e)
		}
		sort.Strings(events)
		for _, e := range events {
			fmt.Printf("    hook   %-18s → %s\n", e, p.Hooks[e])
		}
		for _, a := range p.Actions {
			fmt.Printf("    action %-18s → %s  (ty plugins run %s %s)\n", a.DisplayLabel(), a.Command, p.Name, a.ID)
		}
		for _, w := range p.Workflows {
			fmt.Printf("    workflow %-16s (ty pipeline -d %s \"<goal>\")\n", w, w)
		}
		for _, s := range p.Services {
			fmt.Printf("    service  %-16s → %s  (supervised while the daemon runs)\n", s.Name, s.Command)
		}
		fmt.Println()
	}
}
