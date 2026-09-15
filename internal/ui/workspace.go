package ui

import (
	"context"
	"path"
	"reflect"
	"strings"
	"time"

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
	cursor        int
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
	m.cursor = 0
	m.viewport.GotoTop()
	return m.load()
}
func (m *WorkspaceModel) choices() []panel.Provider {
	out := []panel.Provider{}
	q := strings.ToLower(m.query.Value())
	for _, p := range m.service.Providers() {
		if p.ID != "file" && (q == "" || strings.Contains(strings.ToLower(p.Title), q)) {
			out = append(out, p)
		}
	}
	return out
}
func (m *WorkspaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = max(1, msg.Width-4)
		m.viewport.Height = max(1, msg.Height-5)
		m.query.Width = max(1, msg.Width-6)
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
		if changed {
			m.renderContent()
		}
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		switch key {
		case "alt+t":
			m.launcher = true
			m.cursor = 0
			m.query.SetValue("")
			return m, m.query.Focus()
		case "alt+left", "alt+right":
			if len(m.tabs) == 0 {
				return m, nil
			}
			index := 0
			for i, t := range m.tabs {
				if t.ID == m.active {
					index = i
				}
			}
			if key == "alt+right" {
				index++
			} else {
				index--
			}
			m.active = m.tabs[(index+len(m.tabs))%len(m.tabs)].ID
			m.launcher = false
			m.cursor = 0
			return m, m.load()
		case "alt+w":
			if err := m.service.Close(m.taskID, m.active); err != nil {
				m.err = err.Error()
				return m, nil
			}
			m.active = ""
			m.launcher = false
			return m, m.load()
		case "alt+r":
			return m, m.load()
		}
		if m.launcher || len(m.tabs) == 0 {
			m.launcher = true
			switch key {
			case "esc":
				m.launcher = false
				m.query.Blur()
				return m, nil
			case "up":
				m.cursor = max(0, m.cursor-1)
				return m, nil
			case "down":
				m.cursor = min(len(m.choices()), m.cursor+1)
				return m, nil
			case "enter":
				choices := m.choices()
				if m.cursor < len(choices) {
					return m, m.open(choices[m.cursor].ID, "")
				}
				if m.query.Value() != "" {
					return m, m.open("file", m.query.Value())
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.query, cmd = m.query.Update(msg)
			m.cursor = 0
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
			switch key {
			case "up", "k":
				m.cursor = max(0, m.cursor-1)
				m.renderContent()
				return m, nil
			case "down", "j":
				m.cursor = min(max(0, len(m.content.Entries)-1), m.cursor+1)
				m.renderContent()
				return m, nil
			case "backspace":
				for _, t := range m.tabs {
					if t.ID == m.active {
						return m, m.open("files", path.Dir(t.Resource))
					}
				}
			case "enter":
				if m.cursor < len(m.content.Entries) {
					e := m.content.Entries[m.cursor]
					provider := "file"
					if e.Directory {
						provider = "files"
					}
					return m, m.open(provider, e.Path)
				}
			}
		}
	}
	var cmd tea.Cmd
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
	if m.err == "" && m.content.Kind == "files" {
		var b strings.Builder
		for i, e := range m.content.Entries {
			icon := "  "
			if e.Directory {
				icon = "▸ "
			}
			line := icon + ansi.Strip(e.Name)
			if i == m.cursor {
				line = lipgloss.NewStyle().Foreground(lipgloss.Color("#8bd5ca")).Bold(true).Render("› " + line)
			} else {
				line = "  " + line
			}
			b.WriteString(line + "\n")
		}
		text = b.String()
		if text == "" {
			text = "This directory is empty."
		}
	}
	m.viewport.SetContent(text)
	if m.content.Kind == "files" {
		if m.cursor < m.viewport.YOffset {
			m.viewport.SetYOffset(m.cursor)
		}
		if m.cursor >= m.viewport.YOffset+m.viewport.Height {
			m.viewport.SetYOffset(m.cursor - m.viewport.Height + 1)
		}
	}
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
		lines := []string{"", accent.Render("Your workspace"), "Keep tools and files beside the conversation.", "", m.query.View(), "", "Actions"}
		for i, p := range m.choices() {
			label := "  " + p.Title
			if i == m.cursor {
				label = accent.Render("› " + p.Title)
			}
			lines = append(lines, label)
		}
		if m.query.Value() != "" {
			label := "  Open file: " + m.query.Value()
			if m.cursor >= len(m.choices()) {
				label = accent.Render("› Open file: " + m.query.Value())
			}
			lines = append(lines, label)
		}
		if m.err != "" {
			lines = append(lines, "", m.err)
		}
		body = lipgloss.NewStyle().Width(max(1, m.width-4)).Height(max(1, m.height-5)).MaxHeight(max(1, m.height-5)).Render(strings.Join(lines, "\n"))
	}
	footer := "Alt+t new · Alt+←/→ tabs · Alt+w close · Alt+r refresh"
	return lipgloss.NewStyle().Padding(0, 1).Render(header + "\n" + strings.Repeat("─", max(1, m.width-2)) + "\n" + body + "\n" + ansi.Truncate(footer, m.width-2, "…"))
}
