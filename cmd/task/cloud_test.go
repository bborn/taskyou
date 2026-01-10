package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestGetCloudSettings(t *testing.T) {
	// Create temp directory for test database
	tmpDir, err := os.MkdirTemp("", "task-cloud-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Test with no settings - should return defaults
	settings, err := getCloudSettings(database)
	if err != nil {
		t.Fatalf("getCloudSettings failed: %v", err)
	}

	// Check defaults
	if settings[SettingCloudSSHPort] != defaultCloudSSHPort {
		t.Errorf("Expected SSH port %s, got %s", defaultCloudSSHPort, settings[SettingCloudSSHPort])
	}
	if settings[SettingCloudTaskPort] != defaultCloudTaskPort {
		t.Errorf("Expected task port %s, got %s", defaultCloudTaskPort, settings[SettingCloudTaskPort])
	}
	if settings[SettingCloudRemoteUser] != defaultCloudRemoteUser {
		t.Errorf("Expected remote user %s, got %s", defaultCloudRemoteUser, settings[SettingCloudRemoteUser])
	}
	if settings[SettingCloudRemoteDir] != defaultCloudRemoteDir {
		t.Errorf("Expected remote dir %s, got %s", defaultCloudRemoteDir, settings[SettingCloudRemoteDir])
	}
	if settings[SettingCloudServer] != "" {
		t.Errorf("Expected empty server, got %s", settings[SettingCloudServer])
	}

	// Set custom values
	database.SetSetting(SettingCloudServer, "root@test.com")
	database.SetSetting(SettingCloudSSHPort, "2200")
	database.SetSetting(SettingCloudTaskPort, "3000")
	database.SetSetting(SettingCloudRemoteUser, "admin")
	database.SetSetting(SettingCloudRemoteDir, "/opt/taskd")

	// Re-read settings
	settings, err = getCloudSettings(database)
	if err != nil {
		t.Fatalf("getCloudSettings failed: %v", err)
	}

	// Verify custom values
	if settings[SettingCloudServer] != "root@test.com" {
		t.Errorf("Expected server root@test.com, got %s", settings[SettingCloudServer])
	}
	if settings[SettingCloudSSHPort] != "2200" {
		t.Errorf("Expected SSH port 2200, got %s", settings[SettingCloudSSHPort])
	}
	if settings[SettingCloudTaskPort] != "3000" {
		t.Errorf("Expected task port 3000, got %s", settings[SettingCloudTaskPort])
	}
	if settings[SettingCloudRemoteUser] != "admin" {
		t.Errorf("Expected remote user admin, got %s", settings[SettingCloudRemoteUser])
	}
	if settings[SettingCloudRemoteDir] != "/opt/taskd" {
		t.Errorf("Expected remote dir /opt/taskd, got %s", settings[SettingCloudRemoteDir])
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"root@my-server.com", "my-server.com"},
		{"user@192.168.1.1", "192.168.1.1"},
		{"my-server.com", "my-server.com"},
		{"admin@cloud-claude", "cloud-claude"},
		{"", ""},
	}

	for _, tt := range tests {
		result := extractHost(tt.input)
		if result != tt.expected {
			t.Errorf("extractHost(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestGetProjectRoot(t *testing.T) {
	// getProjectRoot should find the directory containing go.mod
	root := getProjectRoot()

	// Verify go.mod exists in the returned path
	goModPath := filepath.Join(root, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		t.Errorf("getProjectRoot returned %s, but go.mod not found there", root)
	}
}

func TestCreateCloudCommand(t *testing.T) {
	cmd := createCloudCommand()

	// Verify command structure
	if cmd.Use != "cloud" {
		t.Errorf("Expected Use 'cloud', got %s", cmd.Use)
	}

	// Check subcommands exist
	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Use] = true
	}

	expected := []string{"init", "status", "logs", "sync"}
	for _, name := range expected {
		if !subcommands[name] {
			t.Errorf("Expected subcommand %s not found", name)
		}
	}
}
