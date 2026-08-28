package db

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Per-task model overrides pick which model an executor's CLI runs (its
// `--model` flag). An empty value means "no override": the task uses the
// agent's own default, leaving the user's global setting untouched.
//
// Overrides are validated before they are stored because a bad one is
// invisible: the task launches, the agent CLI rejects the flag inside tmux,
// and the card just sits there looking busy. An early default baked the
// executor slug "claude" into every row (see the clear-model repair in
// sqlite.go) — exactly that failure.
const (
	ModelFable  = "fable"
	ModelOpus   = "opus"
	ModelSonnet = "sonnet"
	ModelHaiku  = "haiku"
	// ModelOpusPlan is Claude Code's planning alias (Opus to plan, Sonnet to
	// execute). Accepted as an override but kept out of the UI picker.
	ModelOpusPlan = "opusplan"
)

// executorModelPrefix maps an executor to the ID prefixes its models carry.
// Only executors listed here pass a --model flag to their CLI at all; for the
// rest (codex, gemini, pi, opencode, openclaw) a model override is dead config
// — the launch command never mentions it. Cursor is multi-vendor, so it has
// several prefixes rather than one.
var executorModelPrefix = map[string][]string{
	ExecutorClaude: {"claude-"},
	ExecutorGrok:   {"grok-"},
	ExecutorCursor: {"gpt-", "composer-", "claude-", "grok-", "gemini-", "sonnet-", "opus-"},
}

// executorModelAliases lists the short names an executor's CLI accepts in place
// of a full model ID. Full IDs are matched by prefix + shape instead (see
// ValidateModel), so a newly released model needs no code change here; a new
// *alias* does — that's how "fable" arrived alongside opus/sonnet/haiku.
var executorModelAliases = map[string][]string{
	ExecutorClaude: {ModelOpus, ModelSonnet, ModelHaiku, ModelFable, ModelOpusPlan},
	// The grok CLI takes full IDs only; these are examples for help text and
	// shell completion, not a closed set (any grok-* ID is accepted).
	ExecutorGrok: {"grok-4", "grok-4-fast", "grok-code-fast-1"},
	// Cursor is multi-vendor; these are examples for help text and shell
	// completion. Any well-formed model ID is also accepted.
	ExecutorCursor: {"auto", "composer-1", "gpt-5", "sonnet-4", "grok"},
}

