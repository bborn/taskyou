package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bborn/workflow/internal/db"
)

// PiModelProfile describes a named provider+model selection for the Pi executor.
//
// Two flavours are supported, distinguished by BaseURL:
//
//   - Built-in provider (BaseURL == ""): the provider/model already exist in Pi's
//     bundled model registry (e.g. provider "openrouter", model "qwen/qwen3-coder").
//     Selection is just `--provider <p> --model <m>`; the API key is resolved by Pi
//     from the provider's conventional env var (e.g. OPENROUTER_API_KEY).
//
//   - Custom OpenAI-compatible endpoint (BaseURL != ""): a local or self-hosted
//     server (Ollama, vLLM, LM Studio, …). Pi has no CLI flag for a base URL, so we
//     register the provider in Pi's models.json (see EnsurePiCustomProvider) and then
//     select it with the same `--provider/--model` flags.
//
// The API key never appears on the command line. For custom endpoints it is written
// into models.json as the *name* of an env var (APIKeyEnv), which Pi resolves at
// runtime; the env var must therefore be present in the daemon's environment.
type PiModelProfile struct {
	Name      string `json:"name"`
	Provider  string `json:"provider"`              // Pi provider id, e.g. "openrouter", "ollama"
	Model     string `json:"model"`                 // Model id, e.g. "qwen/qwen3-coder"
	BaseURL   string `json:"base_url,omitempty"`    // OpenAI-compatible endpoint for custom providers ("" = built-in)
	APIKeyEnv string `json:"api_key_env,omitempty"` // Name of env var holding the API key (custom providers)
	API       string `json:"api,omitempty"`         // Pi API protocol (default "openai-completions")

	// Optional model metadata required by Pi's models.json schema for custom providers.
	ContextWindow int  `json:"context_window,omitempty"`
	MaxTokens     int  `json:"max_tokens,omitempty"`
	Reasoning     bool `json:"reasoning,omitempty"`
}

// piModelProfilesFile is the on-disk shape of the model-profile config.
type piModelProfilesFile struct {
	Profiles map[string]PiModelProfile `json:"profiles"`
}

// safeProviderModelToken guards values that are injected unquoted into the Pi
// command line. Provider/model ids are simple tokens (alnum plus a few path-ish
// separators); anything else is rejected so a profile can never inject shell.
var safeProviderModelToken = regexp.MustCompile(`^[A-Za-z0-9._:/@-]+$`)

// PiModelProfilesPath returns the path to the model-profile config file.
// Override with TASKYOU_PI_MODELS (used by tests); defaults to
// ~/.config/taskyou/pi-models.json.
func PiModelProfilesPath() string {
	if p := os.Getenv("TASKYOU_PI_MODELS"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "taskyou", "pi-models.json")
}

// loadPiModelProfiles reads all profiles from the given path. A missing file is
// not an error (returns an empty map) so the feature is purely opt-in.
func loadPiModelProfiles(path string) (map[string]PiModelProfile, error) {
	if path == "" {
		return map[string]PiModelProfile{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]PiModelProfile{}, nil
		}
		return nil, fmt.Errorf("read pi model profiles: %w", err)
	}
	var file piModelProfilesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse pi model profiles %s: %w", path, err)
	}
	if file.Profiles == nil {
		file.Profiles = map[string]PiModelProfile{}
	}
	// Backfill the Name field from the map key so callers always have it.
	for name, p := range file.Profiles {
		p.Name = name
		file.Profiles[name] = p
	}
	return file.Profiles, nil
}

