package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/hooks"
	"github.com/bborn/workflow/internal/registry"
)

var browserEntries = []registry.Entry{
	{ID: "slack", Name: "Slack notifications", Description: "Post task updates to a Slack channel.",
		Source: "https://example.test/plugins", Subdir: "slack", Category: "notifications",
		Tags: []string{"chat"}, Provides: []string{"hook"}},
	{ID: "rpi", Name: "Research → Plan → Implement", Description: "A gated implementation workflow.",
		Source: "https://example.test/plugins", Subdir: "rpi", Category: "workflows",
		Provides: []string{"workflow"}, Requires: []string{"a make verify target"}},
}

// stubBrowser wires the browser's three side effects to fakes: no network, no
// plugins dir, no git.
func stubBrowser(t *testing.T, installed []hooks.Plugin) (installs *[]hooks.InstallRequest, removes *[]string) {
	t.Helper()
	var gotInstalls []hooks.InstallRequest
	var gotRemoves []string

	origLoad, origInstall, origRemove := browserLoad, browserInstall, browserRemove
	browserLoad = func(ctx context.Context, refresh bool) ([]registry.Entry, []hooks.Plugin, bool, error) {
		return browserEntries, installed, false, nil
	}
	browserInstall = func(ctx context.Context, req hooks.InstallRequest) (hooks.InstallResult, error) {
		gotInstalls = append(gotInstalls, req)
		return hooks.InstallResult{Dir: "/tmp/" + req.Name, Name: req.Name, Plugins: []string{req.ID}}, nil
	}
	browserRemove = func(name string) error {
		gotRemoves = append(gotRemoves, name)
		return nil
	}
	t.Cleanup(func() { browserLoad, browserInstall, browserRemove = origLoad, origInstall, origRemove })
	return &gotInstalls, &gotRemoves
}

// loadedBrowser returns a browser with its catalog already delivered.
func loadedBrowser(t *testing.T, installed []hooks.Plugin) *PluginBrowserModel {
	t.Helper()
	m := NewPluginBrowserModel(100, 40)
	msg := m.loadCmd(false)()
	m, _ = m.Update(msg)
	return m
}

func browserKey(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	}
	t := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	return t
}

func rowIDs(m *PluginBrowserModel) []string {
	out := make([]string, 0, len(m.Rows()))
	for _, r := range m.Rows() {
		out = append(out, r.ID())
	}
	return out
}

func TestPluginBrowser_ListsCatalogAndMarksInstalled(t *testing.T) {
	stubBrowser(t, []hooks.Plugin{{Name: "slack", Dir: "/plugins/slack", Description: "installed copy"}})
	m := loadedBrowser(t, nil)

	if got := rowIDs(m); len(got) != 2 {
		t.Fatalf("rows = %v, want both catalog entries", got)
	}
	for _, r := range m.Rows() {
		if r.ID() == "slack" && !r.IsInstalled() {
			t.Error("slack should read as installed (a plugin with that manifest name is on disk)")
		}
		if r.ID() == "rpi" && r.IsInstalled() {
			t.Error("rpi should not read as installed")
		}
	}
}

// Typing filters, and the catalog scorer decides the order.
func TestPluginBrowser_SearchFilters(t *testing.T) {
	stubBrowser(t, nil)
	m := loadedBrowser(t, nil)

	for _, r := range "slack" {
		m, _ = m.Update(browserKey(string(r)))
	}
	if got := rowIDs(m); len(got) != 1 || got[0] != "slack" {
		t.Errorf("rows after typing slack = %v, want [slack]", got)
	}

	// Esc clears the query before it closes the view.
	m, _ = m.Update(browserKey("esc"))
	if m.Done() {
		t.Error("esc with a query should clear the query, not close the view")
	}
	if len(rowIDs(m)) != 2 {
		t.Errorf("rows after clearing = %v, want both", rowIDs(m))
	}
	m, _ = m.Update(browserKey("esc"))
	if !m.Done() {
		t.Error("esc with an empty query should close the view")
	}
}

