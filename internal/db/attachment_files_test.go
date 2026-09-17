package db

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAttachmentFilesAndLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	task := &Task{Title: "files", Status: StatusBacklog}
	if err = database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("photo"), 10000)
	a, err := database.AddAttachment(task.ID, "photo.png", "image/png", payload)
	if err != nil {
		t.Fatal(err)
	}
	var size int
	if err = database.QueryRow("SELECT length(data) FROM task_attachments WHERE id=?", a.ID).Scan(&size); err != nil || size != 0 {
		t.Fatalf("blob size=%d err=%v", size, err)
	}
	if _, err = database.Exec(`INSERT INTO task_attachments(task_id,filename,size,data) VALUES(?,?,?,?)`, task.ID, "legacy", len(payload), payload); err != nil {
		t.Fatal(err)
	}
	database.Close()
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	all, err := database.ListAttachmentsWithData(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatal(len(all))
	}
	for _, a := range all {
		if !bytes.Equal(a.Data, payload) {
			t.Fatal("bytes changed")
		}
	}
	entries, err := os.ReadDir(database.AttachmentDirectory())
	if err != nil || len(entries) != 1 {
		t.Fatalf("dedup: %v %v", entries, err)
	}
	if err = database.DeleteAttachment(a.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := database.GetAttachment(all[1].ID)
	if err != nil || !bytes.Equal(remaining.Data, payload) {
		t.Fatal("shared file lost", err)
	}
	if err = os.WriteFile(filepath.Join(database.AttachmentDirectory(), entries[0].Name()), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = database.GetAttachment(all[1].ID); err == nil {
		t.Fatal("corruption accepted")
	}
}
