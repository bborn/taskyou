package ui

import (
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

func widthTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func widthModel(database *db.DB, taskID int64) *DetailModel {
	return &DetailModel{task: &db.Task{ID: taskID}, database: database}
}

// The shell split used to live in one global setting, so widening the shell in
// a task that needed a lot of terminal narrowed the agent in every other task.
// A saved width must reach its own task and no other.
func TestShellPaneWidthIsPerTask(t *testing.T) {
	database := widthTestDB(t)
	wide, narrow := widthModel(database, 7), widthModel(database, 8)

	wide.setShellPaneWidth(70)
	narrow.setShellPaneWidth(25)

	if got := wide.getShellPaneWidth(); got != "70%" {
		t.Errorf("task 7 width = %q, want 70%%", got)
	}
	if got := narrow.getShellPaneWidth(); got != "25%" {
		t.Errorf("task 8 width = %q, want 25%%", got)
	}
	// Neither resize may leak into the shared default, which is what other
	// tasks fall back to.
	if got, _ := database.GetSetting(config.SettingShellPaneWidth); got != "" {
		t.Errorf("global width = %q, want it untouched", got)
	}
	if got := widthModel(database, 9).getShellPaneWidth(); got != defaultShellPaneWidth {
		t.Errorf("unresized task width = %q, want %s", got, defaultShellPaneWidth)
	}
}

// A task that has never been resized still opens at the width the user had
// before widths became per-task.
func TestShellPaneWidthFallsBackToGlobalSetting(t *testing.T) {
	database := widthTestDB(t)
	if err := database.SetSetting(config.SettingShellPaneWidth, "35%"); err != nil {
		t.Fatal(err)
	}
	m := widthModel(database, 7)
	if got := m.getShellPaneWidth(); got != "35%" {
		t.Errorf("width = %q, want the global 35%%", got)
	}

	m.setShellPaneWidth(60)
	if got := m.getShellPaneWidth(); got != "60%" {
		t.Errorf("width after resize = %q, want 60%%", got)
	}
	if got := widthModel(database, 8).getShellPaneWidth(); got != "35%" {
		t.Errorf("other task width = %q, want the global 35%%", got)
	}
}

func TestShellPaneWidthIgnoresUnusableValues(t *testing.T) {
	for _, tc := range []struct{ name, stored string }{
		{"no percent sign", "40"},
		{"not a number", "half%"},
		{"too narrow", "5%"},
		{"too wide", "95%"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := widthTestDB(t)
			if err := database.SetSetting(config.ShellPaneWidthKey(7), tc.stored); err != nil {
				t.Fatal(err)
			}
			if got := widthModel(database, 7).getShellPaneWidth(); got != defaultShellPaneWidth {
				t.Errorf("width = %q, want %s", got, defaultShellPaneWidth)
			}
		})
	}
}

// Without a task there is nothing to key a width to, and nothing to save.
func TestShellPaneWidthWithoutTaskUsesGlobalDefault(t *testing.T) {
	database := widthTestDB(t)
	if err := database.SetSetting(config.SettingShellPaneWidth, "30%"); err != nil {
		t.Fatal(err)
	}
	m := &DetailModel{database: database}
	if got := m.getShellPaneWidth(); got != "30%" {
		t.Errorf("width = %q, want 30%%", got)
	}
	m.setShellPaneWidth(70)
	if got, _ := database.GetSetting(config.SettingShellPaneWidth); got != "30%" {
		t.Errorf("global width = %q, want it untouched", got)
	}
}

func TestShellPaneWidthWithoutDatabase(t *testing.T) {
	m := &DetailModel{task: &db.Task{ID: 7}}
	if got := m.getShellPaneWidth(); got != defaultShellPaneWidth {
		t.Errorf("width = %q, want %s", got, defaultShellPaneWidth)
	}
	m.setShellPaneWidth(70) // must not panic
}
