package ui

import (
	"strings"
	"testing"
)

func TestFormatDetectedAgents(t *testing.T) {
	if got := formatDetectedAgents(nil); got != "" {
		t.Errorf("no agents should render nothing, got %q", got)
	}
	if got := formatDetectedAgents([]string{"claude"}); got != "Detected agents: claude" {
		t.Errorf("single agent: got %q", got)
	}
	if got := formatDetectedAgents([]string{"claude", "codex"}); got != "Detected agents: claude, codex" {
		t.Errorf("multiple agents: got %q", got)
	}
}

func TestMissingPrereqNotices(t *testing.T) {
	// Everything present → no notices.
	if got := missingPrereqNotices(true, []string{"claude"}); len(got) != 0 {
		t.Errorf("expected no notices when prerequisites are met, got %v", got)
	}

	// tmux missing → exact install hint.
	got := missingPrereqNotices(false, []string{"claude"})
	if len(got) != 1 || got[0] != "tmux not found — brew install tmux" {
		t.Errorf("tmux notice wrong: %v", got)
	}

	// No executor CLI → install hint mirroring the desktop SetupCheck.
	got = missingPrereqNotices(true, nil)
	if len(got) != 1 {
		t.Fatalf("want 1 executor notice, got %v", got)
	}
	if !strings.Contains(got[0], "no coding agent found") || !strings.Contains(got[0], "npm install -g @anthropic-ai/claude-code") {
		t.Errorf("executor notice wrong: %q", got[0])
	}

	// Both missing → both notices, tmux first.
	got = missingPrereqNotices(false, nil)
	if len(got) != 2 || !strings.Contains(got[0], "tmux") {
		t.Errorf("want tmux notice first of 2, got %v", got)
	}
}

func TestWelcomeViewShowsEnvironmentStatus(t *testing.T) {
	// Agents detected, tmux missing: confidence beat + visible, non-blocking notice.
	m := NewWelcomeModel(100, 40, []string{"claude", "codex"}, false)
	view := m.View()
	if !strings.Contains(view, "Detected agents: claude, codex") {
		t.Error("welcome view missing detected-agents line")
	}
	if !strings.Contains(view, "brew install tmux") {
		t.Error("welcome view missing tmux install hint")
	}
	// Still non-blocking: the fork choices remain on screen.
	if !strings.Contains(view, "Set up a project") || !strings.Contains(view, "Just start a task") {
		t.Error("welcome fork choices must remain visible alongside notices")
	}

	// All prerequisites met: no notices, just the confidence beat.
	m = NewWelcomeModel(100, 40, []string{"claude"}, true)
	view = m.View()
	if strings.Contains(view, "not found") {
		t.Error("no notices expected when prerequisites are met")
	}
	if !strings.Contains(view, "Detected agents: claude") {
		t.Error("welcome view missing detected-agents line")
	}
}
