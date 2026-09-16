package ui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/config"
	"github.com/bborn/workflow/internal/db"
)

// How the list is arranged is two choices: what to group by, and how to sort.
// They are a visible widget in the list header and a modal on `O`, rather than
// hidden keybindings, because the arrangement is the thing you fiddle with —
// "group by project today, by status tomorrow" — and a setting you cannot see
// is a setting you forget exists.
//
// Row height is deliberately NOT one of them. "How should this look" is a
// design decision, not a preference to hand the user: a row is one line, and
// grows a second only when the task is actually doing something (see
// renderCompactRow). Offering compact/relaxed would have been us declining to
// decide.

// ListGroupBy is the field the list breaks into sections on.
type ListGroupBy string

const (
	GroupByStatus  ListGroupBy = "status"
	GroupByProject ListGroupBy = "project"
	GroupByNone    ListGroupBy = "none"
)

// ListSort orders tasks within each section.
type ListSort string

const (
	// SortUrgency is "what needs me first": running, blocked, queued, backlog,
	// done — then most recently touched inside each of those.
	SortUrgency ListSort = "urgency"
	SortUpdated ListSort = "updated"
	SortCreated ListSort = "created"
	SortTitle   ListSort = "title"
)

// ListOptions is the arrangement of the list view.
type ListOptions struct {
	GroupBy ListGroupBy
	Sort    ListSort
}

// DefaultListOptions is what a board that has never been configured uses.
func DefaultListOptions() ListOptions {
	return ListOptions{GroupBy: GroupByStatus, Sort: SortUrgency}
}

var (
	groupByCycle = []ListGroupBy{GroupByStatus, GroupByProject, GroupByNone}
	sortCycle    = []ListSort{SortUrgency, SortUpdated, SortCreated, SortTitle}
)

// Normalize replaces unknown values with the defaults, so a hand-edited setting
// can never leave the list in a state with no renderer.
func (o ListOptions) Normalize() ListOptions {
	d := DefaultListOptions()
	if !containsGroupBy(groupByCycle, o.GroupBy) {
		o.GroupBy = d.GroupBy
	}
	if !containsSort(sortCycle, o.Sort) {
		o.Sort = d.Sort
	}
	return o
}

func containsGroupBy(all []ListGroupBy, v ListGroupBy) bool {
	for _, x := range all {
		if x == v {
			return true
		}
	}
	return false
}

func containsSort(all []ListSort, v ListSort) bool {
	for _, x := range all {
		if x == v {
			return true
		}
	}
	return false
}

// Summary is the one-line widget shown under the list header.
func (o ListOptions) Summary() string {
	return "group: " + string(o.GroupBy) + "   sort: " + string(o.Sort)
}

// LoadListOptions reads the arrangement from settings.
func LoadListOptions(get func(string) (string, error)) ListOptions {
	if get == nil {
		return DefaultListOptions()
	}
	read := func(key string) string {
		v, err := get(key)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(v)
	}
	return ListOptions{
		GroupBy: ListGroupBy(read(config.SettingListGroupBy)),
		Sort:    ListSort(read(config.SettingListSort)),
	}.Normalize()
}

// Save writes the arrangement back. Errors are ignored: a lost settings write
// costs a preference next launch, never the current session.
func (o ListOptions) Save(set func(string, string) error) {
	if set == nil {
		return
	}
	_ = set(config.SettingListGroupBy, string(o.GroupBy))
	_ = set(config.SettingListSort, string(o.Sort))
}

// --- grouping and sorting --------------------------------------------------

// pinnedGroupKey is the section pinned tasks lead with, whatever the grouping.
// Pinning means "keep this in sight"; scattering pinned tasks through status or
// project sections is exactly what pinning is meant to prevent, so the rule is
// the same under every grouping rather than a special case under one.
const pinnedGroupKey = "\x00pinned"

// groupKeyFor returns the section a task belongs to.
func (o ListOptions) groupKeyFor(t *db.Task) string {
	if t.Pinned {
		return pinnedGroupKey
	}
	switch o.GroupBy {
	case GroupByProject:
		if t.Project == "" {
			return "\x00noproject"
		}
		return t.Project
	case GroupByNone:
		return ""
	default:
		// The In Progress column covers queued and processing; the list follows
		// suit so a task does not jump sections when it starts running.
		if t.Status == db.StatusProcessing {
			return db.StatusQueued
		}
		return t.Status
	}
}

// groupRank orders the sections themselves.
func (o ListOptions) groupRank(key string) int {
	if key == pinnedGroupKey {
		return -1 // always first
	}
	if o.GroupBy == GroupByStatus {
		return listRank(key)
	}
	return 0 // projects sort by name, handled in Arrange
}

// Arrange returns the tasks in display order for these options.
func (o ListOptions) Arrange(tasks []*db.Task) []*db.Task {
	out := make([]*db.Task, len(tasks))
	copy(out, tasks)

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]

		ka, kb := o.groupKeyFor(a), o.groupKeyFor(b)
		if ka != kb {
			ra, rb := o.groupRank(ka), o.groupRank(kb)
			if ra != rb {
				return ra < rb
			}
			// Same rank means the key itself orders them: project names
			// alphabetically, with "no project" last.
			if ka == "\x00noproject" {
				return false
			}
			if kb == "\x00noproject" {
				return true
			}
			return strings.ToLower(ka) < strings.ToLower(kb)
		}
		return o.less(a, b)
	})
	return out
}

