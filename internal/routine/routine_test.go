package routine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeRoutine creates a routine directory with the given prompt.md content.
func writeRoutine(t *testing.T, dir, name, prompt string) string {
	t.Helper()
	rtDir := filepath.Join(dir, name)
	if err := os.MkdirAll(rtDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rtDir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	return rtDir
}

func setupRoutinesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TY_ROUTINES_DIR", dir)
	t.Setenv("TY_ROUTINES_STATE_DIR", filepath.Join(t.TempDir(), "state"))
	return dir
}

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantMeta map[string]string
		wantBody string
		wantErr  string
	}{
		{
			name:     "no frontmatter",
			content:  "just a prompt",
			wantMeta: nil,
			wantBody: "just a prompt",
		},
		{
			name:     "basic frontmatter",
			content:  "---\nmodel: opus\nproject: oss\n---\nthe prompt",
			wantMeta: map[string]string{"model": "opus", "project": "oss"},
			wantBody: "the prompt",
		},
		{
			name:     "comments and blank lines",
			content:  "---\n# a comment\n\nmodel: haiku\n---\nbody",
			wantMeta: map[string]string{"model": "haiku"},
			wantBody: "body",
		},
		{
			name:     "value containing colon",
			content:  "---\ntimeout: 1h30m\n---\nbody",
			wantMeta: map[string]string{"timeout": "1h30m"},
			wantBody: "body",
		},
		{
			name:    "unclosed frontmatter",
			content: "---\nmodel: opus\nbody without closing",
			wantErr: "never closed",
		},
		{
			name:    "invalid line",
			content: "---\njust some words\n---\nbody",
			wantErr: "expected key: value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, body, err := parseFrontmatter(tt.content)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if body != tt.wantBody {
				t.Errorf("body: expected %q, got %q", tt.wantBody, body)
			}
			if len(meta) != len(tt.wantMeta) {
				t.Fatalf("meta: expected %v, got %v", tt.wantMeta, meta)
			}
			for k, v := range tt.wantMeta {
				if meta[k] != v {
					t.Errorf("meta[%s]: expected %q, got %q", k, v, meta[k])
				}
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := setupRoutinesDir(t)
	writeRoutine(t, dir, "scout", "do the scouting")

	rt, err := Load("scout")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rt.Model != DefaultModel {
		t.Errorf("expected default model %q, got %q", DefaultModel, rt.Model)
	}
	if rt.Timeout != DefaultTimeout {
		t.Errorf("expected default timeout %v, got %v", DefaultTimeout, rt.Timeout)
	}
	if rt.PermissionMode != "dangerous" {
		t.Errorf("expected dangerous permission mode, got %q", rt.PermissionMode)
	}
	if rt.Disabled {
		t.Error("expected enabled by default")
	}
	if rt.Prompt != "do the scouting" {
		t.Errorf("unexpected prompt: %q", rt.Prompt)
	}
}

func TestLoadFrontmatterOverrides(t *testing.T) {
	dir := setupRoutinesDir(t)
	writeRoutine(t, dir, "scout", "---\nmodel: opus\nproject: oss\ntimeout: 5m\npermission-mode: auto\ndir: ~/Projects/somerepo\n---\nprompt body")

	rt, err := Load("scout")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rt.Model != "opus" || rt.Project != "oss" || rt.Timeout != 5*time.Minute || rt.PermissionMode != "auto" {
		t.Errorf("overrides not applied: %+v", rt)
	}
	home, _ := os.UserHomeDir()
	if rt.WorkDir != filepath.Join(home, "Projects", "somerepo") {
		t.Errorf("dir not expanded: %q", rt.WorkDir)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := setupRoutinesDir(t)

	if _, err := Load("missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}

	writeRoutine(t, dir, "badkey", "---\nschedule: daily\n---\nbody")
	if _, err := Load("badkey"); err == nil || !strings.Contains(err.Error(), "unknown frontmatter key") {
		t.Errorf("expected unknown-key error, got %v", err)
	}

	writeRoutine(t, dir, "badtimeout", "---\ntimeout: tomorrow\n---\nbody")
	if _, err := Load("badtimeout"); err == nil || !strings.Contains(err.Error(), "invalid timeout") {
		t.Errorf("expected timeout error, got %v", err)
	}

	writeRoutine(t, dir, "empty", "---\nmodel: sonnet\n---\n  \n")
	if _, err := Load("empty"); err == nil || !strings.Contains(err.Error(), "no prompt body") {
		t.Errorf("expected empty-body error, got %v", err)
	}

	if _, err := Load("../escape"); err == nil {
		t.Error("expected invalid-name error for path traversal")
	}
}

func TestValidateName(t *testing.T) {
	valid := []string{"twitter-monitor", "oss_scout", "a", "scout2"}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("expected %q valid: %v", name, err)
		}
	}
	invalid := []string{"", "Twitter", "../etc", "a b", "-leading", ".hidden", "a/b"}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Errorf("expected %q invalid", name)
		}
	}
}

func TestSetDisabled(t *testing.T) {
	dir := setupRoutinesDir(t)
	writeRoutine(t, dir, "scout", "body")

	rt, err := Load("scout")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := rt.SetDisabled(true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	reloaded, err := Load("scout")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Disabled {
		t.Error("expected disabled after SetDisabled(true)")
	}
	if err := reloaded.SetDisabled(false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	reloaded, _ = Load("scout")
	if reloaded.Disabled {
		t.Error("expected enabled after SetDisabled(false)")
	}
}

func TestList(t *testing.T) {
	dir := setupRoutinesDir(t)
	writeRoutine(t, dir, "zebra", "z")
	writeRoutine(t, dir, "alpha", "a")
	// Directory without prompt.md is skipped
	if err := os.MkdirAll(filepath.Join(dir, "not-a-routine"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	routines, err := List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(routines) != 2 {
		t.Fatalf("expected 2 routines, got %d", len(routines))
	}
	if routines[0].Name != "alpha" || routines[1].Name != "zebra" {
		t.Errorf("expected sorted [alpha zebra], got [%s %s]", routines[0].Name, routines[1].Name)
	}
}

func TestListEmptyDir(t *testing.T) {
	t.Setenv("TY_ROUTINES_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	routines, err := List()
	if err != nil {
		t.Fatalf("list on missing dir should not error: %v", err)
	}
	if len(routines) != 0 {
		t.Errorf("expected no routines, got %d", len(routines))
	}
}

func TestScaffold(t *testing.T) {
	setupRoutinesDir(t)

	rt, err := Scaffold("new-scout")
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if rt.Model != "sonnet" {
		t.Errorf("template should default to sonnet, got %q", rt.Model)
	}
	if rt.Prompt == "" {
		t.Error("template should have a prompt body")
	}
	if _, err := Scaffold("new-scout"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected already-exists error, got %v", err)
	}
}
