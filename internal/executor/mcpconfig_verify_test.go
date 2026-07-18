package executor

import (
	"encoding/json"
	"os"
	"testing"
)

// TestEnsureWorktreeMCPConfig_Shape verifies the file is written with a taskyou
// stdio server pointing at `ty mcp-server --task-id <id>` — the exact shape proven
// to connect via `claude --mcp-config`.
func TestEnsureWorktreeMCPConfig_Shape(t *testing.T) {
	path, err := ensureWorktreeMCPConfig(999999)
	if err != nil {
		t.Fatalf("ensureWorktreeMCPConfig: %v", err)
	}
	defer os.Remove(path)
	data, _ := os.ReadFile(path)
	var cfg struct {
		MCPServers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	s, ok := cfg.MCPServers["taskyou"]
	if !ok {
		t.Fatalf("no taskyou server in %s", data)
	}
	if s.Type != "stdio" {
		t.Errorf("type = %q, want stdio", s.Type)
	}
	want := []string{"mcp-server", "--task-id", "999999"}
	if len(s.Args) != 3 || s.Args[0] != want[0] || s.Args[1] != want[1] || s.Args[2] != want[2] {
		t.Errorf("args = %v, want %v", s.Args, want)
	}
	t.Logf("wrote %s -> command=%s args=%v", path, s.Command, s.Args)
}
