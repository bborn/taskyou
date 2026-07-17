package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// A workflow shipped by an installed plugin (contributed via the PluginWorkflowDirs
// seam) is resolvable everywhere definitions are listed — DefinitionNames(), Get(),
// KindResolver — without being passed as an explicit extraDir.
func TestRegistryIncludesPluginWorkflows(t *testing.T) {
	// Point the global workflows dir somewhere empty so only the plugin dir supplies
	// this definition.
	t.Setenv("TY_WORKFLOWS_DIR", t.TempDir())

	pluginWF := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginWF, "rpi-go.yaml"),
		[]byte("name: rpi-go\nsteps:\n  - name: build\n    prompt: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := PluginWorkflowDirs
	PluginWorkflowDirs = func() []string { return []string{pluginWF} }
	t.Cleanup(func() { PluginWorkflowDirs = prev })

	found := false
	for _, n := range DefinitionNames() {
		if n == "rpi-go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DefinitionNames() = %v, want it to include plugin workflow \"rpi-go\"", DefinitionNames())
	}

	if _, ok := Get("rpi-go"); !ok {
		t.Error("Get(\"rpi-go\") did not resolve the plugin workflow")
	}
}
