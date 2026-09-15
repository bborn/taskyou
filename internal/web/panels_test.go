package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/panel"
)

func TestPanelRoutes(t *testing.T) {
	srv, d, _ := setupServer(t)
	task := createTestTask(t, d, &db.Task{Title: "Checkout accessibility", Status: "backlog", Project: "panel-fixture", WorktreePath: t.TempDir()})
	if err := d.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(task.WorktreePath, "README.md"), []byte("# Checkout"), 0600)
	request := func(method, url, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(body)))
		return w
	}
	base := fmt.Sprintf("/api/tasks/%d", task.ID)
	w := request("GET", base+"/panel-providers", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = request("POST", base+"/panels", `{"provider_id":"file","resource":"README.md"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var p panel.Instance
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	w = request("GET", base+"/panels/"+p.ID+"/content", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "# Checkout") {
		t.Fatal(w.Code, w.Body)
	}
	for _, body := range []string{`{"provider_id":"file","resource":"../private"}`, `{"provider_id":"file","unexpected":true}`, `{} {}`} {
		w = request("POST", base+"/panels", body)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	w = request("GET", "/api/tasks/999999/panels", "")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = request("DELETE", base+"/panels/"+p.ID, "")
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = request("GET", base+"/panels/"+p.ID+"/content", "")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
