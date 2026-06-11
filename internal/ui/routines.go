package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/routine"
)

// RoutinesModel is a read-only fleet-health view over all routines: name,
// target project, model, enabled state, and last run outcome. Routines are a
// global namespace (a routine *targets* a project, it doesn't belong to one),
// so this view is global too — a dead routine must be visible even when its
// project hasn't been opened in weeks.
type RoutinesModel struct {
	database *db.DB
	width    int
	height   int

	routines  []*routine.Routine
	latest    map[string]*db.RoutineRun
	schedules map[string]*routine.Schedule
	cursor    int
	loadErr   error

	// viewingLog switches to a scrollable view of the selected routine's
	// latest run log.
	viewingLog bool
	logTitle   string
	viewport   viewport.Model

	// done signals the app to return to the dashboard.
	done bool
}

// NewRoutinesModel loads routine definitions and their latest runs.
func NewRoutinesModel(database *db.DB, width, height int) *RoutinesModel {
	m := &RoutinesModel{
		database: database,
		width:    width,
		height:   height,
	}
	m.reload()
	return m
}

func (m *RoutinesModel) reload() {
	m.loadErr = nil
	routines, err := routine.List()
	if err != nil {
		m.loadErr = err
		return
	}
	m.routines = routines
	latest, err := m.database.LatestRoutineRuns()
	if err != nil {
		m.loadErr = err
		return
	}
	m.latest = latest

	names := make([]string, len(routines))
	for i, rt := range routines {
		names[i] = rt.Name
	}
	// Live OS-scheduler lookup — ty keeps no schedule state of its own.
	schedules, err := routine.LoadSchedules(names)
	if err != nil {
		m.loadErr = err
		return
	}
	m.schedules = schedules
	if m.cursor >= len(m.routines) {
		m.cursor = max(0, len(m.routines)-1)
	}
}

// Init implements tea.Model.
func (m *RoutinesModel) Init() tea.Cmd {
	return nil
}

// SetSize updates the view dimensions.
func (m *RoutinesModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	if m.viewingLog {
		m.viewport.Width = width - 4
		m.viewport.Height = height - 6
	}
}

// Update implements tea.Model.
func (m *RoutinesModel) Update(msg tea.Msg) (*RoutinesModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.viewingLog {
		switch keyMsg.String() {
		case "esc", "q":
			m.viewingLog = false
			return m, nil
		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	switch keyMsg.String() {
	case "esc", "q":
		m.done = true
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.routines)-1 {
			m.cursor++
		}
	case "enter":
		m.openLog()
	case "d":
		m.toggleDisabled()
	case "r":
		m.reload()
	}
	return m, nil
}

func (m *RoutinesModel) selected() *routine.Routine {
	if m.cursor < 0 || m.cursor >= len(m.routines) {
		return nil
	}
	return m.routines[m.cursor]
}

func (m *RoutinesModel) toggleDisabled() {
	rt := m.selected()
	if rt == nil {
		return
	}
	if err := rt.SetDisabled(!rt.Disabled); err != nil {
		m.loadErr = err
		return
	}
	m.reload()
}

func (m *RoutinesModel) openLog() {
	rt := m.selected()
	if rt == nil {
		return
	}
	run, ok := m.latest[rt.Name]
	if !ok {
		return
	}

	content := run.Output
	if data, err := os.ReadFile(run.LogPath); err == nil {
		content = string(data)
	}
	if strings.TrimSpace(content) == "" {
		content = "(no output)"
	}

	m.logTitle = fmt.Sprintf("%s — run #%d (%s)", rt.Name, run.ID, run.Status)
	m.viewport = viewport.New(m.width-4, m.height-6)
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
	m.viewingLog = true
}

// View implements tea.Model.
func (m *RoutinesModel) View() string {
	if m.viewingLog {
		return m.logView()
	}
	return m.tableView()
}

func (m *RoutinesModel) logView() string {
	var b strings.Builder
	b.WriteString(Title.Render(m.logTitle) + "\n\n")
	b.WriteString(m.viewport.View() + "\n\n")
	b.WriteString(Dim.Render("↑/↓ scroll • esc: back"))
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

func (m *RoutinesModel) tableView() string {
	var b strings.Builder
	b.WriteString(Title.Render("Routines") + "\n")
	b.WriteString(Dim.Render("Named unattended agent runs — scheduled externally via `ty run <name>`") + "\n\n")

	if m.loadErr != nil {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorError).Render("Error: "+m.loadErr.Error()) + "\n\n")
	}

	if len(m.routines) == 0 {
		b.WriteString(Dim.Render("No routines yet. Create one with: ty routines create <name>") + "\n")
	} else {
		header := fmt.Sprintf("  %-20s %-10s %-7s %-9s %-12s %s", "NAME", "PROJECT", "MODEL", "STATE", "SCHEDULE", "LAST RUN")
		b.WriteString(Dim.Render(header) + "\n")
		b.WriteString(Dim.Render("  "+strings.Repeat("─", min(m.width-4, 84))) + "\n")

		for i, rt := range m.routines {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}

			state := lipgloss.NewStyle().Foreground(ColorSuccess).Render(fmt.Sprintf("%-9s", "enabled"))
			if rt.Disabled {
				state = Dim.Render(fmt.Sprintf("%-9s", "disabled"))
			}

			project := rt.Project
			if project == "" {
				project = "personal"
			}

			schedule := "—"
			if sched := m.schedules[rt.Name]; sched != nil {
				schedule = sched.Detail
			}

			line := fmt.Sprintf("%s%-20s %-10s %-7s %s %-12s %s",
				cursor, truncateRunes(rt.Name, 20), truncateRunes(project, 10), truncateRunes(rt.Model, 7),
				state, truncateRunes(schedule, 12), m.renderLastRun(rt.Name))
			if i == m.cursor {
				line = lipgloss.NewStyle().Bold(true).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n" + Dim.Render("enter: view log • d: enable/disable • r: refresh • esc: back"))
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

func (m *RoutinesModel) renderLastRun(name string) string {
	run, ok := m.latest[name]
	if !ok {
		return Dim.Render("never run")
	}

	var status string
	switch run.Status {
	case db.RoutineRunStatusOK:
		status = lipgloss.NewStyle().Foreground(ColorSuccess).Render("ok")
	case db.RoutineRunStatusFailed:
		status = lipgloss.NewStyle().Foreground(ColorError).Render("failed")
	default:
		status = lipgloss.NewStyle().Foreground(ColorWarning).Render(run.Status)
	}

	detail := relativeAge(run.StartedAt.Time) + " ago"
	if run.FinishedAt != nil {
		detail += fmt.Sprintf(" · %s", run.FinishedAt.Sub(run.StartedAt.Time).Round(time.Second))
	}
	return status + " " + Dim.Render(detail)
}

func relativeAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
