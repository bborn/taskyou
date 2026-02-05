// Package processor handles the email processing pipeline.
package processor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bborn/workflow/extensions/ty-email/internal/adapter"
	"github.com/bborn/workflow/extensions/ty-email/internal/bridge"
	"github.com/bborn/workflow/extensions/ty-email/internal/classifier"
	"github.com/bborn/workflow/extensions/ty-email/internal/state"
)

// Processor handles the email → classify → execute → reply pipeline.
type Processor struct {
	adapter    adapter.Adapter
	classifier classifier.Classifier
	bridge     *bridge.Bridge
	state      *state.DB
	logger     *slog.Logger
}

// Config holds processor configuration.
type Config struct {
	DefaultProject string
	FromAddress    string // Reply-from address
}

// New creates a new processor.
func New(
	adp adapter.Adapter,
	cls classifier.Classifier,
	br *bridge.Bridge,
	st *state.DB,
	logger *slog.Logger,
) *Processor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Processor{
		adapter:    adp,
		classifier: cls,
		bridge:     br,
		state:      st,
		logger:     logger,
	}
}

// ProcessEmail handles a single inbound email.
func (p *Processor) ProcessEmail(ctx context.Context, email *adapter.Email) error {
	p.logger.Info("processing email",
		"from", email.From,
		"subject", email.Subject,
		"id", email.ID,
	)

	// Check if already processed
	processed, err := p.state.IsProcessed(email.ID)
	if err != nil {
		return fmt.Errorf("failed to check processed status: %w", err)
	}
	if processed {
		p.logger.Debug("email already processed", "id", email.ID)
		return nil
	}

	// Check if this is part of an existing thread
	var threadTaskID *int64
	if email.InReplyTo != "" {
		threadTaskID, _ = p.state.GetThreadTask(email.InReplyTo)
	}
	// Also check references
	if threadTaskID == nil && len(email.References) > 0 {
		for _, ref := range email.References {
			threadTaskID, _ = p.state.GetThreadTask(ref)
			if threadTaskID != nil {
				break
			}
		}
	}

	// Get current tasks for context
	tasks, err := p.bridge.ListTasks("")
	if err != nil {
		p.logger.Warn("failed to list tasks for context", "error", err)
		tasks = []bridge.Task{}
	}

	// Classify the email
	action, err := p.classifier.Classify(ctx, email, bridge.ToClassifierTasks(tasks), threadTaskID)
	if err != nil {
		return fmt.Errorf("failed to classify email: %w", err)
	}

	p.logger.Info("classified email",
		"action", action.Type,
		"confidence", action.Confidence,
		"reasoning", action.Reasoning,
	)

	// Execute the action
	var taskID *int64
	var reply string

	switch action.Type {
	case classifier.ActionCreate:
		taskID, reply, err = p.handleCreate(ctx, action)
	case classifier.ActionInput:
		taskID, reply, err = p.handleInput(ctx, action, threadTaskID)
	case classifier.ActionExecute:
		taskID, reply, err = p.handleExecute(ctx, action)
	case classifier.ActionQuery:
		reply, err = p.handleQuery(ctx, action)
	case classifier.ActionIgnore:
		p.logger.Info("ignoring email", "reasoning", action.Reasoning)
		reply = "" // No reply for ignored emails
	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
	}

	if err != nil {
		// Still mark as processed to avoid retry loops
		p.state.MarkProcessed(email.ID, taskID, string(action.Type)+":error")
		return fmt.Errorf("failed to execute action: %w", err)
	}

	// Link thread to task if we created or interacted with a task
	if taskID != nil && email.ID != "" {
		p.state.LinkThread(email.ID, *taskID)
	}

	// Mark as processed
	p.state.MarkProcessed(email.ID, taskID, string(action.Type))

	// Queue reply if we have one
	if reply != "" || action.Reply != "" {
		replyText := reply
		if replyText == "" {
			replyText = action.Reply
		}

		subject := email.Subject
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}

		_, err = p.state.QueueOutbound(
			email.From,
			subject,
			replyText,
			taskID,
			email.ID,
		)
		if err != nil {
			p.logger.Error("failed to queue reply", "error", err)
		}
	}

	// Mark email as processed in the adapter (move to folder, add label, etc.)
	if err := p.adapter.MarkProcessed(ctx, email.ID); err != nil {
		p.logger.Warn("failed to mark email as processed in adapter", "error", err)
	}

	return nil
}

func (p *Processor) handleCreate(ctx context.Context, action *classifier.Action) (*int64, string, error) {
	result, err := p.bridge.CreateTask(action)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create task: %w", err)
	}

	taskID := result.ID
	reply := fmt.Sprintf("Created task #%d: %s\nStatus: %s\nProject: %s",
		result.ID, result.Title, result.Status, result.Project)

	if action.Reply != "" {
		reply = action.Reply + "\n\n---\n" + reply
	}

	p.logger.Info("created task", "id", taskID, "title", result.Title)
	return &taskID, reply, nil
}

