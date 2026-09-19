package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/hooks"
	"github.com/bborn/workflow/internal/registry"
)

// The plugin browser is the answer to "how was I supposed to know
// github.com/taskyou/plugins existed?". It is a searchable catalog with install
// and remove on a keystroke, so nobody has to leave the board, find a URL, and
// come back through the CLI to get a workflow.
//
// One list holds both halves of the story: catalog entries (installable) and
// installed plugins (removable, catalogued or hand-copied). Search filters the
// whole thing, so the same view answers "what can I add?" and "what do I have?".

// PluginScope is which slice of the list is shown.
type PluginScope int

const (
	// ScopeAll shows the catalog plus anything installed from outside it.
	ScopeAll PluginScope = iota
	// ScopeInstalled shows only what is on disk.
	ScopeInstalled
	// ScopeAvailable shows only catalog entries that are not installed yet.
	ScopeAvailable
)

func (s PluginScope) String() string {
	switch s {
	case ScopeInstalled:
		return "Installed"
	case ScopeAvailable:
		return "Available"
	default:
		return "All"
	}
}

func (s PluginScope) next() PluginScope { return (s + 1) % 3 }

// PluginRow is one line in the browser: a catalog entry, an installed plugin, or
// both when an installed plugin is also catalogued.
type PluginRow struct {
	Entry     *registry.Entry
	Installed *hooks.Plugin
	// Dir is the installed directory's base name, when installed.
	Dir string
}

// ID is the row's stable handle: the catalog ID when there is one, else the
// installed plugin's manifest name.
func (r PluginRow) ID() string {
	if r.Entry != nil {
		return r.Entry.ID
	}
	if r.Installed != nil {
		return r.Installed.Name
	}
	return ""
}

// Title is what the row shows as its name.
func (r PluginRow) Title() string {
	if r.Entry != nil {
		return r.Entry.DisplayName()
	}
	if r.Installed != nil {
		return r.Installed.Name
	}
	return ""
}

// Description prefers the catalog copy (written for a reader deciding whether to
// install) over the manifest's, which is often a note to the plugin's own author.
func (r PluginRow) Description() string {
	if r.Entry != nil && r.Entry.Description != "" {
		return strings.Join(strings.Fields(r.Entry.Description), " ")
	}
	if r.Installed != nil {
		return strings.Join(strings.Fields(r.Installed.Description), " ")
	}
	return ""
}

// Category is the catalog category, or "installed" for an uncatalogued plugin.
func (r PluginRow) Category() string {
	if r.Entry != nil && r.Entry.Category != "" {
		return r.Entry.Category
	}
	if r.Installed != nil {
		return "local"
	}
	return ""
}

// IsInstalled reports whether the row is on disk.
func (r PluginRow) IsInstalled() bool { return r.Installed != nil }

// PluginBrowserModel is the searchable plugin catalog view.
type PluginBrowserModel struct {
	width, height int

	search textinput.Model
	scope  PluginScope

	entries []registry.Entry
	plugins []hooks.Plugin
	rows    []PluginRow
	cursor  int
	offset  int

	loading bool
	loadErr error
	stale   bool

	// busyID is the row being installed or removed, so the list can say so and
	// a second Enter can't start the same clone twice.
	busyID  string
	busyVrb string

	// status is the last install/remove outcome, shown under the list.
	status    string
	statusErr bool

	// confirmRemove holds the plugin name awaiting a y/n confirmation.
	confirmRemove string

	done bool
}

// browserLoad loads the catalog and the installed plugins. A var so tests don't
// depend on the machine's plugins dir or the network.
var browserLoad = func(ctx context.Context, refresh bool) ([]registry.Entry, []hooks.Plugin, bool, error) {
	loader := registry.Default()
	res := loader.Load(ctx)
	if refresh {
		res = loader.Refresh(ctx)
	}
	plugins, _ := hooks.LoadPlugins(hooks.DefaultPluginsDir())
	var err error
	for _, o := range res.Origins {
		if o.Err != nil && o.URL != registry.BundledOrigin {
			err = o.Err
		}
	}
	if len(res.Entries) > 0 {
		// A stale or unreachable catalog is worth a note, not an error: the
		// bundled snapshot still lists everything the binary shipped knowing.
		err = nil
	}
	return res.Entries, plugins, res.Stale(), err
}

