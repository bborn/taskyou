package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

type placementFinishedMsg struct {
	result executor.PlacementResult
	err    error
}

func (m *AppModel) showPlacement(task *db.Task) (tea.Model, tea.Cmd) {
	p, err := m.db.GetTaskPlacementDecision(task.ID)
	if err != nil {
		return m, nil
	}
	health, _ := m.db.RemoteHostHealth(p.Target)
	m.placementTarget = p.Target
	if m.placementTarget == "" {
		m.placementTarget = "local"
	}
	m.placementDir = p.WorkDir
	m.placementTaskID = task.ID
	m.placementBusy = false
	m.placementMessage = ""
	description := fmt.Sprintf("%s\nHost: %s · %s\n%s", task.Title, m.placementTarget, health.State, p.Reason)
	if health.LastSeen != "" {
		description += "\nLast seen: " + health.LastSeen
	}
	m.placementForm = huh.NewForm(huh.NewGroup(
		huh.NewNote().Title("Task placement").Description(description),
		huh.NewInput().Title("SSH destination or local").Value(&m.placementTarget).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("enter a destination")
			}
			return nil
		}),
		huh.NewInput().Title("Remote checkout directory").Description("Required for a remote host. Leave empty for local.").Value(&m.placementDir),
		huh.NewNote().Description("Moving carries code through Git and asks for a handoff. Git-ignored files stay on the source machine. The next run starts at the destination."),
	)).WithTheme(huh.ThemeDracula()).WithWidth(min(68, m.width-12))
	m.previousView = m.currentView
	m.currentView = ViewPlacement
	return m, m.placementForm.Init()
}

func (m *AppModel) updatePlacement(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" && !m.placementBusy {
		m.currentView = m.previousView
		m.placementForm = nil
		return m, nil
	}
	if m.placementBusy || m.placementMessage != "" {
		return m, nil
	}
	f, cmd := m.placementForm.Update(msg)
	if form, ok := f.(*huh.Form); ok {
		m.placementForm = form
	}
	if m.placementForm.State == huh.StateCompleted {
		m.placementBusy = true
		id, target, dir := m.placementTaskID, m.placementTarget, m.placementDir
		return m, func() tea.Msg {
			result, err := executor.PlaceTask(context.Background(), m.db, id, target, dir, false)
			return placementFinishedMsg{result, err}
		}
	}
	return m, cmd
}

func (m *AppModel) viewPlacement() string {
	content := ""
	if m.placementForm != nil {
		content = m.placementForm.View()
	}
	if m.placementBusy {
		content = "Moving the task…\nChecking the destination and carrying its work."
	}
	if m.placementMessage != "" {
		content = m.placementMessage + "\n\nEsc to return"
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorSecondary).Padding(1, 2).Width(min(72, m.width-8)).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
