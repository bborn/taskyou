package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The worktree write-guard keeps an isolated task from mutating anything outside
// its own git worktree. TaskYou already tells the agent "the parent repo does not
// exist for you", but that is a soft instruction the model can drift from — most
// easily because the worktree is nested inside the main checkout, so an absolute
// path like /repo/app/x.rb resolves to a real file in main and the write succeeds
// silently. This guard turns that silent escape into a permission decision.
//
// Policy (decided with the user):
//   - Reads are always allowed — legitimate cross-tree access (reference code in
//     main, prod over ssh, shared configs) is overwhelmingly reads.
//   - ANY write whose destination resolves outside the worktree asks the user
//     (main repo, sibling worktrees, $HOME — all the same rule), EXCEPT the system
//     temp dirs (/tmp, /private/tmp, /var/tmp, $TMPDIR), which are always allowed.
//     Temp dirs are shared, ephemeral scratch space; a write there can't corrupt
//     the main checkout or a sibling worktree (the boundary this guard protects),
//     and agents legitimately stage scratch files there — prompting on every /tmp
//     write is pure friction that stalls otherwise-fine work.
//   - In bypassPermissions mode (--dangerously-skip-permissions) there is no human
//     to answer, so the guard fails closed and denies instead of asking.
//   - Only taskyou-managed worktrees are guarded. "Shared dir" projects opted out
//     of isolation, so there is no boundary to protect and the guard is inert.

// WorktreeGuardInput is the canonical, executor-agnostic slice of a pre-tool-use
// payload the guard needs. Every supported agent CLI (Claude, Codex, Gemini,
// OpenCode) normalizes its native hook payload into this shape before evaluating
// the guard, so the policy below has a single source of truth. It is also
// decoupled from any hook JSON struct so the guard can be unit-tested without
// constructing full hook payloads.
//
// ToolName uses each executor's real tool vocabulary (e.g. Claude "Write",
// Codex "apply_patch", Gemini "write_file"/"run_shell_command"); externalWriteTargets
// knows how to read the write destinations out of each.
type WorktreeGuardInput struct {
	ToolName       string
	ToolInput      json.RawMessage
	Cwd            string
	PermissionMode string
}

// WorktreeGuardDecision is the executor-agnostic outcome. The transport layer for
// each CLI maps it onto that CLI's wire format: Claude/Codex emit
// hookSpecificOutput.permissionDecision; Gemini emits {decision,reason}; OpenCode
// throws from its plugin. "ask" is only honored by Claude (whose hook protocol has
// an interactive permission prompt); executors without one downgrade ask→deny to
// fail safe.
type WorktreeGuardDecision struct {
	Decision string // "ask" or "deny"
	Reason   string
}

// bypassPermissionMode is the permission_mode Claude reports when launched with
// --dangerously-skip-permissions. No interactive prompt exists in that mode, so
// the guard denies rather than asks.
const bypassPermissionMode = "bypassPermissions"

// taskWorktreesSegment is the path component every managed worktree lives under
// (see setupWorktree, which creates <project>/.task-worktrees/<id>-<slug>).
var taskWorktreesSegment = string(filepath.Separator) + ".task-worktrees" + string(filepath.Separator)

// EvaluateWorktreeWriteGuard returns a decision when the pending tool call would
// write outside the task's isolated worktree, or nil to let Claude's normal
// permission flow proceed. worktreePath is the task's worktree root (task.WorktreePath);
// allowExternal is an optional allowlist of absolute path prefixes that may be
// written even though they live outside the worktree.
func EvaluateWorktreeWriteGuard(worktreePath string, allowExternal []string, in WorktreeGuardInput) *WorktreeGuardDecision {
	root := cleanPath(worktreePath, "")
	if root == "" || !IsManagedWorktree(root) {
		return nil // not an isolated worktree task — nothing to protect
	}

	escapes := externalWriteTargets(in, root, allowExternal)
	if len(escapes) == 0 {
		return nil
	}

	decision := "ask"
	if in.PermissionMode == bypassPermissionMode {
		decision = "deny"
	}
	return &WorktreeGuardDecision{
		Decision: decision,
		Reason: fmt.Sprintf(
			"%s would write outside your isolated worktree, to: %s. Your worktree is %s — "+
				"all work must stay inside it; the main checkout and other worktrees are off-limits even "+
				"though they exist on disk. Use a path under your worktree instead. If this external write "+
				"is genuinely required, the user must approve it.",
			toolLabel(in.ToolName), strings.Join(escapes, ", "), root,
		),
	}
}

// IsManagedWorktree reports whether path is inside a taskyou-managed worktree
// (under a .task-worktrees/ directory). Mirrors the check used elsewhere in the
// executor for task.WorktreePath.
func IsManagedWorktree(path string) bool {
	return strings.Contains(path, taskWorktreesSegment)
}

