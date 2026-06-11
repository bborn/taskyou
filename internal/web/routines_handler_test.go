package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// setupRoutinesDir isolates routine definitions and state for a test.
func setupRoutinesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TY_ROUTINES_DIR", dir)
	t.Setenv("TY_ROUTINES_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	return dir
}

func writeRoutine(t *testing.T, dir, name, prompt string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, "prompt.md"), []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHandleListRoutines(t *testing.T) {
	dir := setupRoutinesDir(t)
	srv, database, _ := setupServer(t)
	writeRoutine(t, dir, "scout", "---\nproject: webapp\nmodel: opus\n---\ndo the thing")
	writeRoutine(t, dir, "digest", "summarize the day")

	// Give scout a finished run.
	runID, err := database.CreateRoutineRun("scout")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.FinishRoutineRun(runID, "ok", 0, "all good"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/routines", nil)
	w := httptest.NewRecorder()
	srv.handleListRoutines(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var routines []routineJSON
	if err := json.NewDecoder(w.Body).Decode(&routines); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(routines) != 2 {
		t.Fatalf("expected 2 routines, got %d", len(routines))
	}
	byName := map[string]routineJSON{}
	for _, rt := range routines {
		byName[rt.Name] = rt
	}
	scout := byName["scout"]
	if scout.Project != "webapp" || scout.Model != "opus" {
		t.Errorf("scout frontmatter not surfaced: %+v", scout)
	}
	if scout.LastRun == nil || scout.LastRun.Status != "ok" {
		t.Errorf("scout last run missing: %+v", scout.LastRun)
	}
	if byName["digest"].LastRun != nil {
		t.Error("digest should have no runs")
	}
}

func TestHandleListRoutineRuns(t *testing.T) {
	dir := setupRoutinesDir(t)
	srv, database, _ := setupServer(t)
	writeRoutine(t, dir, "scout", "x")

	for i := 0; i < 3; i++ {
		id, err := database.CreateRoutineRun("scout")
		if err != nil {
			t.Fatal(err)
		}
		if err := database.FinishRoutineRun(id, "ok", 0, fmt.Sprintf("run %d", i)); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest("GET", "/api/routines/scout/runs?limit=2", nil)
	req.SetPathValue("name", "scout")
	w := httptest.NewRecorder()
	srv.handleListRoutineRuns(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var runs []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("expected limit=2 runs, got %d", len(runs))
	}
}

func TestHandleListRoutineRuns_UnknownRoutine(t *testing.T) {
	setupRoutinesDir(t)
	srv, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/routines/ghost/runs", nil)
	req.SetPathValue("name", "ghost")
	w := httptest.NewRecorder()
	srv.handleListRoutineRuns(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleRoutineRunLog(t *testing.T) {
	dir := setupRoutinesDir(t)
	srv, database, _ := setupServer(t)
	writeRoutine(t, dir, "scout", "x")

	logPath := filepath.Join(t.TempDir(), "run-1.log")
	if err := os.WriteFile(logPath, []byte("full log contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, _ := database.CreateRoutineRun("scout")
	if err := database.SetRoutineRunLogPath(id, logPath); err != nil {
		t.Fatal(err)
	}
	if err := database.FinishRoutineRun(id, "ok", 0, "tail"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/routines/scout/runs/%d/log", id), nil)
	req.SetPathValue("name", "scout")
	req.SetPathValue("run", fmt.Sprint(id))
	w := httptest.NewRecorder()
	srv.handleRoutineRunLog(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["log"] != "full log contents" {
		t.Errorf("log = %q", resp["log"])
	}

	// Wrong routine for the run id → 404.
	writeRoutine(t, dir, "other", "y")
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/routines/other/runs/%d/log", id), nil)
	req.SetPathValue("name", "other")
	req.SetPathValue("run", fmt.Sprint(id))
	w = httptest.NewRecorder()
	srv.handleRoutineRunLog(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for mismatched routine, got %d", w.Code)
	}
}

func TestHandleRunRoutine_Conflicts(t *testing.T) {
	dir := setupRoutinesDir(t)
	srv, database, _ := setupServer(t)

	// Disabled routine refuses to run.
	writeRoutine(t, dir, "sleepy", "x")
	if err := os.WriteFile(filepath.Join(dir, "sleepy", "disabled"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/routines/sleepy/run", nil)
	req.SetPathValue("name", "sleepy")
	w := httptest.NewRecorder()
	srv.handleRunRoutine(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for disabled routine, got %d", w.Code)
	}

	// Already-running routine refuses a second run.
	writeRoutine(t, dir, "busy", "x")
	if _, err := database.CreateRoutineRun("busy"); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("POST", "/api/routines/busy/run", nil)
	req.SetPathValue("name", "busy")
	w = httptest.NewRecorder()
	srv.handleRunRoutine(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for running routine, got %d", w.Code)
	}
}