func (p *Processor) handleInput(ctx context.Context, action *classifier.Action, threadTaskID *int64) (*int64, string, error) {
	taskID := action.TaskID
	if taskID == 0 && threadTaskID != nil {
		taskID = *threadTaskID
	}
	if taskID == 0 {
		return nil, "", fmt.Errorf("no task ID specified for input")
	}

	err := p.bridge.SendInput(taskID, action.InputText)
	if err != nil {
		return nil, "", fmt.Errorf("failed to send input: %w", err)
	}

	reply := fmt.Sprintf("Sent your input to task #%d.", taskID)
	if action.Reply != "" {
		reply = action.Reply
	}

	p.logger.Info("sent input to task", "id", taskID)
	return &taskID, reply, nil
}

func (p *Processor) handleExecute(ctx context.Context, action *classifier.Action) (*int64, string, error) {
	taskID := action.TaskID
	if taskID == 0 {
		return nil, "", fmt.Errorf("no task ID specified for execute")
	}

	err := p.bridge.ExecuteTask(taskID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to execute task: %w", err)
	}

	reply := fmt.Sprintf("Queued task #%d for execution.", taskID)
	if action.Reply != "" {
		reply = action.Reply
	}

	p.logger.Info("queued task for execution", "id", taskID)
	return &taskID, reply, nil
}

func (p *Processor) handleQuery(ctx context.Context, action *classifier.Action) (string, error) {
	// Get all tasks and format a status report
	tasks, err := p.bridge.ListTasks("")
	if err != nil {
		return "", fmt.Errorf("failed to list tasks: %w", err)
	}

	if len(tasks) == 0 {
		return "No active tasks.", nil
	}

	var sb strings.Builder
	sb.WriteString("Current tasks:\n\n")

	// Group by status
	byStatus := make(map[string][]bridge.Task)
	for _, t := range tasks {
		byStatus[t.Status] = append(byStatus[t.Status], t)
	}

	statusOrder := []string{"processing", "blocked", "queued", "backlog"}
	for _, status := range statusOrder {
		if ts, ok := byStatus[status]; ok && len(ts) > 0 {
			sb.WriteString(fmt.Sprintf("## %s\n", strings.Title(status)))
			for _, t := range ts {
				sb.WriteString(fmt.Sprintf("- #%d: %s (%s)\n", t.ID, t.Title, t.Project))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String(), nil
}

// SendPendingReplies sends queued outbound emails.
func (p *Processor) SendPendingReplies(ctx context.Context) error {
	emails, err := p.state.GetPendingOutbound(3) // Max 3 attempts
	if err != nil {
		return fmt.Errorf("failed to get pending emails: %w", err)
	}

	for _, e := range emails {
		outbound := &adapter.OutboundEmail{
			To:        []string{e.To},
			Subject:   e.Subject,
			Body:      e.Body,
			InReplyTo: e.InReplyTo,
			TaskID:    0,
		}
		if e.TaskID != nil {
			outbound.TaskID = *e.TaskID
		}

		err := p.adapter.Send(ctx, outbound)
		if err != nil {
			p.logger.Error("failed to send email", "id", e.ID, "error", err)
			p.state.MarkOutboundFailed(e.ID, err.Error())
			continue
		}

		p.state.MarkOutboundSent(e.ID)
		p.logger.Info("sent reply", "to", e.To, "subject", e.Subject)
	}

	return nil
}

// CheckBlockedTasks checks for blocked tasks and sends notification emails.
func (p *Processor) CheckBlockedTasks(ctx context.Context, notifyAddress string) error {
	blocked, err := p.bridge.GetBlockedTasks()
	if err != nil {
		return fmt.Errorf("failed to get blocked tasks: %w", err)
	}

	for _, task := range blocked {
		// Check if we already have a thread for this task
		threadID, err := p.state.GetTaskThread(task.ID)
		if err != nil {
			p.logger.Warn("failed to get thread for task", "id", task.ID, "error", err)
			continue
		}

		// If no thread exists, this task wasn't created via email - skip
		if threadID == "" {
			continue
		}

		// Get recent output to include in the notification
		output, _ := p.bridge.GetTaskOutput(task.ID, 50)

		subject := fmt.Sprintf("Task #%d needs your input: %s", task.ID, task.Title)
		body := fmt.Sprintf("[Task #%d needs input]\n\n%s\n\n---\nRecent output:\n%s",
			task.ID, task.Body, output)

		taskID := task.ID
		_, err = p.state.QueueOutbound(notifyAddress, subject, body, &taskID, threadID)
		if err != nil {
			p.logger.Error("failed to queue blocked notification", "task", task.ID, "error", err)
		}
	}

	return nil
}
