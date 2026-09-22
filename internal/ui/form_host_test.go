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

	m.Update(hostsLoadedMsg{project: "something-else", executor: "claude", choices: offeredHosts()})

	if len(m.hostChoices) != 0 {
		t.Fatalf("hostChoices = %+v, want none: that answer was for another project", m.hostChoices)
	}
}

// A project change reloads the list, and a machine that does not serve the new
// project must not stay selected just because it held the same index.
func TestFormDropsAHostTheNewProjectDoesNotServe(t *testing.T) {
	m := hostForm(offeredHosts())
	m.hostIdx = 2 // mona

	m.Update(hostsLoadedMsg{project: "taskyou", executor: "claude", choices: []hostChoice{
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

	m.Update(hostsLoadedMsg{project: "taskyou", executor: "claude", choices: offeredHosts()})

	if target, _ := m.PlacementChoice(); target != "mona" {
		t.Fatalf("PlacementChoice() = %q, want mona kept", target)
	}
}

// When the hosts go away while the field has the focus, the focus has to go
// somewhere real.
func TestFormMovesFocusOffAVanishedHostField(t *testing.T) {
	m := hostForm(offeredHosts())
	m.focused = FieldHost

	m.Update(hostsLoadedMsg{project: "taskyou", executor: "claude"})

	if m.focused == FieldHost {
		t.Error("focus stayed on the Host field after it disappeared")
	}
}

// TestStaleHostsArriveAfterClearInProductionOrdering reproduces the production
// race the executor guard exists for. Switching from a remote-capable executor
// (claude) to a non-remote one (gemini) fires loadHosts for the new executor,
// whose PlacementChoices short-circuits to empty immediately; the earlier
// claude lookup, which shells out to a plugin, resolves later. Both messages
// carry the same project, so without an executor on hostsLoadedMsg the stale
// claude answer would repopulate the selector with machines ChoosePlacement
// then refuses on submit. The message now names the executor it was fetched
// for, so the stale claude answer is dropped once the form's executor is gemini.
func TestStaleHostsArriveAfterClearInProductionOrdering(t *testing.T) {
	m := hostForm(offeredHosts())
	m.executor = "claude"
	m.executors = []string{"claude", "gemini"}
	m.executorIdx = 0

	// Keystroke path changes executor to gemini (form.go FieldExecutor right);
	// the loadHosts it fires is modelled here as the two messages it produces,
	// in the order they resolve: gemini first (short-circuit), claude last.
	m.executorIdx = 1
	m.executor = "gemini"

	// 1) gemini's loadHosts resolves immediately: empty choices, field hidden.
	g1, _ := m.Update(hostsLoadedMsg{project: "taskyou", executor: "gemini"})
	m = g1.(*FormModel)
	if m.isFieldVisible(FieldHost) {
		t.Fatal("field should be hidden after gemini (non-remote) cleared choices")
	}

	// 2) claude's stale loadHosts resolves later with its hosts. The executor
	//    guard must drop it: the form's executor is now gemini, so the selector
	//    must stay hidden and PlacementChoice must remain automatic (the
	//    behaviour ChoosePlacement would actually honour for gemini).
	g2, _ := m.Update(hostsLoadedMsg{project: "taskyou", executor: "claude", choices: offeredHosts()})
	m = g2.(*FormModel)

	if m.isFieldVisible(FieldHost) {
		t.Fatal("stale claude answer re-showed the Host selector for a non-remote executor")
	}
	if got := len(m.hostChoices); got != 0 {
		t.Fatalf("hostChoices len = %d, want 0: the stale answer must be dropped, not applied", got)
	}
	if target, _ := m.PlacementChoice(); target != "" {
		t.Fatalf("PlacementChoice() = %q, want automatic after dropping the stale answer", target)
	}
}

// A fresh answer for the executor the user switched to is still applied: the
// guard drops only answers for a *prior* executor, not the one the user is now
// waiting for. This locks in that the guard keys on executor identity rather
// than over-dropping every answer that follows an executor change.
func TestFreshHostsAppliedAfterExecutorSwitch(t *testing.T) {
	m := hostForm(nil)
	m.executors = []string{"claude", "codex"}
	m.executor = "claude"
	m.executorIdx = 0

	// Switch to codex; its lookup resolves and must repopulate the selector.
	m.executor = "codex"
	m.executorIdx = 1
	g, _ := m.Update(hostsLoadedMsg{project: "taskyou", executor: "codex", choices: offeredHosts()})
	m = g.(*FormModel)

	if !m.isFieldVisible(FieldHost) {
		t.Fatal("the Host field stayed hidden after a fresh answer for the current executor")
	}
	if got := len(m.hostChoices); got != len(offeredHosts()) {
		t.Fatalf("hostChoices len = %d, want %d: the fresh answer must be applied", got, len(offeredHosts()))
	}
	if target, _ := m.PlacementChoice(); target != "" {
		t.Fatalf("PlacementChoice() = %q, want automatic by default on a fresh load", target)
	}
}
