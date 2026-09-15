package panel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func fixture(t *testing.T) (*Service, *db.Task) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.CreateProject(&db.Project{Name: "panel-fixture", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	task := &db.Task{Title: "Review checkout", Status: "backlog", Project: "panel-fixture", WorktreePath: t.TempDir()}
	if err := d.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	return New(d), task
}

func TestTabsRestoreAndDeduplicate(t *testing.T) {
	s, task := fixture(t)
	tabs, err := s.List(task.ID)
	if err != nil || len(tabs) != 1 || tabs[0].ProviderID != "shell" {
		t.Fatalf("defaults: %v %v", tabs, err)
	}
	a, err := s.Open(task.ID, "file", "docs/../README.md")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Open(task.ID, "file", "README.md")
	if err != nil || a.ID != b.ID {
		t.Fatalf("duplicate: %v %v", b, err)
	}
	tabs, err = New(s.DB).List(task.ID)
	if err != nil || len(tabs) != 2 {
		t.Fatalf("restore: %v %v", tabs, err)
	}
	if err := s.Close(task.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	tabs, _ = s.List(task.ID)
	if len(tabs) != 1 {
		t.Fatal(tabs)
	}
	if err := s.Close(task.ID, tabs[0].ID); err != nil {
		t.Fatal(err)
	}
	tabs, _ = s.List(task.ID)
	if len(tabs) != 0 {
		t.Fatalf("closed shell resurrected: %v", tabs)
	}
}

func TestFilesConfinedAndBounded(t *testing.T) {
	s, task := fixture(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(task.WorktreePath, "README.md"), []byte("# Checkout\n"), 0600)
	p, err := s.Open(task.ID, "file", "README.md")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Content(ctx, task.ID, p.ID)
	if err != nil || c.Text != "# Checkout\n" || c.Kind != "markdown" {
		t.Fatalf("preview: %+v %v", c, err)
	}
	for _, path := range []string{"../outside", "/etc/passwd", "../../README.md"} {
		if _, err := s.Open(task.ID, "file", path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	os.WriteFile(outside, []byte("private"), 0600)
	os.Symlink(outside, filepath.Join(task.WorktreePath, "escape"))
	p, _ = s.Open(task.ID, "file", "escape")
	if _, err := s.Content(ctx, task.ID, p.ID); err == nil {
		t.Fatal("symlink escaped root")
	}
	os.WriteFile(filepath.Join(task.WorktreePath, "large.txt"), make([]byte, MaxPreviewBytes+1), 0600)
	p, _ = s.Open(task.ID, "file", "large.txt")
	if _, err := s.Content(ctx, task.ID, p.ID); err == nil {
		t.Fatal("oversized preview accepted")
	}
	os.WriteFile(filepath.Join(task.WorktreePath, "binary"), []byte{0, 1, 2}, 0600)
	p, _ = s.Open(task.ID, "file", "binary")
	if _, err := s.Content(ctx, task.ID, p.ID); err == nil {
		t.Fatal("binary preview accepted")
	}
}

func TestTaskIsolationAndUnknownProvider(t *testing.T) {
	s, task := fixture(t)
	other := &db.Task{Title: "Another task", Status: "backlog"}
	s.DB.CreateTask(other)
	p, _ := s.Open(task.ID, "pr", "")
	if _, err := s.Content(context.Background(), other.ID, p.ID); err == nil {
		t.Fatal("foreign panel accessible")
	}
	if _, err := s.Open(task.ID, "missing", ""); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if _, err := s.List(999999); err == nil {
		t.Fatal("missing task accepted")
	}
}

func TestConcurrentOpenIsOneTab(t *testing.T) {
	s, task := fixture(t)
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func() { _, err := s.Open(task.ID, "file", "README.md"); errors <- err }()
	}
	for i := 0; i < 12; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	tabs, err := s.List(task.ID)
	if err != nil || len(tabs) != 2 {
		t.Fatalf("concurrent opens: %v %v", tabs, err)
	}
}
