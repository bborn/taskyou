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
	"os/exec"
	"sort"
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
	Instruction string   // Full body template (built-in / verbatim steps). Takes precedence over Prompt.
	Prompt      string   // Custom-workflow body: what the step does; the git handoff is composed from the DAG (see compose.go).
	Deps        []string // Names of steps that must complete before this one runs.
}

// Definition is a named workflow DAG.
type Definition struct {
	Name        string
	Description string
	Steps       []Step
	Custom      bool // true for workflows loaded from YAML files (vs the built-ins)
}

// DefaultDefinition is the definition used when none is named. It is empty: there
// are no built-in workflows — every workflow is installed from a plugin (or dropped
// into the workflows dir). `ty pipeline` with no -d therefore has nothing to run
// until the user installs a workflow, and Create says so.
const DefaultDefinition = ""

// registry loads workflow definitions from disk: installed-plugin workflows, the
// global workflows dir, and any extraDirs (a project's .taskyou/workflows). There
// are no built-in definitions — the plan-code-review flow that used to live here
// now ships as an example plugin (examples/plugins/plan-code-review).
func registry(extraDirs ...string) map[string]Definition {
	out := make(map[string]Definition)
	// Search order, lowest precedence first: installed-plugin workflows, then the
	// global workflows dir, then extraDirs (a project's .taskyou/workflows). Later
	// dirs shadow earlier ones on a name collision, so a user's own on-disk workflow
	// wins over one shipped by an installed plugin. dedupeKeepingLast collapses the
	// global dir that WorkflowDirs also re-includes, keeping precedence unambiguous.
	var dirs []string
	if PluginWorkflowDirs != nil {
		dirs = append(dirs, PluginWorkflowDirs()...)
	}
	dirs = append(dirs, WorkflowsDir())
	dirs = append(dirs, extraDirs...)
	custom, _ := loadCustomDefinitions(dedupeKeepingLast(dirs))
	for name, def := range custom {
		out[name] = def
	}
	return out
}

// PluginWorkflowDirs, when set, returns extra directories to search for workflow
// definitions — the workflows/ subdir of every installed plugin. It is a seam so
// the pipeline package needn't import internal/hooks (which would cycle); main
// wires it at startup. nil = no plugin workflows.
var PluginWorkflowDirs func() []string

// dedupeKeepingLast returns dirs with duplicates removed, keeping each dir at its
// LAST position — so the highest-precedence occurrence (later dirs shadow earlier)
// is the one that survives.
func dedupeKeepingLast(dirs []string) []string {
	lastAt := make(map[string]int, len(dirs))
	for i, d := range dirs {
		lastAt[d] = i
	}
	out := make([]string, 0, len(dirs))
	for i, d := range dirs {
		if lastAt[d] == i {
			out = append(out, d)
		}
	}
	return out
}

// Definitions returns all workflow definitions — built-in plus custom YAML
// workflows discovered in extraDirs and the global workflows dir.
func Definitions(extraDirs ...string) []Definition {
	reg := registry(extraDirs...)
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	// Keep the default first for a stable, friendly listing.
	out := make([]Definition, 0, len(names))
	if def, ok := reg[DefaultDefinition]; ok {
		out = append(out, def)
	}
	for _, name := range names {
		if name == DefaultDefinition {
			continue
		}
		out = append(out, reg[name])
	}
	return out
}

// DefinitionNames returns the names of all definitions (built-in + custom).
func DefinitionNames(extraDirs ...string) []string {
	defs := Definitions(extraDirs...)
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Name
	}
	return out
}

// Get returns the named definition (built-in or custom). An empty name resolves
// to DefaultDefinition.
func Get(name string, extraDirs ...string) (Definition, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = DefaultDefinition
	}
	def, ok := registry(extraDirs...)[name]
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
	if len(d.Roots()) == 0 {
		return fmt.Errorf("workflow %q has no root step (every step depends on another)", d.Name)
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
	projectDir := projectDirFor(database, opts.Project)
	dirs := WorkflowDirs(projectDir)
	def, ok := Get(opts.Definition, dirs...)
	if !ok {
		avail := DefinitionNames(dirs...)
		if strings.TrimSpace(opts.Definition) == "" {
			hint := "install one with `ty plugins add <repo>`"
			if len(avail) > 0 {
				hint = "pass -d <name>; available: " + strings.Join(avail, ", ")
			}
			return nil, fmt.Errorf("no workflow specified and there is no default — %s", hint)
		}
		return nil, fmt.Errorf("unknown workflow definition %q (available: %s)", opts.Definition, strings.Join(avail, ", "))
	}
	if err := def.validate(); err != nil {
		return nil, err
	}

	steps := def.Steps
	roots := def.Roots()
	rootNames := make(map[string]bool, len(roots))
	for _, r := range roots {
		rootNames[r.Name] = true
	}
	multiRoot := len(roots) > 1
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

	// Seed the shared branch from the first task's id so it's unique.
	branch := fmt.Sprintf("pipeline/%d-%s", tasks[0].ID, slug)

	// Branch ownership. With a single root, that root creates the branch (pins
	// BranchName) and everyone else checks it out via SourceBranch — the proven
	// path. With MULTIPLE roots (true parallel entry points), no single step can
	// own the branch, so we pre-create it on the remote and every step (roots
	// included) checks it out; the parallel roots each push to their own branch.
	if multiRoot {
		if err := ensureSharedBranch(projectDir, branch); err != nil {
			for _, t := range tasks {
				_ = database.DeleteTask(t.ID)
			}
			return nil, fmt.Errorf("multi-root workflows need a git-worktree project with a remote: %w", err)
		}
	}

	for _, s := range steps {
		task := byName[s.Name]
		task.Body = render(effectiveInstruction(def, s.Name), goal, branch, s.Name, reviewsList(s, branch))
		if rootNames[s.Name] && !multiRoot {
			task.BranchName = branch // Single root pins/creates the branch.
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
	// 'blocked' on their deps; every root starts (queued) or stages (backlog).
	for _, s := range steps {
		if rootNames[s.Name] {
			continue
		}
		if err := database.UpdateTaskStatus(byName[s.Name].ID, db.StatusBlocked); err != nil {
			return nil, fmt.Errorf("block %s step: %w", s.Name, err)
		}
		byName[s.Name].Status = db.StatusBlocked
	}
	if opts.Execute {
		for _, r := range roots {
			if err := database.UpdateTaskStatus(byName[r.Name].ID, db.StatusQueued); err != nil {
				return nil, fmt.Errorf("queue %s step: %w", r.Name, err)
			}
			byName[r.Name].Status = db.StatusQueued
		}
	}

	return &Result{Definition: def, Branch: branch, Tasks: tasks}, nil
}

// ensureSharedBranch creates the workflow branch on the project's origin remote
// (from the project's current HEAD) so that several parallel root steps can all
// check it out at once. A branch that already exists is fine.
func ensureSharedBranch(projectDir, branch string) error {
	if strings.TrimSpace(projectDir) == "" {
		return fmt.Errorf("project directory not found")
	}
	cmd := exec.Command("git", "-C", projectDir, "push", "origin", "HEAD:refs/heads/"+branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "already exists") {
			return nil
		}
		return fmt.Errorf("git push %s: %v: %s", branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// projectDirFor returns a project's on-disk path (used to find its
// .taskyou/workflows dir), or "" if it can't be resolved.
func projectDirFor(database *db.DB, project string) string {
	if database == nil || project == "" {
		return ""
	}
	if p, err := database.GetProjectByName(project); err == nil && p != nil {
		return p.Path
	}
	return ""
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
