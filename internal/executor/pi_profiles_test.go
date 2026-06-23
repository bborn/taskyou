package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// writeProfiles writes a model-profile config to a temp file and points
// TASKYOU_PI_MODELS at it for the duration of the test.
func writeProfiles(t *testing.T, profiles map[string]PiModelProfile) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pi-models.json")
	data, err := json.Marshal(piModelProfilesFile{Profiles: profiles})
	if err != nil {
		t.Fatalf("marshal profiles: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write profiles: %v", err)
	}
	t.Setenv("TASKYOU_PI_MODELS", path)
	return path
}

func TestLoadAndGetPiModelProfile(t *testing.T) {
	writeProfiles(t, map[string]PiModelProfile{
		"qwen-openrouter": {Provider: "openrouter", Model: "qwen/qwen3-coder", APIKeyEnv: "OPENROUTER_API_KEY"},
		"qwen-local":      {Provider: "ollama", Model: "qwen2.5-coder", BaseURL: "http://localhost:11434/v1"},
	})

	prof, err := GetPiModelProfile("qwen-openrouter")
	if err != nil {
		t.Fatalf("GetPiModelProfile: %v", err)
	}
	if prof == nil {
		t.Fatal("expected profile, got nil")
	}
	if prof.Name != "qwen-openrouter" {
		t.Errorf("expected Name backfilled to map key, got %q", prof.Name)
	}
	if prof.Provider != "openrouter" || prof.Model != "qwen/qwen3-coder" {
		t.Errorf("unexpected profile: %+v", prof)
	}

	// Unknown profile -> nil, no error (graceful fallback to Pi default).
	missing, err := GetPiModelProfile("does-not-exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for unknown profile, got %+v", missing)
	}

	// Empty name -> nil, no error.
	empty, err := GetPiModelProfile("")
	if err != nil || empty != nil {
		t.Errorf("expected (nil,nil) for empty name, got (%+v,%v)", empty, err)
	}
}

func TestGetPiModelProfile_MissingFile(t *testing.T) {
	// Point at a path that does not exist: should be a no-op, not an error.
	t.Setenv("TASKYOU_PI_MODELS", filepath.Join(t.TempDir(), "nope.json"))
	prof, err := GetPiModelProfile("anything")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if prof != nil {
		t.Errorf("expected nil profile, got %+v", prof)
	}
}

