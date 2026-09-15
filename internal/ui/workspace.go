package ui

import (
	"context"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/bborn/workflow/internal/panel"
)

// WorkspaceKinds is the renderer contract checked alongside GUI capabilities.
var WorkspaceKinds = []string{"shell", "markdown", "text", "files"}

type workspaceTick struct{}
type workspaceLoaded struct {
	requested string
	id        string
	tabs      []panel.Instance
	content   panel.Content
	err       error
}
type workspaceInput struct {
	text    string
	literal bool
}

type WorkspaceModel struct {
	loading       bool
	lastRead      time.Time
	ctx           context.Context
	service       *panel.Service
	taskID        int64
	tabs          []panel.Instance
	active        string
	launcher      bool
	query         textinput.Model
	actions       list.Model
	files         list.Model
	help          help.Model
	keys          workspaceKeys
	width, height int
	viewport      viewport.Model
	content       panel.Content
	err           string
	inputs        chan workspaceInput
	inputErrors   chan error
}

func NewWorkspaceModel(ctx context.Context, service *panel.Service, taskID int64) *WorkspaceModel {
	input := textinput.New()
	input.Placeholder = "Search tools or enter a file path"
	input.Prompt = "› "
	input.CharLimit = 4096
	m := &WorkspaceModel{ctx: ctx, service: service, taskID: taskID, query: input, viewport: viewport.New(60, 20), width: 64, height: 24, inputs: make(chan workspaceInput, 128), inputErrors: make(chan error, 1)}
	m.actions, m.files = newWorkspaceList(false), newWorkspaceList(true)
	m.help, m.keys = help.New(), newWorkspaceKeys()
	m.updateActions()
	m.layout()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case key := <-m.inputs:
				ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := service.SendShell(ctx, taskID, key.text, key.literal)
				cancel()
				if err != nil {
					select {
					case m.inputErrors <- err:
					default:
					}
				}
			}
		}
	}()
	return m
}
func (m *WorkspaceModel) Init() tea.Cmd { return tea.Batch(m.load(), workspaceTimer()) }
func (m *WorkspaceModel) load() tea.Cmd {
	if m.loading {
		return nil
	}
	m.loading = true
	m.lastRead = time.Now()
	id := m.active
	return func() tea.Msg {
		tabs, err := m.service.List(m.taskID)
		msg := workspaceLoaded{requested: id, id: id, tabs: tabs, err: err}
		if err != nil {
			return msg
		}
		found := false
		for _, t := range tabs {
			if t.ID == id {
				found = true
			}
		}
		if !found {
			if len(tabs) == 0 {
				return msg
			}
			id = tabs[0].ID
			msg.id = id
		}
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
		defer cancel()
		msg.content, msg.err = m.service.Content(ctx, m.taskID, id)
		if msg.err == nil && msg.content.Kind == "shell" {
			msg.content.Text, msg.err = m.service.ShellFrame(ctx, m.taskID)
		}
		return msg
	}
}
func workspaceTimer() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(time.Time) tea.Msg { return workspaceTick{} })
}
func (m *WorkspaceModel) open(provider, resource string) tea.Cmd {
	p, err := m.service.Open(m.taskID, provider, resource)
	if err != nil {
		m.err = err.Error()
		return nil
	}
	m.active = p.ID
	m.launcher = false
	m.query.Blur()
	m.files.ResetFilter()
	m.files.ResetSelected()
	m.viewport.GotoTop()
	m.layout()
	return m.load()
}
func (m *WorkspaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.renderContent()
		return m, nil
	case workspaceTick:
		select {
		case err := <-m.inputErrors:
			m.err = err.Error()
		default:
		}
		if m.launcher || (m.content.Kind != "shell" && time.Since(m.lastRead) < 3*time.Second) {
			return m, workspaceTimer()
		}
		return m, tea.Batch(m.load(), workspaceTimer())
	case workspaceLoaded:
		// Ignore an older request after the user switches tabs.
		m.loading = false
		if msg.requested != m.active {
			return m, nil
		}
		m.tabs = msg.tabs
		m.active = msg.id
		changed := !reflect.DeepEqual(m.content, msg.content) || (msg.err != nil) != (m.err != "")
		m.content = msg.content
		m.err = ""
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		var cmd tea.Cmd
		if len(m.tabs) == 0 && !m.launcher {
			m.launcher = true
			cmd = m.query.Focus()
			m.updateActions()
			m.layout()
		}
		if changed {
			if m.content.Kind == "files" {
				cmd = m.updateFiles()
			}
			m.layout()
			m.renderContent()
		}
		return m, cmd
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.layout()
			return m, nil
		case key.Matches(msg, m.keys.New):
			m.launcher = true
			m.query.SetValue("")
			m.updateActions()
			m.layout()
			return m, m.query.Focus()
		case key.Matches(msg, m.keys.Previous, m.keys.Next):
			if len(m.tabs) == 0 {
				return m, nil
			}
			index := 0
			for i, t := range m.tabs {
				if t.ID == m.active {
					index = i
				}
			}
			if key.Matches(msg, m.keys.Next) {
				index++
			} else {
				index--
			}
			m.active = m.tabs[(index+len(m.tabs))%len(m.tabs)].ID
			m.launcher = false
			m.files.ResetFilter()
			m.files.ResetSelected()
			m.query.Blur()
			m.layout()
			return m, m.load()
		case key.Matches(msg, m.keys.Close):
			if err := m.service.Close(m.taskID, m.active); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.active = ""
			m.launcher = false
			return m, m.load()
		case key.Matches(msg, m.keys.Refresh):
			return m, m.load()
		}
		if m.launcher || len(m.tabs) == 0 {
			m.launcher = true
			switch {
			case key.Matches(msg, m.keys.Cancel):
				m.launcher = false
				m.query.Blur()
				m.layout()
				return m, nil
			case key.Matches(msg, m.keys.Open):
				if item, ok := m.actions.SelectedItem().(workspaceItem); ok {
					return m, m.open(item.provider, item.resource)
				}
				return m, nil
			case key.Matches(msg, m.actions.KeyMap.CursorUp, m.actions.KeyMap.CursorDown, m.actions.KeyMap.PrevPage, m.actions.KeyMap.NextPage):
				var cmd tea.Cmd
				m.actions, cmd = m.actions.Update(msg)
				return m, cmd
			}
			before := m.query.Value()
			var cmd tea.Cmd
			m.query, cmd = m.query.Update(msg)
			if before != m.query.Value() {
				m.updateActions()
			}
			return m, cmd
		}
		if m.content.Kind == "shell" {
			text, literal := shellKey(msg)
			if text != "" {
				select {
				case m.inputs <- workspaceInput{text, literal}:
				default:
					m.err = "Input queue full; wait for the host to respond"
				}
			}
			return m, nil
		}
		if m.content.Kind == "files" {
			if m.files.FilterState() != list.Filtering {
				switch {
				case key.Matches(msg, m.keys.Parent):
					for _, t := range m.tabs {
						if t.ID == m.active {
							return m, m.open("files", path.Dir(t.Resource))
						}
					}
				case key.Matches(msg, m.keys.Open):
					if item, ok := m.files.SelectedItem().(workspaceItem); ok {
						return m, m.open(item.provider, item.resource)
					}
					return m, nil
				}
			}
			var cmd tea.Cmd
			m.files, cmd = m.files.Update(msg)
			m.layout()
			return m, cmd
		}
	}
	var cmd tea.Cmd
	if m.launcher || len(m.tabs) == 0 {
		m.query, cmd = m.query.Update(msg)
		return m, cmd
	}
	if m.content.Kind == "files" {
		m.files, cmd = m.files.Update(msg)
		return m, cmd
	}
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}
func shellKey(msg tea.KeyMsg) (string, bool) {
	if msg.Type == tea.KeySpace {
		return " ", true
	}
	if msg.Type == tea.KeyRunes {
		text := string(msg.Runes)
		if msg.Alt {
			text = "\x1b" + text
		}
		return text, true
	}
	keys := map[string]string{"enter": "Enter", "backspace": "BSpace", "tab": "Tab", "shift+tab": "BTab", "esc": "Escape", "up": "Up", "down": "Down", "left": "Left", "right": "Right", "home": "Home", "end": "End", "delete": "DC", "pgup": "PPage", "pgdown": "NPage"}
	key := strings.TrimPrefix(msg.String(), "alt+")
	prefix := ""
	if msg.Alt {
		prefix = "M-"
	}
	if k, ok := keys[key]; ok {
		return prefix + k, false
	}
	if strings.HasPrefix(key, "ctrl+") {
		base := strings.TrimPrefix(key, "ctrl+")
		if named, ok := keys[base]; ok {
			base = named
		}
		return prefix + "C-" + base, false
	}
	return "", false
}