func TestPluginBrowser_ScopeCyclesWithTab(t *testing.T) {
	stubBrowser(t, []hooks.Plugin{{Name: "slack", Dir: "/plugins/slack"}})
	m := loadedBrowser(t, nil)

	m, _ = m.Update(browserKey("tab")) // Installed
	if got := rowIDs(m); len(got) != 1 || got[0] != "slack" {
		t.Errorf("Installed scope = %v, want [slack]", got)
	}
	m, _ = m.Update(browserKey("tab")) // Available
	if got := rowIDs(m); len(got) != 1 || got[0] != "rpi" {
		t.Errorf("Available scope = %v, want [rpi]", got)
	}
	m, _ = m.Update(browserKey("tab")) // back to All
	if got := len(rowIDs(m)); got != 2 {
		t.Errorf("All scope has %d rows, want 2", got)
	}
}

// Enter installs the highlighted row, carrying the catalog entry's source AND
// subdir — the part that makes "install just slack out of a collection" work.
func TestPluginBrowser_EnterInstallsSelected(t *testing.T) {
	installs, _ := stubBrowser(t, nil)
	m := loadedBrowser(t, nil)

	// Rows are category-sorted: notifications before workflows.
	if rowIDs(m)[0] != "slack" {
		t.Fatalf("first row = %q, want slack", rowIDs(m)[0])
	}
	m, cmd := m.Update(browserKey("enter"))
	if cmd == nil {
		t.Fatal("enter should start an install")
	}
	if m.busyID != "slack" {
		t.Errorf("busyID = %q, want slack", m.busyID)
	}
	msg := cmd()
	if len(*installs) != 1 {
		t.Fatalf("installs = %v, want one", *installs)
	}
	req := (*installs)[0]
	if req.ID != "slack" || req.Subdir != "slack" || req.Source != "https://example.test/plugins" {
		t.Errorf("install request = %+v, want the slack entry's source and subdir", req)
	}

	m, _ = m.Update(msg)
	if m.busyID != "" {
		t.Error("busy marker should clear when the install finishes")
	}
	if !strings.Contains(m.status, "Installed") {
		t.Errorf("status = %q, want it to report the install", m.status)
	}
}

// A second Enter while a clone is in flight must not start another one.
func TestPluginBrowser_IgnoresEnterWhileBusy(t *testing.T) {
	installs, _ := stubBrowser(t, nil)
	m := loadedBrowser(t, nil)

	m, cmd := m.Update(browserKey("enter"))
	cmd()
	_, second := m.Update(browserKey("enter"))
	if second != nil {
		t.Error("a second enter while installing should be ignored")
	}
	if len(*installs) != 1 {
		t.Errorf("installs = %d, want exactly one", len(*installs))
	}
}

func TestPluginBrowser_InstallFailureShows(t *testing.T) {
	stubBrowser(t, nil)
	m := loadedBrowser(t, nil)
	m, _ = m.Update(pluginInstalledMsg{id: "slack", err: errors.New("clone slack: exit status 128\nfatal: repo not found")})
	if !m.statusErr {
		t.Error("a failed install should be styled as an error")
	}
	if !strings.Contains(m.status, "ty plugins add slack") {
		t.Errorf("status = %q; a boxed git error should point at the CLI instead of being quoted whole", m.status)
	}
	if strings.Contains(m.status, "fatal: repo not found") {
		t.Errorf("status = %q; only the first line belongs in a one-line banner", m.status)
	}
}

// Removing asks first, then removes by the installed plugin's own name.
func TestPluginBrowser_RemoveConfirms(t *testing.T) {
	_, removes := stubBrowser(t, []hooks.Plugin{{Name: "slack", Dir: "/plugins/slack"}})
	m := loadedBrowser(t, nil)

	m, cmd := m.Update(browserKey("ctrl+d"))
	if cmd != nil {
		t.Fatal("ctrl+d should ask for confirmation before removing anything")
	}
	if m.confirmRemove != "slack" {
		t.Fatalf("confirmRemove = %q, want slack", m.confirmRemove)
	}
	// Anything but y cancels.
	m, _ = m.Update(browserKey("n"))
	if m.confirmRemove != "" || len(*removes) != 0 {
		t.Fatal("n should cancel the removal")
	}

	m, _ = m.Update(browserKey("ctrl+d"))
	_, cmd = m.Update(browserKey("y"))
	if cmd == nil {
		t.Fatal("y should start the removal")
	}
	cmd()
	if len(*removes) != 1 || (*removes)[0] != "slack" {
		t.Errorf("removes = %v, want [slack]", *removes)
	}
}

