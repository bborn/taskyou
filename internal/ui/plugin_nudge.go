package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/pipeline"
)

// starterPackSource is the community plugin collection offered to new users and
// announced to existing ones. A fresh ty ships no workflows, so installing this is
// what makes `ty pipeline` do anything.
const starterPackSource = "https://github.com/taskyou/plugins"

// settingPluginNudgeSeen records that the one-time plugins nudge has been shown. It
// is written the moment the nudge first appears, so it shows in exactly one session.
const settingPluginNudgeSeen = "plugins_nudge_seen"

// starterPackNotice is the Welcome fork's readiness line when no workflow exists.
const starterPackNotice = "no workflows installed — press i to add the starter pack (taskyou/plugins)"

// starterPackInstalledMsg carries the result of `ty plugins add` run off the UI loop.
type starterPackInstalledMsg struct {
	output string
	err    error
}

// runPluginsAdd runs `ty plugins add <source>` with this binary, so the TUI installs
// exactly the way the CLI does. A var so tests can fake the install.
var runPluginsAdd = func(source string) ([]byte, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(self, "plugins", "add", source).CombinedOutput()
}

// hasInstalledWorkflows reports whether any workflow resolves (plugin, global or
// on-disk). A var so tests don't depend on the machine's plugins dir.
var hasInstalledWorkflows = func() bool { return len(pipeline.DefinitionNames()) > 0 }

func installStarterPackCmd() tea.Cmd {
	return func() tea.Msg {
		out, err := runPluginsAdd(starterPackSource)
		return starterPackInstalledMsg{output: strings.TrimSpace(string(out)), err: err}
	}
}

// pluginNudgeText is the banner copy. With no workflows the pitch is the starter
// pack itself; with some already installed, it's the news that plugins exist.
func pluginNudgeText(hasWorkflows, installing bool) string {
	if installing {
		return "Installing the starter pack from taskyou/plugins…"
	}
	if !hasWorkflows {
		return "No workflows installed yet — add the starter pack from taskyou/plugins  (i install · esc dismiss)"
	}
	return "TaskYou has plugins now — browse taskyou/plugins · ty plugins add <repo>  (i install · esc dismiss)"
}

// starterPackResultText summarizes an install in one short line; it lands in the
// Welcome box and the notification slot, which both grow with their widest line. A
// failed `ty plugins add` prints a boxed multi-line error, so on failure point at
// the command instead of quoting it.
func starterPackResultText(output string, err error) string {
	if err != nil {
		return fmt.Sprintf("%s Starter pack install failed (run: ty plugins add %s)", IconBlocked(), starterPackSource)
	}
	// "Installed 4 plugin(s): a, b. Run `ty plugins list`…" → "Installed 4 plugin(s)"
	summary, _, _ := strings.Cut(output, ":")
	summary, _, _ = strings.Cut(summary, "\n")
	if summary == "" {
		summary = "Installed the starter pack"
	}
	return IconDone() + " " + summary + " from taskyou/plugins · ty plugins list"
}

// initPluginNudge decides whether to show the one-time plugins nudge and marks it
// seen immediately, so it never nags on a later launch.
func (m *AppModel) initPluginNudge() {
	m.hasWorkflows = hasInstalledWorkflows()
	if m.db == nil {
		return
	}
	if seen, err := m.db.GetSetting(settingPluginNudgeSeen); err != nil || seen != "" {
		return
	}
	m.showPluginNudge = true
	_ = m.db.SetSetting(settingPluginNudgeSeen, "1")
}

// newWelcomeView builds the first-run Welcome fork with the current readiness state.
func (m *AppModel) newWelcomeView() *WelcomeModel {
	w := NewWelcomeModel(m.width, m.height, m.availableExecutors, tmuxAvailable())
	w.missingWorkflows = !m.hasWorkflows
	return w
}

// handlePluginNudgeKey lets the visible nudge claim i (install) and esc (dismiss)
// on the board. Everything else falls through to the normal dashboard keys.
func (m *AppModel) handlePluginNudgeKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !m.showPluginNudge || m.starterPackInstalling {
		return nil, false
	}
	switch msg.String() {
	case "i":
		return m.startStarterPackInstall(), true
	case "esc":
		m.showPluginNudge = false
		return nil, true
	}
	return nil, false
}

func (m *AppModel) startStarterPackInstall() tea.Cmd {
	if m.starterPackInstalling {
		return nil
	}
	m.starterPackInstalling = true
	if m.welcomeView != nil {
		m.welcomeView.installing = true
	}
	return installStarterPackCmd()
}

func (m *AppModel) handleStarterPackInstalled(msg starterPackInstalledMsg) {
	m.starterPackInstalling = false
	m.showPluginNudge = false
	if msg.err == nil {
		m.hasWorkflows = hasInstalledWorkflows()
	}
	text := starterPackResultText(msg.output, msg.err)
	if m.welcomeView != nil {
		m.welcomeView.installing = false
		m.welcomeView.missingWorkflows = !m.hasWorkflows
		m.welcomeView.installResult = text
	}
	m.notification = text
	m.notifyUntil = time.Now().Add(8 * time.Second)
}
