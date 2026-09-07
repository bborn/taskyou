package db

import (
	"path/filepath"
	"testing"
)

func TestRemoteInboxSurvivesRestartAndFencesRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	task := &Task{Title: "Remote work"}
	if err = d.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	coordinator, err := d.CoordinatorID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.BeginRemoteRun(task.ID, "build")
	if err != nil {
		t.Fatal(err)
	}
	ev := RemoteEvent{ID: "event-1", TaskID: task.ID, RunID: run, Host: "build", Kind: "done", Detail: "finished"}
	if err = d.SaveRemoteEvent(ev); err != nil {
		t.Fatal(err)
	}
	ev.Detail = "duplicate must not replace original"
	if err = d.SaveRemoteEvent(ev); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	gotID, _ := d.CoordinatorID()
	if gotID != coordinator {
		t.Fatal("coordinator changed")
	}
	got, ok, err := d.RemoteSignal(task.ID, "build")
	if err != nil || !ok || got.Detail != "finished" {
		t.Fatalf("lost durable event: %+v %v %v", got, ok, err)
	}
	if _, err = d.BeginRemoteRun(task.ID, "build"); err != nil {
		t.Fatal(err)
	}
	ev.ID = "late-old-run"
	if err = d.SaveRemoteEvent(ev); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = d.RemoteSignal(task.ID, "build"); err != nil || ok {
		t.Fatalf("old event crossed retry: %v %v", ok, err)
	}
}

func TestRemoteCoordinatorsAndHostsAreIsolated(t *testing.T) {
	a, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	aid, _ := a.CoordinatorID()
	bid, _ := b.CoordinatorID()
	if aid == bid {
		t.Fatal("shared coordinator namespace")
	}
	task := &Task{Title: "Isolated event"}
	if err := a.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	run, _ := a.BeginRemoteRun(task.ID, "host-a")
	if err := a.SaveRemoteEvent(RemoteEvent{ID: "foreign", TaskID: task.ID, RunID: run, Host: "host-b", Kind: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := a.RemoteSignal(task.ID, "host-a"); ok {
		t.Fatal("accepted another host's event")
	}
	if err := a.SaveRemoteEvent(RemoteEvent{ID: "valid", TaskID: task.ID, RunID: run, Host: "host-a", Kind: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := a.CommitTaskPlacement(task.ID, "host-b", "moved", "/srv/app", "task/carried"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := a.RemoteSignal(task.ID, "host-a"); ok {
		t.Fatal("outgoing run survived move")
	}
	got, _ := a.GetTask(task.ID)
	if got.SourceBranch != "task/carried" || got.PlacementTarget != "host-b" {
		t.Fatalf("incomplete placement: %+v", got)
	}
}
