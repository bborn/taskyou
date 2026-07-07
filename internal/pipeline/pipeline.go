// Package pipeline builds multi-step "workflow" tasks: a single goal is broken
// into a small DAG of step tasks that run on one shared git branch and advance
// automatically. Steps can be sequential (Code waits for Plan) or parallel (two
// reviewers run at once), and a step can join several predecessors (Collect waits
// for both reviews) — exactly the plan → code → parallel-review → collect shape
// the herdr plugin popularized.
//
// It's assembled from primitives TaskYou already has, so there's no bespoke
// execution engine — the normal daemon runs each step task:
//
//   - Per-task Executor + Model overrides give each step its own agent.
//   - Task dependencies with auto_queue encode the DAG: a step is 'blocked' until
//     all its dependencies complete, then db.ProcessCompletedBlocker queues it.
//     Fan-out (two steps depending on one) and join (one step depending on two)
//     both fall out of the dependency graph for free.
//   - The root step pins a shared BranchName; every other step checks that branch
//     out via SourceBranch, so steps hand work forward through git.
//
// The workflow advances with no human in the loop; it only pauses when a step
// genuinely needs one — the terminal step opens a PR (landing in 'blocked' for a
// human merge) or a step calls taskyou_needs_input.
package pipeline

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/bborn/workflow/internal/db"
)

// Step is one node of a workflow DAG. Executor and Model pick the agent that runs
// it; Instruction is the task body template ({{goal}} and {{branch}} are
// substituted at build time); Deps names the steps that must finish first.
type Step struct {
	Name        string   // Unique label, e.g. "Plan", "Code", "Review A", "Collect".
	Executor    string   // Executor slug (db.ExecutorClaude, db.ExecutorCodex, ...).
	Model       string   // Per-task model override ("" = the executor's default).
	Instruction string   // Body template with {{goal}} and {{branch}} placeholders.
	Deps        []string // Names of steps that must complete before this one runs.
}

// Definition is a named workflow DAG.
type Definition struct {
	Name        string
	Description string
	Steps       []Step
}

// DefaultDefinition is the definition used when none is named.
const DefaultDefinition = "plan-code-review"

// planCodeReview is the herdr flow as a DAG: Plan → Code → two independent
// reviewers in parallel → Collect. The parallel reviewers are the point — two
// agents with independent contexts catch different classes of issue and avoid
// single-context self-review bias.
//
// Defaults are all Claude (so it runs anywhere with Claude and never silently
// swaps executors), but each step's executor/model is configurable per project
// (see config.go): point one reviewer at codex — `pipeline config --set
// "Review B=codex"` — for the herdr-style cross-executor review, or add a QA step.
var planCodeReview = Definition{
	Name:        "plan-code-review",
	Description: "Plan → code → two parallel reviewers → collect, on one shared branch. Each step's model/executor is configurable per project.",
	Steps: []Step{
		{Name: "Plan", Executor: db.ExecutorClaude, Model: db.ModelOpus, Instruction: planInstruction},
		{Name: "Code", Executor: db.ExecutorClaude, Model: db.ModelSonnet, Instruction: codeInstruction, Deps: []string{"Plan"}},
		{Name: "Review A", Executor: db.ExecutorClaude, Model: db.ModelOpus, Instruction: reviewInstruction, Deps: []string{"Code"}},
		{Name: "Review B", Executor: db.ExecutorClaude, Model: db.ModelSonnet, Instruction: reviewInstruction, Deps: []string{"Code"}},
		{Name: "Collect", Executor: db.ExecutorClaude, Model: db.ModelSonnet, Instruction: collectInstruction, Deps: []string{"Review A", "Review B"}},
	},
}

var definitions = map[string]Definition{
	planCodeReview.Name: planCodeReview,
}

// Definitions returns all built-in workflow definitions.
func Definitions() []Definition {
	return []Definition{planCodeReview}
}

// DefinitionNames returns the names of all built-in definitions.
func DefinitionNames() []string {
	out := make([]string, 0, len(definitions))
	for _, d := range Definitions() {
		out = append(out, d.Name)
	}
	return out
}

