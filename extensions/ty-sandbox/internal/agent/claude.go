package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/bborn/workflow/extensions/ty-sandbox/internal/events"
	"github.com/google/uuid"
)

// ClaudeAgent implements the Agent interface for Claude Code CLI.
type ClaudeAgent struct {
	mu       sync.Mutex
	sessions map[string]*claudeSession
}

type claudeSession struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	seq    atomic.Uint64
}

func NewClaudeAgent() *ClaudeAgent {
	return &ClaudeAgent{
		sessions: make(map[string]*claudeSession),
	}
}

func (a *ClaudeAgent) ID() AgentID { return AgentClaude }

func (a *ClaudeAgent) Info() AgentInfo {
	return AgentInfo{
		ID:          AgentClaude,
		Name:        "Claude Code",
		Installed:   a.IsInstalled(),
		Available:   a.IsInstalled(),
		Description: "Anthropic's Claude Code CLI agent",
	}
}

func (a *ClaudeAgent) IsInstalled() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

func (a *ClaudeAgent) Install(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "npm", "install", "-g", "@anthropic-ai/claude-code")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (a *ClaudeAgent) Spawn(ctx context.Context, sessionID string, cfg SpawnConfig, eventCh chan<- *events.UniversalEvent) error {
	ctx, cancel := context.WithCancel(ctx)

	args := []string{
		"--output-format", "stream-json",
		"--verbose",
	}

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", cfg.MaxTurns))
	}
	if cfg.SystemPrompt != "" {
		args = append(args, "--system-prompt", cfg.SystemPrompt)
	}
	args = append(args, cfg.Args...)

	if cfg.Prompt != "" {
		args = append(args, "--print", cfg.Prompt)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	sess := &claudeSession{cmd: cmd, cancel: cancel}

	a.mu.Lock()
	a.sessions[sessionID] = sess
	a.mu.Unlock()

	if err := cmd.Start(); err != nil {
		cancel()
		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
		return fmt.Errorf("start claude: %w", err)
	}

	// Emit session.started
	seq := sess.seq.Add(1) - 1
	evt, _ := events.NewEvent(sessionID, seq, events.EventSessionStarted, events.SourceDaemon, &events.SessionStartedData{
		Agent: string(AgentClaude),
		Model: cfg.Model,
	})
	eventCh <- evt

	// Parse streaming JSON output from Claude Code
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for large outputs
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			a.handleClaudeEvent(sessionID, sess, line, eventCh)
		}
	}()

	// Log stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("[claude:%s] stderr: %s", sessionID[:8], scanner.Text())
		}
	}()

	// Wait for process to exit
	go func() {
		err := cmd.Wait()
		reason := events.EndReasonCompleted
		if err != nil {
			if ctx.Err() != nil {
				reason = events.EndReasonTerminated
			} else {
				reason = events.EndReasonError
			}
		}

		seq := sess.seq.Add(1) - 1
		evt, _ := events.NewEvent(sessionID, seq, events.EventSessionEnded, events.SourceDaemon, &events.SessionEndedData{
			Reason: reason,
		})
		eventCh <- evt

		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
	}()

	return nil
}

func (a *ClaudeAgent) SendMessage(ctx context.Context, sessionID string, message string) error {
	a.mu.Lock()
	sess, ok := a.sessions[sessionID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	_ = sess
	// Claude Code in --print mode doesn't support interactive input.
	// For multi-turn, we'd need to use the MCP or session resume approach.
	return fmt.Errorf("interactive messages not yet supported for Claude Code; use a new session for each prompt")
}

func (a *ClaudeAgent) Terminate(ctx context.Context, sessionID string) error {
	a.mu.Lock()
	sess, ok := a.sessions[sessionID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	sess.cancel()
	return nil
}

// handleClaudeEvent parses a single line of Claude Code's stream-json output
// and converts it to universal events.
func (a *ClaudeAgent) handleClaudeEvent(sessionID string, sess *claudeSession, line []byte, eventCh chan<- *events.UniversalEvent) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		// Not JSON - emit as unparsed
		seq := sess.seq.Add(1) - 1
		evt, _ := events.NewEvent(sessionID, seq, events.EventAgentUnparsed, events.SourceDaemon, &events.AgentUnparsedData{
			Error:    "invalid json",
			Location: "stdout",
		})
		eventCh <- evt
		return
	}

	eventType, _ := raw["type"].(string)

	switch eventType {
	case "assistant":
		a.handleAssistantMessage(sessionID, sess, raw, eventCh)
	case "tool_use":
		a.handleToolUse(sessionID, sess, raw, eventCh)
	case "tool_result":
		a.handleToolResult(sessionID, sess, raw, eventCh)
	case "result":
		a.handleResult(sessionID, sess, raw, eventCh)
	case "system":
		// System messages from Claude Code - emit as text item
		msg, _ := raw["message"].(string)
		if msg != "" {
			seq := sess.seq.Add(1) - 1
			itemID := uuid.New().String()
			evt, _ := events.NewEvent(sessionID, seq, events.EventItemStarted, events.SourceAgent, &events.ItemEventData{
				Item: events.UniversalItem{
					ItemID: itemID,
					Kind:   events.ItemKindMessage,
					Role:   events.RoleSystem,
					Status: events.ItemStatusCompleted,
					Content: []events.ContentPart{
						{Type: "text", Text: msg},
					},
				},
			})
			eventCh <- evt

			seq = sess.seq.Add(1) - 1
			evt, _ = events.NewEvent(sessionID, seq, events.EventItemCompleted, events.SourceAgent, &events.ItemEventData{
				Item: events.UniversalItem{
					ItemID: itemID,
					Kind:   events.ItemKindMessage,
					Role:   events.RoleSystem,
					Status: events.ItemStatusCompleted,
					Content: []events.ContentPart{
						{Type: "text", Text: msg},
					},
				},
			})
			eventCh <- evt
		}
	default:
		// Unknown event type - emit raw
		seq := sess.seq.Add(1) - 1
		evt, _ := events.NewEvent(sessionID, seq, events.EventAgentUnparsed, events.SourceDaemon, &events.AgentUnparsedData{
			Error:    fmt.Sprintf("unknown event type: %s", eventType),
			Location: "stdout",
		})
		evt.Raw = line
		eventCh <- evt
	}
}

