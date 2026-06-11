package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// This file wires the worktree write-guard (EvaluateWorktreeWriteGuard, the single
// source of truth in worktree_guard.go) into the *non-Claude* executors by writing
// each CLI's native pre-tool hook configuration into the task's worktree. Every
// hook calls back into this binary's hidden `worktree-guard` subcommand, which runs
// the shared guard and renders the decision in that CLI's wire format.
//
// Per-executor mechanism (researched against each CLI's official docs):
//
//   - Codex CLI  — PreToolUse lifecycle hook (`<worktree>/.codex/hooks.json`). Codex
//     emits the same hookSpecificOutput/permissionDecision contract as Claude, but
//     does NOT support permissionDecision:"ask" (it is "parsed but not supported"),
//     so the transport downgrades ask→deny to fail safe. Codex covers file edits via
//     the apply_patch tool and shell via Bash; the guard understands both.
//
//   - Gemini CLI — BeforeTool hook (`<worktree>/.gemini/settings.json`). Gemini's
//     hook protocol is allow/deny only (no interactive "ask"), so ask→deny as well.
//     Write tools are write_file/replace; shell is run_shell_command.
//
//   - OpenCode   — `tool.execute.before` plugin hook (a generated JS plugin in
//     `<worktree>/.opencode/plugins/`). The plugin normalizes the tool call and
//     shells back into `worktree-guard`; a non-zero exit makes it throw, which
//     OpenCode treats as a denial. OpenCode has no human-prompt path here either,
//     so ask→deny.
//
// Known gap / OS-level fallback: none of the three expose an interactive "ask" in
// their pre-tool hook, so an external write that Claude would prompt on is instead
// hard-denied (the user's escape hatch is worktree.allow_external_writes in
// .taskyou.yml). Additionally, in each CLI's fully-bypassed/dangerous mode the
// hook may not fire; for Codex specifically the default `workspace-write` OS sandbox
// already confines writes to the worktree, which is the recommended defense-in-depth
// when running without the guard.

// resolveTaskBin returns the absolute path to the running ty/task binary so hook
// configs can call back into it. Falls back to a PATH lookup, then the bare name.
func resolveTaskBin() string {
	if bin, err := os.Executable(); err == nil && bin != "" {
		return bin
	}
	if bin, _ := exec.LookPath("ty"); bin != "" {
		return bin
	}
	if bin, _ := exec.LookPath("task"); bin != "" {
		return bin
	}
	return "task"
}

// setupCodexWorktreeGuard writes a Codex PreToolUse hook into the worktree that
// enforces the write-guard. Returns a cleanup that restores the prior state.
func (e *Executor) setupCodexWorktreeGuard(workDir, projectDir string) (func(), error) {
	cmd := fmt.Sprintf("%q worktree-guard --format codex", resolveTaskBin())
	cleanup, err := writeMergedCommandHook(filepath.Join(workDir, ".codex", "hooks.json"), "PreToolUse", cmd)
	if err == nil && projectDir != "" {
		ensureGitExclude(projectDir, ".codex")
	}
	return cleanup, err
}

// setupGeminiWorktreeGuard writes a Gemini BeforeTool hook into the worktree that
// enforces the write-guard. Returns a cleanup that restores the prior state.
func (e *Executor) setupGeminiWorktreeGuard(workDir, projectDir string) (func(), error) {
	cmd := fmt.Sprintf("%q worktree-guard --format gemini", resolveTaskBin())
	cleanup, err := writeMergedCommandHook(filepath.Join(workDir, ".gemini", "settings.json"), "BeforeTool", cmd)
	if err == nil && projectDir != "" {
		ensureGitExclude(projectDir, ".gemini")
	}
	return cleanup, err
}

