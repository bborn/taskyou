package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An instance pointed at its own database must log beside that database. One
// shared ui.log made an isolated QA run's errors indistinguishable from the live
// TUI's, which sent a real debugging session down the wrong path for hours.
func TestLogPathFollowsTheDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WORKTREE_DB_PATH", filepath.Join(dir, "tasks.db"))

	got := LogPath()
	if want := filepath.Join(dir, "ui.log"); got != want {
		t.Fatalf("LogPath() = %q, want %q", got, want)
	}
}

// Several ty processes append to one file concurrently, so a line has to say
// which one wrote it.
func TestLogLinesCarryThePid(t *testing.T) {
	dir := t.TempDir()
	l := &UILogger{}
	f, err := os.CreateTemp(dir, "ui-*.log")
	if err != nil {
		t.Fatal(err)
	}
	l.file, l.path, l.pid = f, f.Name(), 4242
	l.Info("joining pane %s", "%7")

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[4242]") {
		t.Fatalf("log line does not identify the writing process: %q", data)
	}
	if !strings.Contains(string(data), "joining pane %7") {
		t.Fatalf("log line lost its message: %q", data)
	}
}

// Unrotated, this file reached 205MB and eight months of history.
func TestLogRotatesOnceItGrowsPastTheCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui.log")
	if err := os.WriteFile(path, make([]byte, maxLogBytes+1), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	l := &UILogger{file: f, path: path, pid: 1}

	for i := 0; i < logSyncEvery; i++ {
		l.Info("line %d", i)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("previous generation not kept: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= maxLogBytes {
		t.Fatalf("log did not start fresh after rotating: %d bytes", info.Size())
	}
}