// Get returns the named definition. An empty name resolves to DefaultDefinition.
func Get(name string) (Definition, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = DefaultDefinition
	}
	def, ok := definitions[name]
	return def, ok
}

// Roots returns the steps with no dependencies (the entry points).
func (d Definition) Roots() []Step {
	var roots []Step
	for _, s := range d.Steps {
		if len(s.Deps) == 0 {
			roots = append(roots, s)
		}
	}
	return roots
}

// validate checks the DAG is well-formed: unique step names, deps that reference
// real steps, exactly one root (the branch owner), and no cycles.
func (d Definition) validate() error {
	if len(d.Steps) == 0 {
		return fmt.Errorf("workflow %q has no steps", d.Name)
	}
	byName := make(map[string]Step, len(d.Steps))
	for _, s := range d.Steps {
		if _, dup := byName[s.Name]; dup {
			return fmt.Errorf("workflow %q has duplicate step %q", d.Name, s.Name)
		}
		byName[s.Name] = s
	}
	for _, s := range d.Steps {
		for _, dep := range s.Deps {
			if _, ok := byName[dep]; !ok {
				return fmt.Errorf("step %q depends on unknown step %q", s.Name, dep)
			}
		}
	}
	if roots := d.Roots(); len(roots) != 1 {
		return fmt.Errorf("workflow %q must have exactly one root step (found %d)", d.Name, len(roots))
	}
	if err := d.checkAcyclic(byName); err != nil {
		return err
	}
	return nil
}

