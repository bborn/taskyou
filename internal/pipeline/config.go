package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// A workflow's shape (step names, dependencies, prompts) is fixed by its
// Definition, but the executor and model each step runs on are configurable per
// project and persisted, so a user sets them once — "Review B runs on codex for
// this repo" — and every workflow there defaults to that. Configuration is stored
// in the settings table under a per-(project,definition) key.

// StepConfig is the configurable slice of a step: which executor and model it runs
// on. The Name ties it back to a Definition step.
type StepConfig struct {
	Name     string `json:"name"`
	Executor string `json:"executor"`
	Model    string `json:"model"`
}

func configKey(project, definition string) string {
	return fmt.Sprintf("pipeline_config:%s:%s", project, definition)
}

// DefaultStepConfig returns a definition's built-in executor/model for each step.
func DefaultStepConfig(def Definition) []StepConfig {
	out := make([]StepConfig, len(def.Steps))
	for i, s := range def.Steps {
		out[i] = StepConfig{Name: s.Name, Executor: s.Executor, Model: s.Model}
	}
	return out
}

// GetConfig returns the saved per-project step config for a definition, or nil
// when the project has never been configured (callers fall back to the defaults).
func GetConfig(database *db.DB, project, definition string) ([]StepConfig, error) {
	if database == nil {
		return nil, nil
	}
	raw, err := database.GetSetting(configKey(project, definition))
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var cfg []StepConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("parse workflow config: %w", err)
	}
	return cfg, nil
}

// SaveConfig persists the per-project step config for a definition.
func SaveConfig(database *db.DB, project, definition string, cfg []StepConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal workflow config: %w", err)
	}
	return database.SetSetting(configKey(project, definition), string(data))
}

// ClearConfig removes a project's saved config so it reverts to the defaults.
func ClearConfig(database *db.DB, project, definition string) error {
	return database.SetSetting(configKey(project, definition), "")
}

// EffectiveSteps returns a definition's steps with the project's saved
// executor/model overrides applied. Steps without a saved override keep their
// built-in defaults, so partial configuration is fine.
func EffectiveSteps(database *db.DB, project string, def Definition) []Step {
	cfg, _ := GetConfig(database, project, def.Name)
	byName := make(map[string]StepConfig, len(cfg))
	for _, c := range cfg {
		byName[strings.ToLower(c.Name)] = c
	}

	out := make([]Step, len(def.Steps))
	for i, s := range def.Steps {
		out[i] = s
		if c, ok := byName[strings.ToLower(s.Name)]; ok {
			if strings.TrimSpace(c.Executor) != "" {
				out[i].Executor = c.Executor
			}
			out[i].Model = c.Model // Wholesale (a saved empty model means "executor default").
		}
	}
	return out
}

// EffectiveConfig returns the project's current effective step config — its saved
// overrides merged onto the definition defaults — for display.
func EffectiveConfig(database *db.DB, project string, def Definition) []StepConfig {
	steps := EffectiveSteps(database, project, def)
	out := make([]StepConfig, len(steps))
	for i, s := range steps {
		out[i] = StepConfig{Name: s.Name, Executor: s.Executor, Model: s.Model}
	}
	return out
}

// IsConfigured reports whether a project has saved workflow config for a definition.
func IsConfigured(database *db.DB, project, definition string) bool {
	cfg, _ := GetConfig(database, project, definition)
	return len(cfg) > 0
}
