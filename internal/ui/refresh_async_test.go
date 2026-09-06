package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func refreshTestModel(t *testing.T) (*AppModel, string) {
	t.Helper()
	root := t.TempDir()
	marker := filepath.Join(root, "tmux-called")
	// A stub proves which side of the command boundary launches a process.
	if err := os.WriteFile(filepath.Join(root, "tmux"), []byte("#!/bin/sh\nprintf called >> '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	database, err := db.Open(filepath.Join(root, "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return &AppModel{db: database, currentView: ViewDashboard, kanban: NewKanbanBoard(100, 40), keys: DefaultKeyMap(), prevStatuses: map[int64]string{}, tasksNeedingInput: map[int64]bool{}, questionPrompts: map[int64]bool{}, executorPrompts: map[int64]string{}, initialPRRefreshDone: true}, marker
}

func TestDashboardRefreshRunsProcessChecksInCommand(t *testing.T) {
	m, marker := refreshTestModel(t)
	task := &db.Task{Title: "Review checkout coverage", Status: db.StatusProcessing}
	if err := m.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := m.db.AppendTaskLog(task.ID, "output", "Checking retry coverage"); err != nil {
		t.Fatal(err)
	}
	cmd := m.loadTasks()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("process check ran while scheduling refresh: %v", err)
	}
	msg := cmd().(tasksLoadedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("first board load waited for process check: %v", err)
	}
	terminalMsg := m.loadBoardTerminals(msg.choicePrompts)().(boardTerminalsMsg)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("terminal enrichment did not run process check: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	// Applying the completed snapshot must not need another database read.
	m.db = nil
	m.Update(msg)
	m.Update(terminalMsg)
	if log := m.kanban.latestActivity[task.ID]; log == nil || log.Content != "Checking retry coverage" {
		t.Fatalf("activity not delivered: %+v", log)
	}
	m.Update(tickMsg(time.Now()))
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("UI update ran a synchronous process check: %v", err)
	}
}

func TestTaskRefreshCoalescesRequestsAndRecoversAfterError(t *testing.T) {
	m, _ := refreshTestModel(t)
	if m.loadTasks() == nil {
		t.Fatal("first load was not scheduled")
	}
	for i := 0; i < 20; i++ {
		if m.loadTasks() != nil {
			t.Fatal("overlapping load scheduled")
		}
	}
	_, cmd := m.Update(tasksLoadedMsg{})
	if cmd == nil || !m.tasksLoadInFlight || m.tasksLoadPending {
		t.Fatal("pending requests did not become one follow-up")
	}
	// A failed follow-up must release the slot, allowing the next refresh.
	m.Update(tasksLoadedMsg{err: os.ErrPermission})
	if m.tasksLoadInFlight || m.tasksLoadPending {
		t.Fatal("failed load left refresh stuck")
	}
	if m.loadTasks() == nil {
		t.Fatal("refresh could not retry after error")
	}
}

func TestDashboardRefreshAppliesPromptSnapshot(t *testing.T) {
	m, marker := refreshTestModel(t)
	task := &db.Task{Title: "Choose a checkout retry policy", Status: db.StatusBlocked}
	if err := m.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := m.db.AppendTaskLog(task.ID, "question", "How many retries should checkout allow?"); err != nil {
		t.Fatal(err)
	}
	msg := m.loadTasks()().(tasksLoadedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("prompt snapshot waited for terminal capture: %v", err)
	}
	m.db = nil
	m.Update(msg)
	if !m.tasksNeedingInput[task.ID] || !m.questionPrompts[task.ID] || m.executorPrompts[task.ID] != "How many retries should checkout allow?" {
		t.Fatal("pending question was not applied from the background snapshot")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("applying a prompt captured a pane synchronously: %v", err)
	}
}

func TestStartupShowsBoardBeforeTerminalEnrichment(t *testing.T) {
	m, marker := refreshTestModel(t)
	m.loading = true
	for i := 0; i < 80; i++ {
		task := &db.Task{Title: "Pending startup question", Status: db.StatusBlocked}
		if err := m.db.CreateTask(task); err != nil {
			t.Fatal(err)
		}
		if err := m.db.AppendTaskLog(task.ID, "question", "Choose a policy?"); err != nil {
			t.Fatal(err)
		}
	}
	msg := m.loadTasks()().(tasksLoadedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	m.Update(msg)
	if m.loading || len(m.tasks) != 80 || !m.terminalLoadInFlight {
		t.Fatal("board did not become usable before terminal enrichment")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("startup waited for terminal processes: %v", err)
	}
	// A late terminal result must not resurrect a question resolved meanwhile.
	id := m.tasks[0].ID
	delete(m.tasksNeedingInput, id)
	delete(m.executorPrompts, id)
	m.Update(boardTerminalsMsg{prompts: map[int64]taskChoicePrompt{id: {text: "Choose a policy?", paneContent: "stale options"}}})
	if m.executorPrompts[id] != "" || m.terminalLoadInFlight {
		t.Fatal("late enrichment resurrected resolved prompt or kept its slot")
	}
}

func TestTerminalEnrichmentKeepsCurrentPrompt(t *testing.T) {
	m, _ := refreshTestModel(t)
	m.tasksNeedingInput[1] = true
	m.executorPrompts[1] = "Current question"
	m.Update(boardTerminalsMsg{prompts: map[int64]taskChoicePrompt{1: {text: "Old question", paneContent: "Old options"}}})
	if m.executorPrompts[1] != "Current question" {
		t.Fatal("old capture replaced current question")
	}
	m.Update(boardTerminalsMsg{prompts: map[int64]taskChoicePrompt{1: {text: "Current question", paneContent: "Current options"}}})
	if m.executorPrompts[1] != "Current options" {
		t.Fatal("current capture was not applied")
	}
}
