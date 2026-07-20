package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/pipeline"
)

// A workflow is N step tasks sharing one branch, but `ty list` renders each step
// as its own flat row with no indication that they belong together, which one is
// live, or how far along the run is. Reading a workflow's state meant querying
// the DB by hand. These commands render a workflow as the DAG it actually is.

// workflowStepMark is the leading glyph for a step, chosen by status so the
// current frontier is findable at a glance.
func workflowStepMark(t *db.Task) string {
	switch t.Status {
	case db.StatusDone:
		return "✓"
	case db.StatusProcessing:
		return "▶"
	case db.StatusQueued:
		return "»"
	case db.StatusBlocked:
		return "⏸"
	default:
		return "·"
	}
}

// loadWorkflowGroups returns every workflow group, most-recently-active first.
// Closed tasks are included: a workflow is only legible with its finished steps
// shown, and most steps of a healthy run are done.
func loadWorkflowGroups(database *db.DB, project string) ([]*pipeline.Group, error) {
	tasks, err := database.ListTasks(db.ListTasksOptions{
		Project:       project,
		IncludeClosed: true,
		Limit:         5000,
	})
	if err != nil {
		return nil, err
	}
	groups, _ := pipeline.GroupWorkflows(tasks)
	// Most-recently-touched workflow first — the one you're most likely asking about.
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].Members[len(groups[i].Members)-1].ID > groups[j].Members[len(groups[j].Members)-1].ID
	})
	return groups, nil
}

// findWorkflowGroup resolves a workflow by any member's task ID or by (a prefix
// of) its branch, so `ty workflow show 4891` and `ty workflow show pipeline/4887`
// both work.
func findWorkflowGroup(groups []*pipeline.Group, ref string) *pipeline.Group {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if id, err := strconv.ParseInt(strings.TrimPrefix(ref, "#"), 10, 64); err == nil {
		for _, g := range groups {
			for _, m := range g.Members {
				if m.ID == id {
					return g
				}
			}
		}
	}
	for _, g := range groups {
		if g.Branch == ref {
			return g
		}
	}
	for _, g := range groups {
		if strings.Contains(g.Branch, ref) {
			return g
		}
	}
	return nil
}

// renderWorkflow prints one workflow as a vertical DAG: every step in order with
// its status, gate/terminal role, and the live step marked, followed by the
// artifacts produced so far.
func renderWorkflow(database *db.DB, g *pipeline.Group) {
	lead := g.Lead()

	// Goals are free-text and often many paragraphs (the whole original ask), so
	// collapse to the first line for the header.
	fmt.Println(boldStyle.Render("Workflow: " + truncate(firstLine(g.Goal()), 72)))
	fmt.Println(dimStyle.Render("Branch:   " + g.Branch))
	project := ""
	if len(g.Members) > 0 {
		project = g.Members[0].Project
	}
	pct := 0
	if g.Total() > 0 {
		pct = g.DoneCount() * 100 / g.Total()
	}
	fmt.Println(dimStyle.Render(fmt.Sprintf("Project:  %s    Progress: %d/%d steps (%d%%)    Now: %s",
		project, g.DoneCount(), g.Total(), pct, g.StepLabel())))
	fmt.Println()

	for _, m := range g.Members {
		mark := workflowStepMark(m)
		name := stepDisplayName(m.Title)

		var role string
		switch {
		case pipeline.IsGateStep(m):
			role = "gate"
		case pipeline.IsTerminalStep(database, m):
			role = "final"
		}

		line := fmt.Sprintf("  %s  #%-5d %-20s %-6s %s", mark, m.ID, truncate(name, 20), role, m.Status)

		// Point at the step the workflow is actually sitting on.
		if lead != nil && m.ID == lead.ID && m.Status != db.StatusDone {
			line += "   ← current"
		}
		// Every step on the shared branch ends up carrying the same PR URL, so
		// showing it per-row repeats one link N times. The PR belongs to the
		// terminal step — show it only there.
		if m.PRURL != "" && role == "final" {
			line += dimStyle.Render("   " + m.PRURL)
		}

		switch m.Status {
		case db.StatusDone:
			fmt.Println(successStyle.Render(line))
		case db.StatusProcessing:
			fmt.Println(boldStyle.Render(line))
		case db.StatusBlocked:
			fmt.Println(line)
		default:
			fmt.Println(dimStyle.Render(line))
		}
	}

	// Artifacts are the workflow's real hand-off payload; showing which exist makes
	// "did the design phase actually produce anything?" answerable without sqlite.
	if arts, err := database.ListPipelineArtifacts(g.Branch); err == nil && len(arts) > 0 {
		names := make([]string, 0, len(arts))
		for _, a := range arts {
			names = append(names, fmt.Sprintf("%s (%s)", a.Name, humanBytes(len(a.Content))))
		}
		fmt.Println()
		fmt.Println(dimStyle.Render(fmt.Sprintf("Artifacts (%d): %s", len(arts), strings.Join(names, ", "))))
		fmt.Println(dimStyle.Render("Read one with: ty artifact get <name> --task-id " + fmt.Sprint(g.Members[0].ID)))
	}

	// A parked gate is the most common reason a run looks "stuck" but isn't broken,
	// so say plainly what releases it.
	if lead != nil && lead.Status == db.StatusBlocked && pipeline.IsGateStep(lead) {
		fmt.Println()
		fmt.Printf("⏸  Parked at a human gate. Approve with: ty close %d\n", lead.ID)
	}
}

