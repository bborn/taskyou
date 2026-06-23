package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// fakeStore is an in-memory SettingsStore for tests.
type fakeStore struct {
	settings map[string]string
	logs     map[int64][]*db.TaskLog
}

func newFakeStore(settings map[string]string) *fakeStore {
	return &fakeStore{settings: settings, logs: map[int64][]*db.TaskLog{}}
}

func (f *fakeStore) GetSetting(key string) (string, error) {
	return f.settings[key], nil
}

func (f *fakeStore) GetTaskLogs(taskID int64, limit int) ([]*db.TaskLog, error) {
	return f.logs[taskID], nil
}

func sampleTask() *db.Task {
	return &db.Task{ID: 42, Title: "Fix the login bug", Project: "webapp", Status: "blocked"}
}

func TestNotifyDisabledByDefault(t *testing.T) {
	// No settings at all → notifications off.
	n := New(newFakeStore(map[string]string{}))
	if n.Enabled() {
		t.Fatal("expected notifications to be disabled by default")
	}

	// A topic is configured but notify_enabled is not set: still off, and Notify
	// must not panic or attempt delivery.
	n = New(newFakeStore(map[string]string{config.SettingNtfyTopic: "mytopic"}))
	if n.Enabled() {
		t.Fatal("expected notifications disabled when notify_enabled unset")
	}
	n.Notify("task.blocked", sampleTask(), "needs input") // should be a no-op
}

func TestNtfyDeliversBlockedWithAction(t *testing.T) {
	var got ntfyPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("missing/wrong auth header: %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(newFakeStore(map[string]string{
		config.SettingNotifyEnabled: "true",
		config.SettingNtfyServer:    srv.URL,
		config.SettingNtfyTopic:     "ty-bruno",
		config.SettingNtfyToken:     "secret-token",
		config.SettingNotifyBaseURL: "https://ty.example.ts.net:8080",
	}))

	n.Notify("task.blocked", sampleTask(), "Which database should I use?")

	if got.Topic != "ty-bruno" {
		t.Errorf("topic = %q, want ty-bruno", got.Topic)
	}
	if !strings.Contains(got.Title, "Fix the login bug") {
		t.Errorf("title %q missing task title", got.Title)
	}
	if !strings.Contains(got.Message, "webapp") {
		t.Errorf("body %q missing project", got.Message)
	}
	if !strings.Contains(got.Message, "Which database") {
		t.Errorf("body %q missing reason", got.Message)
	}

	// One-tap action must POST to the input endpoint with a JSON reply body.
	var httpAction *ntfyAction
	for i := range got.Actions {
		if got.Actions[i].Action == "http" {
			httpAction = &got.Actions[i]
		}
	}
	if httpAction == nil {
		t.Fatal("expected an http action for one-tap unblock")
	}
	wantURL := "https://ty.example.ts.net:8080/api/tasks/42/input"
	if httpAction.URL != wantURL {
		t.Errorf("action URL = %q, want %q", httpAction.URL, wantURL)
	}
	if httpAction.Method != http.MethodPost {
		t.Errorf("action method = %q, want POST", httpAction.Method)
	}
	if !strings.Contains(httpAction.Body, "continue") {
		t.Errorf("action body %q missing default reply", httpAction.Body)
	}
}

func TestNotifyIgnoresNonNotifiableEvents(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(newFakeStore(map[string]string{
		config.SettingNotifyEnabled: "true",
		config.SettingNtfyServer:    srv.URL,
		config.SettingNtfyTopic:     "ty-bruno",
	}))

	n.Notify("task.created", sampleTask(), "")
	n.Notify("task.updated", sampleTask(), "")
	if called {
		t.Fatal("non-notifiable events must not trigger delivery")
	}

	n.Notify("task.completed", sampleTask(), "")
	if !called {
		t.Fatal("task.completed should trigger delivery")
	}
}

func TestReasonFallsBackToLatestQuestion(t *testing.T) {
	store := newFakeStore(map[string]string{
		config.SettingNotifyEnabled: "true",
	})
	// Newest-first ordering, like GetTaskLogs.
	store.logs[42] = []*db.TaskLog{
		{ID: 3, TaskID: 42, LineType: "output", Content: "some output"},
		{ID: 2, TaskID: 42, LineType: "question", Content: "Should I delete the old table?"},
		{ID: 1, TaskID: 42, LineType: "question", Content: "an older question"},
	}
	n := New(store)

	// A generic reason should be replaced by the latest question log.
	if got := n.reasonFor(sampleTask(), "status change"); got != "Should I delete the old table?" {
		t.Errorf("reason = %q, want the latest question", got)
	}
	// A specific reason should be preserved as-is.
	if got := n.reasonFor(sampleTask(), "OAuth token expired"); got != "OAuth token expired" {
		t.Errorf("reason = %q, want the provided message", got)
	}
}

func TestBaseURLFallback(t *testing.T) {
	n := New(newFakeStore(map[string]string{}))
	if got := n.baseURL(); got != "http://localhost:8080" {
		t.Errorf("baseURL = %q, want http://localhost:8080", got)
	}

	n = New(newFakeStore(map[string]string{config.SettingHTTPAPIPort: "9000"}))
	if got := n.baseURL(); got != "http://localhost:9000" {
		t.Errorf("baseURL = %q, want http://localhost:9000", got)
	}

	n = New(newFakeStore(map[string]string{config.SettingNotifyBaseURL: "https://x.ts.net:8080/"}))
	if got := n.baseURL(); got != "https://x.ts.net:8080" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", got)
	}
}

func TestTelegramDeliversWithDeepLink(t *testing.T) {
	var got telegramPayload
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &telegramProvider{client: srv.Client(), token: "bot-token", chatID: "12345", apiBase: srv.URL}
	msg := Message{
		Title:    "Needs input: Fix the login bug",
		Body:     "Project: webapp\nWhich database?",
		ClickURL: "https://ty.example/",
		Actions: []Action{
			{Type: "view", Label: "Open task", URL: "https://ty.example/"},
			// An http action has no URL-navigation equivalent and must be dropped.
			{Type: "http", Label: "Reply", URL: "https://ty.example/api/tasks/42/input"},
		},
	}

	if err := p.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if path != "/botbot-token/sendMessage" {
		t.Errorf("path = %q, want /botbot-token/sendMessage", path)
	}
	if got.ChatID != "12345" {
		t.Errorf("chat_id = %q, want 12345", got.ChatID)
	}
	if !strings.Contains(got.Text, "Fix the login bug") || !strings.Contains(got.Text, "Which database?") {
		t.Errorf("text %q missing expected content", got.Text)
	}
	if got.ReplyMarkup == nil || len(got.ReplyMarkup.InlineKeyboard) != 1 {
		t.Fatalf("expected one inline keyboard row, got %+v", got.ReplyMarkup)
	}
	row := got.ReplyMarkup.InlineKeyboard[0]
	if len(row) != 1 || row[0].URL != "https://ty.example/" {
		t.Errorf("expected single view button to the web UI, got %+v", row)
	}
}
