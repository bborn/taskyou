package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/github"
)

// folderEntry is one selectable folder in the picker.
type folderEntry struct {
	path  string
	isGit bool
}

// folderPickedMsg is emitted when the user picks a folder (enter).
type folderPickedMsg struct{ path string }

// repoRequestedMsg is emitted when the text typed into the picker is a repo
// URL rather than a filter term, and the user pressed enter on it.
type repoRequestedMsg struct{ ref github.RepoRef }

// folderItem adapts a folderEntry to bubbles/list: the title is the folder
// basename and the description is the ~-collapsed parent directory.
type folderItem struct {
	path   string
	isGit  bool
	parent string // pre-collapsed parent dir, for display
}

func (i folderItem) Title() string       { return filepath.Base(i.path) }
func (i folderItem) Description() string { return i.parent }

// FilterValue returns the basename: it's what the list fuzzy-matches against,
// and the delegate underlines the matched runes inside the rendered title, so
// the two must be the same string for the highlights to line up.
func (i folderItem) FilterValue() string { return filepath.Base(i.path) }

// folderRowHeight is the rendered height of one list row: two lines
// (title + description) plus one line of spacing.
const folderRowHeight = 3

// folderPanelChromeRows is everything in the centered panel that isn't the
// list: borders (2), vertical padding (2), title, subtitle, blank, input,
// blank, count line, help bar (3 incl. its padding), pagination line.
const folderPanelChromeRows = 14

// FolderPickerModel is a fuzzy, type-to-search folder picker built on
// bubbles/list. It seeds the list with likely project roots; typing drives the
// list's built-in fuzzy filter (matched runes are underlined), → descends into
// the selected folder, and enter picks it.
type FolderPickerModel struct {
	input  textinput.Model
	list   list.Model
	width  int
	height int
	home   string
	root   string // non-empty once the user has descended into a folder

	// repoRef is set while the typed text parses as a repo URL: enter clones
	// it instead of picking a folder. repoErr holds the inline complaint when
	// the text looks like a URL but isn't one we can clone.
	repoRef *github.RepoRef
	repoErr string
}

// NewFolderPickerModel seeds the picker from common project roots.
func NewFolderPickerModel(width, height int) *FolderPickerModel {
	ti := textinput.New()
	ti.Placeholder = "type to filter, or paste a repo URL…"
	ti.Focus()
	ti.Prompt = Icon("❯ ", "> ")
	ti.PromptStyle = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)

	l := list.New(nil, newFolderDelegate(), 0, 0)
	l.SetShowTitle(false)
	l.SetShowFilter(false) // filtering is driven through our own input below
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(true)
	l.SetStatusBarItemName("folder", "folders")
	l.DisableQuitKeybindings()
	l.Styles.PaginationStyle = lipgloss.NewStyle().PaddingLeft(2)
	l.Styles.ActivePaginationDot = lipgloss.NewStyle().Foreground(ColorPrimary).SetString("•")
	l.Styles.InactivePaginationDot = lipgloss.NewStyle().Foreground(ColorMuted).SetString("•")
	l.Styles.NoItems = lipgloss.NewStyle().Foreground(ColorMuted).PaddingLeft(2)

	home, _ := os.UserHomeDir()
	m := &FolderPickerModel{input: ti, list: l, width: width, height: height, home: home}
	m.setEntries(seedCandidateFolders())
	m.layout()
	return m
}

// seedCandidateFolders gathers likely project dirs from common roots under
// $HOME, git repos first.
func seedCandidateFolders() []folderEntry {
	home, _ := os.UserHomeDir()
	var roots []string
	for _, r := range []string{"Projects", "src", "code", "dev", "work"} {
		roots = append(roots, filepath.Join(home, r))
	}
	return collectCandidateFolders(roots)
}

// collectCandidateFolders lists the visible sub-directories of each root,
// de-duplicates them, and sorts git repos first.
func collectCandidateFolders(roots []string) []folderEntry {
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
	sortFolderEntries(out)
	return out
}

// sortFolderEntries orders git repositories first, then alphabetically by path.
func sortFolderEntries(entries []folderEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].isGit != entries[j].isGit {
			return entries[i].isGit
		}
		return entries[i].path < entries[j].path
	})
}

