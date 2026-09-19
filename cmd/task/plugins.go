package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
	"github.com/bborn/workflow/internal/registry"
)

// newPluginsCmd returns the `ty plugins` command group: browse and search the
// catalog, install and update plugins, and inspect what's installed.
func newPluginsCmd() *cobra.Command {
	pluginsCmd := &cobra.Command{
		Use:   "plugins",
		Short: "Find, install, and inspect task plugins",
		Long: `Task plugins are self-contained directories under ~/.config/task/plugins/
that add workflows, routines, event hooks, actions, and services. Any number of
plugins may handle the same event.

Start with discovery rather than a URL:

  ty plugins browse              # the whole catalog
  ty plugins search slack        # find one
  ty plugins info slack          # read about it
  ty plugins add slack           # install it by name

A git URL or local path still works, for anything not in the catalog.
The TUI has the same catalog behind the ` + "`m`" + ` key.`,
		Run: func(cmd *cobra.Command, args []string) {
			listPlugins()
		},
	}

	pluginsCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List installed plugins and what they provide",
		Run:   func(cmd *cobra.Command, args []string) { listPlugins() },
	})

	pluginsCmd.AddCommand(&cobra.Command{
		Use:   "dir",
		Short: "Print the plugins directory path",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(hooks.DefaultPluginsDir())
		},
	})

	searchCmd := &cobra.Command{
		Use:          "search <query>",
		Short:        "Search the plugin catalog",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			refresh, _ := cmd.Flags().GetBool("refresh")
			return searchCatalog(cmd.Context(), strings.Join(args, " "), refresh)
		},
	}
	searchCmd.Flags().Bool("refresh", false, "Re-fetch the catalog instead of using the cached copy")
	pluginsCmd.AddCommand(searchCmd)

	browseCmd := &cobra.Command{
		Use:          "browse",
		Aliases:      []string{"catalog", "available"},
		Short:        "List every plugin in the catalog, grouped by category",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			refresh, _ := cmd.Flags().GetBool("refresh")
			return browseCatalog(cmd.Context(), refresh)
		},
	}
	browseCmd.Flags().Bool("refresh", false, "Re-fetch the catalog instead of using the cached copy")
	pluginsCmd.AddCommand(browseCmd)

	pluginsCmd.AddCommand(&cobra.Command{
		Use:          "info <id>",
		Short:        "Show everything known about one plugin, installed or not",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return pluginInfo(cmd.Context(), args[0])
		},
	})

	addCmd := &cobra.Command{
		Use:     "add <id|git-url|path>",
		Aliases: []string{"install"},
		Short:   "Install a plugin by catalog name, git URL, or local path",
		Long: `Install a plugin. The argument can be:

  a catalog name   ty plugins add slack        (see ` + "`ty plugins search`" + `)
  owner/repo       ty plugins add taskyou/plugins
  a git URL        ty plugins add https://github.com/taskyou/plugins
  a local path     ty plugins add ./my-plugin

Installing by catalog name takes just that plugin, even when it lives inside a
collection repo. Installing a whole repo installs every plugin in it. Re-running
add on something already installed updates it in place.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			subdir, _ := cmd.Flags().GetString("subdir")
			return addPluginCmd(cmd.Context(), args[0], name, subdir)
		},
	}
	addCmd.Flags().String("name", "", "Install under this directory name (default: derived from the source)")
	addCmd.Flags().String("subdir", "", "Install only this subdirectory of the source repo")
	pluginsCmd.AddCommand(addCmd)

	updateCmd := &cobra.Command{
		Use:          "update [name]",
		Aliases:      []string{"upgrade"},
		Short:        "Update an installed plugin (or all of them) from its source",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return updateOnePlugin(cmd.Context(), args[0])
			}
			return updateAllPlugins(cmd.Context())
		},
	}
	pluginsCmd.AddCommand(updateCmd)

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
			dir, inCheckout, err := hooks.Remove(args[0], hooks.DefaultPluginsDir())
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

// loadCatalog returns the catalog entries, printing a one-line warning when every
// remote source was unreachable so a stale listing never looks authoritative.
func loadCatalog(ctx context.Context, refresh bool) []registry.Entry {
	loader := registry.Default()
	res := loader.Load(ctx)
	if refresh {
		res = loader.Refresh(ctx)
	}
	for _, o := range res.Origins {
		if o.Err != nil && o.URL != registry.BundledOrigin {
			fmt.Fprintf(os.Stderr, "warning: catalog %s unavailable (%v); using the copy shipped with ty\n", o.URL, o.Err)
		}
	}
	return res.Entries
}

// installedIDs maps every catalog ID that is already installed to its directory.
func installedIDs(entries []registry.Entry) map[string]string {
	dir := hooks.DefaultPluginsDir()
	plugins, _ := hooks.LoadPlugins(dir)
	out := map[string]string{}
	for _, e := range entries {
		if installedDir, ok := hooks.IsInstalled(dir, e.ID, plugins); ok {
			out[e.ID] = installedDir
		}
	}
	return out
}

func searchCatalog(ctx context.Context, query string, refresh bool) error {
	entries := loadCatalog(ctx, refresh)
	hits := registry.Search(entries, query)
	if len(hits) == 0 {
		fmt.Printf("No plugins match %q.\n", query)
		if guesses := registry.DidYouMean(entries, query, 3); len(guesses) > 0 {
			fmt.Printf("Close by: %s\n", strings.Join(guesses, ", "))
		}
		fmt.Println("Run `ty plugins browse` to see the whole catalog.")
		return nil
	}
	fmt.Printf("%d of %d plugins match %q:\n\n", len(hits), len(entries), query)
	printEntryTable(hits, installedIDs(entries))
	fmt.Println("\nInstall one with: ty plugins add <id>   ·   details: ty plugins info <id>")
	return nil
}

func browseCatalog(ctx context.Context, refresh bool) error {
	entries := loadCatalog(ctx, refresh)
	if len(entries) == 0 {
		fmt.Println("The plugin catalog is empty.")
		return nil
	}
	installed := installedIDs(entries)
	fmt.Printf("%d plugins available (%d installed):\n", len(entries), len(installed))
	// Column widths measured across the WHOLE catalog, so the category groups line
	// up with each other instead of each finding its own alignment.
	cols := entryColumns(entries)
	for _, category := range append(registry.Categories(entries), "") {
		var group []registry.Entry
		for _, e := range entries {
			if e.Category == category {
				group = append(group, e)
			}
		}
		if len(group) == 0 {
			continue
		}
		label := category
		if label == "" {
			label = "other"
		}
		fmt.Printf("\n%s\n", strings.ToUpper(label))
		printEntryRows(group, installed, cols)
	}
	fmt.Println("\nInstall one with: ty plugins add <id>   ·   details: ty plugins info <id>")
	return nil
}

// entryCols are the measured widths of the id and name columns.
type entryCols struct{ id, name int }

func entryColumns(entries []registry.Entry) entryCols {
	var cols entryCols
	for _, e := range entries {
		cols.id = max(cols.id, len([]rune(e.ID)))
		cols.name = max(cols.name, len([]rune(e.DisplayName())))
	}
	return cols
}

// printEntryTable renders entries as an aligned id/name/description table with an
// installed marker.
func printEntryTable(entries []registry.Entry, installed map[string]string) {
	printEntryRows(entries, installed, entryColumns(entries))
}

func printEntryRows(entries []registry.Entry, installed map[string]string, cols entryCols) {
	for _, e := range entries {
		mark := " "
		if _, ok := installed[e.ID]; ok {
			mark = "✓"
		}
		fmt.Printf("  %s %-*s  %-*s  %s\n", mark,
			cols.id, e.ID, cols.name, e.DisplayName(), firstSentence(e.Description, 72))
	}
}

// firstSentence trims a description to one readable line for table output.
func firstSentence(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i > 0 && i < limit {
		return s[:i+1]
	}
	if len(s) > limit {
		return strings.TrimSpace(s[:limit-1]) + "…"
	}
	return s
}

func pluginInfo(ctx context.Context, id string) error {
	entries := loadCatalog(ctx, false)
	dir := hooks.DefaultPluginsDir()
	plugins, _ := hooks.LoadPlugins(dir)

	entry, inCatalog := registry.Find(entries, id)
	var local *hooks.Plugin
	for i := range plugins {
		if strings.EqualFold(plugins[i].Name, id) || strings.EqualFold(filepath.Base(plugins[i].Dir), id) {
			local = &plugins[i]
			break
		}
	}
	if !inCatalog && local == nil {
		if guesses := registry.DidYouMean(entries, id, 3); len(guesses) > 0 {
			return fmt.Errorf("no plugin %q, installed or in the catalog — did you mean %s?", id, strings.Join(guesses, ", "))
		}
		return fmt.Errorf("no plugin %q, installed or in the catalog (run `ty plugins browse`)", id)
	}

	if inCatalog {
		fmt.Printf("%s (%s)\n", entry.DisplayName(), entry.ID)
		fmt.Printf("  %s\n\n", strings.Join(strings.Fields(entry.Description), " "))
		if entry.Author != "" {
			fmt.Printf("  author     %s\n", entry.Author)
		}
		if entry.Category != "" {
			fmt.Printf("  category   %s\n", entry.Category)
		}
		if len(entry.Tags) > 0 {
			fmt.Printf("  tags       %s\n", strings.Join(entry.Tags, ", "))
		}
		if len(entry.Provides) > 0 {
			fmt.Printf("  provides   %s\n", strings.Join(entry.Provides, ", "))
		}
		src := entry.Source
		if entry.Subdir != "" {
			src += " (" + entry.Subdir + ")"
		}
		fmt.Printf("  source     %s\n", src)
		if entry.Homepage != "" {
			fmt.Printf("  homepage   %s\n", entry.Homepage)
		}
		for i, r := range entry.Requires {
			label := "requires"
			if i > 0 {
				label = "        "
			}
			fmt.Printf("  %s   %s\n", label, r)
		}
	}

	if local == nil {
		fmt.Printf("\nNot installed. Install it with: ty plugins add %s\n", entry.ID)
		return nil
	}
	fmt.Printf("\nInstalled at %s\n", local.Dir)
	printPluginProvides(*local, "  ")
	if src, ok := hooks.LoadSources(dir)[filepath.Base(local.Dir)]; ok {
		fmt.Printf("  from       %s", src.Source)
		if src.Subdir != "" {
			fmt.Printf(" (%s)", src.Subdir)
		}
		if src.InstalledAt != "" {
			fmt.Printf(" · %s", src.InstalledAt)
		}
		fmt.Println()
	}
	return nil
}

func addPluginCmd(ctx context.Context, arg, name, subdir string) error {
	entries := loadCatalog(ctx, false)
	req, err := hooks.Resolve(ctx, arg, entries)
	if err != nil {
		return err
	}
	if name != "" {
		req.Name = name
	}
	if subdir != "" {
		req.Subdir = subdir
	}
	fmt.Printf("Installing from %s…\n", req.Source)
	res, err := hooks.Install(ctx, hooks.DefaultPluginsDir(), req)
	if err != nil {
		return err
	}
	fmt.Printf("%s %d plugin(s): %s\n", res.Verb(), len(res.Plugins), strings.Join(res.Plugins, ", "))
	printPostInstallHints(res)
	return nil
}

// printPostInstallHints tells the user what they can now *do*, which is the step
// an install that just says "Installed" leaves them to guess at. With several
// plugins in one repo each gets its own heading, so the commands are attributable.
func printPostInstallHints(res hooks.InstallResult) {
	plugins, _ := hooks.LoadPlugins(hooks.DefaultPluginsDir())
	multi := len(res.Plugins) > 1
	for _, name := range res.Plugins {
		for _, p := range plugins {
			if p.Name != name {
				continue
			}
			indent := "  "
			if multi {
				fmt.Printf("  %s\n", p.Name)
				indent = "    "
			}
			printPluginProvides(p, indent)
		}
	}
}

// printPluginProvides lists a plugin's capabilities and how to invoke each,
// indented by prefix so it sits correctly under either a bare heading or a
// plugin's own indented entry in `plugins list`.
func printPluginProvides(p hooks.Plugin, prefix string) {
	events := make([]string, 0, len(p.Hooks))
	for e := range p.Hooks {
		events = append(events, e)
	}
	sort.Strings(events)
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, e := range events {
		fmt.Fprintf(tw, "%shook\t%s\t→ %s\n", prefix, e, p.Hooks[e])
	}
	for _, a := range p.Actions {
		fmt.Fprintf(tw, "%saction\t%s\tty plugins run %s %s\n", prefix, a.DisplayLabel(), p.Name, a.ID)
	}
	for _, w := range p.Workflows {
		fmt.Fprintf(tw, "%sworkflow\t%s\tty pipeline -d %s \"<goal>\"\n", prefix, w, w)
	}
	for _, s := range p.Services {
		fmt.Fprintf(tw, "%sservice\t%s\tsupervised while the daemon runs\n", prefix, s.Name)
	}
	for _, r := range p.Routines {
		fmt.Fprintf(tw, "%sroutine\t%s\tty run %s\n", prefix, r, r)
	}
	_ = tw.Flush()
}

func updateOnePlugin(ctx context.Context, name string) error {
	res, err := hooks.Update(ctx, hooks.DefaultPluginsDir(), name)
	if err != nil {
		return err
	}
	fmt.Printf("Updated %s (%s).\n", name, strings.Join(res.Plugins, ", "))
	return nil
}

func updateAllPlugins(ctx context.Context) error {
	dir := hooks.DefaultPluginsDir()
	plugins, _ := hooks.LoadPlugins(dir)
	if len(plugins) == 0 {
		fmt.Printf("No plugins installed in %s.\n", dir)
		return nil
	}
	// Update by install root, not by manifest name: one `plugins add` of a
	// collection repo is one checkout holding many plugins, and pulling it once
	// updates all of them.
	seen := map[string]bool{}
	var failures int
	for _, p := range plugins {
		root, _, ok := hooks.InstallRoot(dir, p.Dir)
		if !ok {
			root = p.Dir
		}
		if seen[root] {
			continue
		}
		seen[root] = true
		res, err := hooks.Update(ctx, dir, p.Name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", filepath.Base(root), err)
			failures++
			continue
		}
		fmt.Printf("  %s updated (%s)\n", filepath.Base(root), strings.Join(res.Plugins, ", "))
	}
	if failures > 0 {
		return fmt.Errorf("%d plugin(s) could not be updated", failures)
	}
	return nil
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
		fmt.Println("Find one with `ty plugins browse` or `ty plugins search <term>`, then " +
			"`ty plugins add <id>`. In the TUI, press m.")
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
		printPluginProvides(p, "    ")
		fmt.Println()
	}
	fmt.Println("More: ty plugins browse · ty plugins search <term> · ty plugins update")
}