func (d Definition) checkAcyclic(byName map[string]Step) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(byName))
	var visit func(name string) error
	visit = func(name string) error {
		switch color[name] {
		case gray:
			return fmt.Errorf("workflow %q has a dependency cycle at %q", d.Name, name)
		case black:
			return nil
		}
		color[name] = gray
		for _, dep := range byName[name].Deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		color[name] = black
		return nil
	}
	for name := range byName {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

// Options configures a workflow build.
type Options struct {
	Goal           string // The overall goal, threaded into every step's prompt.
	Project        string // Project the step tasks belong to (must already exist).
	Definition     string // Definition name; "" resolves to DefaultDefinition.
	PermissionMode string // Permission mode for every step ("" inherits project default).
	Execute        bool   // If true, queue the root step so the workflow starts now.
}

// Result describes a built workflow.
type Result struct {
	Definition Definition
	Branch     string     // The shared branch every step runs on.
	Tasks      []*db.Task // Step tasks, in definition order.
}

// Create builds a workflow: one task per step, wired into a dependency DAG on a
// shared branch. The root step is created (and, with Execute, queued) as a normal
// task; every other step is created 'blocked' with dependencies on its
// predecessors, so db.ProcessCompletedBlocker queues it once they all complete.
func Create(database *db.DB, opts Options) (*Result, error) {
	goal := strings.TrimSpace(opts.Goal)
	if goal == "" {
		return nil, fmt.Errorf("workflow goal is required")
	}
	if strings.TrimSpace(opts.Project) == "" {
		return nil, fmt.Errorf("workflow project is required")
	}
	def, ok := Get(opts.Definition)
	if !ok {
		return nil, fmt.Errorf("unknown workflow definition %q (available: %s)", opts.Definition, strings.Join(DefinitionNames(), ", "))
	}
	if err := def.validate(); err != nil {
		return nil, err
	}

	// Apply the project's saved per-step executor/model choices.
	steps := EffectiveSteps(database, opts.Project, def)
	rootName := def.Roots()[0].Name
	slug := slugify(goal, 40)

	// Create every step task first (in 'backlog', so the daemon leaves them alone),
	// recording name → task so we can wire dependencies and pin the branch.
	byName := make(map[string]*db.Task, len(steps))
	tasks := make([]*db.Task, 0, len(steps))
	for _, s := range steps {
		task := &db.Task{
			Title:          stepTitle(s.Name, goal),
			Body:           goal, // Placeholder; rewritten once the branch is known.
			Status:         db.StatusBacklog,
			Type:           db.TypeCode,
			Project:        opts.Project,
			Executor:       s.Executor,
			Model:          s.Model,
			PermissionMode: opts.PermissionMode,
			Tags:           "pipeline",
		}
		if err := database.CreateTask(task); err != nil {
			return nil, fmt.Errorf("create %s step: %w", s.Name, err)
		}
		byName[s.Name] = task
		tasks = append(tasks, task)
	}

	// Seed the shared branch from the root task's id so it's unique.
	root := byName[rootName]
	branch := fmt.Sprintf("pipeline/%d-%s", root.ID, slug)

	// Configure each task: render its prompt, pin the branch on the root and point
	// every other step at it via SourceBranch.
	for _, s := range steps {
		task := byName[s.Name]
		task.Body = render(s.Instruction, goal, branch, s.Name, reviewsList(s, branch))
		if s.Name == rootName {
			task.BranchName = branch // Pinned; the executor creates this branch.
		} else {
			task.SourceBranch = branch // Checked out from the shared branch.
		}
		if err := database.UpdateTask(task); err != nil {
			return nil, fmt.Errorf("configure %s step: %w", s.Name, err)
		}
	}

	// Wire the DAG: each dependency blocks the dependent, auto-queuing it on completion.
	for _, s := range steps {
		for _, dep := range s.Deps {
			if err := database.AddDependency(byName[dep].ID, byName[s.Name].ID, true); err != nil {
				return nil, fmt.Errorf("wire %s → %s: %w", dep, s.Name, err)
			}
		}
	}

	// Set statuses last, once the graph is fully wired: non-root steps wait
	// 'blocked' on their deps; the root starts (queued) or stages (backlog).
	for _, s := range steps {
		if s.Name == rootName {
			continue
		}
		if err := database.UpdateTaskStatus(byName[s.Name].ID, db.StatusBlocked); err != nil {
			return nil, fmt.Errorf("block %s step: %w", s.Name, err)
		}
		byName[s.Name].Status = db.StatusBlocked
	}
	if opts.Execute {
		if err := database.UpdateTaskStatus(root.ID, db.StatusQueued); err != nil {
			return nil, fmt.Errorf("queue %s step: %w", rootName, err)
		}
		root.Status = db.StatusQueued
	}

	return &Result{Definition: def, Branch: branch, Tasks: tasks}, nil
}

// stepTitle builds a task title like "[Plan] <goal>", trimming a long goal so the
// board card stays readable.
func stepTitle(step, goal string) string {
	const maxGoal = 60
	g := strings.TrimSpace(goal)
	if len(g) > maxGoal {
		g = strings.TrimSpace(g[:maxGoal]) + "…"
	}
	return fmt.Sprintf("[%s] %s", step, g)
}

// render substitutes the {{goal}}, {{branch}}, {{step}}, {{stepslug}} and
// {{reviews}} placeholders in a step template.
func render(tmpl, goal, branch, step, reviews string) string {
	r := strings.NewReplacer(
		"{{goal}}", goal,
		"{{branch}}", branch,
		"{{step}}", step,
		"{{stepslug}}", slugify(step, 40),
		"{{reviews}}", reviews,
	)
	return strings.TrimSpace(r.Replace(tmpl))
}

// reviewsList describes, for a step, where each of its dependency steps pushed its
// review: one bullet per dep naming the review branch and file. It's substituted
// into the Collect step's {{reviews}} placeholder so Collect reads exactly the
// reviewers that fed it, regardless of how the workflow is configured.
func reviewsList(step Step, branch string) string {
	var b strings.Builder
	for _, dep := range step.Deps {
		s := slugify(dep, 40)
		fmt.Fprintf(&b, "     - `%s-%s` → file `review-%s.md`\n", branch, s, s)
	}
	return strings.TrimRight(b.String(), "\n")
}

// slugify converts a string into a lowercase, dash-separated slug, truncated to
// maxLen. It mirrors the executor's worktree slug so workflow branch names read
// like ordinary task branches.
func slugify(s string, maxLen int) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteRune('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if maxLen > 0 && len(slug) > maxLen {
		slug = strings.Trim(slug[:maxLen], "-")
	}
	if slug == "" {
		slug = "pipeline"
	}
	return slug
}
