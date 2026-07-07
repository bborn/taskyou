package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// A pipeline's phase structure (names, order, instructions) is fixed by its
// Definition, but the executor and model each phase runs on are configurable
// per project and persisted, so a user sets them once — "Review runs on codex
// for this repo" — and every pipeline there defaults to that. Configuration is
// stored in the settings table under a per-(project,definition) key.

// PhaseConfig is the configurable slice of a phase: which executor and model it
// runs on. The phase Name ties it back to a Definition phase.
type PhaseConfig struct {
	Name     string `json:"name"`
	Executor string `json:"executor"`
	Model    string `json:"model"`
}

func configKey(project, definition string) string {
	return fmt.Sprintf("pipeline_config:%s:%s", project, definition)
}

// DefaultPhaseConfig returns a definition's built-in executor/model for each phase.
func DefaultPhaseConfig(def Definition) []PhaseConfig {
	out := make([]PhaseConfig, len(def.Phases))
	for i, p := range def.Phases {
		out[i] = PhaseConfig{Name: p.Name, Executor: p.Executor, Model: p.Model}
	}
	return out
}

// GetConfig returns the saved per-project phase config for a definition, or nil
// when the project has never been configured (callers fall back to the defaults).
func GetConfig(database *db.DB, project, definition string) ([]PhaseConfig, error) {
	if database == nil {
		return nil, nil
	}
	raw, err := database.GetSetting(configKey(project, definition))
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var cfg []PhaseConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("parse pipeline config: %w", err)
	}
	return cfg, nil
}

// SaveConfig persists the per-project phase config for a definition.
func SaveConfig(database *db.DB, project, definition string, cfg []PhaseConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal pipeline config: %w", err)
	}
	return database.SetSetting(configKey(project, definition), string(data))
}

// ClearConfig removes a project's saved config so it reverts to the defaults.
func ClearConfig(database *db.DB, project, definition string) error {
	return database.SetSetting(configKey(project, definition), "")
}

// EffectivePhases returns a definition's phases with the project's saved
// executor/model overrides applied. Phases without a saved override keep their
// built-in defaults, so partial configuration is fine.
func EffectivePhases(database *db.DB, project string, def Definition) []Phase {
	cfg, _ := GetConfig(database, project, def.Name)
	byName := make(map[string]PhaseConfig, len(cfg))
	for _, c := range cfg {
		byName[strings.ToLower(c.Name)] = c
	}

	out := make([]Phase, len(def.Phases))
	for i, p := range def.Phases {
		out[i] = p
		if c, ok := byName[strings.ToLower(p.Name)]; ok {
			if strings.TrimSpace(c.Executor) != "" {
				out[i].Executor = c.Executor
			}
			out[i].Model = c.Model // Wholesale (a saved empty model means "executor default").
		}
	}
	return out
}

// EffectiveConfig returns the project's current effective phase config — its
// saved overrides merged onto the definition defaults — for display.
func EffectiveConfig(database *db.DB, project string, def Definition) []PhaseConfig {
	phases := EffectivePhases(database, project, def)
	out := make([]PhaseConfig, len(phases))
	for i, p := range phases {
		out[i] = PhaseConfig{Name: p.Name, Executor: p.Executor, Model: p.Model}
	}
	return out
}

// IsConfigured reports whether a project has saved pipeline config for a definition.
func IsConfigured(database *db.DB, project, definition string) bool {
	cfg, _ := GetConfig(database, project, definition)
	return len(cfg) > 0
}