// browserInstall installs one plugin. A var for tests.
var browserInstall = func(ctx context.Context, req hooks.InstallRequest) (hooks.InstallResult, error) {
	return hooks.Install(ctx, hooks.DefaultPluginsDir(), req)
}

// browserRemove uninstalls one plugin. A var for tests.
var browserRemove = func(name string) error {
	_, _, err := hooks.Remove(name, hooks.DefaultPluginsDir())
	return err
}

// pluginCatalogMsg carries a finished catalog load.
type pluginCatalogMsg struct {
	entries []registry.Entry
	plugins []hooks.Plugin
	stale   bool
	err     error
}

// pluginInstalledMsg carries the result of an install.
type pluginInstalledMsg struct {
	id     string
	result hooks.InstallResult
	err    error
}

// pluginRemovedMsg carries the result of a remove.
type pluginRemovedMsg struct {
	name string
	err  error
}

// NewPluginBrowserModel creates the browser and starts the catalog load.
func NewPluginBrowserModel(width, height int) *PluginBrowserModel {
	in := textinput.New()
	in.Placeholder = "search plugins — name, what it does, a tag…"
	in.Prompt = "  "
	in.CharLimit = 64
	in.Focus()

	m := &PluginBrowserModel{width: width, height: height, search: in, loading: true}
	return m
}

// Init starts the first catalog load.
func (m *PluginBrowserModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.loadCmd(false))
}

func (m *PluginBrowserModel) loadCmd(refresh bool) tea.Cmd {
	return func() tea.Msg {
		// Bounded: a hung catalog host must not wedge the view's loading state.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		entries, plugins, stale, err := browserLoad(ctx, refresh)
		return pluginCatalogMsg{entries: entries, plugins: plugins, stale: stale, err: err}
	}
}

func (m *PluginBrowserModel) installCmd(req hooks.InstallRequest, id string) tea.Cmd {
	return func() tea.Msg {
		// Generous: a cold clone of a big collection repo is slow, and an install
		// the user asked for should not be cancelled out from under them.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := browserInstall(ctx, req)
		return pluginInstalledMsg{id: id, result: res, err: err}
	}
}

func (m *PluginBrowserModel) removeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		return pluginRemovedMsg{name: name, err: browserRemove(name)}
	}
}

// SetSize updates the view dimensions.
func (m *PluginBrowserModel) SetSize(width, height int) {
	m.width, m.height = width, height
	m.search.Width = max(10, width-10)
	m.clampCursor()
}

// Update handles input and the async load/install/remove results.
func (m *PluginBrowserModel) Update(msg tea.Msg) (*PluginBrowserModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pluginCatalogMsg:
		m.loading = false
		m.loadErr = msg.err
		m.stale = msg.stale
		m.entries = msg.entries
		m.plugins = msg.plugins
		m.rebuild()
		return m, nil

	case pluginInstalledMsg:
		m.busyID, m.busyVrb = "", ""
		if msg.err != nil {
			m.status, m.statusErr = installFailureText(msg.id, msg.err), true
			return m, nil
		}
		m.status, m.statusErr = installSuccessText(msg.result), false
		// Re-read the plugins dir so the row flips to installed. The workflow and
		// routine registries resolve plugin dirs on every lookup, so a new
		// plugin's cargo is live without any invalidation here.
		return m, m.loadCmd(false)

	case pluginRemovedMsg:
		m.busyID, m.busyVrb = "", ""
		if msg.err != nil {
			m.status, m.statusErr = msg.err.Error(), true
			return m, nil
		}
		m.status, m.statusErr = fmt.Sprintf("Removed %s.", msg.name), false
		return m, m.loadCmd(false)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *PluginBrowserModel) handleKey(msg tea.KeyMsg) (*PluginBrowserModel, tea.Cmd) {
	if m.confirmRemove != "" {
		switch msg.String() {
		case "y", "Y", "enter":
			name := m.confirmRemove
			m.confirmRemove = ""
			m.busyID, m.busyVrb = name, "Removing"
			return m, m.removeCmd(name)
		default:
			m.confirmRemove = ""
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		// Esc clears a search before it leaves the view: a typed query is work
		// the user may want back, and losing the whole view to one keystroke is
		// the thing that makes a search box feel hostile.
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.rebuild()
			return m, nil
		}
		m.done = true
		return m, nil
	case "up", "ctrl+p":
		m.move(-1)
		return m, nil
	case "down", "ctrl+n":
		m.move(1)
		return m, nil
	case "pgup":
		m.move(-m.visibleRows())
		return m, nil
	case "pgdown":
		m.move(m.visibleRows())
		return m, nil
	case "tab":
		m.scope = m.scope.next()
		m.rebuild()
		return m, nil
	case "shift+tab":
		m.scope = m.scope.next().next()
		m.rebuild()
		return m, nil
	case "ctrl+r":
		m.loading = true
		m.status = ""
		return m, m.loadCmd(true)
	case "enter":
		return m, m.installSelected()
	case "ctrl+d", "delete":
		row, ok := m.Selected()
		if !ok || !row.IsInstalled() {
			return m, nil
		}
		m.confirmRemove = row.Installed.Name
		return m, nil
	}

	var cmd tea.Cmd
	before := m.search.Value()
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != before {
		m.rebuild()
	}
	return m, cmd
}