func (m *WorkspaceModel) renderContent() {
	text := m.content.Text
	if m.content.Kind != "shell" {
		text = ansi.Strip(text)
	}
	if m.err != "" {
		text = ansi.Strip(m.err)
	}
	if m.err == "" && m.content.Kind == "markdown" {
		renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(max(10, m.width-6)))
		if err == nil {
			if rendered, err := renderer.Render(text); err == nil {
				text = rendered
			}
		}
	}
	if m.content.URL != "" {
		text += "\n\n" + ansi.Strip(m.content.URL)
	}
	m.viewport.SetContent(text)
	if m.content.Kind == "shell" {
		m.viewport.GotoBottom()
	}
}
func (m *WorkspaceModel) View() string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#8bd5ca")).Bold(true)
	var tabs []string
	for _, t := range m.tabs {
		label := ansi.Strip(t.Title)
		if t.ID == m.active && !m.launcher {
			label = accent.Render("[" + label + "]")
		}
		tabs = append(tabs, label)
	}
	header := ansi.Truncate(strings.Join(tabs, "  ")+"  +", m.width-2, "…")
	body := m.viewport.View()
	if m.launcher || len(m.tabs) == 0 {
		body = m.launcherView()
	} else if m.content.Kind == "files" && m.err == "" {
		body = m.files.View()
	}
	body = lipgloss.NewStyle().Height(m.viewport.Height).MaxHeight(m.viewport.Height).Width(max(1, m.width-2)).Render(body)
	return lipgloss.NewStyle().Padding(0, 1).Render(header + "\n" + strings.Repeat("─", max(1, m.width-2)) + "\n" + body + "\n" + m.help.View(m))
}