// ctrl+d on a row that isn't installed has nothing to remove.
func TestPluginBrowser_RemoveIgnoredWhenNotInstalled(t *testing.T) {
	_, removes := stubBrowser(t, nil)
	m := loadedBrowser(t, nil)
	m, _ = m.Update(browserKey("ctrl+d"))
	if m.confirmRemove != "" || len(*removes) != 0 {
		t.Error("ctrl+d on an uninstalled plugin should do nothing")
	}
}

// A plugin installed by hand still appears, so the browser is also the place to
// see and remove what you already have.
func TestPluginBrowser_ShowsUncataloguedInstalls(t *testing.T) {
	stubBrowser(t, []hooks.Plugin{{
		Name: "my-own-thing", Dir: "/plugins/my-own-thing", Description: "hand written",
		Actions: []hooks.Action{{ID: "go", Command: "go.sh"}},
	}})
	m := loadedBrowser(t, nil)

	var row *PluginRow
	for i, r := range m.Rows() {
		if r.ID() == "my-own-thing" {
			row = &m.Rows()[i]
		}
	}
	if row == nil {
		t.Fatalf("hand-installed plugin missing from the browser: %v", rowIDs(m))
	}
	if row.Entry != nil {
		t.Error("an uncatalogued plugin should have no catalog entry")
	}
	if !row.IsInstalled() {
		t.Error("it is on disk; it must read as installed")
	}

	// Enter has no source to install from and should say so rather than no-op.
	m.cursor = len(m.Rows()) - 1
	m, cmd := m.Update(browserKey("enter"))
	if cmd != nil {
		t.Error("there is nothing to clone for a local-only plugin")
	}
	if !m.statusErr || !strings.Contains(m.status, "not in the catalog") {
		t.Errorf("status = %q, want an explanation", m.status)
	}
}

func TestPluginBrowser_ViewRendersWithoutCatalog(t *testing.T) {
	origLoad := browserLoad
	browserLoad = func(ctx context.Context, refresh bool) ([]registry.Entry, []hooks.Plugin, bool, error) {
		return nil, nil, true, errors.New("dial tcp: no route to host")
	}
	t.Cleanup(func() { browserLoad = origLoad })

	m := NewPluginBrowserModel(80, 30)
	out := m.View()
	if !strings.Contains(out, "Loading") {
		t.Errorf("initial view should say it is loading:\n%s", out)
	}
	m, _ = m.Update(m.loadCmd(false)())
	out = m.View()
	if !strings.Contains(out, "unavailable") {
		t.Errorf("an unreachable catalog should be reported:\n%s", out)
	}
}

// installSuccessText names what the plugin made runnable, which is the thing a
// bare "Installed" leaves the user to guess.
func TestInstallSuccessTextNamesWhatToRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TY_PLUGINS_DIR", dir)
	pdir := filepath.Join(dir, "rpi")
	if err := os.MkdirAll(filepath.Join(pdir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "plugin.yaml"), []byte("name: rpi\ndescription: d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "workflows", "rpi.yaml"),
		[]byte("name: rpi\nsteps:\n  - name: b\n    prompt: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := installSuccessText(hooks.InstallResult{Name: "rpi", Plugins: []string{"rpi"}})
	if !strings.Contains(got, "ty pipeline -d rpi") {
		t.Errorf("installSuccessText = %q, want it to name the workflow command", got)
	}
}

// Leaving the browser while a clone is in flight must not swallow the outcome:
// the result lands on the board's notification line instead.
func TestPluginInstallResultSurvivesLeavingTheBrowser(t *testing.T) {
	stubBrowser(t, nil)
	m := &AppModel{currentView: ViewDashboard}

	model, _ := m.Update(pluginInstalledMsg{id: "rpi", result: hooks.InstallResult{Name: "rpi", Plugins: []string{"rpi"}}})
	app, ok := model.(*AppModel)
	if !ok {
		t.Fatalf("Update returned %T", model)
	}
	if !strings.Contains(app.notification, "Installed rpi") {
		t.Errorf("notification = %q, want the install outcome", app.notification)
	}

	model, _ = app.Update(pluginRemovedMsg{name: "rpi"})
	app = model.(*AppModel)
	if !strings.Contains(app.notification, "Removed rpi") {
		t.Errorf("notification = %q, want the remove outcome", app.notification)
	}
}