// installSelected starts an install (or re-install) of the highlighted row.
func (m *PluginBrowserModel) installSelected() tea.Cmd {
	row, ok := m.Selected()
	if !ok || m.busyID != "" {
		return nil
	}
	if row.Entry == nil {
		// Installed but not catalogued: there is no source to install from, so
		// say that rather than silently doing nothing.
		m.status, m.statusErr = fmt.Sprintf("%s is installed locally and not in the catalog — "+
			"update it with `ty plugins update %s`.", row.Title(), row.ID()), true
		return nil
	}
	verb := "Installing"
	if row.IsInstalled() {
		verb = "Updating"
	}
	m.busyID, m.busyVrb = row.ID(), verb
	m.status = ""
	return m.installCmd(hooks.InstallRequest{
		ID:     row.Entry.ID,
		Source: row.Entry.Source,
		Subdir: row.Entry.Subdir,
		Name:   row.Entry.ID,
		Entry:  row.Entry,
	}, row.ID())
}

// installSuccessText summarizes an install in one line, naming what the user can
// now run — the part a bare "Installed" leaves them to guess.
func installSuccessText(res hooks.InstallResult) string {
	text := fmt.Sprintf("%s %s.", res.Verb(), strings.Join(res.Plugins, ", "))
	var hints []string
	plugins, _ := hooks.LoadPlugins(hooks.DefaultPluginsDir())
	for _, p := range plugins {
		if filepath.Base(p.Dir) != res.Name {
			continue
		}
		for _, w := range p.Workflows {
			hints = append(hints, "ty pipeline -d "+w)
		}
		for _, r := range p.Routines {
			hints = append(hints, "ty run "+r)
		}
		if len(p.Actions) > 0 {
			hints = append(hints, "press A on a task")
		}
		if len(p.Hooks) > 0 {
			hints = append(hints, "fires on task events")
		}
	}
	if len(hints) > 0 {
		text += " → " + strings.Join(hints, " · ")
	}
	return text
}

// installFailureText keeps the useful first line of a git failure and points at
// the CLI, which shows the whole thing.
func installFailureText(id string, err error) string {
	first, _, _ := strings.Cut(err.Error(), "\n")
	return fmt.Sprintf("%s failed: %s (see `ty plugins add %s`)", id, first, id)
}

// Selected returns the highlighted row.
func (m *PluginBrowserModel) Selected() (PluginRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return PluginRow{}, false
	}
	return m.rows[m.cursor], true
}

// Done reports whether the view asked to close.
func (m *PluginBrowserModel) Done() bool { return m.done }

// Rows returns the currently filtered rows (exported for tests).
func (m *PluginBrowserModel) Rows() []PluginRow { return m.rows }

// SetQuery sets the search text and refilters — used when the view is opened
// with a query already in hand (e.g. from the command palette).
func (m *PluginBrowserModel) SetQuery(q string) {
	m.search.SetValue(q)
	m.search.CursorEnd()
	m.rebuild()
}

