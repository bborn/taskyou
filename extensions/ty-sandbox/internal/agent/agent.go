// Package agent defines the adapter interface for coding agents and provides
// implementations for Claude Code and other agents.
package agent

import (
	"context"
	"fmt"

	"github.com/bborn/workflow/extensions/ty-sandbox/internal/events"
)

// AgentID identifies a supported coding agent.
type AgentID string

const (
	AgentClaude   AgentID = "claude-code"
	AgentCodex    AgentID = "codex"
	AgentOpenCode AgentID = "opencode"
	AgentMock     AgentID = "mock"
)

// AgentInfo describes a supported agent.
type AgentInfo struct {
	ID          AgentID `json:"id"`
	Name        string  `json:"name"`
	Installed   bool    `json:"installed"`
	Available   bool    `json:"available"`
	Description string  `json:"description,omitempty"`
}

// SpawnConfig contains parameters for starting an agent session.
type SpawnConfig struct {
	Agent     AgentID  `json:"agent"`
	Model     string   `json:"model,omitempty"`
	Prompt    string   `json:"prompt,omitempty"`
	WorkDir   string   `json:"work_dir,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	MaxTurns  int      `json:"max_turns,omitempty"`
	Args      []string `json:"args,omitempty"`
}

// Agent defines the interface that all agent adapters must implement.
type Agent interface {
	// ID returns the agent identifier.
	ID() AgentID

	// Info returns metadata about this agent.
	Info() AgentInfo

	// IsInstalled checks if the agent CLI is available on the system.
	IsInstalled() bool

	// Install attempts to install the agent CLI.
	Install(ctx context.Context) error

	// Spawn starts a new agent session with the given config.
	// Events are sent to the provided channel.
	Spawn(ctx context.Context, sessionID string, cfg SpawnConfig, eventCh chan<- *events.UniversalEvent) error

	// SendMessage sends a user message to an active session.
	SendMessage(ctx context.Context, sessionID string, message string) error

	// Terminate stops an active session.
	Terminate(ctx context.Context, sessionID string) error
}

// Registry holds all registered agent adapters.
type Registry struct {
	agents map[AgentID]Agent
}

// NewRegistry creates a new agent registry with the default adapters.
func NewRegistry() *Registry {
	r := &Registry{
		agents: make(map[AgentID]Agent),
	}
	r.Register(NewClaudeAgent())
	r.Register(NewMockAgent())
	return r
}

// Register adds an agent adapter to the registry.
func (r *Registry) Register(a Agent) {
	r.agents[a.ID()] = a
}

// Get returns an agent adapter by ID.
func (r *Registry) Get(id AgentID) (Agent, error) {
	a, ok := r.agents[id]
	if !ok {
		return nil, fmt.Errorf("unknown agent: %s", id)
	}
	return a, nil
}

// List returns info for all registered agents.
func (r *Registry) List() []AgentInfo {
	var infos []AgentInfo
	for _, a := range r.agents {
		infos = append(infos, a.Info())
	}
	return infos
}
