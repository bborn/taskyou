package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bindPaneNavigation runs on every pane setup. Its bind-key body chains two tmux
// commands, and the separator has to survive tmux's own argv parsing: a bare ";"
// ends the tmux command line, so tmux binds only the first half and executes the
// second immediately. That typed C-Up / C-Down into pane 0 — the TUI — every time
// a task was opened, and the detail view reads those as prev/next task, so
// opening a task navigated to a different one.
func TestBindPaneNavigationEscapesCommandSeparator(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := &DetailModel{tuiPaneID: "%7"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.bindPaneNavigation(ctx)

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var forwarding int
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "bind-key") || !strings.Contains(line, "send-keys") {
			continue
		}
		forwarding++
		if !strings.Contains(line, `\;`) {
			t.Fatalf("bind-key chains commands with an unescaped separator; tmux will run the send-keys at bind time instead of binding it: %q", line)
		}
	}
	if forwarding != 2 {
		t.Fatalf("expected the two key-forwarding bindings, saw %d", forwarding)
	}
}

// The forwarding bindings have to name the TUI's pane. ":.0" is not a synonym
// for "the first pane": with pane-base-index 1 there is no pane 0 and tmux
// answers "can't find pane: 0", so Alt+Shift+Up/Down silently did nothing for
// everyone who sets it — and ty run inside the user's own tmux need not be the
// window's first pane either.
func TestBindPaneNavigationForwardsToTheTUIPaneByID(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := &DetailModel{tuiPaneID: "%7"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.bindPaneNavigation(ctx)

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "bind-key") || !strings.Contains(line, "send-keys") {
			continue
		}
		if strings.Contains(line, ":.0") {
			t.Errorf("forwarding binding targets pane index 0, which need not exist: %q", line)
		}
		if !strings.Contains(line, "%7") {
			t.Errorf("forwarding binding does not name the TUI pane: %q", line)
		}
	}
}

// With no pane of its own to name, ty must not guess: a guessed target types
// prev/next-task keys into whatever pane answers.
func TestBindPaneNavigationSkipsForwardingWithoutAPane(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_PANE", "")

	m := &DetailModel{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.bindPaneNavigation(ctx)

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "send-keys") {
		t.Errorf("bound a forwarding key with no pane to forward to:\n%s", data)
	}
	if !strings.Contains(string(data), "S-Down") {
		t.Errorf("the Shift+arrow bindings should still be installed:\n%s", data)
	}
}