// rebuild recomputes the visible rows from the catalog, what's installed, the
// scope, and the search query — in that order, so search always searches the
// union and the scope only narrows it.
func (m *PluginBrowserModel) rebuild() {
	dir := hooks.DefaultPluginsDir()
	byID := map[string]*hooks.Plugin{}
	byDir := map[string]*hooks.Plugin{}
	for i := range m.plugins {
		p := &m.plugins[i]
		byID[strings.ToLower(p.Name)] = p
		byDir[strings.ToLower(filepath.Base(p.Dir))] = p
	}

	claimed := map[string]bool{}
	var rows []PluginRow
	for i := range m.entries {
		e := m.entries[i]
		row := PluginRow{Entry: &m.entries[i]}
		if installedDir, ok := hooks.IsInstalled(dir, e.ID, m.plugins); ok {
			row.Dir = installedDir
			if p := byDir[strings.ToLower(installedDir)]; p != nil {
				row.Installed = p
				claimed[p.Dir] = true
			}
		}
		rows = append(rows, row)
	}
	// Anything installed that no catalog entry claims still belongs here — a
	// hand-written plugin is exactly what this view should help you manage.
	var extra []PluginRow
	for i := range m.plugins {
		p := &m.plugins[i]
		if claimed[p.Dir] {
			continue
		}
		extra = append(extra, PluginRow{Installed: p, Dir: filepath.Base(p.Dir)})
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Title() < extra[j].Title() })
	rows = append(rows, extra...)

	// Scope, then search.
	filtered := rows[:0:len(rows)]
	for _, r := range rows {
		switch m.scope {
		case ScopeInstalled:
			if !r.IsInstalled() {
				continue
			}
		case ScopeAvailable:
			if r.IsInstalled() {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	m.rows = searchRows(filtered, m.search.Value())
	m.clampCursor()
}

// searchRows ranks rows against a query, reusing the catalog scorer so the TUI,
// the CLI and the API all rank the same way. A row with no catalog entry is
// scored against a synthesized entry built from its manifest.
func searchRows(rows []PluginRow, query string) []PluginRow {
	if strings.TrimSpace(query) == "" {
		return rows
	}
	synth := make([]registry.Entry, len(rows))
	// Two hand-written plugins can share a manifest name, so map an ID back to
	// every row that claimed it and consume them in order rather than losing one.
	index := map[string][]int{}
	for i, r := range rows {
		if r.Entry != nil {
			synth[i] = *r.Entry
		} else {
			synth[i] = registry.Entry{
				ID:          r.ID(),
				Name:        r.Title(),
				Description: r.Description(),
				Category:    "local",
				Source:      "local",
			}
		}
		key := strings.ToLower(synth[i].ID)
		index[key] = append(index[key], i)
	}
	ranked := registry.SearchRanked(synth, query)
	out := make([]PluginRow, 0, len(ranked))
	for _, match := range ranked {
		key := strings.ToLower(match.Entry.ID)
		if hits := index[key]; len(hits) > 0 {
			out = append(out, rows[hits[0]])
			index[key] = hits[1:]
		}
	}
	return out
}

func (m *PluginBrowserModel) move(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor += delta
	m.clampCursor()
}

func (m *PluginBrowserModel) clampCursor() {
	if len(m.rows) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	visible := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// visibleRows is how many list rows fit, given the fixed chrome above and the
// detail panel below.
func (m *PluginBrowserModel) visibleRows() int {
	const chrome = 16 // title, search box, scope tabs, detail panel, help
	return max(3, m.height-chrome)
}

// View renders the browser: search box, scope tabs, the result list, a detail
// panel for the highlighted row, and the key hints.
func (m *PluginBrowserModel) View() string {
	inner := max(40, min(m.width-4, 110))
	var b strings.Builder

	b.WriteString(m.renderHeader(inner) + "\n")
	b.WriteString(m.renderSearch(inner) + "\n")
	b.WriteString(m.renderScopes() + "\n\n")
	b.WriteString(m.renderList(inner) + "\n")
	if detail := m.renderDetail(inner); detail != "" {
		b.WriteString("\n" + detail + "\n")
	}
	if status := m.renderStatus(inner); status != "" {
		b.WriteString("\n" + status + "\n")
	}
	b.WriteString("\n" + m.renderHelp())

	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

func (m *PluginBrowserModel) renderHeader(width int) string {
	title := Title.Render("Plugins")
	counts := Dim.Render(fmt.Sprintf("%d in catalog · %d installed", len(m.entries), len(m.plugins)))
	gap := max(1, width-lipgloss.Width(title)-lipgloss.Width(counts))
	return title + strings.Repeat(" ", gap) + counts
}

func (m *PluginBrowserModel) renderSearch(width int) string {
	m.search.Width = max(10, width-6)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Width(width - 2).
		Render(m.search.View())
	return box
}

func (m *PluginBrowserModel) renderScopes() string {
	var parts []string
	for _, s := range []PluginScope{ScopeAll, ScopeInstalled, ScopeAvailable} {
		label := " " + s.String() + " "
		if s == m.scope {
			parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Render("["+strings.TrimSpace(label)+"]"))
			continue
		}
		parts = append(parts, Dim.Render(strings.TrimSpace(label)))
	}
	return "  " + strings.Join(parts, "  ")
}

func (m *PluginBrowserModel) renderList(width int) string {
	switch {
	case m.loading && len(m.rows) == 0:
		return Dim.Render("  Loading the plugin catalog…")
	case m.loadErr != nil && len(m.rows) == 0:
		return Error.Render("  Catalog unavailable: " + m.loadErr.Error())
	case len(m.rows) == 0 && m.search.Value() != "":
		return Dim.Render(fmt.Sprintf("  Nothing matches %q. Tab cycles scope; ctrl+r re-fetches the catalog.", m.search.Value()))
	case len(m.rows) == 0:
		return Dim.Render("  No plugins to show.")
	}

	visible := m.visibleRows()
	end := min(len(m.rows), m.offset+visible)
	cols := m.columnWidths(m.rows[m.offset:end], width)
	var b strings.Builder
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderRow(m.rows[i], i == m.cursor, cols))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if len(m.rows) > visible {
		b.WriteString("\n" + Dim.Render(fmt.Sprintf("  %d–%d of %d", m.offset+1, end, len(m.rows))))
	}
	return b.String()
}

// rowColumns are the list's column widths, measured from what is actually on
// screen. Fixed widths left a canyon between a short title and its category on a
// wide terminal; measuring keeps the three columns reading as one table.
type rowColumns struct{ id, title int }

// cursorWidth is the two columns every row's cursor slot occupies. It is a
// constant rather than len(cursor) because ❯ is three bytes and one column —
// measuring in bytes shifted the selected row out of line with the rest.
const cursorWidth = 2

func (m *PluginBrowserModel) columnWidths(rows []PluginRow, width int) rowColumns {
	cols := rowColumns{id: 8, title: 12}
	for _, r := range rows {
		cols.id = max(cols.id, lipgloss.Width(r.ID()))
		cols.title = max(cols.title, lipgloss.Width(r.Title()))
	}
	cols.id = min(cols.id, 26)
	// Whatever is left after the cursor, marker, category and the gaps.
	room := width - cursorWidth - 2 - cols.id - 2 - categoryWidth - 2
	cols.title = min(cols.title, max(12, room))
	return cols
}

// categoryWidth is the fixed right-hand column.
const categoryWidth = 14

// renderRow is one list line: state marker, handle, title, category.
func (m *PluginBrowserModel) renderRow(row PluginRow, selected bool, cols rowColumns) string {
	marker := " "
	markerStyle := Dim
	switch {
	case row.ID() == m.busyID:
		marker, markerStyle = IconProcessing(), Warning
	case row.IsInstalled():
		marker, markerStyle = IconDone(), Success
	}

	cursor := "  "
	if selected {
		cursor = "❯ "
	}

	id := truncateRunes(row.ID(), cols.id)
	title := truncateRunes(row.Title(), cols.title)
	cat := truncateRunes(row.Category(), categoryWidth)

	line := fmt.Sprintf("%s%s %-*s  %-*s  %s",
		cursor, markerStyle.Render(marker), cols.id, id, cols.title, title, Dim.Render(cat))
	if selected {
		return lipgloss.NewStyle().Bold(true).Render(line)
	}
	return strings.TrimRight(line, " ")
}

// renderDetail is the panel under the list: everything needed to decide whether
// to install the highlighted plugin, which is the question this view exists to
// answer.
func (m *PluginBrowserModel) renderDetail(width int) string {
	row, ok := m.Selected()
	if !ok {
		return ""
	}
	wrap := lipgloss.NewStyle().Width(width - 2)
	var b strings.Builder

	head := Bold.Render(row.Title())
	if row.IsInstalled() {
		head += "  " + Success.Render(IconDone()+" installed")
	}
	if row.ID() == m.busyID {
		head += "  " + Warning.Render(m.busyVrb+"…")
	}
	b.WriteString(Dim.Render(strings.Repeat("─", width-2)) + "\n")
	b.WriteString(head + "\n")
	if d := row.Description(); d != "" {
		b.WriteString(wrap.Render(d) + "\n")
	}

	var meta []string
	if row.Entry != nil {
		if len(row.Entry.Provides) > 0 {
			meta = append(meta, "provides "+strings.Join(row.Entry.Provides, ", "))
		}
		if row.Entry.Author != "" {
			meta = append(meta, "by "+row.Entry.Author)
		}
		if len(row.Entry.Tags) > 0 {
			meta = append(meta, strings.Join(row.Entry.Tags, " · "))
		}
	} else if row.Installed != nil {
		meta = append(meta, "provides "+strings.Join(localProvides(*row.Installed), ", "))
		meta = append(meta, "installed locally, not in the catalog")
	}
	if len(meta) > 0 {
		b.WriteString(Dim.Render(wrap.Render(strings.Join(meta, "  ·  "))) + "\n")
	}
	if row.Entry != nil && len(row.Entry.Requires) > 0 {
		b.WriteString(Warning.Render(wrap.Render("needs: "+strings.Join(row.Entry.Requires, "; "))) + "\n")
	}
	if row.Entry != nil {
		src := row.Entry.Source
		if row.Entry.Subdir != "" {
			src += " (" + row.Entry.Subdir + ")"
		}
		b.WriteString(Dim.Render(wrap.Render(src)))
	} else if row.Installed != nil {
		b.WriteString(Dim.Render(wrap.Render(row.Installed.Dir)))
	}
	return b.String()
}

// localProvides describes an installed plugin's capabilities in catalog terms.
func localProvides(p hooks.Plugin) []string {
	var out []string
	if len(p.Hooks) > 0 {
		out = append(out, fmt.Sprintf("%d hook(s)", len(p.Hooks)))
	}
	if len(p.Actions) > 0 {
		out = append(out, fmt.Sprintf("%d action(s)", len(p.Actions)))
	}
	if len(p.Workflows) > 0 {
		out = append(out, "workflows "+strings.Join(p.Workflows, ", "))
	}
	if len(p.Routines) > 0 {
		out = append(out, "routines "+strings.Join(p.Routines, ", "))
	}
	if len(p.Services) > 0 {
		out = append(out, fmt.Sprintf("%d service(s)", len(p.Services)))
	}
	if len(out) == 0 {
		out = append(out, "nothing usable")
	}
	return out
}

func (m *PluginBrowserModel) renderStatus(width int) string {
	if m.confirmRemove != "" {
		return Warning.Render(fmt.Sprintf("Remove %s and delete its directory? (y/n)", m.confirmRemove))
	}
	if m.status == "" {
		if m.stale && !m.loading {
			return Dim.Render("Catalog served from the copy shipped with ty — ctrl+r to re-fetch.")
		}
		return ""
	}
	style := Success
	if m.statusErr {
		style = Error
	}
	return style.Render(lipgloss.NewStyle().Width(width - 2).Render(m.status))
}

func (m *PluginBrowserModel) renderHelp() string {
	row, ok := m.Selected()
	primary := "enter: install"
	if ok && row.IsInstalled() {
		primary = "enter: update"
	}
	return Dim.Render("type to search • " + primary + " • ctrl+d: remove • tab: scope • ctrl+r: refresh • esc: back")
}
