// Package parity enforces multi-surface feature parity in CI.
//
// The TUI's capability surface is derived mechanically from ui.KeyMap (every
// exported key.Binding field is a user-facing action). Each action must be
// either:
//
//   - covered by the GUI: listed in desktop/capabilities.json, or
//   - explicitly ignored: listed in parity-ignore.json with a reason.
//
// Anything else fails the build. Stale entries (covering or ignoring actions
// that no longer exist) also fail, so the files can't rot. Capabilities that
// declare API routes are checked against the routes actually registered in
// internal/web/server.go.
//
// Adding a TUI feature? Either implement it in the GUI and add it to
// desktop/capabilities.json, or make the skip a deliberate, documented
// decision in parity-ignore.json.
package parity

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"

	"github.com/bborn/workflow/internal/ui"
)

// capability is one GUI-covered TUI action.
type capability struct {
	// API routes (e.g. "POST /api/tasks/{id}/execute") this capability uses;
	// verified against internal/web/server.go.
	API []string `json:"api,omitempty"`
	// Note for humans reading the manifest.
	Note string `json:"note,omitempty"`
}

type manifest struct {
	// Covers maps TUI action names (ui.KeyMap field names) to how the GUI
	// implements them.
	Covers map[string]capability `json:"covers"`
	// GUIOnly documents capabilities with no TUI equivalent (informational).
	GUIOnly []string `json:"gui_only,omitempty"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func loadJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// tuiActions enumerates the TUI's user-facing actions from the keymap struct.
func tuiActions() []string {
	var names []string
	bindingType := reflect.TypeOf(key.Binding{})
	keyMapType := reflect.TypeOf(ui.KeyMap{})
	for i := 0; i < keyMapType.NumField(); i++ {
		field := keyMapType.Field(i)
		if field.IsExported() && field.Type == bindingType {
			names = append(names, field.Name)
		}
	}
	return names
}

// apiRoutes parses internal/web/server.go and returns every literal route
// pattern registered on the mux (e.g. "POST /api/tasks/{id}/execute").
func apiRoutes(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(repoRoot(t), "internal", "web", "server.go")
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	routes := make(map[string]bool)
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern := strings.Trim(lit.Value, `"`)
		routes[pattern] = true
		return true
	})
	if len(routes) == 0 {
		t.Fatal("no routes parsed from server.go — parser drift?")
	}
	return routes
}

func TestGUICoversTUISurface(t *testing.T) {
	root := repoRoot(t)

	var caps manifest
	loadJSON(t, filepath.Join(root, "desktop", "capabilities.json"), &caps)

	var ignored map[string]string
	loadJSON(t, filepath.Join(root, "parity-ignore.json"), &ignored)

	actions := tuiActions()
	if len(actions) < 10 {
		t.Fatalf("suspiciously few TUI actions (%d) — keymap reflection drift?", len(actions))
	}
	actionSet := make(map[string]bool, len(actions))
	for _, a := range actions {
		actionSet[a] = true
	}

	// Every TUI action is covered or explicitly ignored.
	for _, action := range actions {
		_, covered := caps.Covers[action]
		reason, isIgnored := ignored[action]
		switch {
		case covered && isIgnored:
			t.Errorf("%s is both covered and ignored — remove it from parity-ignore.json", action)
		case !covered && !isIgnored:
			t.Errorf(
				"GUI parity gap: TUI action %q is not covered by desktop/capabilities.json.\n"+
					"Either implement it in the GUI and add it to the manifest, or explicitly skip it\n"+
					"by adding it to parity-ignore.json with a reason.",
				action,
			)
		case isIgnored && strings.TrimSpace(reason) == "":
			t.Errorf("parity-ignore.json entry %q needs a non-empty reason", action)
		}
	}

	// No stale or misspelled entries.
	for name := range caps.Covers {
		if !actionSet[name] {
			t.Errorf("desktop/capabilities.json covers %q, which is not a TUI action (typo or removed feature?)", name)
		}
	}
	for name := range ignored {
		if !actionSet[name] {
			t.Errorf("parity-ignore.json ignores %q, which is not a TUI action — remove the stale entry", name)
		}
	}

	// Declared API routes must exist.
	routes := apiRoutes(t)
	for name, capability := range caps.Covers {
		for _, route := range capability.API {
			if !routes[route] {
				t.Errorf("capability %q references API route %q, which is not registered in internal/web/server.go", name, route)
			}
		}
	}
}

// TestParityFilesParse keeps the manifest and ignore files valid JSON.
func TestParityFilesParse(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		filepath.Join("desktop", "capabilities.json"),
		"parity-ignore.json",
	} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Errorf("parse %s: %v", rel, err)
		}
	}
}
