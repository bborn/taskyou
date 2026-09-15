package db

import (
	"path/filepath"
	"testing"
)

func TestTaskSettingKeyNamespacesByTask(t *testing.T) {
	if got, want := TaskSettingKey("shell_pane_width", 42), "shell_pane_width:42"; got != want {
		t.Errorf("TaskSettingKey = %q, want %q", got, want)
	}
	if TaskSettingKey("shell_pane_width", 1) == TaskSettingKey("shell_pane_width", 2) {
		t.Error("two tasks share a settings key")
	}
}

// Task IDs are reused after a delete, so a per-task setting left behind would
// be inherited by an unrelated task.
func TestDeleteTaskClearsPerTaskSettings(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	task := &Task{Title: "Per-task width", Status: StatusBacklog, Type: TypeCode}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	key := TaskSettingKey("shell_pane_width", task.ID)
	if err := database.SetSetting(key, "70%"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting("shell_pane_width", "40%"); err != nil {
		t.Fatal(err)
	}

	if err := database.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}

	if got, _ := database.GetSetting(key); got != "" {
		t.Errorf("%s = %q after delete, want it removed", key, got)
	}
	if got, _ := database.GetSetting("shell_pane_width"); got != "40%" {
		t.Errorf("global width = %q, want it kept", got)
	}
}
