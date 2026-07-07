package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Custom workflows are authored as plain YAML files — one workflow per file —
// so a user (or the LLM in `ty pipeline new`) can define a whole new flow (add a
// QA step, a different shape) without touching Go. Files live in a global dir
// (~/.config/task/workflows) and an optional per-project dir
// (<project>/.taskyou/workflows); a project file shadows a global one of the same
// name, which shadows a built-in.
//
// Authoring is just prompts: each step says what it does and what it depends on;
// the git handoff (which branch to push to, when to open the PR) is derived from
// the step's position in the DAG — see compose.go.

// stepYAML is the on-disk form of a step.
type stepYAML struct {
	Name     string   `yaml:"name"`
	Executor string   `yaml:"executor,omitempty"`
	Model    string   `yaml:"model,omitempty"`
	Deps     []string `yaml:"deps,omitempty"`
	Prompt   string   `yaml:"prompt"`
	// Verbatim marks a step whose prompt IS the full instruction (no DAG-derived
	// git handoff is added). It's set when `ty pipeline edit` ejects a built-in
	// workflow, so the ejected file behaves identically to the built-in.
	Verbatim bool `yaml:"verbatim,omitempty"`
}

// definitionYAML is the on-disk form of a workflow.
type definitionYAML struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description,omitempty"`
	Steps       []stepYAML `yaml:"steps"`
}

// WorkflowsDir returns the global directory custom workflow files live in.
func WorkflowsDir() string {
	if dir := os.Getenv("TY_WORKFLOWS_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "task", "workflows")
}

// WorkflowDirs returns the directories to search for custom workflows: the global
// dir plus the project-local .taskyou/workflows (when a project dir is given).
// Later dirs win on name collisions.
func WorkflowDirs(projectDir string) []string {
	dirs := []string{WorkflowsDir()}
	if projectDir != "" {
		dirs = append(dirs, filepath.Join(projectDir, ".taskyou", "workflows"))
	}
	return dirs
}

// ParseDefinition parses and validates one workflow YAML document.
func ParseDefinition(data []byte) (Definition, error) {
	var doc definitionYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Definition{}, fmt.Errorf("parse workflow yaml: %w", err)
	}
	if strings.TrimSpace(doc.Name) == "" {
		return Definition{}, fmt.Errorf("workflow is missing a name")
	}
	def := Definition{
		Name:        strings.TrimSpace(doc.Name),
		Description: strings.TrimSpace(doc.Description),
		Custom:      true,
	}
	for _, s := range doc.Steps {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return Definition{}, fmt.Errorf("workflow %q has a step with no name", def.Name)
		}
		if strings.TrimSpace(s.Prompt) == "" {
			return Definition{}, fmt.Errorf("step %q has no prompt", name)
		}
		exec := strings.TrimSpace(s.Executor)
		if exec == "" {
			exec = "claude"
		}
		step := Step{
			Name:     name,
			Executor: exec,
			Model:    strings.TrimSpace(s.Model),
			Deps:     s.Deps,
		}
		// A verbatim step's prompt is its full instruction; otherwise the prompt is
		// the work and the git handoff is composed from the DAG.
		if s.Verbatim {
			step.Instruction = s.Prompt
		} else {
			step.Prompt = s.Prompt
		}
		def.Steps = append(def.Steps, step)
	}
	if err := def.validate(); err != nil {
		return Definition{}, err
	}
	return def, nil
}

// Marshal renders a Definition back to YAML (used by `ty pipeline new`).
func Marshal(def Definition) ([]byte, error) {
	doc := definitionYAML{Name: def.Name, Description: def.Description}
	for _, s := range def.Steps {
		out := stepYAML{
			Name:     s.Name,
			Executor: s.Executor,
			Model:    s.Model,
			Deps:     s.Deps,
			Prompt:   s.Prompt,
		}
		// A built-in step carries a full Instruction — write it as a verbatim
		// prompt so the ejected file behaves identically when reloaded.
		if s.Instruction != "" {
			out.Prompt = s.Instruction
			out.Verbatim = true
		}
		doc.Steps = append(doc.Steps, out)
	}
	return yaml.Marshal(doc)
}

// loadCustomDefinitions reads every *.yaml/*.yml workflow in the given dirs.
// Later dirs shadow earlier ones by name. Unreadable or invalid files are
// skipped and returned in the errs slice so callers can surface them without
// failing the whole load.
func loadCustomDefinitions(dirs []string) (map[string]Definition, []error) {
	out := make(map[string]Definition)
	var errs []error
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // Missing dir is fine.
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext != ".yaml" && ext != ".yml" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", path, err))
				continue
			}
			def, err := ParseDefinition(data)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", path, err))
				continue
			}
			out[def.Name] = def
		}
	}
	return out, errs
}