func TestPiModelProfile_CommandFlags(t *testing.T) {
	cases := []struct {
		name string
		p    *PiModelProfile
		want string
	}{
		{"nil", nil, ""},
		{"empty", &PiModelProfile{}, ""},
		{"missing model", &PiModelProfile{Provider: "openrouter"}, ""},
		{"built-in", &PiModelProfile{Provider: "openrouter", Model: "qwen/qwen3-coder"}, " --provider openrouter --model qwen/qwen3-coder"},
		{"local", &PiModelProfile{Provider: "ollama", Model: "qwen2.5-coder", BaseURL: "http://x"}, " --provider ollama --model qwen2.5-coder"},
		// A provider/model with shell metacharacters is rejected (no injection).
		{"unsafe provider", &PiModelProfile{Provider: "ev;il", Model: "m"}, ""},
		{"unsafe model", &PiModelProfile{Provider: "p", Model: "a b"}, ""},
		{"unsafe quote", &PiModelProfile{Provider: "p", Model: "a'b"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.commandFlags(); got != tc.want {
				t.Errorf("commandFlags() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPiExtraFlags_NoProfile(t *testing.T) {
	t.Setenv("TASKYOU_PI_MODELS", filepath.Join(t.TempDir(), "none.json"))
	task := &db.Task{ID: 1}
	got := piExtraFlags(task)

	// No model flags, but the completion system prompt is always appended.
	if strings.Contains(got, "--provider") {
		t.Errorf("expected no --provider for profileless task, got %q", got)
	}
	if !strings.HasPrefix(got, " --append-system-prompt '") || !strings.HasSuffix(got, "'") {
		t.Errorf("expected single-quoted append-system-prompt, got %q", got)
	}
	// Correct CLI arg order: `task status <id> <status>` (id BEFORE status).
	if !strings.Contains(got, "task status $WORKTREE_TASK_ID done") {
		t.Errorf("completion prompt missing/incorrect done command: %q", got)
	}
	if !strings.Contains(got, "task status $WORKTREE_TASK_ID blocked") {
		t.Errorf("completion prompt missing/incorrect blocked command: %q", got)
	}
}

func TestPiExtraFlags_WithProfile(t *testing.T) {
	writeProfiles(t, map[string]PiModelProfile{
		"qwen-openrouter": {Provider: "openrouter", Model: "qwen/qwen3-coder", APIKeyEnv: "OPENROUTER_API_KEY"},
	})
	task := &db.Task{ID: 1, Executor: db.ExecutorPi, ModelProfile: "qwen-openrouter"}
	got := piExtraFlags(task)

	wantPrefix := " --provider openrouter --model qwen/qwen3-coder --append-system-prompt '"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("piExtraFlags() = %q, want prefix %q", got, wantPrefix)
	}
	if !strings.HasSuffix(got, "'") {
		t.Errorf("piExtraFlags() should end with closing quote, got %q", got)
	}
	// The API key must never appear on the command line.
	if strings.Contains(got, "OPENROUTER_API_KEY") || strings.Contains(got, "--api-key") {
		t.Errorf("API key/env leaked into command line: %q", got)
	}
}

func TestBuildPiDaemonScript(t *testing.T) {
	task := &db.Task{ID: 42, WorktreePath: "/tmp/wt", Port: 3100}
	extra := " --provider openrouter --model qwen/qwen3-coder --append-system-prompt 'go'"

	fresh := buildPiDaemonScript(task, "sess-1", extra, "/tmp/s/task-42.jsonl", "/tmp/prompt.txt", false)
	wantFresh := `WORKTREE_TASK_ID=42 WORKTREE_SESSION_ID=sess-1 WORKTREE_PORT=3100 WORKTREE_PATH="/tmp/wt" pi --provider openrouter --model qwen/qwen3-coder --append-system-prompt 'go' --session "/tmp/s/task-42.jsonl" "$(cat "/tmp/prompt.txt")"`
	if fresh != wantFresh {
		t.Errorf("fresh script mismatch:\n got: %s\nwant: %s", fresh, wantFresh)
	}

	resume := buildPiDaemonScript(task, "sess-1", extra, "/tmp/s/task-42.jsonl", "/tmp/fb.txt", true)
	wantResume := `WORKTREE_TASK_ID=42 WORKTREE_SESSION_ID=sess-1 WORKTREE_PORT=3100 WORKTREE_PATH="/tmp/wt" pi --provider openrouter --model qwen/qwen3-coder --append-system-prompt 'go' --session "/tmp/s/task-42.jsonl" --continue "$(cat "/tmp/fb.txt")"`
	if resume != wantResume {
		t.Errorf("resume script mismatch:\n got: %s\nwant: %s", resume, wantResume)
	}
}

// TestPiCommandBuildersStayInSync is the guard against the two-command-builder
// divergence bug: the daemon (buildPiDaemonScript, used by runPi/runPiResume) and
// the TUI (PiExecutor.BuildCommand) must inject the IDENTICAL model+completion
// flag segment for a given task. Both derive it from piExtraFlags(task).
func TestPiCommandBuildersStayInSync(t *testing.T) {
	writeProfiles(t, map[string]PiModelProfile{
		"qwen-openrouter": {Provider: "openrouter", Model: "qwen/qwen3-coder", APIKeyEnv: "OPENROUTER_API_KEY"},
	})

	tmpDB, err := os.CreateTemp("", "sync-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpDB.Name())
	tmpDB.Close()
	database, err := db.Open(tmpDB.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	exec := New(database, &config.Config{})
	piExec := exec.GetExecutor("pi")

	task := &db.Task{ID: 7, WorktreePath: "/tmp/wt", Port: 3107, Executor: db.ExecutorPi, ModelProfile: "qwen-openrouter"}

	extra := piExtraFlags(task)

	// Daemon side.
	daemon := buildPiDaemonScript(task, "s", extra, "/tmp/sess.jsonl", "/tmp/p.txt", false)
	if !strings.Contains(daemon, "pi"+extra+" --session") {
		t.Errorf("daemon script does not embed the shared flag segment %q:\n%s", extra, daemon)
	}

	// TUI side (no session ID = fresh, no prompt = bare launch).
	tui := piExec.BuildCommand(task, "", "")
	if !strings.Contains(tui, "pi"+extra+" --session") {
		t.Errorf("TUI BuildCommand does not embed the shared flag segment %q:\n%s", extra, tui)
	}
}

func TestEnsurePiCustomProvider(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")

	// Seed with an unrelated provider that must be preserved.
	seed := `{"providers":{"keepme":{"baseUrl":"http://keep","apiKey":"K","api":"openai-completions"}}}`
	if err := os.WriteFile(modelsPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	prof := &PiModelProfile{
		Name:      "qwen-local",
		Provider:  "ollama",
		Model:     "qwen2.5-coder",
		BaseURL:   "http://localhost:11434/v1",
		APIKeyEnv: "OLLAMA_API_KEY",
	}
	if err := EnsurePiCustomProvider(modelsPath, prof); err != nil {
		t.Fatalf("EnsurePiCustomProvider: %v", err)
	}

	data, _ := os.ReadFile(modelsPath)
	var cfg struct {
		Providers map[string]struct {
			BaseURL string `json:"baseUrl"`
			APIKey  string `json:"apiKey"`
			API     string `json:"api"`
			Models  []struct {
				ID            string `json:"id"`
				ContextWindow int    `json:"contextWindow"`
				MaxTokens     int    `json:"maxTokens"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse result: %v\n%s", err, data)
	}

	if _, ok := cfg.Providers["keepme"]; !ok {
		t.Error("existing provider 'keepme' was not preserved")
	}
	ollama, ok := cfg.Providers["ollama"]
	if !ok {
		t.Fatal("ollama provider not written")
	}
	if ollama.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("baseUrl = %q", ollama.BaseURL)
	}
	if ollama.APIKey != "OLLAMA_API_KEY" {
		t.Errorf("apiKey should be the env var name, got %q", ollama.APIKey)
	}
	if len(ollama.Models) != 1 || ollama.Models[0].ID != "qwen2.5-coder" {
		t.Errorf("unexpected models: %+v", ollama.Models)
	}
	if ollama.Models[0].ContextWindow <= 0 || ollama.Models[0].MaxTokens <= 0 {
		t.Errorf("expected defaulted contextWindow/maxTokens, got %+v", ollama.Models[0])
	}

	// Idempotent: a second call must not error or duplicate.
	if err := EnsurePiCustomProvider(modelsPath, prof); err != nil {
		t.Fatalf("second EnsurePiCustomProvider: %v", err)
	}
}

func TestEnsurePiCustomProvider_BuiltinIsNoOp(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	// Built-in provider (no BaseURL) must not create a models.json.
	prof := &PiModelProfile{Provider: "openrouter", Model: "qwen/qwen3-coder", APIKeyEnv: "OPENROUTER_API_KEY"}
	if err := EnsurePiCustomProvider(modelsPath, prof); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(modelsPath); !os.IsNotExist(err) {
		t.Error("built-in provider should not have written models.json")
	}
}

func TestWindowDeathResult(t *testing.T) {
	cases := []struct {
		name string
		task *db.Task
		want execResult
	}{
		{"nil -> needs input", nil, execResult{NeedsInput: true, Message: "Task needs review"}},
		{"done -> success", &db.Task{Status: db.StatusDone, Executor: db.ExecutorClaude}, execResult{Success: true}},
		{"backlog -> interrupted", &db.Task{Status: db.StatusBacklog, Executor: db.ExecutorClaude}, execResult{Interrupted: true}},
		// Claude clean exit with no recorded status stays ambiguous (unchanged behavior).
		{"claude processing -> needs input", &db.Task{Status: db.StatusProcessing, Executor: db.ExecutorClaude}, execResult{NeedsInput: true, Message: "Task needs review"}},
		// Pi clean exit with no self-report -> backstop to success (awaiting review).
		{"pi processing -> success", &db.Task{Status: db.StatusProcessing, Executor: db.ExecutorPi}, execResult{Success: true}},
		// Pi that self-reported blocked via the CLI keeps its blocked state.
		{"pi blocked -> needs input (preserve blocked)", &db.Task{Status: db.StatusBlocked, Executor: db.ExecutorPi}, execResult{NeedsInput: true, Message: "Task needs review"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := windowDeathResult(tc.task)
			if got != tc.want {
				t.Errorf("windowDeathResult() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
