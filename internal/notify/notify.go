// Package notify delivers push notifications for task lifecycle events
// (blocked/needs-input, auth-required, completed, failed) to providers like
// ntfy and Telegram. It plugs into the existing events.Emitter rather than
// adding a parallel event path: events.Emitter calls Notifier.Notify for every
// event it emits, and the Notifier decides what (if anything) to send.
//
// Notifications are OFF by default. Nothing is sent unless notify_enabled is
// "true" and at least one provider is configured. Settings are read live from
// the database on every event so config changes take effect without a restart.
package notify

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// sendTimeout bounds how long a single provider delivery may take. Kept short
// so a slow provider never holds up the executor or a CLI command's flush.
const sendTimeout = 8 * time.Second

// SettingsStore is the subset of *db.DB the notifier needs. An interface keeps
// the package testable without a real database.
type SettingsStore interface {
	GetSetting(key string) (string, error)
	GetTaskLogs(taskID int64, limit int) ([]*db.TaskLog, error)
}

// Action is a one-tap button attached to a notification.
type Action struct {
	// Type is the provider-agnostic action kind: "view" opens a URL, "http"
	// performs an HTTP request (used for one-tap unblock).
	Type    string
	Label   string
	URL     string
	Method  string
	Body    string
	Headers map[string]string
	// Clear dismisses the notification after the action succeeds (ntfy only).
	Clear bool
}

// Message is a provider-agnostic notification.
type Message struct {
	Title    string
	Body     string
	Priority int // 1 (min) .. 5 (max); ntfy semantics, mapped per provider
	Tags     []string
	ClickURL string
	Actions  []Action
}

// Provider delivers a Message to a specific backend.
type Provider interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}

// Notifier maps task lifecycle events to notifications and fans them out to the
// configured providers. It satisfies the events.Notifier interface.
type Notifier struct {
	store  SettingsStore
	client *http.Client
	// logf, if set, receives non-fatal delivery errors. nil = silent.
	logf func(format string, args ...any)
}

// New builds a Notifier backed by the given database.
func New(store SettingsStore) *Notifier {
	return &Notifier{
		store:  store,
		client: &http.Client{Timeout: sendTimeout},
	}
}

// SetLogf installs a logging callback for delivery errors (best-effort).
func (n *Notifier) SetLogf(logf func(format string, args ...any)) {
	n.logf = logf
}

func (n *Notifier) logErr(format string, args ...any) {
	if n.logf != nil {
		n.logf(format, args...)
	}
}

