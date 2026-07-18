package routine

import (
	"path/filepath"
	"testing"
)

// A routine shipped by a plugin (contributed via PluginRoutineDirs) is
// resolvable by Load/Exists and shows up in List, just like a user routine.
func TestPluginRoutine_Resolves(t *testing.T) {
	userDir := setupRoutinesDir(t)
	pluginDir := t.TempDir()
	writeRoutine(t, pluginDir, "linear-poll", "poll linear for new issues")

	prev := PluginRoutineDirs
	PluginRoutineDirs = func() []string { return []string{pluginDir} }
	t.Cleanup(func() { PluginRoutineDirs = prev })

	if !Exists("linear-poll") {
		t.Fatal("Exists: plugin routine not found")
	}
	r, err := Load("linear-poll")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Dir != filepath.Join(pluginDir, "linear-poll") {
		t.Errorf("Dir = %q, want the plugin dir", r.Dir)
	}

	// A user routine coexists and both appear in List.
	writeRoutine(t, userDir, "scout", "scout the feedback")
	routines, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]bool{}
	for _, rt := range routines {
		got[rt.Name] = true
	}
	if !got["linear-poll"] || !got["scout"] {
		t.Fatalf("List missing routines; got %v", got)
	}
}

// A user routine shadows a plugin routine of the same name (user dir wins).
func TestPluginRoutine_UserShadows(t *testing.T) {
	userDir := setupRoutinesDir(t)
	pluginDir := t.TempDir()
	writeRoutine(t, pluginDir, "dup", "PLUGIN version")
	writeRoutine(t, userDir, "dup", "USER version")

	prev := PluginRoutineDirs
	PluginRoutineDirs = func() []string { return []string{pluginDir} }
	t.Cleanup(func() { PluginRoutineDirs = prev })

	r, err := Load("dup")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Prompt != "USER version" {
		t.Errorf("Prompt = %q, want the user routine to shadow the plugin one", r.Prompt)
	}
	if r.Dir != filepath.Join(userDir, "dup") {
		t.Errorf("Dir = %q, want the user dir", r.Dir)
	}

	// List must return "dup" exactly once (the user's), not both.
	routines, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	n := 0
	for _, rt := range routines {
		if rt.Name == "dup" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("List returned %d 'dup' routines, want 1 (deduped, user wins)", n)
	}
}