func newWorkflowCmd() *cobra.Command {
	workflowCmd := &cobra.Command{
		Use:     "workflow",
		Aliases: []string{"wf"},
		Short:   "Inspect workflow (pipeline) runs and where they are in the flow",
	}

	workflowListCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List workflow runs with progress and current step",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			project, _ := cmd.Flags().GetString("project")
			groups, err := loadWorkflowGroups(database, project)
			if err != nil {
				return err
			}
			active, _ := cmd.Flags().GetBool("active")
			if len(groups) == 0 {
				fmt.Println("No workflows found.")
				return nil
			}
			shown := 0
			for _, g := range groups {
				if active && g.DoneCount() >= g.Total() {
					continue
				}
				lead := g.Lead()
				leadID := int64(0)
				if lead != nil {
					leadID = lead.ID
				}
				fmt.Printf("#%-5d %-12s %2d/%-2d  %-14s %s\n",
					leadID, g.Members[0].Project, g.DoneCount(), g.Total(),
					truncate(g.StepLabel(), 14), truncate(firstLine(g.Goal()), 46))
				shown++
			}
			if shown == 0 {
				fmt.Println("No active workflows (use without --active to include finished runs).")
			}
			return nil
		},
	}
	workflowListCmd.Flags().StringP("project", "p", "", "Filter by project")
	workflowListCmd.Flags().Bool("active", false, "Only workflows that haven't finished")

	workflowShowCmd := &cobra.Command{
		Use:   "show [task-id|branch]",
		Short: "Show a workflow's steps, current position, and artifacts",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			groups, err := loadWorkflowGroups(database, "")
			if err != nil {
				return err
			}
			if len(groups) == 0 {
				return fmt.Errorf("no workflows found")
			}

			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			var g *pipeline.Group
			if ref == "" {
				// No argument: show the most recently active run.
				g = groups[0]
			} else {
				g = findWorkflowGroup(groups, ref)
			}
			if g == nil {
				return fmt.Errorf("no workflow found matching %q (try: ty workflow list)", ref)
			}
			renderWorkflow(database, g)
			return nil
		},
	}

	workflowCmd.AddCommand(workflowListCmd, workflowShowCmd)
	return workflowCmd
}

// stepDisplayName is the step label ("design") from a "[design] goal" title,
// falling back to a trimmed title for steps that carry no bracketed label.
func stepDisplayName(title string) string {
	if n := stepNameFromTitle(title); n != "" {
		return n
	}
	return truncate(strings.TrimSpace(title), 20)
}

// stepNameFromTitle mirrors pipeline's unexported stepName: "[design] x" -> "design".
func stepNameFromTitle(title string) string {
	title = strings.TrimSpace(title)
	if strings.HasPrefix(title, "[") {
		if end := strings.Index(title, "]"); end > 1 {
			return title[1:end]
		}
	}
	return ""
}

func humanBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	return fmt.Sprintf("%.1fKB", float64(n)/1024)
}

// firstLine reduces a free-text goal/title to its first non-empty line, so a
// multi-paragraph ask renders as a single readable row.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return strings.TrimSpace(s)
}
