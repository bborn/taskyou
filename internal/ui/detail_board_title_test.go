package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
	tea "github.com/charmbracelet/bubbletea"
)

func TestLeavingDetailResetsBoardTitleWithoutLocalPanes(t *testing.T) {
	for _, exit := range []string{"escape", "cleanup", "without-saving"} {
		for _, remote := range []bool{false, true} {
			t.Run(exit+map[bool]string{false: "/no-panes", true: "/remote"}[remote], func(t *testing.T) {
				app, marker := refreshTestModel(t)
				t.Setenv("TMUX", "test")
				t.Setenv("TMUX_PANE", "%42")
				if err := os.WriteFile(filepath.Join(filepath.Dir(marker), "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TITLE_TEST_LOG\"\n"), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TITLE_TEST_LOG", marker)
				detail := &DetailModel{task: &db.Task{ID: 5278, Title: "Previous task"}, uiSessionName: "test-ui"}
				if remote {
					detail.remotePaneID = "%43"
				}
				switch exit {
				case "escape":
					app.detailView, app.currentView = detail, ViewDetail
					_, cmd := app.updateDetail(tea.KeyMsg{Type: tea.KeyEsc})
					if app.currentView != ViewDashboard {
						t.Fatal("did not return to board")
					}
					cmd()
				case "cleanup":
					detail.Cleanup()
				case "without-saving":
					detail.CleanupWithoutSaving()
				}
				data, _ := os.ReadFile(marker)
				calls := string(data)
				if !strings.Contains(calls, "select-pane -t %42 -T Tasks") {
					t.Errorf("board title was not restored on the TUI pane: %s", calls)
				}
				if !strings.Contains(calls, "pane-border-indicators off") {
					t.Errorf("detail border styling remains: %s", calls)
				}
			})
		}
	}
}
