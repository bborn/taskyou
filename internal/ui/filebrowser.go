package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FileBrowserModel is a directory browser for selecting paths.
type FileBrowserModel struct {
	currentDir string
	entries    []dirEntry
	selected   int
	width      int
	height     int
	err        error

	// Callback when path is selected
	onSelect func(path string)
	onCancel func()
}

type dirEntry struct {
	name  string
	isDir bool
}

// NewFileBrowserModel creates a new file browser starting at the given path.
func NewFileBrowserModel(startPath string, width, height int) *FileBrowserModel {
	if startPath == "" {
		startPath, _ = os.UserHomeDir()
	}
	startPath = expandPath(startPath)

	// If startPath is a file, use its directory
	if info, err := os.Stat(startPath); err == nil && !info.IsDir() {
		startPath = filepath.Dir(startPath)
	}

	m := &FileBrowserModel{
		currentDir: startPath,
		width:      width,
		height:     height,
	}
	m.loadDir()
	return m
}

func (m *FileBrowserModel) loadDir() {
	m.entries = nil
	m.selected = 0
	m.err = nil

	entries, err := os.ReadDir(m.currentDir)
	if err != nil {
		m.err = err
		return
	}

	// Add parent directory entry if not at root
	if m.currentDir != "/" {
		m.entries = append(m.entries, dirEntry{name: "..", isDir: true})
	}

	// Collect directories first, then files
	var dirs, files []dirEntry
	for _, e := range entries {
		// Skip hidden files
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, dirEntry{name: e.Name(), isDir: true})
		} else {
			files = append(files, dirEntry{name: e.Name(), isDir: false})
		}
	}

	// Sort alphabetically
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	m.entries = append(m.entries, dirs...)
	m.entries = append(m.entries, files...)
}

// Init initializes the model.
func (m *FileBrowserModel) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m *FileBrowserModel) Update(msg tea.Msg) (*FileBrowserModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.entries)-1 {
				m.selected++
			}
		case "pgup":
			m.selected -= 10
			if m.selected < 0 {
				m.selected = 0
			}
		case "pgdown":
			m.selected += 10
			if m.selected >= len(m.entries) {
				m.selected = len(m.entries) - 1
			}
		case "enter", "right", "l":
			if len(m.entries) > 0 {
				entry := m.entries[m.selected]
				if entry.isDir {
					// Navigate into directory
					if entry.name == ".." {
						m.currentDir = filepath.Dir(m.currentDir)
					} else {
						m.currentDir = filepath.Join(m.currentDir, entry.name)
					}
					m.loadDir()
				}
			}
		case "left", "h", "backspace":
			// Go up one directory
			if m.currentDir != "/" {
				m.currentDir = filepath.Dir(m.currentDir)
				m.loadDir()
			}
		case " ":
			// Select current directory
			if m.onSelect != nil {
				m.onSelect(m.currentDir)
			}
		case "esc", "q":
			if m.onCancel != nil {
				m.onCancel()
			}
		case "~":
			// Go to home
			home, _ := os.UserHomeDir()
			m.currentDir = home
			m.loadDir()
		}
	}

	return m, nil
}

// View renders the file browser.
func (m *FileBrowserModel) View() string {
	var b strings.Builder

	// Header with current path
	header := Bold.Render("Select Directory")
	b.WriteString(lipgloss.NewStyle().Padding(1, 2).Render(header))
	b.WriteString("\n")

	// Current path
	pathStyle := lipgloss.NewStyle().
		Foreground(ColorPrimary).
		Padding(0, 2)
	b.WriteString(pathStyle.Render(m.currentDir))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(Error.Render(m.err.Error())))
		b.WriteString("\n")
	}

	// Calculate visible range
	visibleHeight := m.height - 12
	if visibleHeight < 5 {
		visibleHeight = 5
	}

	start := 0
	if m.selected >= visibleHeight {
		start = m.selected - visibleHeight + 1
	}
	end := start + visibleHeight
	if end > len(m.entries) {
		end = len(m.entries)
	}

	// Entries
	for i := start; i < end; i++ {
		entry := m.entries[i]
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}

		icon := "📄 "
		style := lipgloss.NewStyle()
		if entry.isDir {
			icon = "📁 "
			style = style.Foreground(ColorPrimary)
		}
		if i == m.selected {
			style = style.Bold(true)
		}

		line := prefix + icon + entry.name
		b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(style.Render(line)))
		b.WriteString("\n")
	}

	// Scroll indicator
	if len(m.entries) > visibleHeight {
		scrollInfo := Dim.Render(fmt.Sprintf(" [%d/%d]", m.selected+1, len(m.entries)))
		b.WriteString(lipgloss.NewStyle().Padding(0, 2).Render(scrollInfo))
		b.WriteString("\n")
	}

	// Help
	b.WriteString("\n")
	help := HelpKey.Render(IconArrowUp()+"/"+IconArrowDown()) + " " + HelpDesc.Render("navigate") + "  "
	help += HelpKey.Render("enter") + " " + HelpDesc.Render("open") + "  "
	help += HelpKey.Render("space") + " " + HelpDesc.Render("select this dir") + "  "
	help += HelpKey.Render("~") + " " + HelpDesc.Render("home") + "  "
	help += HelpKey.Render("esc") + " " + HelpDesc.Render("cancel")
	b.WriteString(HelpBar.Render(help))

	return b.String()
}

// SetSize updates the browser size.
func (m *FileBrowserModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// CurrentDir returns the currently displayed directory.
func (m *FileBrowserModel) CurrentDir() string {
	return m.currentDir
}

// OnSelect sets the callback for when a path is selected.
func (m *FileBrowserModel) OnSelect(fn func(path string)) {
	m.onSelect = fn
}

// OnCancel sets the callback for when browsing is cancelled.
func (m *FileBrowserModel) OnCancel(fn func()) {
	m.onCancel = fn
}

func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[1:])
	}
	return path
}
