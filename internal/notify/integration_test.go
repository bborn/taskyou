package notify_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/events"
	"github.com/bborn/workflow/internal/notify"
)

// capture records the most recent ntfy publish payload.
type capture struct {
	mu   sync.Mutex
	body map[string]any
	hits int
}

func (c *capture) record(b map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = b
	c.hits++
}

func (c *capture) snapshot() (map[string]any, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body, c.hits
}

// TestEndToEndBlockedDeliversActionablePush exercises the full real path:
// db.UpdateTaskStatus(blocked) → events.Emitter.Emit → notify.Notifier →
// ntfy HTTP publish, asserting the push carries a one-tap action that targets
// the existing POST /api/tasks/{id}/input endpoint. This proves the wiring is
// not a parallel path — it rides the same emitter every mutation already uses.
func TestEndToEndBlockedDeliversActionablePush(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		cap.record(payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	if err := database.CreateProject(&db.Project{Name: "webapp", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &db.Task{Title: "Fix the login bug", Status: db.StatusProcessing, Type: db.TypeCode, Project: "webapp"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	// The agent asks a question via taskyou_needs_input, which logs it.
	if err := database.AppendTaskLog(task.ID, "question", "Should I use Postgres or SQLite?"); err != nil {
		t.Fatalf("append log: %v", err)
	}

	// Configure notifications to point at our fake ntfy server.
	settings := map[string]string{
		config.SettingNotifyEnabled: "true",
		config.SettingNtfyServer:    srv.URL,
		config.SettingNtfyTopic:     "ty-test",
		config.SettingNotifyBaseURL: "https://ty.example.ts.net:8080",
	}
	for k, v := range settings {
		if err := database.SetSetting(k, v); err != nil {
			t.Fatalf("set setting %s: %v", k, err)
		}
	}

	// Wire the real emitter + notifier exactly like the executor/CLI do.
	emitter := events.New("") // no hooks dir; notifier still fires
	emitter.SetNotifier(notify.New(database))
	database.SetEventEmitter(emitter)

	// Block the task through the normal mutation path.
	if err := database.UpdateTaskStatus(task.ID, db.StatusBlocked); err != nil {
		t.Fatalf("update status: %v", err)
	}
	emitter.Wait() // flush async notification

	body, hits := cap.snapshot()
	if hits == 0 {
		t.Fatal("expected a push to be delivered on task.blocked")
	}
	if body["topic"] != "ty-test" {
		t.Errorf("topic = %v, want ty-test", body["topic"])
	}
	if title, _ := body["title"].(string); !strings.Contains(title, "Fix the login bug") {
		t.Errorf("title %q missing task title", title)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "Postgres") || !strings.Contains(msg, "webapp") {
		t.Errorf("message %q missing question/project", msg)
	}

	actions, _ := body["actions"].([]any)
	var found bool
	wantURL := "https://ty.example.ts.net:8080/api/tasks/" + strconv.FormatInt(task.ID, 10) + "/input"
	for _, a := range actions {
		m, _ := a.(map[string]any)
		if m["action"] == "http" && m["url"] == wantURL {
			found = true
			if m["method"] != http.MethodPost {
				t.Errorf("action method = %v, want POST", m["method"])
			}
			if reply, _ := m["body"].(string); !strings.Contains(reply, "continue") {
				t.Errorf("action body %q missing default reply", reply)
			}
		}
	}
	if !found {
		t.Errorf("no http action targeting %s; actions=%v", wantURL, actions)
	}
}

// TestEndToEndDisabledSendsNothing confirms the default-off behavior end to end.
func TestEndToEndDisabledSendsNothing(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.record(nil)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := database.CreateProject(&db.Project{Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &db.Task{Title: "T", Status: db.StatusProcessing, Type: db.TypeCode, Project: "p"}
	if err := database.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	// ntfy topic set, but notify_enabled NOT set → nothing should send.
	_ = database.SetSetting(config.SettingNtfyServer, srv.URL)
	_ = database.SetSetting(config.SettingNtfyTopic, "ty-test")

	emitter := events.New("")
	emitter.SetNotifier(notify.New(database))
	database.SetEventEmitter(emitter)

	if err := database.UpdateTaskStatus(task.ID, db.StatusBlocked); err != nil {
		t.Fatalf("update status: %v", err)
	}
	emitter.Wait()

	// Give any erroneous async send a brief window to land.
	time.Sleep(50 * time.Millisecond)
	if _, hits := cap.snapshot(); hits != 0 {
		t.Fatalf("expected no delivery when disabled, got %d", hits)
	}
}
