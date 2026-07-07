package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/pipeline"
)

// WorkflowConfigModel is a compact, per-project editor for a workflow's steps:
// one row per step with a horizontal executor/model selector. It edits the same
// persisted config as `ty pipeline config`, so a choice made here is what every
// workflow in the project defaults to.
type WorkflowConfigModel struct {
	db      *db.DB
	project string
	def     pipeline.Definition

	stepNames []string
	stepDeps  [][]string
	values    []string // selectable option values, e.g. "claude/opus", "codex"
	labels    []string // display labels aligned with values
	sel       []int    // per-step index into values
	focused   int

	width, height int
	saved         bool
	cancelled     bool
}

// NewWorkflowConfigModel builds the editor for a project, prefilled with its
// current effective step config.
func NewWorkflowConfigModel(database *db.DB, project string, available []string, width, height int) *WorkflowConfigModel {
	def, _ := pipeline.Get(pipeline.DefaultDefinition)
	values, labels := workflowStepChoices(available)
	cfg := pipeline.EffectiveConfig(database, project, def)

	names := make([]string, len(cfg))
	deps := make([][]string, len(cfg))
	sel := make([]int, len(cfg))
	for i, c := range cfg {
		names[i] = c.Name
		if i < len(def.Steps) {
			deps[i] = def.Steps[i].Deps
		}
		cur := stepChoiceValue(c.Executor, c.Model)
		sel[i] = indexOfString(values, cur) // -1 → clamped to 0 below
		if sel[i] < 0 {
			sel[i] = 0
		}
	}
	return &WorkflowConfigModel{
		db: database, project: project, def: def,
		stepNames: names, stepDeps: deps, values: values, labels: labels,
		sel: sel, width: width, height: height,
	}
}

func (m *WorkflowConfigModel) Init() tea.Cmd { return nil }

func (m *WorkflowConfigModel) Update(msg tea.Msg) (*WorkflowConfigModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "esc", "q", "ctrl+c":
		m.cancelled = true
	case "up", "k":
		if m.focused > 0 {
			m.focused--
		}
	case "down", "j", "tab":
		if m.focused < len(m.stepNames)-1 {
			m.focused++
		}
	case "left", "h":
		if n := len(m.values); n > 0 {
			m.sel[m.focused] = (m.sel[m.focused] - 1 + n) % n
		}
	case "right", "l":
		if n := len(m.values); n > 0 {
			m.sel[m.focused] = (m.sel[m.focused] + 1) % n
		}
	case "enter", "s":
		m.saved = true
	}
	return m, nil
}

// Config returns the edited per-step configuration.
func (m *WorkflowConfigModel) Config() []pipeline.StepConfig {
	out := make([]pipeline.StepConfig, len(m.stepNames))
	for i, name := range m.stepNames {
		exec, model := parseStepChoice(m.values[m.sel[i]])
		out[i] = pipeline.StepConfig{Name: name, Executor: exec, Model: model}
	}
	return out
}

func (m *WorkflowConfigModel) View() string {
	header := lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary).
		Render("⇄ Workflow config · " + m.project)
	sub := Dim.Render("plan → code → review ∥ review → collect · each step's executor & model")

	// Widest step name, so the selectors line up.
	nameW := 0
	for _, n := range m.stepNames {
		if len(n) > nameW {
			nameW = len(n)
		}
	}

	var rows []string
	for i, name := range m.stepNames {
		cursor := "  "
		nameStyle := lipgloss.NewStyle().Foreground(ColorMuted)
		if i == m.focused {
			cursor = FgStyle(ColorSecondary).Render("▸ ")
			nameStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary)
		}
		label := m.labels[m.sel[i]]
		var valStr string
		if i == m.focused {
			valStr = FgStyle(ColorInProgress).Render("‹ " + label + " ›")
		} else {
			valStr = lipgloss.NewStyle().Render("  " + label + "  ")
		}
		padded := name + strings.Repeat(" ", nameW-len(name))
		dep := ""
		if len(m.stepDeps[i]) > 0 {
			dep = Dim.Render("  ← " + strings.Join(m.stepDeps[i], "+"))
		}
		rows = append(rows, cursor+nameStyle.Render(padded)+"   "+valStr+dep)
	}

	help := Dim.Render("↑/↓ step · ←/→ change · enter save · esc cancel")
	body := lipgloss.JoinVertical(lipgloss.Left,
		header, sub, "", strings.Join(rows, "\n"), "", help)

	modalWidth := min(64, m.width-8)
	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSecondary).
		Padding(1, 2).
		Width(modalWidth)
	return lipgloss.NewStyle().
		Width(m.width).Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(modalBox.Render(body))
}

// workflowStepChoices builds the executor/model options for a workflow step: the
// installed Claude models plus each other installed executor at its default
// model. Returns aligned value/label slices.
func workflowStepChoices(available []string) (values, labels []string) {
	has := func(e string) bool {
		if len(available) == 0 {
			return true
		}
		for _, a := range available {
			if a == e {
				return true
			}
		}
		return false
	}
	if has(db.ExecutorClaude) {
		for _, mdl := range []string{db.ModelOpus, db.ModelSonnet, db.ModelHaiku} {
			values = append(values, db.ExecutorClaude+"/"+mdl)
			labels = append(labels, "claude / "+mdl)
		}
	}
	for _, e := range []string{db.ExecutorCodex, db.ExecutorGemini, db.ExecutorPi, db.ExecutorOpenCode, db.ExecutorOpenClaw} {
		if has(e) {
			values = append(values, e)
			labels = append(labels, e)
		}
	}
	return values, labels
}

// stepChoiceValue encodes a step's executor+model as an option value.
func stepChoiceValue(executor, model string) string {
	if model != "" {
		return executor + "/" + model
	}
	return executor
}

// parseStepChoice splits an "executor/model" (or bare "executor") option value.
func parseStepChoice(v string) (executor, model string) {
	if i := strings.Index(v, "/"); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func indexOfString(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}
