package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"

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
		fmt.Println()
	}
}