// modelIDShape matches the shape of a vendor model ID: dash-separated
// lowercase alphanumeric segments, with an optional bracketed variant suffix
// (Claude Code's 1M-context form, e.g. "claude-opus-5[1m]"). It deliberately
// says nothing about which models exist — the vendor prefix does that — so
// "claude-opus-9" passes and "claude opus" or "claude;rm -rf" does not.
var modelIDShape = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+)*(-[a-z0-9]+(\.[a-z0-9]+)*)*(\[[a-z0-9]+\])?$`)

// ModelOptions returns the per-task model override aliases offered in the UI
// picker. It is a curated shortlist, not the set ValidateModel accepts: full
// model IDs and opusplan are valid overrides but not picker entries.
func ModelOptions() []string {
	return []string{ModelOpus, ModelSonnet, ModelHaiku, ModelFable}
}

// ExecutorSupportsModel reports whether the executor's CLI takes a --model
// flag. An empty executor means the default (claude).
func ExecutorSupportsModel(executor string) bool {
	_, ok := executorModelPrefix[resolveExecutor(executor)]
	return ok
}

// ModelCapableExecutors returns the executors that accept a model override, in
// KnownExecutors display order.
func ModelCapableExecutors() []string {
	var out []string
	for _, e := range KnownExecutors() {
		if ExecutorSupportsModel(e) {
			out = append(out, e)
		}
	}
	return out
}

// ModelsForExecutor returns the model names ty knows about for an executor, in
// display order — used for `ty create --model` shell completion and error
// messages. It is not exhaustive: any ID carrying the executor's vendor prefix
// is also accepted. Nil when the executor has no --model flag.
func ModelsForExecutor(executor string) []string {
	aliases := executorModelAliases[resolveExecutor(executor)]
	return append([]string(nil), aliases...)
}

// ValidateModel reports whether model is a model the executor's CLI would
// actually accept. The empty string is always valid and means "no override".
//
// A model is accepted when it is one of the executor's aliases (optionally with
// a "[1m]" variant suffix) or a well-formed ID carrying the executor's vendor
// prefix — "claude-opus-5" and "grok-4-fast" pass without ty having to track
// every release. Everything else is rejected, which catches the two mistakes
// that actually happen: a typo ("opuss") and a model belonging to some other
// vendor ("gpt-5", or the "claude" executor slug).
//
// Callers must skip this check when the agent is routed at a non-stock backend
// — a CLAUDE_CONFIG_DIR override or ANTHROPIC_BASE_URL pointing at a proxy like
// ollama — because the model names are then the proxy's (e.g. "glm-5.2:cloud")
// and ty has no way to know them. See ModelBackendIsCustom.
func ValidateModel(executor, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	executor = resolveExecutor(executor)
	if !ExecutorSupportsModel(executor) {
		return fmt.Errorf("the %s executor has no --model flag, so %q would be silently ignored (model overrides work with: %s)",
			executor, model, strings.Join(ModelCapableExecutors(), ", "))
	}
	lower := strings.ToLower(model)
	for _, alias := range executorModelAliases[executor] {
		if lower == alias || strings.HasPrefix(lower, alias+"[") && modelIDShape.MatchString(lower) {
			return nil
		}
	}
	prefixes := executorModelPrefix[executor]
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) && modelIDShape.MatchString(lower) {
			return nil
		}
	}
	prefixHint := strings.Join(prefixes, "* / ") + "*"
	if len(prefixes) == 1 {
		prefixHint = prefixes[0] + "*"
	}
	return fmt.Errorf("unknown %s model %q — known models: %s (or any %s model ID)",
		executor, model, strings.Join(ModelsForExecutor(executor), ", "), prefixHint)
}

// ValidateTaskModel checks a task's model override against the executor that
// will run it, skipping the check when the task is routed at a non-stock
// backend. This is the entry point every write path should use — it resolves
// the escape hatch (per-task config dir or env, the project's config dir, an
// ambient ANTHROPIC_BASE_URL) that a bare ValidateModel call cannot see.
func (db *DB) ValidateTaskModel(t *Task) error {
	if t == nil || strings.TrimSpace(t.Model) == "" {
		return nil
	}
	if db.taskModelBackendIsCustom(t) {
		return nil
	}
	return ValidateModel(t.Executor, t.Model)
}

// taskModelBackendIsCustom reports whether anything in a task's resolved
// configuration points Claude away from Anthropic's API: the task's own config
// dir or env, the project's config dir, or ANTHROPIC_BASE_URL in the
// environment ty itself is running in.
func (db *DB) taskModelBackendIsCustom(t *Task) bool {
	if ModelBackendIsCustom(t.ClaudeConfigDir, t.EnvMap()) {
		return true
	}
	if strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL")) != "" {
		return true
	}
	if t.Project == "" {
		return false
	}
	p, err := db.GetProjectByName(t.Project)
	if err != nil || p == nil {
		return false
	}
	return ModelBackendIsCustom(p.ClaudeConfigDir, nil)
}

// ModelBackendIsCustom reports whether a Claude run is pointed at something
// other than Anthropic's API — a CLAUDE_CONFIG_DIR override or an
// ANTHROPIC_BASE_URL env override (the two ways ty routes a task through a
// proxy such as ollama). Model names are the proxy's there, so overrides must
// not be validated against Anthropic's.
func ModelBackendIsCustom(configDir string, env map[string]string) bool {
	if strings.TrimSpace(configDir) != "" {
		return true
	}
	return strings.TrimSpace(env["ANTHROPIC_BASE_URL"]) != ""
}

// IsValidModel reports whether s is an acceptable per-task model override for
// *some* executor. The empty string is valid and means "use the agent default".
// Prefer ValidateModel when the executor is known; this looser form exists for
// callers holding a remembered value with no executor in hand (the new-task
// form's per-project default).
func IsValidModel(s string) bool {
	for _, e := range KnownExecutors() {
		if ValidateModel(e, s) == nil {
			return true
		}
	}
	return false
}

// resolveExecutor normalizes an executor slug, mapping "" to the default.
func resolveExecutor(executor string) string {
	executor = strings.ToLower(strings.TrimSpace(executor))
	if executor == "" {
		return DefaultExecutor()
	}
	return executor
}
