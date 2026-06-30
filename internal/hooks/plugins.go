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
	// resolved relative to the plugin directory.
	Hooks map[string]string `yaml:"hooks"`

	// Dir is the absolute path to the plugin directory (not from the manifest).
	Dir string `yaml:"-"`
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
	if len(p.Hooks) == 0 {
		return nil, fmt.Sprintf("plugin %q: manifest declares no hooks; skipping", p.Name)
	}

	// Drop hooks whose script is missing or not a regular file, keeping the
	// rest of the plugin usable.
	var dropped []string
	for event, rel := range p.Hooks {
		script := filepath.Join(dir, rel)
		fi, statErr := os.Stat(script)
		if statErr != nil || fi.IsDir() {
			delete(p.Hooks, event)
			dropped = append(dropped, event)
		}
	}
	if len(p.Hooks) == 0 {
		return nil, fmt.Sprintf("plugin %q: no usable hook scripts found; skipping", p.Name)
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		return &p, fmt.Sprintf("plugin %q: ignored hooks with missing scripts: %v", p.Name, dropped)
	}
	return &p, ""
}
