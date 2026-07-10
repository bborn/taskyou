package ui

import (
	"os/exec"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// welcomeChoice is what the user picked on the first-run Welcome fork.
type welcomeChoice int

const (
	welcomeNone welcomeChoice = iota
	welcomeSetupProject
	welcomeStartTask
)

// WelcomeModel is the first-run fork shown when there's no project to suggest:
// "Set up a project" vs "Just start a task" (in the personal project). It also
// surfaces a machine-readiness status: detected agent CLIs and any missing
// prerequisites (non-blocking — detection does the work, nothing gates the fork).
type WelcomeModel struct {
	cursor         int // 0 = setup, 1 = start task
	width          int
	height         int
	detectedAgents []string // executor CLIs found on this machine (e.g. claude, codex)
	tmuxFound      bool     // tmux binary present on PATH
}

func NewWelcomeModel(width, height int, detectedAgents []string, tmuxFound bool) *WelcomeModel {
	return &WelcomeModel{width: width, height: height, detectedAgents: detectedAgents, tmuxFound: tmuxFound}
}

// tmuxAvailable reports whether the tmux binary is on PATH. TaskYou runs
// agents inside tmux, so its absence is worth a heads-up on the Welcome view.
func tmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// formatDetectedAgents renders the confidence beat shown when executor CLIs
// were found ("Detected agents: claude, codex"). Empty when none were detected.
func formatDetectedAgents(agents []string) string {
	if len(agents) == 0 {
		return ""
	}
	return "Detected agents: " + strings.Join(agents, ", ")
}

// missingPrereqNotices returns human-readable notices for missing machine
// prerequisites (tmux + at least one executor CLI), each with an exact install
// hint. Mirrors the desktop app's SetupCheck, but stays non-blocking in the TUI.
func missingPrereqNotices(tmuxFound bool, agents []string) []string {
	var notices []string
	if !tmuxFound {
		notices = append(notices, "tmux not found — brew install tmux")
	}
	if len(agents) == 0 {
		notices = append(notices, "no coding agent found — npm install -g @anthropic-ai/claude-code")
	}
	return notices
}

// welcomeChoiceHint returns a one-line description of the currently highlighted
// choice so a first-timer knows what each button does before pressing enter
// (the labels alone don't say "picks a folder" vs "no setup needed").
func welcomeChoiceHint(cursor int) string {
	if cursor == 0 {
		return "Point TaskYou at a folder — tasks run against that codebase"
	}
	return "Start a task now in your personal space — no project setup"
}

// MoveLeft/MoveRight/Choice drive selection; key handling lives in app.go so it
// composes with the global update loop (mirrors viewProjectDetectConfirm).
func (m *WelcomeModel) MoveLeft()  { m.cursor = 0 }
func (m *WelcomeModel) MoveRight() { m.cursor = 1 }
func (m *WelcomeModel) Choice() welcomeChoice {
	if m.cursor == 0 {
		return welcomeSetupProject
	}
	return welcomeStartTask
}
func (m *WelcomeModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m *WelcomeModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Render("Welcome to TaskYou 👋")
	body := "How do you want to start?"

	btn := func(label string, active bool) string {
		s := lipgloss.NewStyle().Padding(0, 3).Margin(0, 1).Border(lipgloss.RoundedBorder())
		if active {
			s = s.BorderForeground(ColorPrimary).Bold(true)
		} else {
			s = s.BorderForeground(lipgloss.Color("240"))
		}
		return s.Render(label)
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Top,
		btn("Set up a project", m.cursor == 0),
		btn("Just start a task", m.cursor == 1),
	)
	help := HelpBar.Render(
		HelpKey.Render("←/→") + " " + HelpDesc.Render("choose") + "  " +
			HelpKey.Render("enter") + " " + HelpDesc.Render("select"))

	hint := lipgloss.NewStyle().Foreground(ColorMuted).Italic(true).Render(welcomeChoiceHint(m.cursor))
	parts := []string{title, "", body, "", buttons, "", hint}
	if agents := formatDetectedAgents(m.detectedAgents); agents != "" {
		parts = append(parts, "", Success.Render(agents))
	}
	for i, notice := range missingPrereqNotices(m.tmuxFound, m.detectedAgents) {
		if i == 0 {
			parts = append(parts, "")
		}
		parts = append(parts, Warning.Bold(true).Render(Icon(IconWarningUnicode, IconWarningASCII)+" "+notice))
	}
	parts = append(parts, "", help)
	content := lipgloss.JoinVertical(lipgloss.Center, parts...)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary).Padding(1, 3).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