// externalWriteTargets returns the resolved write destinations of a tool call that
// fall outside root and outside the allowlist. Returns nil for reads and in-bounds
// writes.
func externalWriteTargets(in WorktreeGuardInput, root string, allowExternal []string) []string {
	var raw []string
	switch in.ToolName {
	// File-edit tools whose target lives in a "file_path" field.
	//   Claude: Edit / Write / MultiEdit   ·   Gemini: write_file / replace
	// (The OpenCode plugin normalizes its write/edit tools to "Write" + file_path.)
	case "Edit", "Write", "MultiEdit", "write_file", "replace":
		raw = stringField(in.ToolInput, "file_path")
	case "NotebookEdit":
		raw = stringField(in.ToolInput, "notebook_path")
	// Shell tools whose command lives in a "command" field.
	//   Claude: Bash   ·   Gemini: run_shell_command
	case "Bash", "run_shell_command":
		raw = bashWriteTargets(in.ToolInput)
	// Codex (and the OpenCode plugin) edit files through apply_patch; the write
	// targets live as marker lines inside the patch envelope carried in "command".
	case "apply_patch":
		raw = applyPatchWriteTargets(in.ToolInput)
	default:
		return nil // Read / Grep / Glob / MCP / etc. — reads are always allowed
	}

	var escapes []string
	for _, p := range raw {
		abs := cleanPath(p, in.Cwd)
		if abs == "" || isIgnorableSink(abs) || isAllowedTempDir(abs) {
			continue
		}
		if pathWithin(root, abs) || allowlisted(abs, allowExternal) {
			continue
		}
		escapes = append(escapes, abs)
	}
	return dedupe(escapes)
}

// --- Bash command analysis (best-effort) -----------------------------------
//
// Shell can't be parsed perfectly, so this targets the high-signal, low-false-
// positive patterns that escape a worktree. The structured Edit/Write guard above
// is the reliable backbone; this is the supplement for shell mutations like the
// `cd /main && git checkout` that caused the incident this guard was built for.

var (
	reRedirect  = regexp.MustCompile(`(?:^|[\s;&|(])\d*>>?\s*("?)([^\s"';|&)]+)`)
	reCd        = regexp.MustCompile(`(?:^|[\s;&|(])cd\s+("?)([^\s"';|&)]+)`)
	reGitC      = regexp.MustCompile(`\bgit\s+-C\s+("?)([^\s"';|&)]+)`)
	reGitWrite  = regexp.MustCompile(`\bgit\s+(?:-C\s+\S+\s+)?(?:commit|checkout|switch|reset|restore|clean|apply|stash|rm|mv|add|merge|rebase|cherry-pick|push|pull)\b`)
	reMutator   = regexp.MustCompile(`(?:^|[\s;&|(])(?:rm|mv|cp|tee|touch|mkdir|rmdir|ln|dd|truncate|install|chmod|chown)\b`)
	reMutTarget = regexp.MustCompile(`(?:^|[\s;&|(])(?:rm|tee|touch|mkdir|truncate)\s+(?:-[^\s]+\s+)*("?)((?:/|~/|\.\./)[^\s"';|&)]+)`)
)

// bashWriteTargets extracts candidate write destinations from a Bash command.
func bashWriteTargets(rawInput json.RawMessage) []string {
	cmds := stringField(rawInput, "command")
	if len(cmds) == 0 {
		return nil
	}
	command := cmds[0]

	mutates := reGitWrite.MatchString(command) || reMutator.MatchString(command) || reRedirect.MatchString(command)

	var targets []string
	// 1. Output redirections (`> file`, `2>> file`). /dev/null & friends are filtered later.
	for _, m := range reRedirect.FindAllStringSubmatch(command, -1) {
		targets = append(targets, m[2])
	}
	// 2. `git -C <dir> <mutating>` — the -C dir is where the mutation lands.
	if reGitWrite.MatchString(command) {
		for _, m := range reGitC.FindAllStringSubmatch(command, -1) {
			targets = append(targets, m[2])
		}
	}
	// 3. `cd <dir>` combined with any mutation in the same command line.
	if mutates {
		for _, m := range reCd.FindAllStringSubmatch(command, -1) {
			targets = append(targets, m[2])
		}
	}
	// 4. Single-target mutators given an absolute/home/parent path (`rm -rf /x`).
	for _, m := range reMutTarget.FindAllStringSubmatch(command, -1) {
		targets = append(targets, m[2])
	}
	return targets
}

