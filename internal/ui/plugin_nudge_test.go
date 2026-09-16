package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

func stubWorkflows(t *testing.T, has bool) {
	t.Helper()
	orig := hasInstalledWorkflows
	hasInstalledWorkflows = func() bool { return has }
	t.Cleanup(func() { hasInstalledWorkflows = orig })
}

func stubPluginsAdd(t *testing.T, out string, err error) *string {
	t.Helper()
	var got string
	orig := runPluginsAdd
	runPluginsAdd = func(source string) ([]byte, error) {
		got = source
		return []byte(out), err
	}
	t.Cleanup(func() { runPluginsAdd = orig })
	return &got
}

func nudgeTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestPluginNudgeShowsExactlyOnce(t *testing.T) {
	stubWorkflows(t, true)
	database := nudgeTestDB(t)

	first := &AppModel{db: database}
	first.initPluginNudge()
	if !first.showPluginNudge {
		t.Fatal("first launch should show the plugins nudge")
	}

	second := &AppModel{db: database}
	second.initPluginNudge()
	if second.showPluginNudge {
		t.Fatal("nudge must not show again on a later launch")
	}
}

func TestPluginNudgeEscDismisses(t *testing.T) {
	m := &AppModel{showPluginNudge: true}
	if _, handled := m.handlePluginNudgeKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled {
		t.Fatal("esc should be claimed by the visible nudge")
	}
	if m.showPluginNudge {
		t.Fatal("esc should hide the nudge")
	}
	if _, handled := m.handlePluginNudgeKey(tea.KeyMsg{Type: tea.KeyEsc}); handled {
		t.Fatal("once hidden, esc must fall through to the board")
	}
}

func TestPluginNudgeInstallsStarterPack(t *testing.T) {
	stubWorkflows(t, true)
	source := stubPluginsAdd(t, "Installed 4 plugin(s): arc-solve, plan-code-review, rpi.\n", nil)
	m := &AppModel{showPluginNudge: true, welcomeView: &WelcomeModel{missingWorkflows: true}}

	cmd, handled := m.handlePluginNudgeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if !handled || cmd == nil {
		t.Fatal("i should start the starter pack install")
	}
	if !m.starterPackInstalling || !strings.Contains(pluginNudgeText(false, true), "Installing") {
		t.Fatal("install should be marked in flight")
	}
	msg, ok := cmd().(starterPackInstalledMsg)
	if !ok {
		t.Fatal("install cmd should return starterPackInstalledMsg")
	}
	if *source != starterPackSource {
		t.Errorf("installed %q, want %q", *source, starterPackSource)
	}

	m.handleStarterPackInstalled(msg)
	if m.showPluginNudge || m.starterPackInstalling {
		t.Error("nudge should clear after the install")
	}
	if !m.hasWorkflows || m.welcomeView.missingWorkflows {
		t.Error("workflows should be detected after a successful install")
	}
	if !strings.Contains(m.notification, "Installed 4 plugin(s) from taskyou/plugins") || strings.Contains(m.notification, "arc-solve") {
		t.Errorf("notification = %q", m.notification)
	}
}

func TestStarterPackInstallFailurePointsAtCommand(t *testing.T) {
	stubWorkflows(t, false)
	m := &AppModel{}
	m.handleStarterPackInstalled(starterPackInstalledMsg{output: "╭─ boxed error ─╮", err: errors.New("exit status 1")})
	if !strings.Contains(m.notification, "ty plugins add "+starterPackSource) {
		t.Errorf("failure should name the command to retry, got %q", m.notification)
	}
	if m.hasWorkflows {
		t.Error("a failed install must not claim workflows exist")
	}
}

func TestPluginNudgeTextByState(t *testing.T) {
	if got := pluginNudgeText(false, false); !strings.Contains(got, "starter pack") || !strings.Contains(got, "taskyou/plugins") {
		t.Errorf("no-workflows copy should pitch the starter pack: %q", got)
	}
	if got := pluginNudgeText(true, false); !strings.Contains(got, "plugins now") || !strings.Contains(got, "ty plugins add") {
		t.Errorf("existing-user copy should announce plugins: %q", got)
	}
}

func TestWelcomeOffersStarterPackOnlyWithoutWorkflows(t *testing.T) {
	w := NewWelcomeModel(100, 40, []string{"claude"}, true)
	if strings.Contains(w.View(), "starter pack") {
		t.Fatal("no offer when workflows are installed")
	}
	w.missingWorkflows = true
	if !strings.Contains(w.View(), "starter pack") {
		t.Fatal("Welcome should offer the starter pack when no workflows exist")
	}
}
