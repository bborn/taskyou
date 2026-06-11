package ui

import (
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
// "Set up a project" vs "Just start a task" (in the personal project).
type WelcomeModel struct {
	cursor int // 0 = setup, 1 = start task
	width  int
	height int
}

func NewWelcomeModel(width, height int) *WelcomeModel {
	return &WelcomeModel{width: width, height: height}
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

	content := lipgloss.JoinVertical(lipgloss.Center, title, "", body, "", buttons, "", help)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary).Padding(1, 3).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
