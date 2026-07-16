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
	"encoding/json"
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
	Name        string            // Unique label, e.g. "Plan", "Code", "Review A", "Collect".
	Kind        string            // Optional: another kind this step runs. Sets the task's Type (so that kind's instructions apply); if that kind has steps, it's a sub-workflow inlined at build (see flatten.go).
	Executor    string            // Executor slug (db.ExecutorClaude, db.ExecutorCodex, ...).
	Model       string            // Per-task model override ("" = the executor's default).
	ConfigDir   string            // Per-task CLAUDE_CONFIG_DIR override ("" = use the project's/default config dir). Routes this step's Claude through a different config (e.g. ollama) without changing the project.
	Env         map[string]string // Per-task env overrides injected as a process-env prefix on the claude command (e.g. ANTHROPIC_BASE_URL/AUTH_TOKEN to route through ollama). nil = no overrides. Kept distinct from ConfigDir: env injection keeps the default config dir (plugins, MCP, trusted worktrees) intact and process env wins over stored creds — the mechanism that actually reaches ollama.
	Instruction string            // Full body template (built-in / verbatim steps). Takes precedence over Prompt.
	Prompt      string            // Custom-workflow body: what the step does; the git handoff is composed from the DAG (see compose.go).
	Deps        []string          // Names of steps that must complete before this one runs.
}

// Definition is a "kind": one authored recipe. With Steps it is a workflow (a DAG
// of steps); without Steps it is a single-task kind whose Instructions become the
// task's prompt preset — the same thing a DB task type is. A step may reference
// another kind by name (Step.Kind), so a workflow composes kinds, and picking a
// kind is one action whether it fans out or not.
type Definition struct {
	Name         string
	Description  string
	Instructions string // Single-task kind's prompt preset (used when Steps is empty). Mirrors db.TaskType.Instructions.
	Steps        []Step
	Custom       bool // true for kinds loaded from YAML files (vs the built-ins)
}

// IsSingle reports whether this kind is a single task (no DAG) — its Instructions
// are the whole recipe. The steps-less, instructions-only case is exactly a task type.
func (d Definition) IsSingle() bool { return len(d.Steps) == 0 }

// DefaultDefinition is empty: there is no built-in workflow. Kinds live in the DB
// (task types); a kind runs as a workflow only when a same-named file adds steps.
const DefaultDefinition = ""

// registry loads the workflow definitions: the bundled built-ins compiled into the
// binary (e.g. "rpi") first at lowest precedence, then the on-disk workflow files
// from the global workflows dir plus extraDirs. A same-named on-disk file shadows a
// bundled built-in, and later dirs shadow earlier ones (project shadows global).
func registry(extraDirs ...string) map[string]Definition {
	out := loadBundledDefinitions() // built-ins, lowest precedence
	dirs := append([]string{WorkflowsDir()}, extraDirs...)
	custom, _ := loadCustomDefinitions(dirs)
	for name, def := range custom {
		out[name] = def
	}
	return out
}

// Definitions returns the workflow definitions discovered as files.
func Definitions(extraDirs ...string) []Definition {
	reg := registry(extraDirs...)
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Definition, 0, len(names))
	for _, name := range names {
		out = append(out, reg[name])
	}
	return out
}

// DefinitionNames returns the names of the workflow files.
func DefinitionNames(extraDirs ...string) []string {
	defs := Definitions(extraDirs...)
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Name
	}
	return out
}

// Get returns the workflow definition for a name (a same-named file), if one
// exists. Single-task kinds are DB task types and are not returned here — use
// KindResolver when you need both.
func Get(name string, extraDirs ...string) (Definition, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Definition{}, false
	}
	def, ok := registry(extraDirs...)[name]
	return def, ok
}

