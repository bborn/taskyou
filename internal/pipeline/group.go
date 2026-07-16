package pipeline

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// A workflow's step tasks would otherwise each show as their own board card,
// cluttering the board with N cards for one goal. Group collapses a workflow's
// member tasks so the board can render a single card — the workflow's current
// frontier — badged with progress.

// Group is a workflow's member tasks, keyed by their shared branch.
type Group struct {
	Branch  string
	Members []*db.Task // in step (creation) order
}

// groupKey is a task's workflow key: the shared branch it runs on. The root step
// carries it as BranchName; every other step as SourceBranch.
func groupKey(t *db.Task) string {
	if s := strings.TrimSpace(t.SourceBranch); s != "" {
		return s
	}
	return strings.TrimSpace(t.BranchName)
}

// GroupKey exports groupKey so callers outside this package (e.g. the MCP
// artifact handlers) can derive a workflow task's shared-branch key without
// re-implementing the SourceBranch-else-BranchName rule.
func GroupKey(t *db.Task) string { return groupKey(t) }

// IsWorkflowTask reports whether a task belongs to a workflow (tagged "pipeline"
// with a shared branch).
func IsWorkflowTask(t *db.Task) bool {
	return t != nil && hasPipelineTag(t.Tags) && groupKey(t) != ""
}

func hasPipelineTag(tags string) bool {
	for _, tag := range strings.Split(tags, ",") {
		if strings.EqualFold(strings.TrimSpace(tag), "pipeline") {
			return true
		}
	}
	return false
}

// TerminalStepParkedLog is the system-log line marking a finished terminal step as
// parked in 'blocked' awaiting a human merge. The Stop hook writes it when it catches
// the step finished; the daemon sweep writes it (once) when the Stop hook fired mid-
// push and left the generic "waiting for input" state instead. Shared so both paths
// use the same text and the sweep can dedupe against it.
const TerminalStepParkedLog = "Final workflow step finished — parked in 'blocked' for a human to review and merge."

// IsTerminalStep reports whether a workflow step is the sink — the step nothing
// else depends on, which is the one that opens the PR (see composeInstruction).
// A finished terminal step parks in 'blocked' awaiting a human merge review rather
// than advancing to 'done'; every other step goes 'done' so its dependents
// auto-queue. Non-workflow tasks are never terminal steps.
func IsTerminalStep(database *db.DB, task *db.Task) bool {
	if !IsWorkflowTask(task) {
		return false
	}
	dependents, err := database.GetBlockedBy(task.ID)
	if err != nil {
		return false
	}
	return len(dependents) == 0
}

// GroupWorkflows partitions tasks into workflow groups (2+ members sharing a
// branch) and the remaining ungrouped tasks, preserving input order for the rest.
func GroupWorkflows(tasks []*db.Task) (groups []*Group, rest []*db.Task) {
	idx := make(map[string]*Group)
	var order []string
	for _, t := range tasks {
		if !IsWorkflowTask(t) {
			rest = append(rest, t)
			continue
		}
		key := groupKey(t)
		g := idx[key]
		if g == nil {
			g = &Group{Branch: key}
			idx[key] = g
			order = append(order, key)
		}
		g.Members = append(g.Members, t)
	}
	for _, key := range order {
		g := idx[key]
		// A lone member isn't a workflow to collapse — render it normally.
		if len(g.Members) < 2 {
			rest = append(rest, g.Members...)
			continue
		}
		sort.Slice(g.Members, func(i, j int) bool { return g.Members[i].ID < g.Members[j].ID })
		groups = append(groups, g)
	}
	return groups, rest
}

// statusPriority ranks a member's status by how "live" it is, so Lead() surfaces
// the workflow's current frontier.
func statusPriority(status string) int {
	switch status {
	case db.StatusProcessing:
		return 5
	case db.StatusQueued:
		return 4
	case db.StatusBlocked:
		return 3
	case db.StatusBacklog:
		return 2
	case db.StatusDone:
		return 1
	default:
		return 0
	}
}

// Lead returns the member that best represents the workflow's current state — the
// most-live step (processing > queued > blocked > backlog > done), lowest id on a
// tie. The board renders this task's card for the whole workflow.
func (g *Group) Lead() *db.Task {
	if len(g.Members) == 0 {
		return nil
	}
	lead := g.Members[0]
	for _, t := range g.Members[1:] {
		if statusPriority(t.Status) > statusPriority(lead.Status) {
			lead = t
		}
	}
	return lead
}

// Total returns the number of steps.
func (g *Group) Total() int { return len(g.Members) }

// DoneCount returns how many steps have completed.
func (g *Group) DoneCount() int {
	n := 0
	for _, t := range g.Members {
		if t.Status == db.StatusDone {
			n++
		}
	}
	return n
}

// Goal returns the workflow goal, recovered by stripping the "[Step] " prefix off
// a member title.
func (g *Group) Goal() string {
	if len(g.Members) == 0 {
		return ""
	}
	return stripStepPrefix(g.Members[0].Title)
}

// ActiveSteps returns the step names currently processing or queued (there can be
// two during the parallel review fan-out).
func (g *Group) ActiveSteps() []string {
	var names []string
	for _, t := range g.Members {
		if t.Status == db.StatusProcessing || t.Status == db.StatusQueued {
			if n := stepName(t.Title); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

// StepLabel is a short, board-friendly description of the workflow's current
// position: the active step name, "<prefix> ∥" when several steps run in parallel
// (e.g. "Review ∥"), or "done" when every step has completed.
func (g *Group) StepLabel() string {
	active := g.ActiveSteps()
	switch len(active) {
	case 0:
		if g.DoneCount() >= g.Total() {
			return "done"
		}
		if lead := g.Lead(); lead != nil {
			if n := stepName(lead.Title); n != "" {
				return n
			}
		}
		return "waiting"
	case 1:
		return active[0]
	default:
		// Several steps run at once: collapse to their shared first word when
		// they have one (e.g. "Review A"/"Review B" → "Review ∥").
		prefix := firstWord(active[0])
		for _, a := range active[1:] {
			if firstWord(a) != prefix {
				prefix = ""
				break
			}
		}
		if prefix != "" {
			return prefix + " ∥"
		}
		return fmt.Sprintf("%d ∥", len(active))
	}
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// stepName extracts the step label from a title like "[Plan] add rate limiting".
func stepName(title string) string {
	title = strings.TrimSpace(title)
	if strings.HasPrefix(title, "[") {
		if end := strings.Index(title, "]"); end > 1 {
			return title[1:end]
		}
	}
	return ""
}

// stripStepPrefix removes the leading "[Step] " from a title.
func stripStepPrefix(title string) string {
	title = strings.TrimSpace(title)
	if strings.HasPrefix(title, "[") {
		if end := strings.Index(title, "]"); end > 1 {
			return strings.TrimSpace(title[end+1:])
		}
	}
	return title
}
