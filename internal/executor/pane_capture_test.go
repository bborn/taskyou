package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionalPaneCaptureDoesNotRetryMissingPane(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "calls")
	if err := os.WriteFile(filepath.Join(root, "tmux"), []byte("#!/bin/sh\nprintf 'called\\n' >> '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := CapturePaneContentContext(context.Background(), "missing-session:task-1", 15); got != "" {
		t.Fatalf("missing pane returned %q", got)
	}
	calls, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "called") != 1 {
		t.Fatalf("optional capture retried: %s", calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	CapturePaneContentContext(ctx, "missing-session:task-1", 15)
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(calls) {
		t.Fatal("expired budget launched another capture")
	}
}
