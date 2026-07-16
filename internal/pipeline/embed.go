package pipeline

import (
	"embed"
	"path/filepath"
	"strings"
)

// bundledWorkflows holds the workflow definitions compiled into the binary. Unlike
// the UI embed (internal/web/ui/embed.go), this is unconditional — there is no build
// tag, because no external toolchain is required to produce these files; they are
// authored YAML that ships with the source.
//
//go:embed workflows/*.yaml
var bundledWorkflows embed.FS

// loadBundledDefinitions parses each embedded workflow file into a map keyed by
// definition name. These are the built-in workflows (e.g. "rpi"): they seed the
// registry at lowest precedence, so a same-named on-disk file still shadows them.
// Bundled defs are marked Custom:false so `ty pipeline --list` labels them
// "built-in" rather than "custom".
func loadBundledDefinitions() map[string]Definition {
	out := make(map[string]Definition)
	entries, err := bundledWorkflows.ReadDir("workflows")
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		data, err := bundledWorkflows.ReadFile("workflows/" + e.Name())
		if err != nil {
			continue
		}
		def, err := ParseDefinition(data)
		if err != nil {
			continue
		}
		// ParseDefinition hard-sets Custom:true (it assumes an on-disk file); these
		// are built-ins, so undo that to get the correct "built-in" list label.
		def.Custom = false
		out[def.Name] = def
	}
	return out
}