func (a *ClaudeAgent) handleAssistantMessage(sessionID string, sess *claudeSession, raw map[string]any, eventCh chan<- *events.UniversalEvent) {
	itemID := uuid.New().String()
	message, _ := raw["message"].(string)
	if message == "" {
		// Try nested content
		if content, ok := raw["content"].([]any); ok {
			for _, c := range content {
				if cm, ok := c.(map[string]any); ok {
					if t, ok := cm["text"].(string); ok {
						message += t
					}
				}
			}
		}
	}

	seq := sess.seq.Add(1) - 1
	evt, _ := events.NewEvent(sessionID, seq, events.EventItemStarted, events.SourceAgent, &events.ItemEventData{
		Item: events.UniversalItem{
			ItemID: itemID,
			Kind:   events.ItemKindMessage,
			Role:   events.RoleAssistant,
			Status: events.ItemStatusInProgress,
		},
	})
	eventCh <- evt

	if message != "" {
		seq = sess.seq.Add(1) - 1
		evt, _ = events.NewEvent(sessionID, seq, events.EventItemDelta, events.SourceAgent, &events.ItemDeltaData{
			ItemID: itemID,
			Delta:  events.ContentPart{Type: "text", Text: message},
		})
		eventCh <- evt
	}

	seq = sess.seq.Add(1) - 1
	evt, _ = events.NewEvent(sessionID, seq, events.EventItemCompleted, events.SourceAgent, &events.ItemEventData{
		Item: events.UniversalItem{
			ItemID: itemID,
			Kind:   events.ItemKindMessage,
			Role:   events.RoleAssistant,
			Status: events.ItemStatusCompleted,
			Content: []events.ContentPart{
				{Type: "text", Text: message},
			},
		},
	})
	eventCh <- evt
}

func (a *ClaudeAgent) handleToolUse(sessionID string, sess *claudeSession, raw map[string]any, eventCh chan<- *events.UniversalEvent) {
	toolName, _ := raw["name"].(string)
	callID, _ := raw["id"].(string)
	if callID == "" {
		callID = uuid.New().String()
	}
	argsRaw, _ := raw["input"].(map[string]any)
	argsBytes, _ := json.Marshal(argsRaw)

	seq := sess.seq.Add(1) - 1
	evt, _ := events.NewEvent(sessionID, seq, events.EventItemStarted, events.SourceAgent, &events.ItemEventData{
		Item: events.UniversalItem{
			ItemID: callID,
			Kind:   events.ItemKindToolCall,
			Role:   events.RoleAssistant,
			Status: events.ItemStatusInProgress,
			Content: []events.ContentPart{
				{Type: "tool_call", CallID: callID, Name: toolName, Arguments: string(argsBytes)},
			},
		},
	})
	eventCh <- evt
}

func (a *ClaudeAgent) handleToolResult(sessionID string, sess *claudeSession, raw map[string]any, eventCh chan<- *events.UniversalEvent) {
	callID, _ := raw["tool_use_id"].(string)
	if callID == "" {
		callID = uuid.New().String()
	}
	output, _ := raw["content"].(string)
	if output == "" {
		if contentArr, ok := raw["content"].([]any); ok {
			for _, c := range contentArr {
				if cm, ok := c.(map[string]any); ok {
					if t, ok := cm["text"].(string); ok {
						output += t
					}
				}
			}
		}
	}

	seq := sess.seq.Add(1) - 1
	evt, _ := events.NewEvent(sessionID, seq, events.EventItemCompleted, events.SourceAgent, &events.ItemEventData{
		Item: events.UniversalItem{
			ItemID: callID,
			Kind:   events.ItemKindToolCall,
			Role:   events.RoleAssistant,
			Status: events.ItemStatusCompleted,
			Content: []events.ContentPart{
				{Type: "tool_result", CallID: callID, Output: output},
			},
		},
	})
	eventCh <- evt
}

func (a *ClaudeAgent) handleResult(sessionID string, sess *claudeSession, raw map[string]any, eventCh chan<- *events.UniversalEvent) {
	// Final result from Claude Code - contains cost info, session ID, etc.
	itemID := uuid.New().String()
	result, _ := raw["result"].(string)
	if result == "" {
		result, _ = raw["message"].(string)
	}

	if result != "" {
		seq := sess.seq.Add(1) - 1
		evt, _ := events.NewEvent(sessionID, seq, events.EventItemCompleted, events.SourceAgent, &events.ItemEventData{
			Item: events.UniversalItem{
				ItemID: itemID,
				Kind:   events.ItemKindMessage,
				Role:   events.RoleAssistant,
				Status: events.ItemStatusCompleted,
				Content: []events.ContentPart{
					{Type: "text", Text: result},
				},
			},
		})
		eventCh <- evt
	}
}