// less orders two tasks inside the same section.
func (o ListOptions) less(a, b *db.Task) bool {
	switch o.Sort {
	case SortUpdated:
		return a.UpdatedAt.Time.After(b.UpdatedAt.Time)
	case SortCreated:
		return a.CreatedAt.Time.After(b.CreatedAt.Time)
	case SortTitle:
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	default: // SortUrgency
		if ra, rb := listRank(a.Status), listRank(b.Status); ra != rb {
			return ra < rb
		}
		return a.UpdatedAt.Time.After(b.UpdatedAt.Time)
	}
}

// sectionTitle is the human label for a group key.
func (o ListOptions) sectionTitle(key string) string {
	switch key {
	case pinnedGroupKey:
		return "Pinned"
	case "\x00noproject":
		return "No project"
	case "":
		return ""
	}
	if o.GroupBy == GroupByStatus {
		return statusLabel(key)
	}
	return key
}

// statusLabel is the human name for a status.
func statusLabel(status string) string {
	switch status {
	case db.StatusProcessing:
		return "Running"
	case db.StatusBlocked:
		return "Blocked"
	case db.StatusQueued:
		return "In progress"
	case db.StatusBacklog:
		return "Backlog"
	case db.StatusDone:
		return "Done"
	case db.StatusArchived:
		return "Archived"
	}
	return status
}

// --- the widget ------------------------------------------------------------

// ListOptionsModel is the modal behind `O`: three rows, left/right cycles the
// value under the cursor, and the list re-renders live so the effect of a
// choice is visible before it is committed.
type ListOptionsModel struct {
	opts     ListOptions
	original ListOptions
	row      int
	width    int
	height   int

	done      bool
	cancelled bool
}

// NewListOptionsModel opens the widget on the current arrangement.
func NewListOptionsModel(opts ListOptions, width, height int) *ListOptionsModel {
	opts = opts.Normalize()
	return &ListOptionsModel{opts: opts, original: opts, width: width, height: height}
}

// Init implements tea.Model.
func (m *ListOptionsModel) Init() tea.Cmd { return nil }

// Update handles key input.
func (m *ListOptionsModel) Update(msg tea.Msg) (*ListOptionsModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "esc", "q":
		m.opts = m.original // live preview is undone on cancel
		m.cancelled = true
	case "enter":
		m.done = true
	case "up", "k":
		m.row = (m.row + 1) % 2
	case "down", "j", "tab":
		m.row = (m.row + 1) % 2
	case "left", "h":
		m.cycle(-1)
	case "right", "l", " ":
		m.cycle(1)
	}
	return m, nil
}

func (m *ListOptionsModel) cycle(delta int) {
	switch m.row {
	case 0:
		i := indexOfGroupBy(m.opts.GroupBy)
		m.opts.GroupBy = groupByCycle[(i+delta+len(groupByCycle))%len(groupByCycle)]
	case 1:
		i := indexOfSort(m.opts.Sort)
		m.opts.Sort = sortCycle[(i+delta+len(sortCycle))%len(sortCycle)]
	}
}

func indexOfGroupBy(v ListGroupBy) int {
	for i, x := range groupByCycle {
		if x == v {
			return i
		}
	}
	return 0
}

func indexOfSort(v ListSort) int {
	for i, x := range sortCycle {
		if x == v {
			return i
		}
	}
	return 0
}

// Options returns the arrangement as it currently stands, including while the
// user is still cycling — the caller renders the list behind the modal with it.
func (m *ListOptionsModel) Options() ListOptions { return m.opts }

// IsDone reports whether the user committed.
func (m *ListOptionsModel) IsDone() bool { return m.done }

// IsCancelled reports whether the user backed out.
func (m *ListOptionsModel) IsCancelled() bool { return m.cancelled }

// SetSize updates dimensions.
func (m *ListOptionsModel) SetSize(width, height int) { m.width, m.height = width, height }

// View renders the modal.
func (m *ListOptionsModel) View() string {
	modalWidth := min(60, m.width-4)

	header := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).
		MarginBottom(1).Render("Arrange list")

	rows := []struct {
		label   string
		value   string
		choices []string
	}{
		{"Group by", string(m.opts.GroupBy), groupByLabels()},
		{"Sort", string(m.opts.Sort), sortLabels()},
	}

	var body strings.Builder
	for i, r := range rows {
		cursor := "  "
		labelStyle := lipgloss.NewStyle().Foreground(ColorMuted)
		if i == m.row {
			cursor = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("> ")
			labelStyle = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
		}

		// Show every choice with the active one marked, so the options are
		// discoverable without cycling blindly through them.
		var opts []string
		for _, c := range r.choices {
			if c == r.value {
				opts = append(opts, lipgloss.NewStyle().Bold(true).
					Foreground(ColorPrimary).Render("["+c+"]"))
				continue
			}
			opts = append(opts, Dim.Render(" "+c+" "))
		}

		body.WriteString(cursor + labelStyle.Render(pad(r.label, 10)) + " " + strings.Join(opts, " "))
		if i < len(rows)-1 {
			body.WriteString("\n")
		}
	}

	help := lipgloss.NewStyle().Foreground(ColorMuted).MarginTop(1).
		Render("←/→ change · ↑/↓ row · enter apply · esc cancel")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary).
		Padding(1, 2).Width(modalWidth)

	return lipgloss.NewStyle().Width(m.width).Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(box.Render(lipgloss.JoinVertical(lipgloss.Left, header, body.String(), help)))
}

func groupByLabels() []string {
	out := make([]string, len(groupByCycle))
	for i, v := range groupByCycle {
		out[i] = string(v)
	}
	return out
}

func sortLabels() []string {
	out := make([]string, len(sortCycle))
	for i, v := range sortCycle {
		out[i] = string(v)
	}
	return out
}
