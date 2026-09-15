package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
)

// ViewPickerModel is the modal behind `V`: the saved views, plus the two
// operations that keep them useful — save the filter you are looking at, and
// delete one you no longer want.
//
// A saved view is only ever its query string (see db.SavedView), so applying
// one is exactly "type this into the filter bar". That is why the picker hands
// a query back to the app instead of filtering anything itself.
type ViewPickerModel struct {
	db     *db.DB
	views  []*db.SavedView
	index  int
	width  int
	height int

	// currentFilter is what the board is filtered by right now — the thing "save
	// current filter" saves, and what the header shows as unsaved.
	currentFilter string

	mode      viewPickerMode
	nameInput textinput.Model
	err       string

	applied   *db.SavedView
	clearAll  bool
	cancelled bool
}

type viewPickerMode int

const (
	viewPickerBrowse viewPickerMode = iota
	viewPickerNaming
	viewPickerConfirmDelete
)

// NewViewPickerModel builds the picker over the views stored in the database.
func NewViewPickerModel(database *db.DB, currentFilter string, width, height int) *ViewPickerModel {
	input := textinput.New()
	input.Placeholder = "View name"
	input.CharLimit = db.MaxSavedViewName
	input.Width = 32

	m := &ViewPickerModel{
		db:            database,
		currentFilter: strings.TrimSpace(currentFilter),
		width:         width,
		height:        height,
		nameInput:     input,
	}
	m.reload()
	return m
}

func (m *ViewPickerModel) reload() {
	if m.db == nil {
		return
	}
	views, err := m.db.ListSavedViews()
	if err != nil {
		m.err = "could not load views: " + err.Error()
		return
	}
	m.views = views
	if m.index >= len(m.views) {
		m.index = len(m.views) - 1
	}
	if m.index < 0 {
		m.index = 0
	}
}

// Init implements tea.Model.
func (m *ViewPickerModel) Init() tea.Cmd { return nil }

// Update handles key input. The parent reads Applied()/ClearRequested()/
// IsCancelled() after each update, matching ActionPickerModel.
func (m *ViewPickerModel) Update(msg tea.Msg) (*ViewPickerModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch m.mode {
	case viewPickerNaming:
		return m.updateNaming(keyMsg)
	case viewPickerConfirmDelete:
		return m.updateConfirmDelete(keyMsg)
	}

	m.err = ""
	switch keyMsg.String() {
	case "esc", "q":
		m.cancelled = true
	case "enter":
		if len(m.views) > 0 {
			m.applied = m.views[m.index]
		}
	case "c":
		// Clear the filter entirely — "show me everything again".
		m.clearAll = true
	case "n", "s":
		if m.currentFilter == "" {
			m.err = "nothing to save — set a filter with / first"
			break
		}
		m.mode = viewPickerNaming
		m.nameInput.SetValue("")
		m.nameInput.Focus()
		return m, textinput.Blink
	case "d", "x":
		if len(m.views) > 0 {
			m.mode = viewPickerConfirmDelete
		}
	case "up", "ctrl+p", "ctrl+k", "k":
		m.move(-1)
	case "down", "ctrl+n", "ctrl+j", "j":
		m.move(1)
	}
	return m, nil
}

func (m *ViewPickerModel) move(delta int) {
	if len(m.views) == 0 {
		return
	}
	m.index = (m.index + delta + len(m.views)) % len(m.views)
}

