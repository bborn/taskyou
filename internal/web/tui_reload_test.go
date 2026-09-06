package web

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/tuireload"
)

func TestTUIReloadRouteUsesSharedRequest(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	server := New(Config{DB: database})
	w := httptest.NewRecorder()
	server.srv.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/tui/reload", nil))
	if w.Code != 202 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if token, err := tuireload.Token(database); err != nil || token == "" {
		t.Fatal("reload request not stored", err)
	}
}