// setEntries replaces the list contents, clearing any active filter.
func (m *FolderPickerModel) setEntries(entries []folderEntry) {
	items := make([]list.Item, len(entries))
	for i, e := range entries {
		items[i] = folderItem{
			path:   e.path,
			isGit:  e.isGit,
			parent: collapseHomePath(filepath.Dir(e.path), m.home),
		}
	}
	m.list.ResetFilter()
	m.list.SetItems(items)
	m.list.ResetSelected()
}

func (m *FolderPickerModel) Init() tea.Cmd { return textinput.Blink }

func (m *FolderPickerModel) Update(msg tea.Msg) (*FolderPickerModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "up", "ctrl+k":
			m.list.CursorUp()
			return m, nil
		case "down", "ctrl+j":
			m.list.CursorDown()
			return m, nil
		case "pgup":
			m.list.Paginator.PrevPage()
			return m, nil
		case "pgdown":
			m.list.Paginator.NextPage()
			return m, nil
		case "right":
			if it, ok := m.list.SelectedItem().(folderItem); ok {
				m.descend(it.path)
			}
			return m, nil
		case "enter":
			// A pasted repo URL takes precedence: there is no folder to pick
			// yet, we have to clone it first.
			if m.repoRef != nil {
				ref := *m.repoRef
				return m, func() tea.Msg { return repoRequestedMsg{ref: ref} }
			}
			if it, ok := m.list.SelectedItem().(folderItem); ok {
				picked := it.path
				return m, func() tea.Msg { return folderPickedMsg{path: picked} }
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	before := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if q := m.input.Value(); q != before {
		m.classifyInput(q)
		// A repo URL isn't a filter term: filtering on one empties the list and
		// the panel reads "No folders", which is both useless and untrue. Leave
		// the folder shelf alone and let the clone line below do the talking.
		if strings.TrimSpace(q) == "" || m.repoRef != nil || m.repoErr != "" {
			m.list.ResetFilter()
			m.list.ResetSelected()
		} else {
			m.list.SetFilterText(q)
		}
	}
	return m, cmd
}

// classifyInput decides whether what's been typed is a repo URL to clone or an
// ordinary filter term. Text that merely looks like a URL but doesn't parse
// gets an inline complaint rather than silently filtering to nothing.
func (m *FolderPickerModel) classifyInput(q string) {
	m.repoRef, m.repoErr = nil, ""
	q = strings.TrimSpace(q)
	if q == "" || isExistingDir(q, m.home) {
		return
	}
	ref, err := github.ParseRepoRef(q)
	if err == nil {
		m.repoRef = &ref
		return
	}
	if github.LooksLikeRepoRef(q) {
		m.repoErr = err.Error()
	}
}

// isExistingDir reports whether the typed text is already a directory on this
// machine — if it is, it's a path, not a repo to clone.
func isExistingDir(p, home string) bool {
	if strings.HasPrefix(p, "~") && home != "" {
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// descend repopulates the list with the candidate children of dir. If dir has
// no sub-directories it is treated as a leaf and left as-is: the user can
// press enter to pick it.
func (m *FolderPickerModel) descend(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
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
		return // leaf: user can press enter to pick it
	}
	sortFolderEntries(children)
	m.root = dir
	m.input.SetValue("")
	m.classifyInput("")
	m.setEntries(children)
}

func (m *FolderPickerModel) View() string {
	w := m.contentWidth()

	subtitle := "Pick a folder, or paste a GitHub repo URL"
	if m.root != "" {
		subtitle = "In " + collapseHomePath(m.root, m.home) + " — pick a folder"
	}

	enterDesc := "pick"
	if m.repoRef != nil {
		enterDesc = "clone"
	}
	help := HelpBar.Render(
		HelpKey.Render("↑↓") + " " + HelpDesc.Render("select") + "  " +
			HelpKey.Render("→") + " " + HelpDesc.Render("open") + "  " +
			HelpKey.Render("enter") + " " + HelpDesc.Render(enterDesc) + "  " +
			HelpKey.Render("esc") + " " + HelpDesc.Render("back"))

	content := lipgloss.JoinVertical(lipgloss.Left,
		Title.Render("Set up a project"),
		Dim.Render(truncateRunes(subtitle, w)),
		"",
		m.input.View(),
		"",
		m.list.View(),
		m.countLine(),
		help,
	)

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(w + 4).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// countLine summarises what the list is showing, e.g. "18 folders · git repos
// first", or "3 of 18 folders match" while a filter is active.
func (m *FolderPickerModel) countLine() string {
	w := m.contentWidth()
	if m.repoRef != nil {
		return Success.Width(w).PaddingLeft(2).Render("enter to clone " + m.repoRef.String())
	}
	if m.repoErr != "" {
		// Wrapped, never truncated: the tail of these messages is the example
		// URL, which is the whole point of showing them.
		return Error.Width(w).PaddingLeft(2).Render(m.repoErr)
	}
	total := len(m.list.Items())
	noun := "folders"
	if total == 1 {
		noun = "folder"
	}
	switch {
	case total == 0:
		return Dim.Render("  no candidate folders found — esc to go back")
	case m.list.IsFiltered():
		n := len(m.list.VisibleItems())
		if n == 0 {
			return Dim.Render("  no matches — keep typing, or esc to go back")
		}
		return Dim.Render(fmt.Sprintf("  %d of %d %s match", n, total, noun))
	default:
		return Dim.Render(fmt.Sprintf("  %d %s · git repos first", total, noun))
	}
}

// contentWidth is the inner width of the panel (and the list inside it).
func (m *FolderPickerModel) contentWidth() int {
	w := m.width - 10 // panel borders, padding, and breathing room
	if w > 50 {
		w = 50
	}
	if w < 24 {
		w = 24
	}
	return w
}

// layout re-derives the input and list dimensions from the window size.
func (m *FolderPickerModel) layout() {
	w := m.contentWidth()
	m.input.Width = w - lipgloss.Width(m.input.Prompt) - 1
	rows := (m.height - folderPanelChromeRows) / folderRowHeight
	if rows > 6 {
		rows = 6
	}
	if rows < 2 {
		rows = 2
	}
	m.list.SetSize(w, rows*folderRowHeight+1) // +1: the pagination line
}

func (m *FolderPickerModel) SetSize(w, h int) {
	m.width, m.height = w, h
	m.layout()
}

// collapseHomePath shortens /home/u/... to ~/... for display.
func collapseHomePath(p, home string) string {
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// folderDelegate renders folder rows like list.DefaultDelegate — prominent
// title, dimmed description, accent bar on the selected row, underlined
// filter matches — and appends a styled "git" chip to repositories, which the
// stock delegate can't do without mangling ANSI inside the title.
type folderDelegate struct {
	normalTitle   lipgloss.Style
	normalDesc    lipgloss.Style
	selectedTitle lipgloss.Style
	selectedDesc  lipgloss.Style
	match         lipgloss.Style
	badge         lipgloss.Style
}

func newFolderDelegate() folderDelegate {
	bar := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(ColorPrimary).
		Padding(0, 0, 0, 1)
	return folderDelegate{
		normalTitle:   lipgloss.NewStyle().Padding(0, 0, 0, 2),
		normalDesc:    lipgloss.NewStyle().Foreground(ColorMuted).Padding(0, 0, 0, 2),
		selectedTitle: bar.Foreground(ColorPrimary).Bold(true),
		selectedDesc:  bar.Foreground(ColorSecondary),
		match:         lipgloss.NewStyle().Underline(true),
		badge:         lipgloss.NewStyle().Foreground(ColorSuccess),
	}
}

func (d folderDelegate) Height() int                             { return 2 }
func (d folderDelegate) Spacing() int                            { return 1 }
func (d folderDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d folderDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(folderItem)
	if !ok || m.Width() <= 0 {
		return
	}

	badge := ""
	if it.isGit {
		badge = " " + d.badge.Render(Icon("⏺", "*")+" git")
	}

	textw := m.Width() - 2 // both row styles consume two leading columns
	title := truncateRunes(it.Title(), textw-lipgloss.Width(badge))
	desc := truncateRunes(it.Description(), textw)

	titleStyle, descStyle := d.normalTitle, d.normalDesc
	if index == m.Index() {
		titleStyle, descStyle = d.selectedTitle, d.selectedDesc
	}

	// Underline the runes matched by the active filter. The indices come from
	// the list's fuzzy filter and refer to FilterValue(), which is the same
	// string as the title.
	if state := m.FilterState(); state == list.Filtering || state == list.FilterApplied {
		un := titleStyle.Inline(true)
		title = lipgloss.StyleRunes(title, m.MatchesForItem(index), un.Inherit(d.match), un)
	}

	fmt.Fprintf(w, "%s%s\n%s", titleStyle.Render(title), badge, descStyle.Render(desc))
}