// GetPiModelProfile resolves a profile by name from the default config path.
// Returns nil (no error) when name is empty or the profile is not found, so a
// task with an unknown/empty profile simply falls back to Pi's default model.
func GetPiModelProfile(name string) (*PiModelProfile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	profiles, err := loadPiModelProfiles(PiModelProfilesPath())
	if err != nil {
		return nil, err
	}
	p, ok := profiles[name]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// apiProtocol returns the Pi API protocol for this profile (defaulting to the
// OpenAI Chat Completions protocol used by OpenRouter/Ollama/most local servers).
func (p *PiModelProfile) apiProtocol() string {
	if p.API != "" {
		return p.API
	}
	return "openai-completions"
}

// commandFlags returns the `--provider <p> --model <m>` segment (with a leading
// space) to inject into a Pi invocation, or "" if the profile is nil/incomplete.
// Provider/model are validated to a safe token charset so they can be injected
// unquoted without risk of shell injection.
func (p *PiModelProfile) commandFlags() string {
	if p == nil || p.Provider == "" || p.Model == "" {
		return ""
	}
	if !safeProviderModelToken.MatchString(p.Provider) || !safeProviderModelToken.MatchString(p.Model) {
		return ""
	}
	return fmt.Sprintf(" --provider %s --model %s", p.Provider, p.Model)
}

// EnsurePiCustomProvider registers a custom OpenAI-compatible provider in Pi's
// models.json so `--provider/--model` can select it. It is a no-op for built-in
// providers (BaseURL == ""). Existing providers in the file are preserved; only
// this profile's provider entry is upserted. The write is atomic (temp + rename).
func EnsurePiCustomProvider(modelsPath string, p *PiModelProfile) error {
	if p == nil || p.BaseURL == "" {
		return nil
	}
	if modelsPath == "" {
		return fmt.Errorf("pi models.json path is empty")
	}

	// Read existing config (tolerate a missing file).
	cfg := map[string]any{}
	if data, err := os.ReadFile(modelsPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse existing %s: %w", modelsPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", modelsPath, err)
	}

	providers, _ := cfg["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}

	contextWindow := p.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 32768
	}
	maxTokens := p.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	apiKey := p.APIKeyEnv
	if apiKey == "" {
		// Pi requires an apiKey field; local servers (Ollama) ignore it. A literal
		// placeholder is fine — resolveConfigValue treats an unknown string as a literal.
		apiKey = "local"
	}

	providers[p.Provider] = map[string]any{
		"baseUrl": p.BaseURL,
		"apiKey":  apiKey,
		"api":     p.apiProtocol(),
		"models": []any{
			map[string]any{
				"id":            p.Model,
				"name":          p.Model,
				"api":           p.apiProtocol(),
				"reasoning":     p.Reasoning,
				"input":         []any{"text"},
				"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
				"contextWindow": contextWindow,
				"maxTokens":     maxTokens,
			},
		},
	}
	cfg["providers"] = providers

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal models.json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		return fmt.Errorf("mkdir for models.json: %w", err)
	}
	tmp := modelsPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("write models.json: %w", err)
	}
	if err := os.Rename(tmp, modelsPath); err != nil {
		return fmt.Errorf("rename models.json: %w", err)
	}
	return nil
}

// piAgentDir resolves Pi's config directory the same way the Pi CLI does:
// honoring PI_CODING_AGENT_DIR, otherwise ~/.pi/agent.
func piAgentDir() string {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		if d == "~" {
			if home, err := os.UserHomeDir(); err == nil {
				return home
			}
		}
		if strings.HasPrefix(d, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, d[2:])
			}
		}
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// piModelsJSONPath returns the path to Pi's models.json (agentDir/models.json).
func piModelsJSONPath() string {
	dir := piAgentDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "models.json")
}

// piCompletionPrompt is appended to every Pi task's system prompt (via
// --append-system-prompt) so the agent self-reports its terminal state to the
// TaskYou board. It rides Pi's built-in `bash` tool and the `task` CLI, which
// calls the same db.UpdateTaskStatus path as the MCP completion tool — no MCP
// server required. Single-line and free of single quotes so it can be wrapped in
// shell single quotes; $WORKTREE_TASK_ID is left literal for the agent's shell to
// expand at run time (the var is exported into the pane).
const piCompletionPrompt = "TaskYou board integration: you are running as a task on a Kanban board. " +
	"When you have fully completed the task, use your bash tool to run `task status $WORKTREE_TASK_ID done` to mark it done. " +
	"If you become blocked or need input from the user before you can continue, run `task status $WORKTREE_TASK_ID blocked` instead. " +
	"Always run exactly one of these commands before you finish the session."

// piExtraFlags returns the flag segment (leading space) injected into EVERY Pi
// invocation — both the daemon path (runPi/runPiResume) and the TUI path
// (PiExecutor.BuildCommand). Keeping a single source of truth is what guarantees
// the two command-builders stay in sync (see reference_executor_command_builder_divergence).
//
// It always appends the completion system prompt, and prepends model-selection
// flags when the task names a resolvable model profile.
func piExtraFlags(task *db.Task) string {
	var b strings.Builder
	if task != nil {
		if prof, err := GetPiModelProfile(task.ModelProfile); err == nil && prof != nil {
			b.WriteString(prof.commandFlags())
		}
	}
	b.WriteString(" --append-system-prompt '")
	b.WriteString(piCompletionPrompt)
	b.WriteString("'")
	return b.String()
}

// buildPiDaemonScript constructs the shell command the daemon runs in a tmux
// window for a Pi task. It is the single builder behind both runPi (fresh and
// resume-existing) and runPiResume, so the daemon's command string is built in
// exactly one place. extraFlags must come from piExtraFlags(task) so the daemon
// and TUI (PiExecutor.BuildCommand) inject identical model/completion flags.
//
// inputFile holds the prompt (fresh) or feedback (resume); it is cat'd in so the
// shell, not Pi's argv, carries the (possibly large/multiline) text. resume adds
// Pi's --continue flag to attach to the existing --session file.
func buildPiDaemonScript(task *db.Task, sessionID, extraFlags, sessionPath, inputFile string, resume bool) string {
	cont := ""
	if resume {
		cont = "--continue "
	}
	return fmt.Sprintf(`WORKTREE_TASK_ID=%d WORKTREE_SESSION_ID=%s WORKTREE_PORT=%d WORKTREE_PATH=%q pi%s --session %q %s"$(cat %q)"`,
		task.ID, sessionID, task.Port, task.WorktreePath, extraFlags, sessionPath, cont, inputFile)
}