// setting reads a setting, trimming whitespace and tolerating errors.
func (n *Notifier) setting(key string) string {
	v, err := n.store.GetSetting(key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// Enabled reports whether notifications are switched on.
func (n *Notifier) Enabled() bool {
	return strings.EqualFold(n.setting(config.SettingNotifyEnabled), "true")
}

// Notify is the events.Notifier entry point. It is called for every emitted
// event; non-notifiable types and disabled/unconfigured setups are dropped
// silently. Safe to call from a goroutine.
func (n *Notifier) Notify(eventType string, task *db.Task, message string) {
	if !n.Enabled() {
		return
	}
	spec, ok := notifiableEvents[eventType]
	if !ok {
		return
	}
	providers := n.providers()
	if len(providers) == 0 {
		return
	}

	msg := n.buildMessage(spec, task, message)

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	for _, p := range providers {
		if err := p.Send(ctx, msg); err != nil {
			n.logErr("notify: %s delivery failed: %v", p.Name(), err)
		}
	}
}

// providers builds the set of configured providers from settings.
func (n *Notifier) providers() []Provider {
	var out []Provider

	if topic := n.setting(config.SettingNtfyTopic); topic != "" {
		server := n.setting(config.SettingNtfyServer)
		if server == "" {
			server = config.DefaultNtfyServer
		}
		out = append(out, &ntfyProvider{
			client: n.client,
			server: server,
			topic:  topic,
			token:  n.setting(config.SettingNtfyToken),
		})
	}

	if token := n.setting(config.SettingTelegramToken); token != "" {
		if chatID := n.setting(config.SettingTelegramChatID); chatID != "" {
			out = append(out, &telegramProvider{
				client: n.client,
				token:  token,
				chatID: chatID,
			})
		}
	}

	return out
}

// eventSpec describes how a given event type renders as a notification.
type eventSpec struct {
	title      string
	tags       []string
	priority   int
	actionable bool // attach a one-tap unblock action
}

// notifiableEvents is the allow-list of event types that produce a push. Other
// events (created/updated/started/...) are intentionally ignored.
var notifiableEvents = map[string]eventSpec{
	"task.blocked":       {title: "Needs input", tags: []string{"bell"}, priority: 4, actionable: true},
	"task.auth_required": {title: "Auth required", tags: []string{"closed_lock_with_key"}, priority: 5, actionable: true},
	"task.completed":     {title: "Completed", tags: []string{"white_check_mark"}, priority: 3},
	"task.failed":        {title: "Failed", tags: []string{"x"}, priority: 4},
}

// buildMessage renders a Message for an event.
func (n *Notifier) buildMessage(spec eventSpec, task *db.Task, message string) Message {
	title := spec.title
	var taskID int64
	bodyLines := []string{}

	if task != nil {
		taskID = task.ID
		if task.Title != "" {
			title = fmt.Sprintf("%s: %s", spec.title, task.Title)
		}
		if task.Project != "" {
			bodyLines = append(bodyLines, "Project: "+task.Project)
		}
	}

	reason := n.reasonFor(task, message)
	if reason != "" {
		bodyLines = append(bodyLines, reason)
	}

	msg := Message{
		Title:    title,
		Body:     strings.Join(bodyLines, "\n"),
		Priority: spec.priority,
		Tags:     spec.tags,
	}

	if taskID > 0 {
		base := n.baseURL()
		msg.ClickURL = base
		if spec.actionable {
			reply := n.setting(config.SettingNotifyUnblockReply)
			if reply == "" {
				reply = config.DefaultUnblockReply
			}
			msg.Actions = []Action{
				{
					Type:   "http",
					Label:  fmt.Sprintf("Reply %q", reply),
					URL:    fmt.Sprintf("%s/api/tasks/%d/input", base, taskID),
					Method: http.MethodPost,
					Body:   fmt.Sprintf(`{"message":%q}`, reply),
					Headers: map[string]string{
						"Content-Type": "application/json",
					},
					Clear: true,
				},
				{
					Type:  "view",
					Label: "Open task",
					URL:   base,
				},
			}
		}
	}

	return msg
}

// reasonFor produces a short, human-meaningful reason for the notification
// body. Several code paths emit a generic message ("status change", "Task needs
// input"); in those cases we surface the latest "question" log entry instead,
// which carries the actual taskyou_needs_input question or PR-review note.
func (n *Notifier) reasonFor(task *db.Task, message string) string {
	msg := strings.TrimSpace(message)
	if isGenericReason(msg) && task != nil {
		if q := n.latestQuestion(task.ID); q != "" {
			return truncate(q, 300)
		}
	}
	return truncate(msg, 300)
}

func isGenericReason(msg string) bool {
	switch strings.ToLower(msg) {
	case "", "status change", "task needs input", "task waiting for input":
		return true
	}
	return false
}

// latestQuestion returns the most recent "question" log entry for a task.
func (n *Notifier) latestQuestion(taskID int64) string {
	logs, err := n.store.GetTaskLogs(taskID, 25)
	if err != nil {
		return ""
	}
	// GetTaskLogs returns newest-first, so the first question wins.
	for _, l := range logs {
		if l.LineType == "question" {
			return strings.TrimSpace(l.Content)
		}
	}
	return ""
}

// baseURL returns the externally reachable base URL for action links, with no
// trailing slash. Falls back to http://localhost:<http_api_port>.
func (n *Notifier) baseURL() string {
	if u := n.setting(config.SettingNotifyBaseURL); u != "" {
		return strings.TrimRight(u, "/")
	}
	port := config.DefaultHTTPAPIPort
	if v := n.setting(config.SettingHTTPAPIPort); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &port); err != nil {
			port = config.DefaultHTTPAPIPort
		}
	}
	return fmt.Sprintf("http://localhost:%d", port)
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
