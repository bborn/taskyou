package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

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

	return pluginsCmd
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
			fmt.Printf("    %-22s → %s\n", e, p.Hooks[e])
		}
		fmt.Println()
	}
}
