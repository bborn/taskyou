package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestName is the file every plugin directory must contain to be loaded.
const ManifestName = "plugin.yaml"

// Plugin is a self-contained, droppable unit that reacts to task events.
//
// A plugin is a directory under the plugins dir (default
// ~/.config/task/plugins/<name>/) containing a plugin.yaml manifest and the
// scripts it references. Unlike the legacy single-script hooks dir — where one
// file per event means two integrations collide — any number of plugins may
// each declare a handler for the same event, and all of them run.
type Plugin struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
	// Hooks maps a task event name (e.g. "task.done") to a script path,
	// resolved relative to the plugin directory. Hooks fire automatically.
	Hooks map[string]string `yaml:"hooks"`
	// Actions are user-invoked commands (from `ty plugins run`, the detail-view
	// picker, or the command palette), each backed by a script in the plugin dir.
	Actions []Action `yaml:"actions"`

	// Dir is the absolute path to the plugin directory (not from the manifest).
	Dir string `yaml:"-"`
	// Workflows holds the names (filename stems) of the workflow definitions this
	// plugin ships in its workflows/ subdir. Discovered by convention at load time,
	// not declared in the manifest — a plugin is workflow cargo the moment it has a
	// workflows/*.yaml. Populated for display; the definitions themselves are loaded
	// by the pipeline registry via PluginWorkflowDirs.
	Workflows []string `yaml:"-"`
	// Routines holds the names of the routine definitions this plugin ships in its
	// routines/ subdir (each a <name>/prompt.md). Discovered by convention, like
	// workflows. Populated for display; the routine package resolves the definitions
	// via PluginRoutineDirs. A routine is the right home for a plugin's periodic job
	// (a poller, a digest): the daemon-supervised alternative, a service, is for
	// processes that must stay up, not run-to-completion on a schedule.
	Routines []string `yaml:"-"`
}

// WorkflowsDir returns the plugin's workflows subdir, whether or not it exists.
func (p Plugin) WorkflowsDir() string {
	return filepath.Join(p.Dir, "workflows")
}

// RoutinesDir returns the plugin's routines subdir, whether or not it exists.
func (p Plugin) RoutinesDir() string {
	return filepath.Join(p.Dir, "routines")
}

// Action is a user-triggered plugin command.
type Action struct {
	ID      string `yaml:"id"`      // stable identifier, unique within the plugin
	Label   string `yaml:"label"`   // human-facing label; defaults to ID if empty
	Command string `yaml:"command"` // script path, relative to the plugin dir
}

// DisplayLabel returns the label, falling back to the ID.
func (a Action) DisplayLabel() string {
	if a.Label != "" {
		return a.Label
	}
	return a.ID
}

// ScriptFor returns the absolute path to the script handling event, and whether
// the plugin handles that event at all.
func (p Plugin) ScriptFor(event string) (string, bool) {
	rel, ok := p.Hooks[event]
	if !ok || rel == "" {
		return "", false
	}
	return filepath.Join(p.Dir, rel), true
}

// Action returns the action with the given ID.
func (p Plugin) Action(id string) (Action, bool) {
	for _, a := range p.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// DefaultPluginsDir returns the plugins directory path. The TY_PLUGINS_DIR
// environment variable overrides the default (~/.config/task/plugins), which is
// handy for relocating plugins and for test isolation.
func DefaultPluginsDir() string {
	if dir := os.Getenv("TY_PLUGINS_DIR"); dir != "" {
		return dir
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "task", "plugins")
}

// LoadPlugins discovers and validates every plugin under pluginsDir.
//
// It is intentionally forgiving: a malformed or incomplete plugin is skipped
// (and reported via the returned warnings) rather than failing the whole load,
// so one bad community plugin can't break a user's task pipeline. A missing
// plugins dir is not an error — it just yields no plugins.
func LoadPlugins(pluginsDir string) (plugins []Plugin, warnings []string) {
	if pluginsDir == "" {
		return nil, nil
	}
	plugins, warnings = discoverPluginsIn(pluginsDir, 0)
	// Deterministic order so fan-out and `plugins list` are stable.
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	return plugins, warnings
}

// maxPluginDepth bounds how deep discovery descends into non-plugin directories,
// so a single community repo can nest plugins (repo/plugin, repo/category/plugin)
// without discovery walking an unbounded tree.
const maxPluginDepth = 4

// discoverPluginsIn finds plugins under dir. A subdirectory that contains a
// plugin.yaml IS a plugin (loaded, not descended into); a subdirectory that does
// not is treated as a collection and recursed into. This lets `ty plugins add`
// clone a whole monorepo of plugins as one directory and have every plugin inside
// become active.
func discoverPluginsIn(dir string, depth int) (plugins []Plugin, warnings []string) {
	if depth > maxPluginDepth {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("read plugins dir %s: %v", dir, err)}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sub := filepath.Join(dir, entry.Name())
		if _, err := os.Stat(filepath.Join(sub, ManifestName)); err == nil {
			// It's a plugin; load it and do not descend into its own subdirs.
			p, warn := loadPlugin(sub)
			if warn != "" {
				warnings = append(warnings, warn)
			}
			if p != nil {
				plugins = append(plugins, *p)
			}
			continue
		}
		// No manifest here — treat as a collection and look one level deeper.
		nested, nwarn := discoverPluginsIn(sub, depth+1)
		plugins = append(plugins, nested...)
		warnings = append(warnings, nwarn...)
	}
	return plugins, warnings
}

