package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPaneLayoutUsesOneConsistentWindowSnapshot(t *testing.T) {
	root := t.TempDir()
	stub := "#!/bin/sh\nprintf '%s\\n' '%ui 160 17 50' '%executor 59 32 50' '%shell 40 32 50'\n"
	if err := os.WriteFile(filepath.Join(root, "tmux"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	detail := &DetailModel{tuiPaneID: "%ui", claudePaneID: "%executor", workdirPaneID: "%shell"}
	height, width := detail.readPaneLayout(context.Background())
	if height != 34 || width != 40 {
		t.Fatalf("wrong rounded layout: height=%d shell=%d", height, width)
	}
	detail.workdirPaneID = "%missing"
	height, width = detail.readPaneLayout(context.Background())
	if height != 34 || width != 0 {
		t.Fatal("missing shell produced a saved width")
	}
}
