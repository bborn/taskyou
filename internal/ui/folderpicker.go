package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// folderEntry is one selectable folder in the picker.
type folderEntry struct {
	path  string
	isGit bool
}

func (e folderEntry) label() string { return e.path }

// folderPickedMsg is emitted when the user picks a folder (enter).
type folderPickedMsg struct{ path string }

// FolderPickerModel is a fuzzy, type-to-search folder picker. It seeds the list
// with likely project roots and lets the user filter, descend, and pick.
type FolderPickerModel struct {
	input    textinput.Model
	all      []folderEntry
	filtered []folderEntry
	selected int
	root     string
	width    int
	height   int
}

// NewFolderPickerModel seeds the picker from common project roots.
func NewFolderPickerModel(width, height int) *FolderPickerModel {
	ti := textinput.New()
	ti.Placeholder = "type to search folders…"
	ti.Focus()
	ti.Prompt = "> "

	m := &FolderPickerModel{input: ti, width: width, height: height}
	m.all = seedCandidateFolders()
	m.filtered = m.all
	return m
}

// seedCandidateFolders gathers likely project dirs from common roots, git first.
func seedCandidateFolders() []folderEntry {
	home, _ := os.UserHomeDir()
	var roots []string
	for _, r := range []string{"Projects", "src", "code", "dev", "work"} {
		roots = append(roots, filepath.Join(home, r))
	}
	seen := map[string]bool{}
	var out []folderEntry
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(root, e.Name())
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, folderEntry{path: p, isGit: dirIsGitRepo(p)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].isGit != out[j].isGit {
			return out[i].isGit
		}
		return out[i].path < out[j].path
	})
	return out
}

// fuzzyFilterFolders returns entries fuzzy-matching query (all if empty).
// Uses the project's own fuzzyScore (VS Code-style) from command_palette.go.
func fuzzyFilterFolders(all []folderEntry, query string) []folderEntry {
	if strings.TrimSpace(query) == "" {
		return all
	}
	q := strings.ToLower(query)
	type scored struct {
		entry folderEntry
		score int
	}
	var hits []scored
	for _, e := range all {
		if s := fuzzyScore(strings.ToLower(e.label()), q); s >= 0 {
			hits = append(hits, scored{e, s})
		}
	}
	// Sort by score descending (best match first).
	sort.Slice(hits, func(i, j int) bool {
		return hits[i].score > hits[j].score
	})
	out := make([]folderEntry, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.entry)
	}
	return out
}

func (m *FolderPickerModel) Init() tea.Cmd { return textinput.Blink }

func (m *FolderPickerModel) Update(msg tea.Msg) (*FolderPickerModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "up", "ctrl+k":
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case "down", "ctrl+j":
			if m.selected < len(m.filtered)-1 {
				m.selected++
			}
			return m, nil
		case "right":
			if len(m.filtered) > 0 {
				m.descend(m.filtered[m.selected].path)
			}
			return m, nil
		case "enter":
			if len(m.filtered) > 0 {
				picked := m.filtered[m.selected].path
				return m, func() tea.Msg { return folderPickedMsg{path: picked} }
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filtered = fuzzyFilterFolders(m.all, m.input.Value())
	if m.selected >= len(m.filtered) {
		m.selected = 0
	}
	return m, cmd
}

// descend repopulates the list with the candidate children of dir. If dir has no
// sub-directories it is treated as a leaf and picked directly.
func (m *FolderPickerModel) descend(dir string) tea.Cmd {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var children []folderEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		children = append(children, folderEntry{path: p, isGit: dirIsGitRepo(p)})
	}
	if len(children) == 0 {
		return nil // leaf: user can press enter to pick it
	}
	m.root = dir
	m.all = children
	m.input.SetValue("")
	m.filtered = m.all
	m.selected = 0
	return nil
}

func (m *FolderPickerModel) View() string {
	var b strings.Builder
	b.WriteString(Bold.Render("Set up a project — pick a folder") + "\n\n")
	b.WriteString(m.input.View() + "\n\n")

	visible := m.height - 8
	if visible < 5 {
		visible = 5
	}
	start := 0
	if m.selected >= visible {
		start = m.selected - visible + 1
	}
	end := start + visible
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	for i := start; i < end; i++ {
		e := m.filtered[i]
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		tag := ""
		if e.isGit {
			tag = lipgloss.NewStyle().Foreground(ColorPrimary).Render("  git ●")
		}
		line := prefix + collapseHome(e.path) + tag
		if i == m.selected {
			line = Bold.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if len(m.filtered) == 0 {
		b.WriteString(Dim.Render("  (no matches — keep typing, or esc to go back)") + "\n")
	}
	b.WriteString("\n" + HelpBar.Render(
		HelpKey.Render("↑↓")+" "+HelpDesc.Render("select")+"  "+
			HelpKey.Render("→")+" "+HelpDesc.Render("open")+"  "+
			HelpKey.Render("enter")+" "+HelpDesc.Render("pick")+"  "+
			HelpKey.Render("esc")+" "+HelpDesc.Render("back")))
	return b.String()
}

// collapseHome shortens /home/u/... to ~/... for display.
func collapseHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (m *FolderPickerModel) SetSize(w, h int) { m.width, m.height = w, h }
