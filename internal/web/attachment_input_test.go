package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestAttachmentInputStagesBeforeSending(t *testing.T) {
	srv, database, runner := setupServer(t)
	work := t.TempDir()
	task := &db.Task{Title: "files", Status: db.StatusBlocked, Project: "personal", WorktreePath: work}
	if err := database.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE tasks SET worktree_path=? WHERE id=?", work, task.ID); err != nil {
		t.Fatal(err)
	}
	tagPaneInFakeTmux(runner, task.ID, "%7")
	a, err := database.AddAttachment(task.ID, "photo.txt", "", []byte("image bytes"))
	if err != nil {
		t.Fatal(err)
	}
	w := postInput(t, srv, task.ID, fmt.Sprintf(`{"attachment_ids":[%d]}`, a.ID))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	calls := runner.waitForPrompts(t, 3)
	prompt := calls[0][len(calls[0])-1]
	if !strings.Contains(prompt, "photo.txt") || !strings.Contains(prompt, "Attached files") {
		t.Fatal(prompt)
	}
	found := false
	filepath.WalkDir(work, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "photo.txt" {
			data, e := os.ReadFile(path)
			if e != nil || string(data) != "image bytes" {
				t.Fatal(e, string(data))
			}
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("file missing from execution directory")
	}
	before := len(runner.calls)
	w = postInput(t, srv, task.ID, `{"message":"must not send","attachment_ids":[99999]}`)
	if w.Code == http.StatusOK {
		t.Fatal("missing file accepted")
	}
	if len(runner.calls) != before {
		t.Fatal("sent despite missing attachment")
	}
}
