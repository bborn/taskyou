package db

import (
	"testing"
)

func TestListTasksOpenPROnly(t *testing.T) {
	database := setupTestDB(t)
	if err := database.CreateProject(&Project{Name: "test", Path: "/tmp/test"}); err != nil {
		t.Fatal(err)
	}

	mk := func(title, prJSON string) *Task {
		t.Helper()
		task := &Task{Title: title, Type: "task", Project: "test"}
		if err := database.CreateTask(task); err != nil {
			t.Fatal(err)
		}
		// A done task has, by definition, run first. Going through processing is
		// what gives it a started_at, which the completion gate requires.
		if err := database.SetTaskStatus(task.ID, StatusProcessing, ActorDaemon,
			"test fixture: the task ran", NoEvidence); err != nil {
			t.Fatal(err)
		}
		// Only a human closes a task, so the fixture closes it as one.
		if err := database.SetTaskStatus(task.ID, StatusDone, ActorCLI,
			"test fixture: the task was closed",
			ByHuman("ran `ty close %d`", task.ID)); err != nil {
			t.Fatal(err)
		}
		if prJSON != "" {
			if err := database.UpdateTaskPRInfo(task.ID, "", 1, prJSON); err != nil {
				t.Fatal(err)
			}
		}
		return task
	}
	open := mk("open", `{"number":1,"state":"OPEN"}`)
	draft := mk("draft", `{"number":2,"state":"DRAFT"}`)
	mk("merged", `{"number":3,"state":"MERGED"}`)
	mk("closed", `{"number":4,"state":"CLOSED"}`)
	mk("no pr", "")

	got, err := database.ListTasks(ListTasksOptions{Status: StatusDone, OpenPROnly: true, Limit: -1})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, task := range got {
		ids[task.ID] = true
	}
	if len(got) != 2 || !ids[open.ID] || !ids[draft.ID] {
		t.Fatalf("OpenPROnly listed %d tasks %v, want only the open and draft PRs", len(got), ids)
	}
}