func (m *ViewPickerModel) updateNaming(msg tea.KeyMsg) (*ViewPickerModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = viewPickerBrowse
		m.nameInput.Blur()
		return m, nil
	case "enter":
		name := m.nameInput.Value()
		if err := db.ValidateViewName(name); err != nil {
			m.err = err.Error()
			return m, nil
		}
		if m.db == nil {
			m.err = "no database"
			return m, nil
		}
		saved, err := m.db.SaveView(name, m.currentFilter)
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.mode = viewPickerBrowse
		m.nameInput.Blur()
		m.reload()
		// Land the cursor on what was just saved, so the next Enter applies it.
		for i, v := range m.views {
			if saved != nil && v.ID == saved.ID {
				m.index = i
				break
			}
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m *ViewPickerModel) updateConfirmDelete(msg tea.KeyMsg) (*ViewPickerModel, tea.Cmd) {
	switch msg.String() {
	case "y", "d", "enter":
		if m.db != nil && m.index < len(m.views) {
			if err := m.db.DeleteSavedView(m.views[m.index].Name); err != nil {
				m.err = err.Error()
			}
		}
		m.mode = viewPickerBrowse
		m.reload()
	default:
		m.mode = viewPickerBrowse
	}
	return m, nil
}

// View renders the modal.
func (m *ViewPickerModel) View() string {
	modalWidth := min(72, m.width-4)

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1).
		Render("Saved Views")

	var body strings.Builder
	if len(m.views) == 0 {
		body.WriteString(lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			Render("No saved views yet. Filter with '/', then press 'n' here to save it."))
	} else {
		for i, v := range m.views {
			body.WriteString(m.renderItem(v, i == m.index, modalWidth-6))
			if i < len(m.views)-1 {
				body.WriteString("\n")
			}
		}
	}

	sections := []string{header, body.String()}

	if m.currentFilter != "" {
		sections = append(sections, lipgloss.NewStyle().
			Foreground(ColorMuted).
			MarginTop(1).
			Render("Current filter: "+truncateRunes(m.currentFilter, modalWidth-22)))
	}

	switch m.mode {
	case viewPickerNaming:
		sections = append(sections, lipgloss.NewStyle().MarginTop(1).Render(
			"Save as: "+m.nameInput.View()))
	case viewPickerConfirmDelete:
		name := ""
		if m.index < len(m.views) {
			name = m.views[m.index].Name
		}
		sections = append(sections, lipgloss.NewStyle().
			Foreground(ColorWarning).
			MarginTop(1).
			Render(fmt.Sprintf("Delete view %q? (y/n)", name)))
	}

	if m.err != "" {
		sections = append(sections, lipgloss.NewStyle().
			Foreground(ColorBlocked).
			MarginTop(1).
			Render(m.err))
	}

	sections = append(sections, lipgloss.NewStyle().
		Foreground(ColorMuted).
		MarginTop(1).
		Render(m.helpLine()))

	content := lipgloss.JoinVertical(lipgloss.Left, sections...)

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

func (m *ViewPickerModel) helpLine() string {
	switch m.mode {
	case viewPickerNaming:
		return "enter: save  esc: back"
	case viewPickerConfirmDelete:
		return "y: delete  n/esc: keep"
	}
	return "enter: apply  n: save current  d: delete  c: clear filter  esc: cancel"
}

func (m *ViewPickerModel) renderItem(v *db.SavedView, selected bool, width int) string {
	var line strings.Builder
	if selected {
		line.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("> "))
	} else {
		line.WriteString("  ")
	}

	nameStyle := lipgloss.NewStyle()
	if selected {
		nameStyle = nameStyle.Bold(true).Foreground(ColorPrimary)
	}
	line.WriteString(nameStyle.Render(v.Name))

	// The query is the whole definition of a view, so it belongs on the row —
	// picking a view should never be guesswork about what it will show.
	query := v.Query
	if query == "" {
		query = "(everything)"
	}
	room := width - lipgloss.Width(line.String()) - 3
	if room < 8 {
		room = 8
	}
	line.WriteString(lipgloss.NewStyle().
		Foreground(ColorMuted).
		Render("  " + truncateRunes(query, room)))

	return line.String()
}

// Applied returns the view the user chose, or nil.
func (m *ViewPickerModel) Applied() *db.SavedView { return m.applied }

// ClearRequested reports whether the user asked to drop the filter entirely.
func (m *ViewPickerModel) ClearRequested() bool { return m.clearAll }

// IsCancelled reports whether the user dismissed the picker.
func (m *ViewPickerModel) IsCancelled() bool { return m.cancelled }

// SetSize updates dimensions.
func (m *ViewPickerModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}
