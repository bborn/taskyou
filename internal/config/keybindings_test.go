package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKeybindingsFromPath_NonExistent(t *testing.T) {
	cfg, err := LoadKeybindingsFromPath("/nonexistent/path/keybindings.yaml")
	if err != nil {
		t.Errorf("Expected no error for nonexistent file, got: %v", err)
	}
	if cfg != nil {
		t.Errorf("Expected nil config for nonexistent file, got: %v", cfg)
	}
}

func TestLoadKeybindingsFromPath_EmptyPath(t *testing.T) {
	cfg, err := LoadKeybindingsFromPath("")
	if err != nil {
		t.Errorf("Expected no error for empty path, got: %v", err)
	}
	if cfg != nil {
		t.Errorf("Expected nil config for empty path, got: %v", cfg)
	}
}

func TestLoadKeybindingsFromPath_ValidYAML(t *testing.T) {
	// Create a temporary file with valid YAML
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "keybindings.yaml")

	yaml := `
new:
  keys: ["ctrl+n", "N"]
  help: "create new"
quit:
  keys: ["q"]
  help: "exit"
`
	if err := os.WriteFile(configPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	cfg, err := LoadKeybindingsFromPath(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg == nil {
		t.Fatal("Expected non-nil config")
	}

	// Verify the new binding
	if cfg.New == nil {
		t.Fatal("Expected New binding to be set")
	}
	if len(cfg.New.Keys) != 2 {
		t.Errorf("Expected 2 keys for New, got %d", len(cfg.New.Keys))
	}
	if cfg.New.Keys[0] != "ctrl+n" {
		t.Errorf("Expected first key to be 'ctrl+n', got '%s'", cfg.New.Keys[0])
	}
	if cfg.New.Help != "create new" {
		t.Errorf("Expected help text 'create new', got '%s'", cfg.New.Help)
	}

	// Verify the quit binding
	if cfg.Quit == nil {
		t.Fatal("Expected Quit binding to be set")
	}
	if len(cfg.Quit.Keys) != 1 {
		t.Errorf("Expected 1 key for Quit, got %d", len(cfg.Quit.Keys))
	}
	if cfg.Quit.Keys[0] != "q" {
		t.Errorf("Expected key 'q', got '%s'", cfg.Quit.Keys[0])
	}

	// Verify unset bindings remain nil
	if cfg.Left != nil {
		t.Error("Expected Left binding to be nil")
	}
}

func TestLoadKeybindingsFromPath_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "keybindings.yaml")

	invalidYAML := `
new:
  keys: [missing closing bracket
`
	if err := os.WriteFile(configPath, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	_, err := LoadKeybindingsFromPath(configPath)
	if err == nil {
		t.Error("Expected error for invalid YAML")
	}
}

func TestLoadKeybindingsFromPath_PartialConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "keybindings.yaml")

	// Only customize a single binding
	yaml := `
filter:
  keys: ["f", "ctrl+f"]
  help: "search"
`
	if err := os.WriteFile(configPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	cfg, err := LoadKeybindingsFromPath(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Filter == nil {
		t.Fatal("Expected Filter binding to be set")
	}
	if cfg.Filter.Keys[0] != "f" {
		t.Errorf("Expected first key 'f', got '%s'", cfg.Filter.Keys[0])
	}

	// All other bindings should be nil
	if cfg.New != nil || cfg.Quit != nil || cfg.Left != nil {
		t.Error("Expected unspecified bindings to be nil")
	}
}

func TestDefaultKeybindingsConfigPath(t *testing.T) {
	path := DefaultKeybindingsConfigPath()
	if path == "" {
		t.Error("Expected non-empty default path")
	}

	// Should contain .config/taskyou/keybindings.yaml
	if !filepath.IsAbs(path) {
		t.Errorf("Expected absolute path, got: %s", path)
	}
	if filepath.Base(path) != "keybindings.yaml" {
		t.Errorf("Expected keybindings.yaml, got: %s", filepath.Base(path))
	}
}

func TestGenerateDefaultKeybindingsYAML(t *testing.T) {
	yaml := GenerateDefaultKeybindingsYAML()

	// Verify it contains expected content
	if yaml == "" {
		t.Error("Expected non-empty YAML")
	}

	// Should contain some key bindings
	expectedSubstrings := []string{
		"new:",
		"quit:",
		"filter:",
		"command_palette:",
		"keys:",
		"help:",
	}

	for _, s := range expectedSubstrings {
		if !contains(yaml, s) {
			t.Errorf("Expected YAML to contain '%s'", s)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
