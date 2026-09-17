package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestStageAttachmentsLocalAndRemote(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := &db.Task{Title: "files", Status: db.StatusBacklog}
	if err = database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	a, err := database.AddAttachment(task.ID, "same.txt", "", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := database.AddAttachment(task.ID, "same.txt", "", []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	paths, err := StageAttachments(context.Background(), database, task.ID, work, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] == paths[1] {
		t.Fatal(paths)
	}
	for i, p := range paths {
		data, e := os.ReadFile(p)
		if e != nil || string(data) != []string{"one", "two"}[i] {
			t.Fatal(p, e, string(data))
		}
	}
	if _, err = StageAttachments(context.Background(), database, task.ID+1, work, nil, []int64{a.ID}); err == nil {
		t.Fatal("cross-task attachment accepted")
	}
	// The fake SSH transport executes the exact remote command locally in a
	// separate worktree; it exercises streamed bytes and the checksum script.
	ssh := filepath.Join(t.TempDir(), "ssh")
	if err = os.WriteFile(ssh, []byte("#!/bin/sh\nfor arg do last=$arg; done\nexec sh -c \"$last\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	remoteDir := t.TempDir()
	remote := RemoteRunner{Host: "test-host", WorkDir: remoteDir, SSHBin: ssh}
	paths, err = StageAttachments(context.Background(), database, task.ID, remoteDir, &remote, []int64{b.ID})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(remoteDir, paths[0]))
	if err != nil || string(data) != "two" {
		t.Fatal(err, string(data))
	}
	if err = os.WriteFile(ssh, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = StageAttachments(context.Background(), database, task.ID, remoteDir, &remote, []int64{a.ID}); err == nil {
		t.Fatal("transfer error swallowed")
	}
}
