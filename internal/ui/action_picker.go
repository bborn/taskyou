package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/hooks"
)

// PluginActionItem pairs a plugin with one of its actions for display/execution.
type PluginActionItem struct {
	Plugin hooks.Plugin
	Action hooks.Action
}

// ActionPickerModel is a modal list of plugin actions for the current task.
// It mirrors CommandPaletteModel: a self-contained sub-model with its own View,
// switched to via a dedicated View constant, so it never touches the huh
// form-router path.
type ActionPickerModel struct {
	taskTitle     string
	items         []PluginActionItem
	selectedIndex int
	width         int
	height        int

	// Result
	selected  *PluginActionItem
	cancelled bool
}

// gatherPluginActions loads all installed plugins and flattens their actions
// into a display list. Warnings from malformed plugins are ignored here; they
// surface via `ty plugins list`.
func gatherPluginActions() []PluginActionItem {
	plugins, _ := hooks.LoadPlugins(hooks.DefaultPluginsDir())
	var items []PluginActionItem
	for _, p := range plugins {
		for _, a := range p.Actions {
			items = append(items, PluginActionItem{Plugin: p, Action: a})
		}
	}
	return items
}

// NewActionPickerModel creates an action picker for the given items.
func NewActionPickerModel(taskTitle string, items []PluginActionItem, width, height int) *ActionPickerModel {
	return &ActionPickerModel{
		taskTitle: taskTitle,
		items:     items,
		width:     width,
		height:    height,
	}
}

// Init implements tea.Model.
func (m *ActionPickerModel) Init() tea.Cmd { return nil }

// Update handles key input. It returns the model and never a command; the parent
// reads Selected()/IsCancelled() after each update.
func (m *ActionPickerModel) Update(msg tea.Msg) (*ActionPickerModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "esc", "q":
		m.cancelled = true
	case "enter":
		if len(m.items) > 0 {
			sel := m.items[m.selectedIndex]
			m.selected = &sel
		}
	case "up", "ctrl+p", "ctrl+k", "k":
		if len(m.items) > 0 {
			m.selectedIndex--
			if m.selectedIndex < 0 {
				m.selectedIndex = len(m.items) - 1
			}
		}
	case "down", "ctrl+n", "ctrl+j", "j":
		if len(m.items) > 0 {
			m.selectedIndex++
			if m.selectedIndex >= len(m.items) {
				m.selectedIndex = 0
			}
		}
	}
	return m, nil
}

// View renders the modal.
func (m *ActionPickerModel) View() string {
	modalWidth := min(72, m.width-4)

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1).
		Render("Plugin Actions")

	var body strings.Builder
	if len(m.items) == 0 {
		body.WriteString(lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			Render("No plugin actions installed. See docs/plugins.md."))
	} else {
		for i, it := range m.items {
			body.WriteString(m.renderItem(it, i == m.selectedIndex, modalWidth-6))
			if i < len(m.items)-1 {
				body.WriteString("\n")
			}
		}
	}

	help := lipgloss.NewStyle().
		Foreground(ColorMuted).
		MarginTop(1).
		Render("enter: run  esc: cancel  " + IconArrowUp() + "/" + IconArrowDown() + ": navigate")

	content := lipgloss.JoinVertical(lipgloss.Left, header, body.String(), help)

	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(modalWidth)

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalBox.Render(content))
}

func (m *ActionPickerModel) renderItem(it PluginActionItem, selected bool, width int) string {
	var line strings.Builder
	if selected {
		line.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("> "))
	} else {
		line.WriteString("  ")
	}

	label := it.Action.DisplayLabel()
	labelStyle := lipgloss.NewStyle()
	if selected {
		labelStyle = labelStyle.Bold(true).Foreground(ColorPrimary)
	}
	line.WriteString(labelStyle.Render(label))

	// Dim "· plugin-name" suffix so it's clear which plugin owns the action.
	suffix := "  · " + it.Plugin.Name
	line.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render(suffix))

	return line.String()
}

// Selected returns the chosen item, or nil if none was chosen yet.
func (m *ActionPickerModel) Selected() *PluginActionItem { return m.selected }

// IsCancelled reports whether the user dismissed the picker.
func (m *ActionPickerModel) IsCancelled() bool { return m.cancelled }

// SetSize updates dimensions.
func (m *ActionPickerModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}
