package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectConfig(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "project-config-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("no config file returns nil", func(t *testing.T) {
		config, err := LoadProjectConfig(tmpDir)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if config != nil {
			t.Error("expected nil config when no file exists")
		}
	})

	t.Run("loads .taskyou.yml", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, ".taskyou.yml")
		content := `worktree:
  init_script: bin/worktree-setup
`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(configPath)

		config, err := LoadProjectConfig(tmpDir)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if config == nil {
			t.Fatal("expected config to be loaded")
		}
		if config.Worktree.InitScript != "bin/worktree-setup" {
			t.Errorf("expected init_script 'bin/worktree-setup', got %q", config.Worktree.InitScript)
		}
	})

	t.Run("loads .taskyou.yaml", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, ".taskyou.yaml")
		content := `worktree:
  init_script: scripts/init.sh
`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(configPath)

		config, err := LoadProjectConfig(tmpDir)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if config == nil {
			t.Fatal("expected config to be loaded")
		}
		if config.Worktree.InitScript != "scripts/init.sh" {
			t.Errorf("expected init_script 'scripts/init.sh', got %q", config.Worktree.InitScript)
		}
	})

	t.Run("prefers .taskyou.yml over .taskyou.yaml", func(t *testing.T) {
		ymlPath := filepath.Join(tmpDir, ".taskyou.yml")
		yamlPath := filepath.Join(tmpDir, ".taskyou.yaml")

		if err := os.WriteFile(ymlPath, []byte("worktree:\n  init_script: first\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(ymlPath)

		if err := os.WriteFile(yamlPath, []byte("worktree:\n  init_script: second\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(yamlPath)

		config, err := LoadProjectConfig(tmpDir)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if config == nil {
			t.Fatal("expected config to be loaded")
		}
		if config.Worktree.InitScript != "first" {
			t.Errorf("expected .taskyou.yml to be preferred, got init_script %q", config.Worktree.InitScript)
		}
	})

	t.Run("handles invalid YAML", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, ".taskyou.yml")
		content := `invalid: yaml: content: [[[`
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(configPath)

		_, err := LoadProjectConfig(tmpDir)
		if err == nil {
			t.Error("expected error for invalid YAML")
		}
	})

	t.Run("handles empty config", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, ".taskyou.yml")
		content := ``
		if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(configPath)

		config, err := LoadProjectConfig(tmpDir)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if config == nil {
			t.Fatal("expected config to be loaded (even if empty)")
		}
		if config.Worktree.InitScript != "" {
			t.Errorf("expected empty init_script, got %q", config.Worktree.InitScript)
		}
	})
}

func TestGetWorktreeInitScript(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "init-script-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	t.Run("returns empty when no config or script", func(t *testing.T) {
		script := GetWorktreeInitScript(tmpDir)
		if script != "" {
			t.Errorf("expected empty string, got %q", script)
		}
	})

	t.Run("returns configured script path", func(t *testing.T) {
		// Create config file
		configPath := filepath.Join(tmpDir, ".taskyou.yml")
		if err := os.WriteFile(configPath, []byte("worktree:\n  init_script: bin/setup.sh\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(configPath)

		// Create the script file
		binDir := filepath.Join(tmpDir, "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		scriptPath := filepath.Join(binDir, "setup.sh")
		if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho hello"), 0755); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(binDir)

		script := GetWorktreeInitScript(tmpDir)
		expectedPath := filepath.Join(tmpDir, "bin/setup.sh")
		if script != expectedPath {
			t.Errorf("expected %q, got %q", expectedPath, script)
		}
	})

	t.Run("ignores config if script file doesn't exist", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, ".taskyou.yml")
		if err := os.WriteFile(configPath, []byte("worktree:\n  init_script: nonexistent/script.sh\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(configPath)

		script := GetWorktreeInitScript(tmpDir)
		if script != "" {
			t.Errorf("expected empty string for non-existent script, got %q", script)
		}
	})

	t.Run("returns conventional script when executable", func(t *testing.T) {
		// Create bin/worktree-setup
		binDir := filepath.Join(tmpDir, "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		scriptPath := filepath.Join(binDir, "worktree-setup")
		if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho setup"), 0755); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(binDir)

		script := GetWorktreeInitScript(tmpDir)
		if script != scriptPath {
			t.Errorf("expected %q, got %q", scriptPath, script)
		}
	})

	t.Run("ignores conventional script when not executable", func(t *testing.T) {
		// Create bin/worktree-setup without executable permission
		binDir := filepath.Join(tmpDir, "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		scriptPath := filepath.Join(binDir, "worktree-setup")
		if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho setup"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(binDir)

		script := GetWorktreeInitScript(tmpDir)
		if script != "" {
			t.Errorf("expected empty string for non-executable script, got %q", script)
		}
	})

	t.Run("config takes precedence over conventional script", func(t *testing.T) {
		// Create config pointing to custom script
		configPath := filepath.Join(tmpDir, ".taskyou.yml")
		if err := os.WriteFile(configPath, []byte("worktree:\n  init_script: scripts/custom.sh\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(configPath)

		// Create custom script
		scriptsDir := filepath.Join(tmpDir, "scripts")
		if err := os.MkdirAll(scriptsDir, 0755); err != nil {
			t.Fatal(err)
		}
		customScript := filepath.Join(scriptsDir, "custom.sh")
		if err := os.WriteFile(customScript, []byte("#!/bin/bash\necho custom"), 0755); err != nil {
			t.Fatal(err)
		}

		// Create conventional script too
		binDir := filepath.Join(tmpDir, "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			t.Fatal(err)
		}
		conventionalScript := filepath.Join(binDir, "worktree-setup")
		if err := os.WriteFile(conventionalScript, []byte("#!/bin/bash\necho conventional"), 0755); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(binDir)
		defer os.RemoveAll(scriptsDir)

		script := GetWorktreeInitScript(tmpDir)
		if script != customScript {
			t.Errorf("expected config script %q, got %q", customScript, script)
		}
	})

	t.Run("handles absolute paths in config", func(t *testing.T) {
		// Create config with absolute path
		absPath := filepath.Join(tmpDir, "absolute-script.sh")
		configPath := filepath.Join(tmpDir, ".taskyou.yml")
		if err := os.WriteFile(configPath, []byte("worktree:\n  init_script: "+absPath+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(configPath)

		// Create the script file
		if err := os.WriteFile(absPath, []byte("#!/bin/bash\necho absolute"), 0755); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(absPath)

		script := GetWorktreeInitScript(tmpDir)
		if script != absPath {
			t.Errorf("expected %q, got %q", absPath, script)
		}
	})
}
