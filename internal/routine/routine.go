// Package routine implements named, managed headless agent runs.
//
// A routine is a directory under ~/.config/task/routines/<name>/ containing a
// prompt.md (the agent prompt, with optional frontmatter for model/project/
// timeout) and an optional env.sh sourced before each run for secrets and
// fail-fast checks. Routines have no scheduler: anything — launchd, cron, a
// shell — triggers a run with `ty run <name>`. TaskYou owns the run itself:
// state, logs, history, and failure alerting.
package routine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultModel keeps unattended runs on a cheap model unless the routine
	// explicitly opts into a bigger one.
	DefaultModel = "sonnet"

	// DefaultTimeout bounds a hung headless run so an unattended scheduler
	// invocation can't wedge forever.
	DefaultTimeout = 30 * time.Minute

	promptFile   = "prompt.md"
	envFile      = "env.sh"
	disabledFile = "disabled"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Routine is a loaded routine definition.
type Routine struct {
	Name           string
	Dir            string        // ~/.config/task/routines/<name>
	Prompt         string        // prompt.md body with frontmatter stripped
	Model          string        // claude model (frontmatter "model", default sonnet)
	Project        string        // project for emitted/alert tasks (frontmatter "project")
	PermissionMode string        // frontmatter "permission-mode", default dangerous (headless runs can't answer prompts)
	Timeout        time.Duration // frontmatter "timeout" (Go duration), default 30m
	WorkDir        string        // frontmatter "dir": run cwd override (for repo context / project-scoped MCP); default = state dir
	Disabled       bool          // presence of a "disabled" file in Dir
}

// RoutinesDir returns the directory routine definitions live in.
func RoutinesDir() string {
	if dir := os.Getenv("TY_ROUTINES_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "task", "routines")
}

// StateDir returns the per-routine state directory. It is exported to the
// agent as ROUTINE_STATE_DIR and used as the run's working directory, so
// routines keep cross-run state (seen IDs, cursors) without inventing paths.
func StateDir(name string) string {
	if dir := os.Getenv("TY_ROUTINES_STATE_DIR"); dir != "" {
		return filepath.Join(dir, name)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "task", "routines", name)
}

// PluginRoutineDirs, when set, returns extra directories that hold routine
// definitions contributed by installed plugins (each a routines/ subdir whose
// children are <name>/prompt.md). Wired up in main so this package needn't
// import the hooks/plugins package. Read paths (Load, Exists, List) search the
// user's RoutinesDir first, then these — so a user routine shadows a plugin one
// of the same name, and plugins extend the set the same way they do workflows.
var PluginRoutineDirs func() []string

// searchDirs returns every base directory routine definitions may live in: the
// user's dir first (it wins on name collisions), then plugin-contributed dirs.
func searchDirs() []string {
	dirs := []string{RoutinesDir()}
	if PluginRoutineDirs != nil {
		dirs = append(dirs, PluginRoutineDirs()...)
	}
	return dirs
}

// resolveDir returns the directory holding routine <name> — the first base dir
// (user first, then plugins) that contains <name>/prompt.md — or "" if none does.
func resolveDir(name string) string {
	for _, base := range searchDirs() {
		dir := filepath.Join(base, name)
		if _, err := os.Stat(filepath.Join(dir, promptFile)); err == nil {
			return dir
		}
	}
	return ""
}

// ValidateName rejects names that aren't safe as path components.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid routine name %q: use lowercase letters, digits, - and _", name)
	}
	return nil
}

// Exists reports whether a routine with a prompt.md exists in any search dir.
func Exists(name string) bool {
	if ValidateName(name) != nil {
		return false
	}
	return resolveDir(name) != ""
}

// Load reads a routine definition from disk.
func Load(name string) (*Routine, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	dir := resolveDir(name)
	if dir == "" {
		return nil, fmt.Errorf("routine %q not found (expected %s)", name, filepath.Join(RoutinesDir(), name, promptFile))
	}
	raw, err := os.ReadFile(filepath.Join(dir, promptFile))
	if err != nil {
		return nil, fmt.Errorf("read routine %q: %w", name, err)
	}

	meta, body, err := parseFrontmatter(string(raw))
	if err != nil {
		return nil, fmt.Errorf("routine %q: %w", name, err)
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("routine %q: prompt.md has no prompt body", name)
	}

	r := &Routine{
		Name:           name,
		Dir:            dir,
		Prompt:         body,
		Model:          DefaultModel,
		PermissionMode: "dangerous",
		Timeout:        DefaultTimeout,
	}
	for key, value := range meta {
		switch key {
		case "model":
			r.Model = value
		case "project":
			r.Project = value
		case "permission-mode":
			r.PermissionMode = value
		case "dir":
			if strings.HasPrefix(value, "~/") {
				home, _ := os.UserHomeDir()
				value = filepath.Join(home, value[2:])
			}
			r.WorkDir = value
		case "timeout":
			d, err := time.ParseDuration(value)
			if err != nil {
				return nil, fmt.Errorf("routine %q: invalid timeout %q: %w", name, value, err)
			}
			r.Timeout = d
		default:
			return nil, fmt.Errorf("routine %q: unknown frontmatter key %q (valid: model, project, permission-mode, timeout, dir)", name, key)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, disabledFile)); err == nil {
		r.Disabled = true
	}
	return r, nil
}

