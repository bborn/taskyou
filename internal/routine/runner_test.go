package routine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// writeStub writes an executable script used in place of the claude binary.
func writeStub(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-stub")
	if err := os.WriteFile(path, []byte("#!/bin/bash\n"+script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func setupRunnerTest(t *testing.T) (*db.DB, string) {
	t.Helper()
	dir := setupRoutinesDir(t)

	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// db.Open seeds a default "personal" project, which CreateTask validates
	// against — nothing extra to set up for the alert path.
	return database, dir
}

func TestRunSuccess(t *testing.T) {
	database, dir := setupRunnerTest(t)
	writeRoutine(t, dir, "scout", "---\nmodel: sonnet\n---\nfind things")
	stub := writeStub(t, `echo "model=$3"; cat; echo "cwd=$PWD"; echo "state=$ROUTINE_STATE_DIR"`)

	rt, err := Load("scout")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	runner := &Runner{DB: database, ClaudeBin: stub}
	result, err := runner.Run(context.Background(), rt)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Status != db.RoutineRunStatusOK {
		t.Errorf("expected ok, got %q", result.Status)
	}

	// The stub receives the model flag, the prompt on stdin, and runs in the
	// state dir with ROUTINE_STATE_DIR exported.
	if !strings.Contains(result.Output, "model=sonnet") {
		t.Errorf("model flag not passed: %q", result.Output)
	}
	if !strings.Contains(result.Output, "find things") {
		t.Errorf("prompt not on stdin: %q", result.Output)
	}
	stateDir := StateDir("scout")
	// $PWD resolves symlinks (e.g. /var -> /private/var on macOS), so compare
	// the resolved path for cwd and the literal path for the env var.
	resolved, err := filepath.EvalSymlinks(stateDir)
	if err != nil {
		t.Fatalf("resolve state dir: %v", err)
	}
	if !strings.Contains(result.Output, "cwd="+resolved) || !strings.Contains(result.Output, "state="+stateDir) {
		t.Errorf("state dir not wired: %q", result.Output)
	}

	// Run is recorded and the log file has the output.
	runs, err := database.ListRoutineRuns("scout", 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != db.RoutineRunStatusOK || runs[0].FinishedAt == nil {
		t.Fatalf("run not recorded correctly: %+v", runs)
	}
	logData, err := os.ReadFile(result.LogPath)
	if err != nil || !strings.Contains(string(logData), "find things") {
		t.Errorf("log file missing output: %v %q", err, logData)
	}
}

func TestRunEnvShSourced(t *testing.T) {
	database, dir := setupRunnerTest(t)
	rtDir := writeRoutine(t, dir, "scout", "prompt")
	if err := os.WriteFile(filepath.Join(rtDir, "env.sh"), []byte("export MY_SECRET=hunter2\n"), 0o644); err != nil {
		t.Fatalf("write env.sh: %v", err)
	}
	stub := writeStub(t, `echo "secret=$MY_SECRET"`)

	rt, _ := Load("scout")
	runner := &Runner{DB: database, ClaudeBin: stub}
	result, err := runner.Run(context.Background(), rt)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(result.Output, "secret=hunter2") {
		t.Errorf("env.sh not sourced: %q", result.Output)
	}
}

func TestRunEnvShFailureFailsFast(t *testing.T) {
	database, dir := setupRunnerTest(t)
	rtDir := writeRoutine(t, dir, "scout", "prompt")
	if err := os.WriteFile(filepath.Join(rtDir, "env.sh"), []byte("echo 'cookies expired' >&2\nexit 3\n"), 0o644); err != nil {
		t.Fatalf("write env.sh: %v", err)
	}
	stub := writeStub(t, `echo "should never run"`)

	rt, _ := Load("scout")
	runner := &Runner{DB: database, ClaudeBin: stub}
	result, err := runner.Run(context.Background(), rt)
	if err == nil {
		t.Fatal("expected run error from failing env.sh")
	}
	if result.Status != db.RoutineRunStatusFailed {
		t.Errorf("expected failed, got %q", result.Status)
	}
	if strings.Contains(result.Output, "should never run") {
		t.Error("agent ran despite env.sh failure")
	}
	if !strings.Contains(result.Output, "cookies expired") {
		t.Errorf("env.sh stderr not captured: %q", result.Output)
	}
}

func TestRunFailureCreatesAlertTaskOnce(t *testing.T) {
	database, dir := setupRunnerTest(t)
	writeRoutine(t, dir, "scout", "prompt")
	stub := writeStub(t, `echo "boom"; exit 1`)

	rt, _ := Load("scout")
	runner := &Runner{DB: database, ClaudeBin: stub}

	if _, err := runner.Run(context.Background(), rt); err == nil {
		t.Fatal("expected run error")
	}

	tasks, err := database.ListTasks(db.ListTasksOptions{Project: "personal"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var alerts []*db.Task
	for _, task := range tasks {
		if strings.Contains(task.Title, "Routine failed: scout") {
			alerts = append(alerts, task)
		}
	}
	if len(alerts) != 1 {
		t.Fatalf("expected exactly 1 alert task, got %d", len(alerts))
	}
	if !alerts[0].Pinned {
		t.Error("alert task should be pinned")
	}
	// CreateTask inserts Status verbatim (the schema default doesn't apply),
	// so the alert must explicitly land in backlog or it's invisible on the board.
	if alerts[0].Status != db.StatusBacklog {
		t.Errorf("alert task should be in backlog, got %q", alerts[0].Status)
	}
	if !strings.Contains(alerts[0].Body, "boom") {
		t.Errorf("alert body missing output tail: %q", alerts[0].Body)
	}

	// A second failure while the alert is open must not create a duplicate.
	if _, err := runner.Run(context.Background(), rt); err == nil {
		t.Fatal("expected run error")
	}
	tasks, _ = database.ListTasks(db.ListTasksOptions{Project: "personal"})
	count := 0
	for _, task := range tasks {
		if strings.Contains(task.Title, "Routine failed: scout") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected alert dedup, got %d alert tasks", count)
	}
}

func TestRunTimeout(t *testing.T) {
	database, dir := setupRunnerTest(t)
	writeRoutine(t, dir, "scout", "---\ntimeout: 300ms\n---\nprompt")
	stub := writeStub(t, `sleep 10; echo done`)

	rt, _ := Load("scout")
	runner := &Runner{DB: database, ClaudeBin: stub}

	start := time.Now()
	result, err := runner.Run(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("timeout did not kill promptly (%s)", time.Since(start))
	}
	if result.Status != db.RoutineRunStatusFailed {
		t.Errorf("expected failed, got %q", result.Status)
	}
}