// --- apply_patch analysis (Codex / OpenCode) -------------------------------
//
// Codex performs all file edits through an apply_patch tool whose tool_input.command
// holds a patch envelope. The mutated files are named by marker lines, e.g.:
//
//	*** Begin Patch
//	*** Add File: path/to/new.go
//	*** Update File: app/models/event.rb
//	*** Move to: app/models/renamed.rb
//	*** Delete File: old/thing.txt
//	*** End Patch
//
// Paths are relative to the session cwd (the worktree) unless absolute. We extract
// every Add/Update/Delete target plus any Move destination, then the shared path
// logic decides whether any of them escape the worktree.
var (
	reApplyPatchFile = regexp.MustCompile(`(?m)^\s*\*\*\*\s+(?:Add|Update|Delete) File:\s*(.+?)\s*$`)
	reApplyPatchMove = regexp.MustCompile(`(?m)^\s*\*\*\*\s+Move to:\s*(.+?)\s*$`)
)

// applyPatchWriteTargets extracts the files an apply_patch envelope would mutate.
func applyPatchWriteTargets(rawInput json.RawMessage) []string {
	cmds := stringField(rawInput, "command")
	if len(cmds) == 0 {
		return nil
	}
	patch := cmds[0]

	var targets []string
	for _, m := range reApplyPatchFile.FindAllStringSubmatch(patch, -1) {
		targets = append(targets, strings.Trim(strings.TrimSpace(m[1]), `"'`))
	}
	for _, m := range reApplyPatchMove.FindAllStringSubmatch(patch, -1) {
		targets = append(targets, strings.Trim(strings.TrimSpace(m[1]), `"'`))
	}
	return targets
}

// --- path helpers ----------------------------------------------------------

// cleanPath resolves p to an absolute, cleaned path. Relative paths resolve against
// cwd; a leading ~ expands to the home directory. Returns "" when p is empty or a
// relative path can't be resolved (no cwd) — callers treat "" as in-bounds.
func cleanPath(p, cwd string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"'`)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	if !filepath.IsAbs(p) {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			return ""
		}
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// pathWithin reports whether target is at or below root. Both must be absolute.
// Symlinks are resolved first so a worktree under a symlinked path isn't mistaken for
// an escape — e.g. macOS /tmp → /private/tmp: WORKTREE_PATH is /tmp/… but a tool may
// resolve a write to /private/tmp/…, which without this reads as outside the worktree
// and gets falsely denied (trapping the agent in a retry loop). Resolving also makes
// the guard *stricter* against a real symlink that escapes the worktree.
func pathWithin(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	root = resolveSymlinks(root)
	target = resolveSymlinks(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolveSymlinks returns path with its longest existing prefix resolved through
// symlinks, re-appending any not-yet-existing tail. A write target need not exist yet
// (it's about to be created), so we resolve the deepest ancestor directory that does.
func resolveSymlinks(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	rest := ""
	dir := path
	for {
		parent := filepath.Dir(dir)
		rest = filepath.Join(filepath.Base(dir), rest)
		if parent == dir {
			return path // reached the filesystem root without resolving anything
		}
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
	}
}

// allowlisted reports whether abs is at or below any allowlisted prefix.
func allowlisted(abs string, allow []string) bool {
	for _, a := range allow {
		root := cleanPath(a, "")
		if root != "" && pathWithin(root, abs) {
			return true
		}
	}
	return false
}

// isAllowedTempDir reports whether abs is inside a system temp directory. Writes
// there are always allowed: temp dirs are shared, ephemeral scratch and can't
// corrupt the main checkout or a sibling worktree, which is all this guard exists
// to protect. Symlinks are resolved (macOS /tmp → /private/tmp) so both spellings
// match.
func isAllowedTempDir(abs string) bool {
	for _, root := range tempWriteRoots {
		if pathWithin(root, abs) {
			return true
		}
	}
	return false
}

// tempWriteRoots is the set of system temp directories writes are exempted into.
// os.TempDir() covers $TMPDIR (macOS per-user /var/folders/.../T); the fixed roots
// cover the conventional locations agents reach for directly.
var tempWriteRoots = func() []string {
	roots := []string{"/tmp", "/private/tmp", "/var/tmp"}
	if td := strings.TrimSpace(os.TempDir()); td != "" {
		roots = append(roots, td)
	}
	return roots
}()

// isIgnorableSink reports whether abs is a non-file write sink (e.g. /dev/null)
// that should never count as escaping the worktree.
func isIgnorableSink(abs string) bool {
	switch abs {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/zero":
		return true
	}
	return strings.HasPrefix(abs, "/dev/fd/")
}

func toolLabel(name string) string {
	if name == "Bash" {
		return "This Bash command"
	}
	if name == "" {
		return "This tool call"
	}
	return "This " + name
}

// stringField pulls a single string field out of a tool_input JSON object.
func stringField(raw json.RawMessage, key string) []string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil || strings.TrimSpace(s) == "" {
		return nil
	}
	return []string{s}
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
