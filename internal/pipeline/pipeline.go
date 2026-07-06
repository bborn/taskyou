// Package pipeline builds multi-phase "pipeline" tasks: a single goal is broken
// into an ordered chain of phase tasks (e.g. plan → code → review), each routed
// to its own executor and model, that hand work forward on one shared git branch
// and advance automatically.
//
// The chain is assembled from primitives TaskYou already has:
//
//   - Per-task Executor + Model overrides give each phase a different agent (Opus
//     plans, Sonnet codes, Codex reviews).
//   - Task dependencies with auto_queue chain the phases: phase N blocks phase
//     N+1, and completing N auto-queues N+1 (see db.ProcessCompletedBlocker).
//   - A pinned BranchName on the first phase plus SourceBranch on the rest keeps
//     every phase on one branch, so each phase sees the previous phase's commits.
//
// The result is a "staged pipeline task type" — different phases route to
// different executors automatically — without a bespoke execution engine: the
// normal daemon executor runs each phase task in turn.
package pipeline

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/bborn/workflow/internal/db"
)

// Phase is one stage of a pipeline. Executor and Model pick the agent that runs
// it; Instruction is the task body template, with {{goal}} and {{branch}}
// placeholders substituted at build time.
type Phase struct {
	Name        string // Short label, e.g. "Plan", "Code", "Review".
	Executor    string // Executor slug (db.ExecutorClaude, db.ExecutorCodex, ...).
	Model       string // Per-task model override ("" = the executor's default).
	Instruction string // Body template with {{goal}} and {{branch}} placeholders.
}

// Definition is a named, ordered set of phases.
type Definition struct {
	Name        string
	Description string
	Phases      []Phase
}

// DefaultDefinition is the definition used when none is named.
const DefaultDefinition = "plan-code-review"

// planCodeReview mirrors the herdr plan→code→review flow: Opus plans, Sonnet
// codes, and a different executor (Codex) reviews with fresh eyes. Plan and Code
// run on Claude because they are chain-critical — they must call taskyou_complete
// to advance the pipeline — while Review is the terminal phase and opens the PR.
var planCodeReview = Definition{
	Name:        "plan-code-review",
	Description: "Opus plans, Sonnet codes, Codex reviews — three phases on one shared branch, auto-advancing.",
	Phases: []Phase{
		{Name: "Plan", Executor: db.ExecutorClaude, Model: db.ModelOpus, Instruction: planInstruction},
		{Name: "Code", Executor: db.ExecutorClaude, Model: db.ModelSonnet, Instruction: codeInstruction},
		{Name: "Review", Executor: db.ExecutorCodex, Model: "", Instruction: reviewInstruction},
	},
}

var definitions = map[string]Definition{
	planCodeReview.Name: planCodeReview,
}

// Definitions returns all built-in pipeline definitions.
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

// Options configures a pipeline build.
type Options struct {
	Goal           string // The overall goal, threaded into every phase's prompt.
	Project        string // Project the phase tasks belong to (must already exist).
	Definition     string // Definition name; "" resolves to DefaultDefinition.
	PermissionMode string // Permission mode for every phase ("" inherits project default).
	Execute        bool   // If true, queue the first phase so the pipeline starts now.
}

// Result describes a built pipeline.
type Result struct {
	Definition Definition
	Branch     string     // The shared branch all phases run on.
	Tasks      []*db.Task // Phase tasks in order.
}