// List loads every routine under RoutinesDir, sorted by name. Directories
// without a prompt.md are skipped; unparseable routines are returned as an
// error so a typo doesn't silently hide a routine from the list.
func List() ([]*Routine, error) {
	seen := map[string]bool{}
	var routines []*Routine
	// User dir first (index 0): a parse error there is fatal so a typo can't
	// silently hide a routine. Plugin dirs are forgiving — one broken plugin
	// routine must not break `ty routines` — matching how plugins load.
	for i, base := range searchDirs() {
		userDir := i == 0
		entries, err := os.ReadDir(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if userDir {
				return nil, fmt.Errorf("read routines dir: %w", err)
			}
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || ValidateName(name) != nil || seen[name] {
				continue
			}
			if _, err := os.Stat(filepath.Join(base, name, promptFile)); err != nil {
				continue
			}
			r, err := Load(name)
			if err != nil {
				if userDir {
					return nil, err
				}
				continue
			}
			seen[name] = true
			routines = append(routines, r)
		}
	}
	sort.Slice(routines, func(i, j int) bool { return routines[i].Name < routines[j].Name })
	return routines, nil
}

// EnvPath returns the routine's env.sh path and whether it exists.
func (r *Routine) EnvPath() (string, bool) {
	p := filepath.Join(r.Dir, envFile)
	_, err := os.Stat(p)
	return p, err == nil
}

// SetDisabled creates or removes the routine's disabled marker file.
func (r *Routine) SetDisabled(disabled bool) error {
	marker := filepath.Join(r.Dir, disabledFile)
	if disabled {
		if err := os.WriteFile(marker, []byte("disabled by ty routines disable\n"), 0o644); err != nil {
			return fmt.Errorf("disable routine: %w", err)
		}
	} else {
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("enable routine: %w", err)
		}
	}
	r.Disabled = disabled
	return nil
}

// parseFrontmatter splits an optional leading "---\nkey: value\n---" block
// from the body. Only flat key: value pairs are supported — routines should
// stay simple enough that this never needs a YAML library.
func parseFrontmatter(content string) (map[string]string, string, error) {
	if !strings.HasPrefix(content, "---\n") && content != "---" {
		return nil, content, nil
	}
	rest := strings.TrimPrefix(content, "---\n")
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return nil, "", fmt.Errorf("frontmatter opened with --- but never closed")
	}
	block := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")

	meta := make(map[string]string)
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, "", fmt.Errorf("invalid frontmatter line %q (expected key: value)", line)
		}
		meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return meta, body, nil
}

// Delete removes the routine's definition directory and its state directory
// (including logs). Run history in the database is the caller's to prune via
// db.DeleteRoutineRuns.
func Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(RoutinesDir(), name)); err != nil {
		return fmt.Errorf("delete routine dir: %w", err)
	}
	if err := os.RemoveAll(StateDir(name)); err != nil {
		return fmt.Errorf("delete routine state dir: %w", err)
	}
	return nil
}

// Scaffold creates a new routine directory with a template prompt.md.
func Scaffold(name string) (*Routine, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	dir := filepath.Join(RoutinesDir(), name)
	promptPath := filepath.Join(dir, promptFile)
	if _, err := os.Stat(promptPath); err == nil {
		return nil, fmt.Errorf("routine %q already exists (%s)", name, promptPath)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create routine dir: %w", err)
	}
	template := `---
model: sonnet
# project: personal
# timeout: 30m
# permission-mode: dangerous
---
You are a routine that runs unattended on a schedule. Describe the job here.

Your working directory is your private state directory (also $ROUTINE_STATE_DIR);
keep any cross-run state (seen IDs, cursors) in files there.

If you find work for a human, emit it as a task:
  ty create --project personal --title "..." --body "..."

Exit normally when done. If something is broken, fail loudly — a failed run
creates an alert task automatically.
`
	if err := os.WriteFile(promptPath, []byte(template), 0o644); err != nil {
		return nil, fmt.Errorf("write prompt template: %w", err)
	}
	return Load(name)
}
