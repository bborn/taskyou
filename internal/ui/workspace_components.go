package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type workspaceKeys struct {
	New, Previous, Next, Close, Refresh, Help, Open, Cancel, Parent key.Binding
}

func newWorkspaceKeys() workspaceKeys {
	binding := func(k, label, description string) key.Binding {
		return key.NewBinding(key.WithKeys(k), key.WithHelp(label, description))
	}
	return workspaceKeys{
		New:      binding("alt+t", "alt+t", "new"),
		Previous: binding("alt+left", "alt+←", "previous tab"),
		Next:     binding("alt+right", "alt+→", "next tab"),
		Close:    binding("alt+w", "alt+w", "close tab"),
		Refresh:  binding("alt+r", "alt+r", "refresh"),
		Help:     binding("alt+h", "alt+h", "help"),
		Open:     binding("enter", "enter", "open"),
		Cancel:   binding("esc", "esc", "cancel"),
		Parent:   binding("backspace", "backspace", "parent directory"),
	}
}

// The bindings used to handle input also drive the help view. Ordinary keys
// remain shell input; only Alt shortcuts are reserved by the workspace.
func (m *WorkspaceModel) ShortHelp() []key.Binding {
	return []key.Binding{m.keys.New, m.keys.Help, m.keys.Next, m.keys.Close}
}
func (m *WorkspaceModel) FullHelp() [][]key.Binding {
	groups := [][]key.Binding{{m.keys.New, m.keys.Close, m.keys.Refresh, m.keys.Help, m.keys.Previous, m.keys.Next}}
	if m.launcher || len(m.tabs) == 0 {
		groups = append(groups, []key.Binding{m.actions.KeyMap.CursorUp, m.actions.KeyMap.CursorDown, m.keys.Open, m.keys.Cancel, m.actions.KeyMap.PrevPage, m.actions.KeyMap.NextPage})
	} else if m.content.Kind == "files" {
		bindings := []key.Binding{m.files.KeyMap.CursorUp, m.files.KeyMap.CursorDown, m.files.KeyMap.Filter, m.files.KeyMap.ClearFilter, m.files.KeyMap.AcceptWhileFiltering, m.files.KeyMap.CancelWhileFiltering}
		if m.files.FilterState() != list.Filtering {
			bindings = append(bindings, m.keys.Open, m.keys.Parent)
		}
		groups = append(groups, bindings)
	} else if m.content.Kind != "shell" {
		groups = append(groups, []key.Binding{m.viewport.KeyMap.Up, m.viewport.KeyMap.Down, m.viewport.KeyMap.PageUp, m.viewport.KeyMap.PageDown})
	}
	return groups
}

type workspaceItem struct{ title, provider, resource string }

func (i workspaceItem) Title() string       { return i.title }
func (i workspaceItem) Description() string { return "" }
func (i workspaceItem) FilterValue() string { return i.title }

func newWorkspaceList(filter bool) list.Model {
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(lipgloss.Color("#8bd5ca")).BorderForeground(lipgloss.Color("#8bd5ca"))
	l := list.New(nil, delegate, 60, 12)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(filter)
	l.SetShowFilter(filter)
	l.DisableQuitKeybindings()
	// Help is owned by the workspace, including while a list is focused.
	l.KeyMap.ShowFullHelp.SetKeys()
	l.KeyMap.CloseFullHelp.SetKeys()
	if !filter {
		// Printable characters and home/end edit the path input, not selection.
		l.KeyMap.CursorUp.SetKeys("up")
		l.KeyMap.CursorUp.SetHelp("↑", "up")
		l.KeyMap.CursorDown.SetKeys("down")
		l.KeyMap.CursorDown.SetHelp("↓", "down")
		l.KeyMap.PrevPage.SetKeys("pgup")
		l.KeyMap.PrevPage.SetHelp("pgup", "previous page")
		l.KeyMap.NextPage.SetKeys("pgdown")
		l.KeyMap.NextPage.SetHelp("pgdown", "next page")
		l.KeyMap.GoToStart.SetKeys()
		l.KeyMap.GoToEnd.SetKeys()
	}
	return l
}

func (m *WorkspaceModel) updateActions() {
	var items []list.Item
	var titles []string
	for _, p := range m.service.Providers() {
		if p.ID != "file" {
			items = append(items, workspaceItem{p.Title, p.ID, ""})
			titles = append(titles, p.Title)
		}
	}
	query := m.query.Value()
	if query != "" {
		filtered := []list.Item{}
		for _, match := range list.DefaultFilter(query, titles) {
			filtered = append(filtered, items[match.Index])
		}
		items = append(filtered, workspaceItem{"Open file: " + query, "file", query})
	}
	m.actions.SetItems(items) // filtering is synchronous through DefaultFilter above
	m.actions.ResetSelected()
}
func (m *WorkspaceModel) updateFiles() tea.Cmd {
	items := make([]list.Item, 0, len(m.content.Entries))
	for _, e := range m.content.Entries {
		title, provider := ansi.Strip(e.Name), "file"
		if e.Directory {
			title += "/"
			provider = "files"
		}
		items = append(items, workspaceItem{title, provider, e.Path})
	}
	return m.files.SetItems(items)
}

func (m *WorkspaceModel) layout() {
	width := max(1, m.width-2)
	m.help.Width = width
	bodyHeight := max(1, m.height-3-lipgloss.Height(m.help.View(m)))
	m.viewport.Width, m.viewport.Height = max(1, width-2), bodyHeight
	m.query.Width = max(1, width-4)
	// Launcher heading, subtitle, search box and Actions label occupy 7 rows.
	m.actions.SetSize(width, max(1, bodyHeight-7))
	m.files.SetSize(width, bodyHeight)
}

func (m *WorkspaceModel) launcherView() string {
	heading := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8bd5ca"))
	lines := []string{"", heading.Render("Your workspace"), "Keep tools and files beside the conversation.", "", m.query.View(), "", "Actions", m.actions.View()}
	if m.err != "" {
		lines = append(lines, ansi.Strip(m.err))
	}
	return strings.Join(lines, "\n")
}