// writeMergedCommandHook appends a {"type":"command","command":cmd} hook under
// hooks.<event> in the JSON file at path, preserving any existing configuration,
// and returns a cleanup that restores the original file (or removes it if it was
// created here). Codex (hooks.json) and Gemini (settings.json) share this shape.
func writeMergedCommandHook(path, event, command string) (func(), error) {
	existingData, existingErr := os.ReadFile(path)

	cfg := map[string]any{}
	if existingErr == nil {
		if err := json.Unmarshal(existingData, &cfg); err != nil {
			cfg = map[string]any{}
		}
	}

	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	events, _ := hooks[event].([]any)
	hooks[event] = append(events, map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	})
	cfg["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, err
	}

	cleanup := func() {
		if existingErr == nil {
			_ = os.WriteFile(path, existingData, 0644)
		} else {
			_ = os.Remove(path)
		}
	}
	return cleanup, nil
}

// setupOpenCodeWorktreeGuard writes a generated OpenCode plugin into the worktree
// that enforces the write-guard via the tool.execute.before hook. Returns a cleanup
// that removes the generated plugin file.
func (e *Executor) setupOpenCodeWorktreeGuard(workDir, projectDir string) (func(), error) {
	dir := filepath.Join(workDir, ".opencode", "plugins")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "taskyou-worktree-guard.js")

	binJSON, err := json.Marshal(resolveTaskBin())
	if err != nil {
		return nil, err
	}
	contents := "// Auto-generated by TaskYou — worktree write-guard for OpenCode.\n" +
		"// Removed automatically when the task session ends.\n" +
		"const TY_BIN = " + string(binJSON) + "\n" +
		openCodeGuardPluginBody

	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		return nil, err
	}
	if projectDir != "" {
		ensureGitExclude(projectDir, ".opencode")
	}
	return func() { _ = os.Remove(path) }, nil
}

// openCodeGuardPluginBody is the static JS body of the generated OpenCode plugin.
// TY_BIN is prepended at generation time. The plugin normalizes each tool call into
// the guard's canonical shape and shells back into `worktree-guard --format opencode`;
// a clean exit code of 1 means "deny", which it surfaces by throwing (aborting the
// tool call). Any other outcome fails open so a guard malfunction never wedges the
// agent.
const openCodeGuardPluginBody = `import { spawnSync } from "node:child_process"

const WORKTREE_PATH = process.env.WORKTREE_PATH || ""

// Map an OpenCode tool call to the guard's canonical {tool_name, tool_input}.
// Reads are not mapped (returns null) and therefore always allowed.
function normalizeToolCall(tool, args) {
  switch (tool) {
    case "bash":
      return { tool_name: "Bash", tool_input: { command: (args && args.command) || "" } }
    case "write":
    case "edit":
      return { tool_name: "Write", tool_input: { file_path: (args && args.filePath) || "" } }
    case "apply_patch":
      return { tool_name: "apply_patch", tool_input: { command: (args && args.patchText) || "" } }
    default:
      return null
  }
}

export const TaskYouWorktreeGuard = async ({ directory }) => {
  return {
    "tool.execute.before": async (input, output) => {
      if (!WORKTREE_PATH) return
      const payload = normalizeToolCall(input && input.tool, output && output.args)
      if (!payload) return
      payload.cwd = directory || WORKTREE_PATH
      payload.permission_mode = process.env.WORKTREE_DANGEROUS_MODE === "1" ? "bypassPermissions" : ""

      let res
      try {
        res = spawnSync(TY_BIN, ["worktree-guard", "--format", "opencode"], {
          input: JSON.stringify(payload),
          encoding: "utf8",
        })
      } catch (err) {
        return // fail open: never block the agent on a guard malfunction
      }
      // Only a clean exit code of 1 is a deny; anything else (allow, spawn error,
      // unexpected code) is treated as allow.
      if (res && res.status === 1) {
        const reason = ((res.stdout || "").trim()) || "Write blocked: outside the isolated worktree."
        throw new Error(reason)
      }
    },
  }
}
`