// loadPlugin parses and validates a single plugin directory. It returns a nil
// plugin (with a warning) when the directory should be skipped.
func loadPlugin(dir string) (*Plugin, string) {
	manifestPath := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Not a plugin directory; silently ignore.
			return nil, ""
		}
		return nil, fmt.Sprintf("plugin %s: read manifest: %v", filepath.Base(dir), err)
	}

	var p Plugin
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Sprintf("plugin %s: invalid %s: %v", filepath.Base(dir), ManifestName, err)
	}
	p.Dir = dir

	if p.Name == "" {
		return nil, fmt.Sprintf("plugin %s: manifest is missing a name", filepath.Base(dir))
	}
	// Workflows and routines are discovered by convention: any workflows/*.yaml or
	// routines/<name>/prompt.md makes the plugin cargo for them, no manifest
	// declaration needed.
	p.Workflows = discoverPluginWorkflows(dir)
	p.Routines = discoverPluginRoutines(dir)
	if len(p.Hooks) == 0 && len(p.Actions) == 0 && len(p.Workflows) == 0 && len(p.Routines) == 0 {
		return nil, fmt.Sprintf("plugin %q: manifest declares no hooks, actions, workflows, or routines; skipping", p.Name)
	}

	// Drop hooks and actions whose script is missing or not a regular file,
	// keeping the rest of the plugin usable.
	var dropped []string
	for event, rel := range p.Hooks {
		if !isExecutableFile(filepath.Join(dir, rel)) {
			delete(p.Hooks, event)
			dropped = append(dropped, "hook:"+event)
		}
	}
	kept := p.Actions[:0]
	for _, a := range p.Actions {
		switch {
		case a.ID == "" || a.Command == "":
			dropped = append(dropped, "action:<malformed>")
		case !isExecutableFile(filepath.Join(dir, a.Command)):
			dropped = append(dropped, "action:"+a.ID)
		default:
			kept = append(kept, a)
		}
	}
	p.Actions = kept

	if len(p.Hooks) == 0 && len(p.Actions) == 0 && len(p.Workflows) == 0 && len(p.Routines) == 0 {
		return nil, fmt.Sprintf("plugin %q: no usable hook, action, workflow, or routine found; skipping", p.Name)
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		return &p, fmt.Sprintf("plugin %q: ignored entries with missing/invalid scripts: %v", p.Name, dropped)
	}
	return &p, ""
}

// isExecutableFile reports whether path exists and is a regular file.
func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// discoverPluginWorkflows returns the filename stems of every workflow definition
// (workflows/*.yaml or *.yml) a plugin ships, sorted for deterministic display.
func discoverPluginWorkflows(dir string) []string {
	wdir := filepath.Join(dir, "workflows")
	entries, err := os.ReadDir(wdir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext == ".yaml" || ext == ".yml" {
			names = append(names, strings.TrimSuffix(e.Name(), ext))
		}
	}
	sort.Strings(names)
	return names
}

// PluginWorkflowDirs returns the workflows/ subdir of every installed plugin that
// actually ships one. The pipeline registry feeds these into its workflow search
// path, so an installed plugin's workflows become resolvable by `ty pipeline -d`.
func PluginWorkflowDirs(pluginsDir string) []string {
	plugins, _ := LoadPlugins(pluginsDir)
	var dirs []string
	for _, p := range plugins {
		if len(p.Workflows) > 0 {
			dirs = append(dirs, p.WorkflowsDir())
		}
	}
	return dirs
}

// discoverPluginRoutines returns the names of every routine a plugin ships in its
// routines/ subdir — a subdirectory is a routine when it contains a prompt.md (the
// same shape a routine has under ~/.config/task/routines/). Sorted for stable display.
func discoverPluginRoutines(dir string) []string {
	rdir := filepath.Join(dir, "routines")
	entries, err := os.ReadDir(rdir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(rdir, e.Name(), "prompt.md")); err != nil {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// PluginRoutineDirs returns the routines/ subdir of every installed plugin that
// ships one. The routine package folds these into its search path, so a plugin's
// routines become resolvable by `ty run <name>` and visible in `ty routines`.
func PluginRoutineDirs(pluginsDir string) []string {
	plugins, _ := LoadPlugins(pluginsDir)
	var dirs []string
	for _, p := range plugins {
		if len(p.Routines) > 0 {
			dirs = append(dirs, p.RoutinesDir())
		}
	}
	return dirs
}
