package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mapStore is an in-memory SettingsStore for tests.
type mapStore map[string]string

func (m mapStore) GetSetting(key string) (string, error) { return m[key], nil }

func TestEventKey(t *testing.T) {
	cases := map[string]string{
		"task.blocked":       "blocked",
		"task.auth_required": "auth_required",
		"task.completed":     "completed",
		"task.failed":        "failed",
		"task.updated":       "", // not notifiable
		"nonsense":           "",
	}
	for in, want := range cases {
		if got := EventKey(in); got != want {
			t.Errorf("EventKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnabledRequiresFlagAndTarget(t *testing.T) {
	if New(mapStore{SettingEnabled: "true"}).Enabled() {
		t.Error("Enabled() should be false without a target")
	}
	if New(mapStore{SettingTarget: "https://ntfy.sh/x"}).Enabled() {
		t.Error("Enabled() should be false when not switched on")
	}
	if !New(mapStore{SettingEnabled: "true", SettingTarget: "https://ntfy.sh/x"}).Enabled() {
		t.Error("Enabled() should be true with flag + target")
	}
}

func TestShouldNotifyDefaultsAndCustom(t *testing.T) {
	def := New(mapStore{})
	if !def.ShouldNotify("blocked") || !def.ShouldNotify("completed") {
		t.Error("default events should include blocked and completed")
	}
	if def.ShouldNotify("created") {
		t.Error("default events should not include created")
	}
	custom := New(mapStore{SettingEvents: "created, blocked"})
	if !custom.ShouldNotify("created") || !custom.ShouldNotify("blocked") {
		t.Error("custom event list not honored")
	}
	if custom.ShouldNotify("completed") {
		t.Error("completed should be excluded by custom list")
	}
	if def.ShouldNotify("") {
		t.Error("empty event key must never notify")
	}
}

func TestNotifyDisabledSendsNothing(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()

	// Enabled flag off -> no send even with a valid target.
	n := New(mapStore{SettingTarget: srv.URL})
	if err := n.Notify(Notification{Event: "blocked", TaskID: 1, Title: "x"}); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}
	if hits != 0 {
		t.Fatalf("expected no HTTP calls when disabled, got %d", hits)
	}
}

func TestNotifyNtfySetsHeadersAndLink(t *testing.T) {
	var gotTitle, gotPriority, gotClick, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotClick = r.Header.Get("Click")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(mapStore{
		SettingEnabled: "true",
		SettingTarget:  srv.URL,
		SettingURL:     "http://host:8080",
	})
	err := n.Notify(Notification{Event: "blocked", TaskID: 42, Title: "Fix bug", Message: "needs input"})
	if err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	if !strings.Contains(gotTitle, "#42") {
		t.Errorf("title missing task id: %q", gotTitle)
	}
	if gotPriority != "high" {
		t.Errorf("blocked should be high priority, got %q", gotPriority)
	}
	if gotClick != "http://host:8080/m?task=42" {
		t.Errorf("unexpected deep link: %q", gotClick)
	}
	if !strings.Contains(gotBody, "Fix bug") || !strings.Contains(gotBody, "needs input") {
		t.Errorf("body missing content: %q", gotBody)
	}
}

func TestNotifyLinkFallsBackToServerURL(t *testing.T) {
	var gotClick string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClick = r.Header.Get("Click")
	}))
	defer srv.Close()

	n := New(mapStore{
		SettingEnabled: "true",
		SettingTarget:  srv.URL,
		"server_url":   "http://fallback:9999/",
	})
	_ = n.Notify(Notification{Event: "completed", TaskID: 7, Title: "done"})
	if gotClick != "http://fallback:9999/m?task=7" {
		t.Errorf("expected server_url fallback link, got %q", gotClick)
	}
}

func TestNotifyWebhookPostsJSON(t *testing.T) {
	var payload map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected json content type, got %q", ct)
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
	}))
	defer srv.Close()

	n := New(mapStore{
		SettingEnabled:  "true",
		SettingProvider: ProviderWebhook,
		SettingTarget:   srv.URL,
		SettingURL:      "http://host",
	})
	err := n.Notify(Notification{Event: "failed", TaskID: 5, Title: "boom", Status: "failed", Project: "p"})
	if err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	if payload["event"] != "failed" || payload["title"] != "boom" || payload["project"] != "p" {
		t.Errorf("unexpected webhook payload: %#v", payload)
	}
	if payload["url"] != "http://host/m?task=5" {
		t.Errorf("webhook url wrong: %v", payload["url"])
	}
}

func TestTestBypassesEventFilterButRequiresTarget(t *testing.T) {
	if err := New(mapStore{}).Test(); err == nil {
		t.Error("Test() should error without a target")
	}

	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()

	// Even with an event list that excludes 'completed', Test() still sends.
	n := New(mapStore{SettingTarget: srv.URL, SettingEvents: "blocked"})
	if err := n.Test(); err != nil {
		t.Fatalf("Test() error: %v", err)
	}
	if !hit {
		t.Error("Test() should have sent a notification")
	}
}

func TestUnknownProviderErrors(t *testing.T) {
	n := New(mapStore{SettingTarget: "http://x", SettingProvider: "carrier-pigeon"})
	if err := n.send(Notification{Event: "blocked"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}
