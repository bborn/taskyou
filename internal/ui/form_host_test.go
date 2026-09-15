package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// hostForm builds a new-task form with the placement options a plugin would
// have answered with, without running one.
func hostForm(choices []hostChoice) *FormModel {
	m := NewFormModel(nil, 120, 40, "", []string{"claude"})
	m.showAdvanced = true
	m.project = "taskyou"
	m.hostChoices = choices
	return m
}

func offeredHosts() []hostChoice {
	return []hostChoice{
		{Label: "automatic"},
		{Label: "this machine", Target: "local"},
		{Label: "mona", Target: "mona", WorkDir: "~/Projects/taskyou"},
		{Label: "ol-agents", Target: "agents.example", WorkDir: "~/projects/taskyou"},
	}
}

// Nobody offered a choice of machines, so the form must be the form it has
// always been: no Host field, and nothing recorded when it is submitted.
func TestFormHidesTheHostFieldWithNoHosts(t *testing.T) {
	m := hostForm(nil)

	if m.isFieldVisible(FieldHost) {
		t.Error("the Host field is visible with no hosts offered")
	}
	if strings.Contains(m.View(), "Host") {
		t.Error("the form renders a Host selector with no hosts offered")
	}
	if target, dir := m.PlacementChoice(); target != "" || dir != "" {
		t.Errorf("PlacementChoice() = (%q, %q), want the automatic answer", target, dir)
	}
}

// With hosts offered, the field appears and defaults to automatic — the
// behaviour of every task created before this existed.
func TestFormDefaultsToAutomaticPlacement(t *testing.T) {
	m := hostForm(offeredHosts())

	if !m.isFieldVisible(FieldHost) {
		t.Fatal("the Host field is hidden even though hosts were offered")
	}
	if !strings.Contains(m.View(), "Host") {
		t.Error("the form does not render the Host selector")
	}
	if target, dir := m.PlacementChoice(); target != "" || dir != "" {
		t.Errorf("PlacementChoice() = (%q, %q), want automatic by default", target, dir)
	}
}

// Picking a host hands back both the destination and that project's directory
// on it, so nobody has to look the path up to place a task there.
func TestFormPicksAHostAndItsDirectory(t *testing.T) {
	m := hostForm(offeredHosts())
	m.focused = FieldHost

	for i := 0; i < 2; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}

	target, dir := m.PlacementChoice()
	if target != "mona" || dir != "~/Projects/taskyou" {
		t.Fatalf("PlacementChoice() = (%q, %q), want mona and its checkout", target, dir)
	}
}

// "This machine" is a real choice and has to survive as one: it pins the task
// here rather than leaving the resolver to answer.
func TestFormCanPinToThisMachine(t *testing.T) {
	m := hostForm(offeredHosts())
	m.focused = FieldHost

	m.Update(tea.KeyMsg{Type: tea.KeyRight})

	if target, _ := m.PlacementChoice(); target != "local" {
		t.Fatalf("PlacementChoice() = %q, want local", target)
	}
}

// Typing a letter jumps to the matching machine, like every other selector in
// this form.
func TestFormTypeToSelectAHost(t *testing.T) {
	m := hostForm(offeredHosts())
	m.focused = FieldHost

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if target, _ := m.PlacementChoice(); target != "agents.example" {
		t.Fatalf("PlacementChoice() = %q, want the host whose name starts with o", target)
	}
}

// The lookup is out-of-process, so an answer can arrive after the user has moved
// on. One for another project must not repopulate the selector.
func TestFormIgnoresHostsForAnotherProject(t *testing.T) {
	m := hostForm(nil)

	m.Update(hostsLoadedMsg{project: "something-else", choices: offeredHosts()})

	if len(m.hostChoices) != 0 {
		t.Fatalf("hostChoices = %+v, want none: that answer was for another project", m.hostChoices)
	}
}

// A project change reloads the list, and a machine that does not serve the new
// project must not stay selected just because it held the same index.
func TestFormDropsAHostTheNewProjectDoesNotServe(t *testing.T) {
	m := hostForm(offeredHosts())
	m.hostIdx = 2 // mona

	m.Update(hostsLoadedMsg{project: "taskyou", choices: []hostChoice{
		{Label: "automatic"},
		{Label: "this machine", Target: "local"},
		{Label: "rex", Target: "rex", WorkDir: "/root/other"},
	}})

	if target, _ := m.PlacementChoice(); target != "" {
		t.Fatalf("PlacementChoice() = %q, want automatic once the chosen host is gone", target)
	}
}

// A host still offered after a reload keeps the selection: reloading happens on
// every executor change, and silently un-choosing the machine someone picked
// would be worse than not offering the choice at all.
func TestFormKeepsAHostThatIsStillOffered(t *testing.T) {
	m := hostForm(offeredHosts())
	m.hostIdx = 2 // mona

	m.Update(hostsLoadedMsg{project: "taskyou", choices: offeredHosts()})

	if target, _ := m.PlacementChoice(); target != "mona" {
		t.Fatalf("PlacementChoice() = %q, want mona kept", target)
	}
}

// When the hosts go away while the field has the focus, the focus has to go
// somewhere real.
func TestFormMovesFocusOffAVanishedHostField(t *testing.T) {
	m := hostForm(offeredHosts())
	m.focused = FieldHost

	m.Update(hostsLoadedMsg{project: "taskyou"})

	if m.focused == FieldHost {
		t.Error("focus stayed on the Host field after it disappeared")
	}
}
