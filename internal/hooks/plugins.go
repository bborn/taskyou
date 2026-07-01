package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

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

// DefaultPluginsDir returns the default plugins directory path.
func DefaultPluginsDir() string {
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
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("read plugins dir %s: %v", pluginsDir, err)}
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(pluginsDir, entry.Name())
		p, warn := loadPlugin(dir)
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if p != nil {
			plugins = append(plugins, *p)
		}
	}

	// Deterministic order so fan-out and `plugins list` are stable.
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
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
	if len(p.Hooks) == 0 && len(p.Actions) == 0 {
		return nil, fmt.Sprintf("plugin %q: manifest declares no hooks or actions; skipping", p.Name)
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

	if len(p.Hooks) == 0 && len(p.Actions) == 0 {
		return nil, fmt.Sprintf("plugin %q: no usable hook or action scripts found; skipping", p.Name)
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
