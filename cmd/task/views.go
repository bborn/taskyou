package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/taskfilter"
)

// Saved views on the CLI are the same rows the TUI's `V` picker manages: a name
// and a filter query. Keeping them here — rather than only in the TUI — is what
// lets `ty list --view active` mean exactly what the board means, and lets a
// script or an agent define a view without opening a terminal UI.

func newViewsCmd() *cobra.Command {
	viewsCmd := &cobra.Command{
		Use:   "views",
		Short: "Manage saved task views",
		Long: `List, save, and delete named filter views.

A view is a name plus a filter query. The same query works in the TUI filter
bar (press /), in the saved-view picker (press V), and here.

Query grammar:
  status:blocked      is:blocked        only blocked tasks
  status:in-progress  is:in-progress    queued + processing
  status:open         is:open           anything not done/archived
  is:pinned           is:unpinned       pin state
  is:workflow         is:task           workflow steps vs standalone tasks
  has:pr              no:pr             tasks with/without a pull request
  tag:release                           tasks carrying a tag
  [project]                             project tag (repeatable, OR'd)
  anything else                         free-text search

Examples:
  ty views                                              # List saved views
  ty views save active "status:in-progress status:blocked"
  ty views save offerlab "[offerlab] status:open"
  ty views show active
  ty views delete active
  ty list --view active`,
		Run: func(cmd *cobra.Command, args []string) {
			listViewsCLI(cmd)
		},
	}
	viewsCmd.Flags().Bool("json", false, "Output in JSON format")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List saved views",
		Run: func(cmd *cobra.Command, args []string) {
			listViewsCLI(cmd)
		},
	}
	listCmd.Flags().Bool("json", false, "Output in JSON format")
	viewsCmd.AddCommand(listCmd)

	showCmd := &cobra.Command{
		Use:               "show <name>",
		Short:             "Show a saved view and the tasks it matches",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeViewNames,
		Run: func(cmd *cobra.Command, args []string) {
			showViewCLI(cmd, args[0])
		},
	}
	showCmd.Flags().Bool("json", false, "Output in JSON format")
	viewsCmd.AddCommand(showCmd)

	saveCmd := &cobra.Command{
		Use:   "save <name> <query>",
		Short: "Create or replace a saved view",
		Long: `Create a saved view, or replace the query of one that already exists.

Examples:
  ty views save active "status:in-progress status:blocked"
  ty views save pinned is:pinned
  ty views save offerlab "[offerlab]"`,
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: completeViewNames,
		Run: func(cmd *cobra.Command, args []string) {
			saveViewCLI(args[0], strings.Join(args[1:], " "))
		},
	}
	viewsCmd.AddCommand(saveCmd)

	deleteCmd := &cobra.Command{
		Use:               "delete <name>",
		Short:             "Delete a saved view",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeViewNames,
		Run: func(cmd *cobra.Command, args []string) {
			deleteViewCLI(args[0])
		},
	}
	viewsCmd.AddCommand(deleteCmd)

	return viewsCmd
}

// openViewDB opens the task database or exits with a message.
func openViewDB() *db.DB {
	database, err := openTaskDB(db.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	return database
}

func listViewsCLI(cmd *cobra.Command) {
	outputJSON, _ := cmd.Flags().GetBool("json")

	database := openViewDB()
	defer database.Close()

	views, err := database.ListSavedViews()
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	if outputJSON {
		if views == nil {
			views = []*db.SavedView{}
		}
		jsonBytes, _ := json.Marshal(views)
		fmt.Println(string(jsonBytes))
		return
	}

	if len(views) == 0 {
		fmt.Println(dimStyle.Render(`No saved views. Create one: ty views save active "status:in-progress status:blocked"`))
		return
	}

	fmt.Println(boldStyle.Render("Saved views"))
	fmt.Println(strings.Repeat("─", 60))
	for _, v := range views {
		query := v.Query
		if query == "" {
			query = "(everything)"
		}
		fmt.Printf("%s  %s\n", boldStyle.Render(v.Name), dimStyle.Render(query))
	}
}

func showViewCLI(cmd *cobra.Command, name string) {
	outputJSON, _ := cmd.Flags().GetBool("json")

	database := openViewDB()
	defer database.Close()

	view, err := database.GetSavedView(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if view == nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("View %q not found", name)))
		os.Exit(1)
	}

	tasks, err := tasksForView(database, view)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}

	if outputJSON {
		items := make([]map[string]interface{}, 0, len(tasks))
		for _, t := range tasks {
			items = append(items, map[string]interface{}{
				"id":      t.ID,
				"title":   t.Title,
				"status":  t.Status,
				"project": t.Project,
				"pinned":  t.Pinned,
				"pr_url":  t.PRURL,
			})
		}
		jsonBytes, _ := json.Marshal(map[string]interface{}{
			"name":       view.Name,
			"query":      view.Query,
			"task_count": len(tasks),
			"tasks":      items,
		})
		fmt.Println(string(jsonBytes))
		return
	}

	fmt.Printf("%s  %s\n", boldStyle.Render(view.Name), dimStyle.Render(view.Query))
	fmt.Println(strings.Repeat("─", 60))
	if len(tasks) == 0 {
		fmt.Println(dimStyle.Render("No tasks match this view"))
		return
	}
	for _, t := range tasks {
		fmt.Printf("  #%-6d %-12s %s\n", t.ID, t.Status, t.Title)
	}
}

func saveViewCLI(name, query string) {
	database := openViewDB()
	defer database.Close()

	view, err := database.SaveView(name, query)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	fmt.Printf("%s Saved view %s: %s\n",
		successStyle.Render("✓"), boldStyle.Render(view.Name), dimStyle.Render(view.Query))
}

func deleteViewCLI(name string) {
	database := openViewDB()
	defer database.Close()

	view, err := database.GetSavedView(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if view == nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("View %q not found", name)))
		os.Exit(1)
	}
	if err := database.DeleteSavedView(view.Name); err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	fmt.Printf("%s Deleted view %s\n", successStyle.Render("✓"), boldStyle.Render(view.Name))
}

// resolveViewQuery turns a --view name into its stored query, exiting with a
// clear message when the name is unknown. An unknown view must never silently
// degrade into "no filter": that would list every task and look like the view
// simply matched everything.
func resolveViewQuery(database *db.DB, name string) string {
	view, err := database.GetSavedView(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
	if view == nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(
			fmt.Sprintf("View %q not found. List them with: ty views", name)))
		os.Exit(1)
	}
	return view.Query
}

// parseViewQuery parses a filter query and resolves its [project] tags through
// the projects table, so a view written as "[ol]" matches the offerlab project.
func parseViewQuery(database *db.DB, query string) taskfilter.Query {
	return taskfilter.Parse(query).ResolveProjects(func(name string) string {
		if p, err := database.GetProjectByName(name); err == nil && p != nil {
			return p.Name
		}
		return ""
	})
}

// tasksForView returns every task a view matches. It reads the whole task list
// (including closed tasks) because a view is free to ask for done ones.
func tasksForView(database *db.DB, view *db.SavedView) ([]*db.Task, error) {
	tasks, err := database.ListTasks(db.ListTasksOptions{IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	return parseViewQuery(database, view.Query).Filter(tasks), nil
}

// completeViewNames provides shell completion for saved view names.
func completeViewNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	database, err := openTaskDB(db.DefaultPath())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer database.Close()

	views, err := database.ListSavedViews()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, v := range views {
		if strings.HasPrefix(strings.ToLower(v.Name), strings.ToLower(toComplete)) {
			names = append(names, v.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
