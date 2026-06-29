package events

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/notify"
)

type notifyStore map[string]string

func (m notifyStore) GetSetting(key string) (string, error) { return m[key], nil }

func TestEmitterFiresNotificationAndFlushes(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
	}))
	defer srv.Close()

	store := notifyStore{
		notify.SettingEnabled: "true",
		notify.SettingTarget:  srv.URL,
	}
	// No hooks dir: isolates the notification path.
	e := New("")
	e.SetNotifier(notify.New(store))

	// Blocked is in the default notify set -> should fire.
	e.EmitTaskBlocked(&db.Task{ID: 1, Title: "needs you"}, "waiting")
	// Updated is not notifiable -> should not fire.
	e.EmitTaskUpdated(&db.Task{ID: 1, Title: "x"}, nil)
	e.Wait() // CLI-exit semantics: Wait must flush notifications too.

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("expected exactly 1 notification, got %d", got)
	}
}

func TestEmitterNoNotifierIsSafe(t *testing.T) {
	e := New("")
	e.EmitTaskCompleted(&db.Task{ID: 2, Title: "ok"})
	e.Wait() // must not panic without a notifier configured
}
