package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/panel"
	"github.com/bborn/workflow/internal/ui"
)

// Dynamic provider kinds must be renderable in both interfaces, even when no
// new exported key binding was added. Keep the existing key/route gate intact.
func TestPanelProviderRenderers(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "desktop/src/components/WorkspacePanel.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	prefix := "export const PANEL_RENDERERS = ["
	_, list, ok := strings.Cut(string(source), prefix)
	if !ok {
		t.Fatal("GUI renderer contract missing")
	}
	list, _, _ = strings.Cut(list, "]")
	for _, provider := range panel.New(nil).Providers() {
		tui := false
		for _, kind := range ui.WorkspaceKinds {
			if provider.Kind == kind {
				tui = true
			}
		}
		if !tui || !strings.Contains(list, `"`+provider.Kind+`"`) {
			t.Errorf("provider %q kind %q needs TUI and GUI renderers", provider.ID, provider.Kind)
		}
	}
}
