package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// TestUpdateDetailPinnedJump drives the real detail-view key handler: with the
// pinned quick-nav bar populated, pressing "]" must start a task transition to
// the next pinned task (reusing the same guard as prev/next navigation), and a
// digit must jump directly to that pill.
func TestUpdateDetailPinnedJump(t *testing.T) {
	t.Setenv("TMUX", "") // keep DetailModel tmux-free

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	exec := executor.New(database, &config.Config{})

	current := &db.Task{Title: "current", Status: db.StatusProcessing, Pinned: true}
	other := &db.Task{Title: "other", Status: db.StatusProcessing, Pinned: true}
	for _, tk := range []*db.Task{current, other} {
		if err := database.CreateTask(tk); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}

	detail, _ := NewDetailModel(current, database, exec, 120, 40, false)
	detail.SetPinnedNav([]PinnedNavItem{
		{ID: current.ID, Title: "current"},
		{ID: other.ID, Title: "other"},
	}, current.ID)

	kanban := NewKanbanBoard(120, 40)
	kanban.SetTasks([]*db.Task{current, other})

	m := &AppModel{
		width:        120,
		height:       40,
		currentView:  ViewDetail,
		db:           database,
		executor:     exec,
		keys:         DefaultKeyMap(),
		kanban:       kanban,
		detailView:   detail,
		selectedTask: current,
		lastViewedAt: map[int64]time.Time{},
	}

	// Press "]" -> transition to next pinned task should begin.
	model, cmd := m.updateDetail(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	am := model.(*AppModel)
	if !am.taskTransitionInProgress {
		t.Fatal(`pressing "]" should begin a task transition to the next pinned task`)
	}
	if cmd == nil {
		t.Fatal(`pressing "]" should return a loadTask command`)
	}

	// Reset and try a direct digit jump to pill 2 (the "other" task).
	am.taskTransitionInProgress = false
	am.detailView, _ = NewDetailModel(current, database, exec, 120, 40, false)
	am.detailView.SetPinnedNav([]PinnedNavItem{
		{ID: current.ID, Title: "current"},
		{ID: other.ID, Title: "other"},
	}, current.ID)

	_, cmd = am.updateDetail(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if cmd == nil || !am.taskTransitionInProgress {
		t.Fatal(`pressing "2" should jump directly to the second pinned task`)
	}
}
