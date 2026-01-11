// Package sprites provides a wrapper around the Fly.io Sprites SDK.
package sprites

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bborn/workflow/internal/db"
	sdk "github.com/superfly/sprites-go"
)

const (
	// SettingSpriteToken is the database key for the Sprites API token.
	SettingSpriteToken = "sprite_token"
)

// HookEvent represents a Claude hook event from the sprite.
type HookEvent struct {
	TaskID    int64  `json:"task_id"`
	Event     string `json:"event"`      // PreToolUse, PostToolUse, Notification, Stop
	ToolName  string `json:"tool,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Matcher   string `json:"matcher,omitempty"`
}

// Client wraps the Sprites SDK client.
type Client struct {
	sdk *sdk.Client
}

// NewClient creates a new Sprites client.
// It looks for the token in SPRITES_TOKEN env var first, then falls back to database.
func NewClient(database *db.DB) (*Client, error) {
	token := os.Getenv("SPRITES_TOKEN")
	if token == "" && database != nil {
		token, _ = database.GetSetting(SettingSpriteToken)
	}
	if token == "" {
		return nil, fmt.Errorf("no Sprites token configured. Set SPRITES_TOKEN env var or run: task config set sprite_token <token>")
	}
	return &Client{sdk: sdk.New(token)}, nil
}

// NewClientWithToken creates a new Sprites client with the given token.
func NewClientWithToken(token string) *Client {
	return &Client{sdk: sdk.New(token)}
}

// SDK returns the underlying SDK client for advanced operations.
func (c *Client) SDK() *sdk.Client {
	return c.sdk
}

// Sprite returns a sprite reference by name.
func (c *Client) Sprite(name string) *sdk.Sprite {
	return c.sdk.Sprite(name)
}

// EnsureActive ensures the sprite is active (not checkpointed).
// If checkpointed, it restores from the latest checkpoint.
func (c *Client) EnsureActive(ctx context.Context, name string) error {
	sprite, err := c.sdk.GetSprite(ctx, name)
	if err != nil {
		return fmt.Errorf("get sprite: %w", err)
	}

	// Check if sprite is checkpointed (status might indicate this)
	if sprite.Status == "suspended" || sprite.Status == "stopped" {
		// Get latest checkpoint
		checkpoints, err := sprite.ListCheckpoints(ctx, "")
		if err != nil {
			return fmt.Errorf("list checkpoints: %w", err)
		}
		if len(checkpoints) == 0 {
			return fmt.Errorf("sprite is suspended but has no checkpoints")
		}

		// Restore from latest checkpoint
		restoreStream, err := sprite.RestoreCheckpoint(ctx, checkpoints[0].ID)
		if err != nil {
			return fmt.Errorf("restore checkpoint: %w", err)
		}

		// Wait for restore to complete
		if err := restoreStream.ProcessAll(func(msg *sdk.StreamMessage) error {
			return nil
		}); err != nil {
			return fmt.Errorf("restore failed: %w", err)
		}
	}

	return nil
}

// Exec runs a command on the sprite and returns the output.
func (c *Client) Exec(ctx context.Context, spriteName string, command string) ([]byte, error) {
	sprite := c.sdk.Sprite(spriteName)
	cmd := sprite.CommandContext(ctx, "sh", "-c", command)
	return cmd.CombinedOutput()
}

// ExecInteractive runs an interactive command on the sprite.
func (c *Client) ExecInteractive(ctx context.Context, spriteName string, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	sprite := c.sdk.Sprite(spriteName)
	cmd := sprite.CommandContext(ctx, "sh", "-c", command)
	cmd.SetTTY(true)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// StreamHooks starts streaming hook events from the sprite.
// The returned channel will receive hook events as they occur.
// The returned cancel function should be called to stop streaming.
func (c *Client) StreamHooks(ctx context.Context, spriteName string) (<-chan HookEvent, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(ctx)
	events := make(chan HookEvent, 100)

	sprite := c.sdk.Sprite(spriteName)
	cmd := sprite.CommandContext(ctx, "tail", "-n0", "-f", "/tmp/task-hooks.log")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("start tail: %w", err)
	}

	go func() {
		defer close(events)
		defer cmd.Wait()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			var event HookEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue // Skip malformed lines
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return events, cancel, nil
}

// SendInput sends input to a task's tmux window on the sprite.
func (c *Client) SendInput(ctx context.Context, spriteName string, taskID int64, input string) error {
	windowTarget := fmt.Sprintf("task-daemon:task-%d", taskID)
	// Escape single quotes in input
	escapedInput := fmt.Sprintf("%q", input)
	cmd := fmt.Sprintf("tmux send-keys -t %s %s Enter", windowTarget, escapedInput)
	_, err := c.Exec(ctx, spriteName, cmd)
	return err
}

// SetupWorktree creates a worktree on the sprite for the task.
func (c *Client) SetupWorktree(ctx context.Context, spriteName, taskSlug, branchName string) (string, error) {
	worktreePath := fmt.Sprintf("/workspace/.task-worktrees/%s", taskSlug)

	// Check if worktree already exists and remove it
	c.Exec(ctx, spriteName, fmt.Sprintf("cd /workspace && git worktree remove %s --force 2>/dev/null || true", worktreePath))

	// Fetch latest
	if _, err := c.Exec(ctx, spriteName, "cd /workspace && git fetch origin"); err != nil {
		return "", fmt.Errorf("git fetch: %w", err)
	}

	// Create worktree
	cmd := fmt.Sprintf("cd /workspace && git worktree add %s -b %s origin/HEAD", worktreePath, branchName)
	if _, err := c.Exec(ctx, spriteName, cmd); err != nil {
		return "", fmt.Errorf("create worktree: %w", err)
	}

	return worktreePath, nil
}

// SetupHooks writes the Claude hooks configuration to the sprite.
func (c *Client) SetupHooks(ctx context.Context, spriteName, workDir string, taskID int64) error {
	// Create the hooks config that writes to /tmp/task-hooks.log
	hooksConfig := fmt.Sprintf(`{
  "hooks": {
    "PreToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "echo '{\"task_id\":%d,\"event\":\"PreToolUse\",\"tool\":\"'\"$TOOL_NAME\"'\"}' >> /tmp/task-hooks.log"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "echo '{\"task_id\":%d,\"event\":\"PostToolUse\"}' >> /tmp/task-hooks.log"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "idle_prompt|permission_prompt",
        "hooks": [
          {
            "type": "command",
            "command": "echo '{\"task_id\":%d,\"event\":\"Notification\",\"matcher\":\"'\"$NOTIFICATION_TYPE\"'\"}' >> /tmp/task-hooks.log"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "echo '{\"task_id\":%d,\"event\":\"Stop\",\"reason\":\"'\"$STOP_REASON\"'\"}' >> /tmp/task-hooks.log"
          }
        ]
      }
    ]
  }
}`, taskID, taskID, taskID, taskID)

	// Write hooks config
	claudeDir := fmt.Sprintf("%s/.claude", workDir)
	_, err := c.Exec(ctx, spriteName, fmt.Sprintf("mkdir -p %s", claudeDir))
	if err != nil {
		return fmt.Errorf("create .claude dir: %w", err)
	}

	settingsPath := fmt.Sprintf("%s/settings.local.json", claudeDir)
	// Use printf to write the config
	cmd := fmt.Sprintf("cat > %s << 'EOFHOOKS'\n%s\nEOFHOOKS", settingsPath, hooksConfig)
	_, err = c.Exec(ctx, spriteName, cmd)
	if err != nil {
		return fmt.Errorf("write hooks config: %w", err)
	}

	return nil
}

// RunClaude runs Claude in a tmux window on the sprite.
func (c *Client) RunClaude(ctx context.Context, spriteName string, taskID int64, workDir, prompt string) error {
	windowName := fmt.Sprintf("task-%d", taskID)
	sessionName := "task-daemon"

	// Ensure tmux session exists
	c.Exec(ctx, spriteName, fmt.Sprintf("tmux new-session -d -s %s 2>/dev/null || true", sessionName))

	// Kill any existing window
	c.Exec(ctx, spriteName, fmt.Sprintf("tmux kill-window -t %s:%s 2>/dev/null || true", sessionName, windowName))

	// Write prompt to temp file
	promptFile := fmt.Sprintf("/tmp/task-prompt-%d.txt", taskID)
	promptCmd := fmt.Sprintf("cat > %s << 'EOFPROMPT'\n%s\nEOFPROMPT", promptFile, prompt)
	if _, err := c.Exec(ctx, spriteName, promptCmd); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}

	// Build Claude command - always use dangerous mode on sprites (they're isolated)
	claudeCmd := fmt.Sprintf("TASK_ID=%d claude --dangerously-skip-permissions --chrome \"$(cat %s)\"", taskID, promptFile)

	// Create tmux window and run Claude
	tmuxCmd := fmt.Sprintf("tmux new-window -d -t %s -n %s -c %s sh -c %q",
		sessionName, windowName, workDir, claudeCmd)

	if _, err := c.Exec(ctx, spriteName, tmuxCmd); err != nil {
		return fmt.Errorf("create tmux window: %w", err)
	}

	return nil
}

// TmuxWindowExists checks if a tmux window exists for the task.
func (c *Client) TmuxWindowExists(ctx context.Context, spriteName string, taskID int64) bool {
	windowTarget := fmt.Sprintf("task-daemon:task-%d", taskID)
	_, err := c.Exec(ctx, spriteName, fmt.Sprintf("tmux list-panes -t %s", windowTarget))
	return err == nil
}

// Checkpoint creates a checkpoint of the sprite.
func (c *Client) Checkpoint(ctx context.Context, spriteName, comment string) error {
	sprite := c.sdk.Sprite(spriteName)
	if comment == "" {
		comment = fmt.Sprintf("auto-%s", time.Now().Format("2006-01-02-150405"))
	}

	stream, err := sprite.CreateCheckpointWithComment(ctx, comment)
	if err != nil {
		return fmt.Errorf("create checkpoint: %w", err)
	}

	return stream.ProcessAll(func(msg *sdk.StreamMessage) error {
		return nil
	})
}
