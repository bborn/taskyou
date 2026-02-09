package classifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/bborn/workflow/extensions/ty-email/internal/adapter"
)

// ClaudeClassifier uses Claude API for email classification.
type ClaudeClassifier struct {
	client *anthropic.Client
	model  string
}

// NewClaudeClassifier creates a new Claude classifier.
func NewClaudeClassifier(cfg *Config) (*ClaudeClassifier, error) {
	apiKey := cfg.APIKey
	if apiKey == "" && cfg.APIKeyCmd != "" {
		out, err := exec.Command("sh", "-c", cfg.APIKeyCmd).Output()
		if err != nil {
			return nil, fmt.Errorf("failed to get API key: %w", err)
		}
		apiKey = strings.TrimSpace(string(out))
	}

	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured")
	}

	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
	)

	model := cfg.Model
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}

	return &ClaudeClassifier{
		client: client,
		model:  model,
	}, nil
}

func (c *ClaudeClassifier) Name() string {
	return "claude"
}

func (c *ClaudeClassifier) IsAvailable() bool {
	return c.client != nil
}

func (c *ClaudeClassifier) Classify(ctx context.Context, email *adapter.Email, tasks []Task, threadTaskID *int64) (*Action, error) {
	// Limit task context to avoid excessive input tokens
	limitedTasks := tasks
	if len(limitedTasks) > 10 {
		limitedTasks = limitedTasks[:10]
	}

	prompt := c.buildPrompt(email, limitedTasks, threadTaskID)

	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(c.model),
		MaxTokens: anthropic.Int(256),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("claude API error: %w", err)
	}

	// Log token usage
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		slog.Info("classifier token usage",
			"model", c.model,
			"input_tokens", resp.Usage.InputTokens,
			"output_tokens", resp.Usage.OutputTokens,
		)
	}

	// Extract text response
	var responseText string
	for _, block := range resp.Content {
		if block.Type == anthropic.ContentBlockTypeText {
			responseText = block.Text
			break
		}
	}

	// Parse JSON response
	action, err := c.parseResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return action, nil
}

func (c *ClaudeClassifier) buildPrompt(email *adapter.Email, tasks []Task, threadTaskID *int64) string {
	var sb strings.Builder

	sb.WriteString(`You are an email classifier for TaskYou, a task management system.

Your job is to understand the intent of incoming emails and translate them to TaskYou actions.

Available actions:
- "create": Create a new task
- "input": Provide input to a task that's waiting (status=blocked)
- "execute": Queue a task for execution
- "query": User is asking about task status
- "ignore": Email is spam, irrelevant, or doesn't need action

`)

	// Add current tasks context
	if len(tasks) > 0 {
		sb.WriteString("Current tasks:\n")
		for _, t := range tasks {
			sb.WriteString(fmt.Sprintf("- #%d: %s (status: %s, project: %s)\n", t.ID, t.Title, t.Status, t.Project))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("No current tasks.\n\n")
	}

	// Add thread context if this is a reply
	if threadTaskID != nil {
		sb.WriteString(fmt.Sprintf("This email is part of a thread related to task #%d.\n\n", *threadTaskID))
	}

	// Add the email
	sb.WriteString("Incoming email:\n")
	sb.WriteString(fmt.Sprintf("From: %s\n", email.From))
	sb.WriteString(fmt.Sprintf("Subject: %s\n", email.Subject))
	sb.WriteString(fmt.Sprintf("Body:\n%s\n\n", email.Body))

	// Instructions
	sb.WriteString(`Analyze this email and respond with a JSON object:

{
  "type": "create|input|execute|query|ignore",
  "title": "task title",           // for create
  "body": "task description",      // for create
  "project": "project name",       // for create (optional)
  "task_type": "code|writing|thinking", // for create (optional, default: code)
  "execute": false,                // for create: queue immediately?
  "task_id": 123,                  // for input/execute
  "input_text": "the input",       // for input
  "query": "what they're asking",  // for query
  "reply": "what to reply",        // always include a friendly reply
  "reasoning": "why this action",  // brief explanation
  "confidence": 0.95               // 0-1 confidence score
}

Guidelines:
- If the email is clearly about a specific existing task (by ID or context), use "input" or relate to that task
- If it's a new request/bug report/feature ask, use "create"
- Extract a clear, actionable title for new tasks
- Include relevant details in the body
- If the email is a reply in a thread about a blocked task, it's likely providing "input"
- Set "execute": true if user wants immediate execution (phrases like "and run it", "execute now", "do it", "start this", "asap", etc.)
- Be friendly in replies, confirm what action you took
- If unsure, ask for clarification in the reply and use lower confidence

Respond with only the JSON object, no other text.`)

	return sb.String()
}

func (c *ClaudeClassifier) parseResponse(text string) (*Action, error) {
	// Try to extract JSON from the response
	text = strings.TrimSpace(text)

	// Handle markdown code blocks
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		var jsonLines []string
		inBlock := false
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock {
				jsonLines = append(jsonLines, line)
			}
		}
		text = strings.Join(jsonLines, "\n")
	}

	var action Action
	if err := json.Unmarshal([]byte(text), &action); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w\nresponse: %s", err, text)
	}

	return &action, nil
}