// Create builds a pipeline: one phase task per Definition.Phase, wired into a
// dependency chain on a shared branch. The first phase is created (and, when
// Execute is set, queued) as a normal task; each later phase is created 'blocked'
// with a dependency on its predecessor so db.ProcessCompletedBlocker auto-queues
// it the moment the previous phase completes.
func Create(database *db.DB, opts Options) (*Result, error) {
	goal := strings.TrimSpace(opts.Goal)
	if goal == "" {
		return nil, fmt.Errorf("pipeline goal is required")
	}
	if strings.TrimSpace(opts.Project) == "" {
		return nil, fmt.Errorf("pipeline project is required")
	}
	def, ok := Get(opts.Definition)
	if !ok {
		return nil, fmt.Errorf("unknown pipeline definition %q (available: %s)", opts.Definition, strings.Join(DefinitionNames(), ", "))
	}
	if len(def.Phases) == 0 {
		return nil, fmt.Errorf("pipeline definition %q has no phases", def.Name)
	}

	slug := slugify(goal, 40)

	// Phase 1 is created first so its ID can seed a unique, shared branch name.
	first := def.Phases[0]
	firstTask := &db.Task{
		Title:          phaseTitle(first.Name, goal),
		Body:           goal, // Placeholder; rewritten below once the branch is known.
		Status:         db.StatusBacklog,
		Type:           db.TypeCode,
		Project:        opts.Project,
		Executor:       first.Executor,
		Model:          first.Model,
		PermissionMode: opts.PermissionMode,
		Tags:           "pipeline",
	}
	if err := database.CreateTask(firstTask); err != nil {
		return nil, fmt.Errorf("create %s phase: %w", first.Name, err)
	}

	branch := fmt.Sprintf("pipeline/%d-%s", firstTask.ID, slug)

	// Pin the shared branch on phase 1 and render its real instructions. The
	// executor honors a pre-set BranchName when it creates the fresh worktree, so
	// phase 1 owns the branch; later phases check it out via SourceBranch.
	firstTask.BranchName = branch
	firstTask.Body = render(first.Instruction, goal, branch)
	if err := database.UpdateTask(firstTask); err != nil {
		return nil, fmt.Errorf("configure %s phase: %w", first.Name, err)
	}

	tasks := []*db.Task{firstTask}
	prevID := firstTask.ID

	for _, ph := range def.Phases[1:] {
		task := &db.Task{
			Title:          phaseTitle(ph.Name, goal),
			Body:           render(ph.Instruction, goal, branch),
			Status:         db.StatusBlocked, // Waits for its blocker; auto-queued on completion.
			Type:           db.TypeCode,
			Project:        opts.Project,
			Executor:       ph.Executor,
			Model:          ph.Model,
			PermissionMode: opts.PermissionMode,
			SourceBranch:   branch,
			Tags:           "pipeline",
		}
		if err := database.CreateTask(task); err != nil {
			return nil, fmt.Errorf("create %s phase: %w", ph.Name, err)
		}
		if err := database.AddDependency(prevID, task.ID, true); err != nil {
			return nil, fmt.Errorf("chain %s phase: %w", ph.Name, err)
		}
		tasks = append(tasks, task)
		prevID = task.ID
	}

	// Start the pipeline by queueing phase 1. Everything downstream is already
	// wired, so it advances on its own as each phase calls taskyou_complete.
	if opts.Execute {
		if err := database.UpdateTaskStatus(firstTask.ID, db.StatusQueued); err != nil {
			return nil, fmt.Errorf("queue %s phase: %w", first.Name, err)
		}
		firstTask.Status = db.StatusQueued
	}

	return &Result{Definition: def, Branch: branch, Tasks: tasks}, nil
}

// phaseTitle builds a task title like "[Plan] <goal>", trimming a long goal so
// the board card stays readable.
func phaseTitle(phase, goal string) string {
	const maxGoal = 60
	g := strings.TrimSpace(goal)
	if len(g) > maxGoal {
		g = strings.TrimSpace(g[:maxGoal]) + "…"
	}
	return fmt.Sprintf("[%s] %s", phase, g)
}

// render substitutes the {{goal}} and {{branch}} placeholders in a phase template.
func render(tmpl, goal, branch string) string {
	r := strings.NewReplacer("{{goal}}", goal, "{{branch}}", branch)
	return strings.TrimSpace(r.Replace(tmpl))
}

// slugify converts a string into a lowercase, dash-separated slug, truncated to
// maxLen. It mirrors the executor's worktree slug so pipeline branch names read
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