// KindResolver resolves a kind name to its definition, by convention: if a
// same-named workflow file exists it runs as a workflow; otherwise the kind is a
// DB task type (a single task using its instructions). The DB is the single kind
// store; a workflow is just a file that adds steps to a kind of the same name.
func KindResolver(database *db.DB, extraDirs ...string) ResolveFunc {
	reg := registry(extraDirs...)
	return func(name string) (Definition, bool) {
		name = strings.TrimSpace(name)
		if name == "" {
			return Definition{}, false
		}
		if def, ok := reg[name]; ok {
			return def, true
		}
		if database != nil {
			if tt, err := database.GetTaskTypeByName(name); err == nil && tt != nil {
				return Definition{Name: tt.Name, Description: tt.Label, Instructions: tt.Instructions}, true
			}
		}
		return Definition{}, false
	}
}

// LookupKindInstructions returns the instructions of a single-task kind defined in
// a YAML/built-in file (not a DB task type), or "" if there is no such kind. The
// executor uses it so a file-defined kind works as a task Type just like a DB type.
func LookupKindInstructions(name string, extraDirs ...string) string {
	if def, ok := Get(name, extraDirs...); ok && def.IsSingle() {
		return def.Instructions
	}
	return ""
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
	resolve := KindResolver(database, dirs...)
	def, ok := resolve(opts.Definition)
	if !ok {
		return nil, fmt.Errorf("unknown kind %q (available: %s)", opts.Definition, strings.Join(DefinitionNames(dirs...), ", "))
	}
	// Compose kinds: inline any step that runs a multi-step kind (nesting) into one
	// flat DAG. A single-task kind (no steps) is just one ordinary task.
	def, err := Flatten(def, resolve)
	if err != nil {
		return nil, err
	}
	if def.IsSingle() {
		return createSingleTask(database, def, opts)
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
			Title:           stepTitle(s.Name, goal),
			Body:            goal, // Placeholder; rewritten once the branch is known.
			Status:          db.StatusBacklog,
			Type:            stepType(s),
			Project:         opts.Project,
			Executor:        s.Executor,
			Model:           s.Model,
			ClaudeConfigDir: s.ConfigDir,
			EnvJSON:         encodeStepEnv(s.Env),
			PermissionMode:  opts.PermissionMode,
			Tags:            "pipeline",
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

// stepType is the task Type for a step: the kind it runs (so that kind's
// instructions apply at execution), or "code" for a plain inline-prompt step.
func stepType(s Step) string {
	if k := strings.TrimSpace(s.Kind); k != "" {
		return k
	}
	return db.TypeCode
}

// createSingleTask handles a single-task kind (no steps): it's just one ordinary
// task typed with the kind, so the executor applies the kind's instructions — the
// same thing as creating a task of that type. No branch, no PR machinery.
func createSingleTask(database *db.DB, def Definition, opts Options) (*Result, error) {
	task := &db.Task{
		Title:          stepTitle(def.Name, opts.Goal),
		Body:           strings.TrimSpace(opts.Goal),
		Status:         db.StatusBacklog,
		Type:           def.Name,
		Project:        opts.Project,
		PermissionMode: opts.PermissionMode,
	}
	if err := database.CreateTask(task); err != nil {
		return nil, fmt.Errorf("create %s task: %w", def.Name, err)
	}
	if opts.Execute {
		if err := database.UpdateTaskStatus(task.ID, db.StatusQueued); err != nil {
			return nil, fmt.Errorf("queue %s task: %w", def.Name, err)
		}
		task.Status = db.StatusQueued
	}
	return &Result{Definition: def, Tasks: []*db.Task{task}}, nil
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
// encodeStepEnv serializes a step's `env:` map into the JSON blob stored on the
// task (Task.EnvJSON). nil/empty → "" (no override, no stored blob). The
// executor's EnvMap parses it back. Errors are impossible for a
// map[string]string and would only indicate a programming mistake, so on
// failure we fall back to "" rather than blocking pipeline creation.
func encodeStepEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	b, err := json.Marshal(env)
	if err != nil {
		return ""
	}
	return string(b)
}

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
