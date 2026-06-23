// Package notify sends push notifications for task lifecycle events so you can
// step away from the keyboard and still know when an agent needs you.
//
// It is deliberately self-contained (stdlib only) and config-driven: nothing is
// sent unless the user opts in via settings. Two delivery providers are
// supported:
//
//   - "ntfy"    — POST the message to an ntfy topic URL (ntfy.sh or self-hosted).
//     ntfy has free iOS/Android apps, so this is the fastest path to a push on
//     your phone with no accounts or API keys.
//   - "webhook" — POST a JSON payload to an arbitrary URL (Slack/Discord/
//     Telegram bridges, Zapier, your own endpoint, etc.).
//
// Notifications carry a deep link back into the mobile console (GET /m) so a tap
// opens the task and you can reply, retry, or approve from a phone.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Setting keys (stored in the task database settings table).
const (
	SettingEnabled  = "notify_enabled"  // "true" to enable push notifications
	SettingProvider = "notify_provider" // "ntfy" (default) or "webhook"
	SettingTarget   = "notify_target"   // ntfy topic URL or webhook URL
	SettingEvents   = "notify_events"   // comma list of event keys to notify on
	SettingURL      = "notify_url"      // base URL for deep links into the console
)

// ProviderNtfy and ProviderWebhook are the supported delivery providers.
const (
	ProviderNtfy    = "ntfy"
	ProviderWebhook = "webhook"
)

// DefaultEvents is the set of event keys that fire a notification when the user
// hasn't customized notify_events. These are the moments where a walking user
// actually needs to act; created/started/updated are intentionally excluded as
// too noisy.
const DefaultEvents = "blocked,auth_required,completed,failed"

// SettingsStore is the minimal slice of the database the notifier needs. *db.DB
// satisfies it; defining it here keeps this package free of a db import.
type SettingsStore interface {
	GetSetting(key string) (string, error)
}

// Notification is a provider-agnostic description of something worth a push.
type Notification struct {
	Event   string // short event key, e.g. "blocked", "completed"
	TaskID  int64
	Title   string // task title
	Status  string // task status
	Project string
	Message string // reason / summary, optional
}

// Notifier delivers notifications using settings read fresh on each send, so
// toggling config takes effect immediately without a restart.
type Notifier struct {
	store  SettingsStore
	client *http.Client
}

// New creates a Notifier backed by the given settings store.
func New(store SettingsStore) *Notifier {
	return &Notifier{
		store:  store,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// EventKey maps a full events.* type string ("task.blocked") to the short key
// used in settings and notification copy ("blocked"). Unknown/irrelevant types
// return "" and are never notified.
func EventKey(eventType string) string {
	switch eventType {
	case "task.blocked":
		return "blocked"
	case "task.auth_required":
		return "auth_required"
	case "task.completed":
		return "completed"
	case "task.failed":
		return "failed"
	case "task.created":
		return "created"
	case "task.started":
		return "started"
	case "task.worktree_ready":
		return "worktree_ready"
	default:
		return ""
	}
}

// Enabled reports whether notifications are switched on and a target is set.
func (n *Notifier) Enabled() bool {
	if n == nil || n.store == nil {
		return false
	}
	if v, _ := n.store.GetSetting(SettingEnabled); v != "true" {
		return false
	}
	target, _ := n.store.GetSetting(SettingTarget)
	return strings.TrimSpace(target) != ""
}

// ShouldNotify reports whether the given short event key is in the user's
// configured set (or the default set when unconfigured).
func (n *Notifier) ShouldNotify(eventKey string) bool {
	if eventKey == "" {
		return false
	}
	configured, _ := n.store.GetSetting(SettingEvents)
	if strings.TrimSpace(configured) == "" {
		configured = DefaultEvents
	}
	for _, e := range strings.Split(configured, ",") {
		if strings.TrimSpace(e) == eventKey {
			return true
		}
	}
	return false
}

// Notify sends a push for the notification if notifications are enabled and the
// event is in scope. It is best-effort: errors are returned for callers that
// want them (e.g. `ty notify test`) but the event pipeline ignores them.
func (n *Notifier) Notify(note Notification) error {
	if !n.Enabled() || !n.ShouldNotify(note.Event) {
		return nil
	}
	return n.send(note)
}

// Test sends a verification notification, bypassing the event filter but still
// requiring a target to be configured. Used by `ty notify test`.
func (n *Notifier) Test() error {
	target, _ := n.store.GetSetting(SettingTarget)
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("notify_target is not set")
	}
	return n.send(Notification{
		Event:   "completed",
		Title:   "TaskYou test notification",
		Status:  "done",
		Message: "If you can see this on your phone, you're all set. Go for that walk.",
	})
}

// send dispatches to the configured provider regardless of enablement; used by
// the test command to verify delivery.
func (n *Notifier) send(note Notification) error {
	target, _ := n.store.GetSetting(SettingTarget)
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("notify_target is not set")
	}
	provider, _ := n.store.GetSetting(SettingProvider)
	switch strings.TrimSpace(provider) {
	case "", ProviderNtfy:
		return n.sendNtfy(target, note)
	case ProviderWebhook:
		return n.sendWebhook(target, note)
	default:
		return fmt.Errorf("unknown notify_provider %q (use %q or %q)", provider, ProviderNtfy, ProviderWebhook)
	}
}

// linkFor builds a deep link into the mobile console for the task, or "" when no
// base URL is configured.
func (n *Notifier) linkFor(taskID int64) string {
	base, _ := n.store.GetSetting(SettingURL)
	base = strings.TrimSpace(base)
	if base == "" {
		base, _ = n.store.GetSetting("server_url")
		base = strings.TrimSpace(base)
	}
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/m?task=%d", strings.TrimRight(base, "/"), taskID)
}

func (n *Notifier) sendNtfy(target string, note Notification) error {
	title, priority, tag := decorate(note)
	body := note.Title
	if note.Message != "" {
		body = fmt.Sprintf("%s\n%s", note.Title, note.Message)
	}

	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tag)
	if link := n.linkFor(note.TaskID); link != "" {
		req.Header.Set("Click", link)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy responded %s", resp.Status)
	}
	return nil
}

func (n *Notifier) sendWebhook(target string, note Notification) error {
	payload := map[string]interface{}{
		"event":   note.Event,
		"task_id": note.TaskID,
		"title":   note.Title,
		"status":  note.Status,
		"project": note.Project,
		"message": note.Message,
		"url":     n.linkFor(note.TaskID),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook responded %s", resp.Status)
	}
	return nil
}

// decorate returns the title, ntfy priority, and ntfy tag (emoji) for an event.
func decorate(note Notification) (title, priority, tag string) {
	id := fmt.Sprintf("#%d", note.TaskID)
	switch note.Event {
	case "blocked":
		return fmt.Sprintf("%s needs you", id), "high", "bell"
	case "auth_required":
		return fmt.Sprintf("%s needs sign-in", id), "high", "lock"
	case "failed":
		return fmt.Sprintf("%s failed", id), "high", "x"
	case "completed":
		return fmt.Sprintf("%s done", id), "default", "white_check_mark"
	default:
		return fmt.Sprintf("%s %s", id, note.Event), "default", "loudspeaker"
	}
}
