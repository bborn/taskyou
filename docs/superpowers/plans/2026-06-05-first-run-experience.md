# First-Run / First-Folder Experience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the "drops you straight into a new task" first-run behavior with a smart, gentle onboarding: detect real project folders (git or markers), offer an LLM-enriched project setup, give a Welcome fork when there's nothing to suggest, add a delightful fuzzy folder picker, keep git optional, and never lose the user's form input.

**Architecture:** A launch decision tree in `AppModel` (run on every startup, not just first run) routes to one of: the existing board, an upgraded **Project Setup Suggestion** card, or a first-time **Welcome fork**. Project metadata (name/alias/description) is inferred by shelling out to `claude -p` (reusing the `RenameClaudeSession` pattern — no API key needed), degrading gracefully to rule-based prefill. A new bubbletea **fuzzy folder picker** is the entry point for "set up a project". Git stays optional: git repos default to worktrees on, non-git folders become plain projects with no git/worktree prompt at all.

**Tech Stack:** Go 1.24, bubbletea v1.3.10, bubbles v1.0.0 (`textinput`), huh v0.8.0, lipgloss, `github.com/sahilm/fuzzy v0.1.1` (already in module graph), `claude` CLI in print mode.

---

## Background / Current State (verified via QA harness)

- `cmd/task/main.go` → `runLocal()` → `ui.NewAppModel()`. On `tasksLoadedMsg` with `isFirstLoad`, `app.go:835-855` calls `maybeOfferProjectCreation()` then, failing that, auto-opens the New Task form when `db.IsFirstRun()`.
- `internal/ui/project_detect.go` only treats **git repos** as candidates; name = `filepath.Base`; instructions imported from `AGENTS.md/CLAUDE.md/CLAUDE.local.md/.cursorrules/README.md` (cap 4000 chars); `UseWorktrees` hardcoded `true`.
- `app.go:3150-3323`: `maybeOfferProjectCreation` / `showProjectDetectConfirm` (huh Confirm modal, `ViewProjectDetectConfirm`) / `createDetectedProject`. Per-path dismiss via `projectSuggestionDismissedKey` → `db.SetSetting`.
- `internal/ui/filebrowser.go`: basic dir browser, `space` to select, hidden dirs skipped, no search. Only reachable from Settings → New Project.
- `internal/ui/settings.go:560-699`: project form save. Validation branches at `:584` (`path does not exist`) and `:615` (`not a directory`) set `m.err` and bail (lose state); only git-init failures route through the state-preserving `reshowProjectFormWithError` (`:686`). Nested/sub-git repos already allowed (`:620-624`). Git coupled to `useWorktrees`.
- AI naming: `internal/autocomplete` + `internal/ai` use the Anthropic HTTP API + `anthropic_api_key`. The **only** headless `claude -p` call is `RenameClaudeSession` (`executor.go:3267-3288`), using `exec.CommandContext(ctx, "claude", ..., "-p", ...)` with `cmd.Dir` and `CLAUDE_CONFIG_DIR=ResolveClaudeConfigDir(custom)` env. This is the pattern we reuse for inference.

**Key interfaces (do not redefine):**
```go
// internal/db/tasks.go
type Project struct {
    ID int64; Name, Path, Aliases, Instructions string
    Color, ClaudeConfigDir string; UseWorktrees bool
    DefaultPermissionMode string; CreatedAt LocalTime
    Actions []ProjectAction
}
func (db *DB) CreateProject(p *Project) error
func (db *DB) GetProjectByName(name string) (*Project, error) // nil,nil when absent
func (db *DB) GetProjectByPath(cwd string) (*Project, error)
func (db *DB) SetSetting(key, value string) error
func (db *DB) GetSetting(key string) (string, error)
func (db *DB) SetLastUsedProject(project string) error
func (db *DB) IsFirstRun() bool

// internal/executor/executor.go
func ResolveClaudeConfigDir(custom string) string

// internal/ui/settings.go
func isGitRepo(path string) bool
func ensureGitRepoHasCommit(path string) error
func initGitRepo(path string) error

// internal/ui/project_detect.go (existing)
func dirIsGitRepo(dir string) bool
func inferProjectName(dir string) string
func readProjectInstructions(dir string) (instructions, sourceFile string)
func detectProjectFromDir(dir string) (*db.Project, string)
func uniqueProjectName(database *db.DB, name string) string
func projectSuggestionDismissedKey(path string) string
```

---

## File Structure

- `internal/ui/project_detect.go` — **modify**: add folder-candidacy heuristic (deny-list + markers); `detectProjectFromDir` uses it and sets `UseWorktrees` from git presence.
- `internal/ui/project_detect_test.go` — **create**: table tests for the heuristic.
- `internal/ai/project_infer.go` — **create**: `claude -p` inference + pure JSON parser.
- `internal/ai/project_infer_test.go` — **create**: parser + degradation tests.
- `internal/ui/folderpicker.go` — **create**: fuzzy folder picker bubbletea model.
- `internal/ui/welcome.go` — **create**: first-run Welcome fork model + render.
- `internal/ui/app.go` — **modify**: launch decision tree; new view consts `ViewWelcome`, `ViewFolderPicker`; wire welcome/picker/inference; upgrade suggestion card to show inferred fields + a "Customize" route.
- `internal/ui/settings.go` — **modify**: harden project-form validation to never lose input; non-git path skips worktree/git entirely.
- `scripts/qa/ty-qa-firstrun.sh` — **create**: scripted first-run QA scenario.

---

## Task 1: Folder-candidacy heuristic (pure logic, TDD)

**Files:**
- Modify: `internal/ui/project_detect.go`
- Test: `internal/ui/project_detect_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/ui/project_detect_test.go`:
```go
package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsProjectCandidate(t *testing.T) {
	home, _ := os.UserHomeDir()

	// Junk dir: home itself and standard bare children → never a candidate.
	for _, junk := range []string{home, filepath.Join(home, "Desktop"), filepath.Join(home, "Downloads"), "/", "/tmp"} {
		if isProjectCandidate(junk) {
			t.Errorf("isProjectCandidate(%q) = true, want false", junk)
		}
	}

	// A git repo is a candidate.
	gitDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isProjectCandidate(gitDir) {
		t.Errorf("git repo %q should be a candidate", gitDir)
	}

	// A non-git dir with a project marker is a candidate.
	markerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(markerDir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isProjectCandidate(markerDir) {
		t.Errorf("dir with go.mod %q should be a candidate", markerDir)
	}

	// An empty, signal-less dir is NOT a candidate.
	emptyDir := t.TempDir()
	if isProjectCandidate(emptyDir) {
		t.Errorf("empty dir %q should not be a candidate", emptyDir)
	}
}
```

- [ ] **Step 2: Run it; expect FAIL (undefined: isProjectCandidate)**

Run: `go test ./internal/ui/ -run TestIsProjectCandidate -v`
Expected: FAIL — `undefined: isProjectCandidate`.

- [ ] **Step 3: Implement the heuristic**

Append to `internal/ui/project_detect.go` (and ensure `runtime` is NOT needed; uses `os`, `path/filepath`, `strings` already imported):
```go
// projectMarkerFiles signal that a directory is a real project even without git.
var projectMarkerFiles = []string{
	".git", "package.json", "go.mod", "Cargo.toml", "pyproject.toml",
	"requirements.txt", "Gemfile", "pom.xml", "build.gradle", "composer.json",
	"AGENTS.md", "CLAUDE.md", ".cursorrules", "Makefile",
}

// denyListedDirs are directory names that, directly under $HOME, are never
// project candidates (system / dumping-ground folders).
var denyListedHomeChildren = map[string]bool{
	"Desktop": true, "Documents": true, "Downloads": true, "Music": true,
	"Pictures": true, "Movies": true, "Public": true, "Library": true,
	"Applications": true,
}

// isProjectCandidate reports whether dir is worth proactively offering as a
// TaskYou project: it must not be a system/dumping dir, and must show at least
// one positive signal (git repo or a project marker file).
func isProjectCandidate(dir string) bool {
	if dir == "" {
		return false
	}
	clean := filepath.Clean(dir)

	// Deny-list: root, temp, $HOME itself, and bare home children.
	if clean == "/" || clean == filepath.Clean(os.TempDir()) || strings.HasPrefix(clean, "/tmp") {
		return false
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		if clean == home {
			return false
		}
		if filepath.Dir(clean) == home && denyListedHomeChildren[filepath.Base(clean)] {
			return false
		}
	}

	// Positive signal: git repo or any marker file present.
	if dirIsGitRepo(clean) {
		return true
	}
	for _, marker := range projectMarkerFiles {
		if _, err := os.Stat(filepath.Join(clean, marker)); err == nil {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Update `detectProjectFromDir` to use the heuristic and git-aware worktrees**

Replace the body of `detectProjectFromDir` in `internal/ui/project_detect.go`:
```go
// detectProjectFromDir builds a project pre-filled with values inferred from a
// candidate directory. Returns nil when dir is not a project candidate. Worktrees
// default ON only for git repos (git is optional; non-git projects skip worktrees).
func detectProjectFromDir(dir string) (project *db.Project, instructionSource string) {
	if !isProjectCandidate(dir) {
		return nil, ""
	}
	instructions, source := readProjectInstructions(dir)
	return &db.Project{
		Name:         inferProjectName(dir),
		Path:         filepath.Clean(dir),
		Instructions: instructions,
		UseWorktrees: dirIsGitRepo(dir),
	}, source
}
```

- [ ] **Step 5: Run tests; expect PASS**

Run: `go test ./internal/ui/ -run TestIsProjectCandidate -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/project_detect.go internal/ui/project_detect_test.go
git commit -m "feat(onboarding): folder-candidacy heuristic (git or markers, minus deny-list)"
```

---

## Task 2: `claude -p` project inference helper (TDD on parsing/degradation)

**Files:**
- Create: `internal/ai/project_infer.go`
- Test: `internal/ai/project_infer_test.go`

- [ ] **Step 1: Write the failing test (pure parser + degradation)**

Create `internal/ai/project_infer_test.go`:
```go
package ai

import "testing"

func TestParseInferenceJSON(t *testing.T) {
	// Plain JSON object.
	meta, err := parseInferenceJSON(`{"name":"acme-rocket","alias":"acme","description":"Rust CLI for rockets"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Name != "acme-rocket" || meta.Alias != "acme" || meta.Description != "Rust CLI for rockets" {
		t.Errorf("got %+v", meta)
	}

	// JSON wrapped in prose / code fences (claude -p sometimes does this).
	meta, err = parseInferenceJSON("Here you go:\n```json\n{\"name\":\"foo\",\"alias\":\"f\",\"description\":\"d\"}\n```")
	if err != nil || meta.Name != "foo" {
		t.Errorf("fenced parse failed: %+v err=%v", meta, err)
	}

	// Garbage → error.
	if _, err := parseInferenceJSON("not json at all"); err == nil {
		t.Error("expected error on non-JSON")
	}
}

func TestInferProjectMetadata_DegradesWhenClaudeMissing(t *testing.T) {
	// Point PATH at an empty dir so `claude` cannot be found; helper must return
	// an error (caller falls back) rather than panic/hang.
	t.Setenv("PATH", t.TempDir())
	_, err := InferProjectMetadata(t.TempDir(), "")
	if err == nil {
		t.Error("expected error when claude binary is unavailable")
	}
}
```

- [ ] **Step 2: Run it; expect FAIL (undefined symbols)**

Run: `go test ./internal/ai/ -run 'TestParseInferenceJSON|TestInferProjectMetadata' -v`
Expected: FAIL — `undefined: parseInferenceJSON` / `InferProjectMetadata`.

- [ ] **Step 3: Implement the helper**

Create `internal/ai/project_infer.go`:
```go
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/executor"
)

// ProjectMetadata is the structured result of inferring a project's identity.
type ProjectMetadata struct {
	Name        string `json:"name"`
	Alias       string `json:"alias"`
	Description string `json:"description"`
}

// inferenceTimeout caps how long we wait on `claude -p` before giving up and
// letting the caller fall back to rule-based prefill.
const inferenceTimeout = 12 * time.Second

// InferProjectMetadata shells out to `claude -p` (print mode) to infer a clean
// project name, short alias, and one-line description from the folder. It reuses
// the user's existing Claude CLI auth via CLAUDE_CONFIG_DIR (no API key needed),
// mirroring executor.RenameClaudeSession. Returns an error when claude is
// unavailable, times out, or returns unparseable output — callers MUST degrade
// gracefully (basename name + imported instructions) rather than block.
func InferProjectMetadata(dir, configDir string) (ProjectMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), inferenceTimeout)
	defer cancel()

	prompt := buildInferencePrompt(dir)

	cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("CLAUDE_CONFIG_DIR=%s", executor.ResolveClaudeConfigDir(configDir)))
	out, err := cmd.Output()
	if err != nil {
		return ProjectMetadata{}, fmt.Errorf("claude -p inference failed: %w", err)
	}
	return parseInferenceJSON(string(out))
}

// buildInferencePrompt assembles a prompt with the folder name, a shallow file
// listing, and a README/AGENTS snippet, asking for strict JSON.
func buildInferencePrompt(dir string) string {
	var sb strings.Builder
	sb.WriteString("You are naming a software project for a task manager. ")
	sb.WriteString("Given a folder, respond with ONLY a JSON object: ")
	sb.WriteString(`{"name": "...", "alias": "...", "description": "..."}`)
	sb.WriteString(".\n- name: a clean human project name (kebab or title case), not a file path.\n")
	sb.WriteString("- alias: a short lowercase handle (3-12 chars), no spaces.\n")
	sb.WriteString("- description: one sentence (<= 12 words) describing what the project is.\n")
	sb.WriteString("Output JSON only, no prose, no code fences.\n\n")
	sb.WriteString("Folder name: " + filepath.Base(filepath.Clean(dir)) + "\n\n")
	sb.WriteString("Files:\n" + shallowFileListing(dir) + "\n")
	if snippet := readmeSnippet(dir); snippet != "" {
		sb.WriteString("\nREADME/AGENTS excerpt:\n" + snippet + "\n")
	}
	return sb.String()
}

// shallowFileListing returns up to 40 top-level entry names, dirs marked with /.
func shallowFileListing(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "(unreadable)"
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			names = append(names, e.Name()+"/")
		} else {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) > 40 {
		names = names[:40]
	}
	return strings.Join(names, "\n")
}

// readmeSnippet returns the first ~1200 chars of the best available doc file.
func readmeSnippet(dir string) string {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "README.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			continue
		}
		if len(s) > 1200 {
			s = s[:1200]
		}
		return s
	}
	return ""
}

// parseInferenceJSON extracts a ProjectMetadata from claude's output, tolerating
// surrounding prose or ```json fences by scanning for the first {...} block.
func parseInferenceJSON(raw string) (ProjectMetadata, error) {
	s := strings.TrimSpace(raw)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end <= start {
		return ProjectMetadata{}, fmt.Errorf("no JSON object found in inference output")
	}
	var meta ProjectMetadata
	if err := json.Unmarshal([]byte(s[start:end+1]), &meta); err != nil {
		return ProjectMetadata{}, fmt.Errorf("parse inference JSON: %w", err)
	}
	if strings.TrimSpace(meta.Name) == "" {
		return ProjectMetadata{}, fmt.Errorf("inference returned empty name")
	}
	return meta, nil
}
```

- [ ] **Step 4: Run tests; expect PASS**

Run: `go test ./internal/ai/ -run 'TestParseInferenceJSON|TestInferProjectMetadata' -v`
Expected: PASS (the degradation test passes because `claude` is unreachable via the emptied PATH).

Note: verify no import cycle (`internal/ai` importing `internal/executor`). If `go build ./...` reports a cycle, copy the tiny `ResolveClaudeConfigDir` resolution inline instead of importing executor (it only reads `CLAUDE_CONFIG_DIR`/default). Check with:
Run: `go build ./internal/ai/`
Expected: builds clean.

- [ ] **Step 5: Commit**

```bash
git add internal/ai/project_infer.go internal/ai/project_infer_test.go
git commit -m "feat(onboarding): infer project name/alias/description via claude -p"
```

---

## Task 3: Enrich detected project with inference (caller-side degradation)

**Files:**
- Modify: `internal/ui/project_detect.go`
- Test: `internal/ui/project_detect_test.go`

- [ ] **Step 1: Write the failing test (enrichment merges, never erases)**

Add to `internal/ui/project_detect_test.go`:
```go
import aipkg "github.com/bborn/workflow/internal/ai"

func TestApplyInferredMetadata(t *testing.T) {
	base := &dbProjectStub() // see helper below
	applyInferredMetadata(base, aipkg.ProjectMetadata{Name: "Acme Rocket", Alias: "acme", Description: "Rust CLI"})
	if base.Name != "Acme Rocket" || base.Aliases != "acme" {
		t.Errorf("inference not applied: %+v", base)
	}
	// Empty inference fields must not overwrite existing values.
	applyInferredMetadata(base, aipkg.ProjectMetadata{})
	if base.Name != "Acme Rocket" {
		t.Errorf("empty inference erased name: %+v", base)
	}
}
```
Add helper near top of the test file:
```go
func dbProjectStub() *db.Project { return &db.Project{Name: "acme-rocket", Path: "/x"} }
```
(Adjust import of `db` if needed — `github.com/bborn/workflow/internal/db`.)

- [ ] **Step 2: Run it; expect FAIL (undefined: applyInferredMetadata)**

Run: `go test ./internal/ui/ -run TestApplyInferredMetadata -v`
Expected: FAIL.

- [ ] **Step 3: Implement `applyInferredMetadata`**

Append to `internal/ui/project_detect.go` (add import `aipkg "github.com/bborn/workflow/internal/ai"`):
```go
// applyInferredMetadata overlays LLM-inferred fields onto a detected project.
// Empty inferred fields are ignored so rule-based defaults are never erased.
// The description is appended to Instructions as a one-line summary header when
// instructions are empty (we keep imported instructions when present).
func applyInferredMetadata(p *db.Project, meta aipkg.ProjectMetadata) {
	if p == nil {
		return
	}
	if n := strings.TrimSpace(meta.Name); n != "" {
		p.Name = n
	}
	if a := strings.TrimSpace(meta.Alias); a != "" {
		p.Aliases = a
	}
	if d := strings.TrimSpace(meta.Description); d != "" && strings.TrimSpace(p.Instructions) == "" {
		p.Instructions = d
	}
}
```

- [ ] **Step 4: Run tests; expect PASS**

Run: `go test ./internal/ui/ -run 'TestApplyInferredMetadata|TestIsProjectCandidate' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/project_detect.go internal/ui/project_detect_test.go
git commit -m "feat(onboarding): merge inferred metadata without erasing defaults"
```

---

## Task 4: Fuzzy folder picker component

**Files:**
- Create: `internal/ui/folderpicker.go`
- Test: `internal/ui/folderpicker_test.go`

- [ ] **Step 1: Write the failing test (candidate-root seeding + fuzzy filter, pure)**

Create `internal/ui/folderpicker_test.go`:
```go
package ui

import "testing"

func TestFuzzyFilterFolders(t *testing.T) {
	all := []folderEntry{
		{path: "/home/u/Projects/acme-rocket", isGit: true},
		{path: "/home/u/Projects/rocket-sim", isGit: true},
		{path: "/home/u/work/notes", isGit: false},
	}
	got := fuzzyFilterFolders(all, "rocket")
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d (%+v)", len(got), got)
	}
	// Empty query returns everything, unfiltered.
	if len(fuzzyFilterFolders(all, "")) != 3 {
		t.Errorf("empty query should return all entries")
	}
}
```

- [ ] **Step 2: Run it; expect FAIL**

Run: `go test ./internal/ui/ -run TestFuzzyFilterFolders -v`
Expected: FAIL — `undefined: folderEntry / fuzzyFilterFolders`.

- [ ] **Step 3: Implement the picker**

Create `internal/ui/folderpicker.go`:
```go
package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// folderEntry is one selectable folder in the picker.
type folderEntry struct {
	path  string
	isGit bool
}

func (e folderEntry) label() string { return e.path }

// FolderPickerModel is a fuzzy, type-to-search folder picker. It seeds the list
// with likely project roots and lets the user filter, descend, and pick.
type FolderPickerModel struct {
	input    textinput.Model
	all      []folderEntry // current directory's candidate children + seeds
	filtered []folderEntry
	selected int
	root     string
	width    int
	height   int

	onSelect func(path string)
	onCancel func()
}

// NewFolderPickerModel seeds the picker from common project roots.
func NewFolderPickerModel(width, height int) *FolderPickerModel {
	ti := textinput.New()
	ti.Placeholder = "type to search folders…"
	ti.Focus()
	ti.Prompt = "> "

	m := &FolderPickerModel{input: ti, width: width, height: height}
	m.all = seedCandidateFolders()
	m.filtered = m.all
	return m
}

// seedCandidateFolders gathers likely project dirs: children of ~/Projects,
// ~/src, ~/code, and the home dir, tagging git repos.
func seedCandidateFolders() []folderEntry {
	home, _ := os.UserHomeDir()
	var roots []string
	for _, r := range []string{"Projects", "src", "code", "dev", "work"} {
		roots = append(roots, filepath.Join(home, r))
	}
	seen := map[string]bool{}
	var out []folderEntry
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(root, e.Name())
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, folderEntry{path: p, isGit: dirIsGitRepo(p)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].isGit != out[j].isGit {
			return out[i].isGit // git repos first
		}
		return out[i].path < out[j].path
	})
	return out
}

// fuzzyFilterFolders returns entries fuzzy-matching query (all entries if empty).
func fuzzyFilterFolders(all []folderEntry, query string) []folderEntry {
	if strings.TrimSpace(query) == "" {
		return all
	}
	targets := make([]string, len(all))
	for i, e := range all {
		targets[i] = e.label()
	}
	matches := fuzzy.Find(query, targets)
	out := make([]folderEntry, 0, len(matches))
	for _, mt := range matches {
		out = append(out, all[mt.Index])
	}
	return out
}

func (m *FolderPickerModel) Init() tea.Cmd { return textinput.Blink }

func (m *FolderPickerModel) Update(msg tea.Msg) (*FolderPickerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			if m.onCancel != nil {
				m.onCancel()
			}
			return m, nil
		case "up", "ctrl+k":
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case "down", "ctrl+j":
			if m.selected < len(m.filtered)-1 {
				m.selected++
			}
			return m, nil
		case "right":
			// Descend into the highlighted folder.
			if len(m.filtered) > 0 {
				m.descend(m.filtered[m.selected].path)
			}
			return m, nil
		case "enter":
			if len(m.filtered) > 0 && m.onSelect != nil {
				m.onSelect(m.filtered[m.selected].path)
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filtered = fuzzyFilterFolders(m.all, m.input.Value())
	if m.selected >= len(m.filtered) {
		m.selected = 0
	}
	return m, cmd
}

// descend repopulates the list with the candidate children of dir.
func (m *FolderPickerModel) descend(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var children []folderEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		children = append(children, folderEntry{path: p, isGit: dirIsGitRepo(p)})
	}
	if len(children) == 0 {
		// Leaf: selecting it is the intent.
		if m.onSelect != nil {
			m.onSelect(dir)
		}
		return
	}
	m.root = dir
	m.all = children
	m.input.SetValue("")
	m.filtered = m.all
	m.selected = 0
}

func (m *FolderPickerModel) View() string {
	var b strings.Builder
	b.WriteString(Bold.Render("Set up a project — pick a folder") + "\n\n")
	b.WriteString(m.input.View() + "\n\n")

	visible := m.height - 8
	if visible < 5 {
		visible = 5
	}
	start := 0
	if m.selected >= visible {
		start = m.selected - visible + 1
	}
	end := start + visible
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	for i := start; i < end; i++ {
		e := m.filtered[i]
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		tag := ""
		if e.isGit {
			tag = lipgloss.NewStyle().Foreground(ColorPrimary).Render("  git ●")
		}
		line := prefix + collapseHome(e.path) + tag
		if i == m.selected {
			line = Bold.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + HelpBar.Render(
		HelpKey.Render("↑↓")+" "+HelpDesc.Render("select")+"  "+
			HelpKey.Render("→")+" "+HelpDesc.Render("open")+"  "+
			HelpKey.Render("enter")+" "+HelpDesc.Render("pick")+"  "+
			HelpKey.Render("esc")+" "+HelpDesc.Render("back")))
	return b.String()
}

// collapseHome shortens /home/u/... to ~/... for display.
func collapseHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (m *FolderPickerModel) SetSize(w, h int) { m.width, m.height = w, h }
func (m *FolderPickerModel) OnSelect(fn func(string)) { m.onSelect = fn }
func (m *FolderPickerModel) OnCancel(fn func()) { m.onCancel = fn }
```

Note: `Bold`, `HelpBar`, `HelpKey`, `HelpDesc`, `ColorPrimary` are existing styles in `internal/ui` (used by `filebrowser.go`). Confirm names with `grep -n 'HelpBar\|HelpKey\|HelpDesc\|ColorPrimary\|^var Bold' internal/ui/*.go` before relying on them; substitute the actual identifiers if different.

- [ ] **Step 4: Run tests; expect PASS, and build**

Run: `go test ./internal/ui/ -run TestFuzzyFilterFolders -v && go build ./internal/ui/`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/folderpicker.go internal/ui/folderpicker_test.go
git commit -m "feat(onboarding): fuzzy folder picker (type-to-search, git-tagged)"
```

---

## Task 5: Welcome fork model

**Files:**
- Create: `internal/ui/welcome.go`

(UI-only; verified by build + harness in Task 8. No unit test — rendering/return-value model has no pure logic worth isolating.)

- [ ] **Step 1: Implement the Welcome fork**

Create `internal/ui/welcome.go`:
```go
package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// welcomeChoice is what the user picked on the first-run Welcome fork.
type welcomeChoice int

const (
	welcomeNone welcomeChoice = iota
	welcomeSetupProject
	welcomeStartTask
)

// WelcomeModel is the first-run fork shown when there's no project to suggest:
// "Set up a project" vs "Just start a task" (in the personal project).
type WelcomeModel struct {
	cursor int // 0 = setup, 1 = start task
	width  int
	height int
}

func NewWelcomeModel(width, height int) *WelcomeModel {
	return &WelcomeModel{width: width, height: height}
}

// MoveLeft/MoveRight/Choice drive selection; key handling lives in app.go so it
// composes with the global update loop (mirrors viewProjectDetectConfirm).
func (m *WelcomeModel) MoveLeft()  { m.cursor = 0 }
func (m *WelcomeModel) MoveRight() { m.cursor = 1 }
func (m *WelcomeModel) Choice() welcomeChoice {
	if m.cursor == 0 {
		return welcomeSetupProject
	}
	return welcomeStartTask
}
func (m *WelcomeModel) SetSize(w, h int) { m.width, m.height = w, h }

func (m *WelcomeModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Render("Welcome to TaskYou 👋")
	body := "How do you want to start?"

	btn := func(label string, active bool) string {
		s := lipgloss.NewStyle().Padding(0, 3).Margin(0, 1).Border(lipgloss.RoundedBorder())
		if active {
			s = s.BorderForeground(ColorPrimary).Bold(true)
		} else {
			s = s.BorderForeground(lipgloss.Color("240"))
		}
		return s.Render(label)
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Top,
		btn("Set up a project", m.cursor == 0),
		btn("Just start a task", m.cursor == 1),
	)
	help := HelpBar.Render(
		HelpKey.Render("←/→") + " " + HelpDesc.Render("choose") + "  " +
			HelpKey.Render("enter") + " " + HelpDesc.Render("select"))

	content := lipgloss.JoinVertical(lipgloss.Center, title, "", body, "", buttons, "", help)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary).Padding(1, 3).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/ui/`
Expected: clean build (substitute style identifiers if grep in Task 4 showed different names).

- [ ] **Step 3: Commit**

```bash
git add internal/ui/welcome.go
git commit -m "feat(onboarding): first-run Welcome fork model"
```

---

## Task 6: Launch decision tree + view wiring

**Files:**
- Modify: `internal/ui/app.go`

This rewires the first-load flow. UI integration — verified by build + harness (Task 8).

- [ ] **Step 1: Add view constants**

In `internal/ui/app.go`, in the `const (...)` View block (after `ViewProjectDetectConfirm`, ~line 41), add:
```go
	ViewWelcome      // first-run fork: set up a project vs start a task
	ViewFolderPicker // fuzzy folder picker for "set up a project"
```

- [ ] **Step 2: Add model fields**

In the `AppModel` struct (near `projectDetectConfirm`), add:
```go
	welcomeView   *WelcomeModel
	folderPicker  *FolderPickerModel
```

- [ ] **Step 3: Replace the first-load routing block**

Replace `app.go:835-855` (the `if m.isFirstLoad { ... }` block inside `case tasksLoadedMsg:`) with:
```go
		// First-load onboarding routing (runs once per process start).
		if m.isFirstLoad {
			m.isFirstLoad = false
			m.showWelcome = len(msg.tasks) == 0

			// 1. In a real project folder we don't yet track? Offer to set it up
			//    (LLM-enriched). Works on every launch, until dismissed per-path.
			if model, cmd, offered := m.maybeOfferProjectCreation(); offered {
				return model, cmd
			}

			// 2. No real projects yet (only "personal") and we're in a junk folder:
			//    show the Welcome fork instead of dumping into a task form.
			if m.shouldShowWelcomeFork(msg.tasks) {
				m.welcomeView = NewWelcomeModel(m.width, m.height)
				m.previousView = m.currentView
				m.currentView = ViewWelcome
				return m, nil
			}
		}
```

- [ ] **Step 4: Add the welcome-fork gate and helpers**

Add to `app.go` (near `maybeOfferProjectCreation`):
```go
// shouldShowWelcomeFork reports whether to show the first-run Welcome fork:
// no real projects beyond "personal", and the cwd is not a project candidate
// (otherwise maybeOfferProjectCreation already handled it).
func (m *AppModel) shouldShowWelcomeFork(tasks []*db.Task) bool {
	if m.db == nil {
		return false
	}
	if isProjectCandidate(m.workingDir) {
		return false // suggestion card territory, not the fork
	}
	if !m.onlyPersonalProject() {
		return false
	}
	return len(tasks) == 0 && m.db.IsFirstRun()
}

// onlyPersonalProject reports whether the only project is the auto-created
// "personal" one (i.e. the user hasn't set up a real project yet).
func (m *AppModel) onlyPersonalProject() bool {
	projects, err := m.db.ListProjects()
	if err != nil {
		return false
	}
	for _, p := range projects {
		if p.Name != "personal" {
			return false
		}
	}
	return true
}
```
Confirm the list method name: `grep -n 'func (db \*DB) ListProjects\|func (db \*DB) GetProjects\|func (db \*DB) AllProjects' internal/db/*.go` and use the actual one.

- [ ] **Step 5: Make `maybeOfferProjectCreation` enrich via inference**

Replace the inference section of `maybeOfferProjectCreation` (`app.go:3176-3184`) so it overlays inferred metadata before showing the card:
```go
	detected, source := detectProjectFromDir(m.workingDir)
	if detected == nil {
		return m, nil, false
	}
	// Enrich with claude -p inference; ignore failures (graceful degrade).
	if meta, err := aipkg.InferProjectMetadata(m.workingDir, ""); err == nil {
		applyInferredMetadata(detected, meta)
	}
	detected.Name = uniqueProjectName(m.db, detected.Name)

	m.projectDetectionOffered = true
	model, cmd := m.showProjectDetectConfirm(detected, source)
	return model, cmd, true
```
Add import `aipkg "github.com/bborn/workflow/internal/ai"` to `app.go` if not present.

Note on blocking: `InferProjectMetadata` runs synchronously here (≤12s timeout). If a follow-up wants the non-blocking spinner from the design, move this into a `tea.Cmd` that emits a `projectInferredMsg`; for v1 the synchronous call with timeout is acceptable and far simpler. Keep v1 synchronous.

- [ ] **Step 6: Update the suggestion card copy to show inferred fields**

In `showProjectDetectConfirm` (`app.go:3192-3199`), include alias/description in the description builder:
```go
	desc.WriteString(fmt.Sprintf("This directory looks like a project.\n\nName: %s\n", project.Name))
	if project.Aliases != "" {
		desc.WriteString(fmt.Sprintf("Alias: %s\n", project.Aliases))
	}
	desc.WriteString(fmt.Sprintf("Path: %s\n", project.Path))
	if project.Instructions != "" {
		if instructionSource != "" {
			desc.WriteString(fmt.Sprintf("Instructions: imported from %s\n", instructionSource))
		} else {
			desc.WriteString("Description: " + firstLine(project.Instructions) + "\n")
		}
	}
	desc.WriteString(fmt.Sprintf("Worktrees: %v\n", project.UseWorktrees))
	desc.WriteString("\nYou can edit any of this later in Settings.")
```
Add helper:
```go
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
```

- [ ] **Step 7: Render + route the new views**

In `View()` switch (`app.go:1475`), add cases:
```go
	case ViewWelcome:
		if m.welcomeView != nil {
			return m.welcomeView.View()
		}
	case ViewFolderPicker:
		if m.folderPicker != nil {
			return m.folderPicker.View()
		}
```

In `Update()`'s key handling, add a routed update for the two views. Near the other `m.currentView == ViewX` key blocks (e.g. around `app.go:739`), add:
```go
		if m.currentView == ViewWelcome && m.welcomeView != nil {
			switch keyMsg.String() {
			case "left", "h":
				m.welcomeView.MoveLeft()
				return m, nil
			case "right", "l":
				m.welcomeView.MoveRight()
				return m, nil
			case "enter":
				switch m.welcomeView.Choice() {
				case welcomeSetupProject:
					m.folderPicker = NewFolderPickerModel(m.width, m.height)
					m.folderPicker.OnSelect(func(path string) { /* set in Step 8 */ })
					m.folderPicker.OnCancel(func() {})
					m.currentView = ViewFolderPicker
					return m, m.folderPicker.Init()
				case welcomeStartTask:
					m.welcomeView = nil
					m.newTaskForm = NewFormModel(m.db, m.width, m.height, m.workingDir, m.availableExecutors)
					m.previousView = ViewDashboard
					m.currentView = ViewNewTask
					return m, m.newTaskForm.Init()
				}
			case "esc":
				m.welcomeView = nil
				m.currentView = ViewDashboard
				return m, nil
			}
		}
		if m.currentView == ViewFolderPicker && m.folderPicker != nil {
			var cmd tea.Cmd
			m.folderPicker, cmd = m.folderPicker.Update(keyMsg)
			return m, cmd
		}
```

- [ ] **Step 8: Wire folder-picker selection → create+enrich project**

Define the OnSelect/OnCancel callbacks (replace the placeholder in Step 7) so picking a folder builds a detected project, enriches it, and shows the same suggestion card (reusing `showProjectDetectConfirm`):
```go
				case welcomeSetupProject:
					m.folderPicker = NewFolderPickerModel(m.width, m.height)
					m.folderPicker.OnSelect(func(path string) {
						detected, source := detectProjectFromDir(path)
						if detected == nil {
							// Folder had no signals; treat the chosen dir as a plain project.
							detected = &db.Project{Name: inferProjectName(path), Path: filepath.Clean(path), UseWorktrees: dirIsGitRepo(path)}
						}
						if meta, err := aipkg.InferProjectMetadata(path, ""); err == nil {
							applyInferredMetadata(detected, meta)
						}
						detected.Name = uniqueProjectName(m.db, detected.Name)
						m.folderPicker = nil
						mdl, c := m.showProjectDetectConfirm(detected, source)
						_ = mdl
						m.pendingCmd = c // see note
					})
					m.folderPicker.OnCancel(func() {
						m.folderPicker = nil
						m.welcomeView = NewWelcomeModel(m.width, m.height)
						m.currentView = ViewWelcome
					})
					m.currentView = ViewFolderPicker
					return m, m.folderPicker.Init()
```
Note: bubbletea callbacks can't return a `tea.Cmd` directly. Two clean options — pick ONE and apply consistently:
  - (a) Have `FolderPickerModel.Update` return a `folderPickedMsg{path}` on `enter` instead of invoking `onSelect`, and handle that msg in `app.go`'s `Update` (preferred — idiomatic bubbletea). In that case drop `OnSelect`/`pendingCmd` and add `case folderPickedMsg:` to the app update that runs the detect+enrich+`showProjectDetectConfirm` and returns its cmd.
  - (b) Keep callbacks but store the resulting `tea.Cmd` on the model (`m.pendingCmd`) and flush it after `Update` returns.

Implement option (a). Replace the picker's `enter` case (Task 4, Step 3) with:
```go
		case "enter":
			if len(m.filtered) > 0 {
				return m, func() tea.Msg { return folderPickedMsg{path: m.filtered[m.selected].path} }
			}
			return m, nil
```
and define in `folderpicker.go`:
```go
type folderPickedMsg struct{ path string }
```
Then in `app.go` `Update`, add a top-level `case folderPickedMsg:` that performs detect → enrich → `showProjectDetectConfirm` and returns its `tea.Cmd`. Remove the `onSelect`/`pendingCmd` plumbing.

- [ ] **Step 9: Build the whole binary**

Run: `go build ./cmd/task && go vet ./internal/ui/`
Expected: clean build.

- [ ] **Step 10: Commit**

```bash
git add internal/ui/app.go internal/ui/folderpicker.go
git commit -m "feat(onboarding): launch decision tree, welcome fork, picker→enriched suggestion"
```

---

## Task 7: Harden project-form validation (never lose input)

**Files:**
- Modify: `internal/ui/settings.go`

- [ ] **Step 1: Route the bail-out validation branches through the state-preserving reshow**

In `saveProject` (`settings.go`), change the two branches that currently set `m.err` and `return m, nil` to use `reshowProjectFormWithError`, so every field is retained.

Replace `settings.go:583-588` (`path does not exist`):
```go
		if absPath != m.editProject.Path {
			if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
				return m.reshowProjectFormWithError(fmt.Errorf("path does not exist: %s", absPath))
			}
		}
```
Replace `settings.go:615-617` (`path is not a directory`):
```go
		if !info.IsDir() {
			return m.reshowProjectFormWithError(fmt.Errorf("path is not a directory: %s", path))
		}
```
Also locate any "name is required" / duplicate-name validation in `saveProject` and route it the same way (grep: `grep -n 'm.err =' internal/ui/settings.go`). For each project-form validation, prefer `return m.reshowProjectFormWithError(err)` over `m.err = err; return m, nil`.

- [ ] **Step 2: Ensure non-git folders skip worktrees/git entirely (git optional)**

Confirm `saveProject` already does this (`settings.go:620-648`): when `useWorktrees` is false it does NOT init git. The new detection sets `UseWorktrees=false` for non-git folders, so this path is exercised. Add a guard so a non-git, worktrees-off project never triggers `initGitRepo`. No code change expected if the existing `if useWorktrees { ... }` guards hold — verify by reading the branch and add a regression note.

- [ ] **Step 3: Build + manual reasoning check**

Run: `go build ./internal/ui/`
Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add internal/ui/settings.go
git commit -m "fix(onboarding): preserve project-form input on validation errors"
```

---

## Task 8: First-run QA scenario script + live verification

**Files:**
- Create: `scripts/qa/ty-qa-firstrun.sh`

- [ ] **Step 1: Write the scenario script**

Create `scripts/qa/ty-qa-firstrun.sh` (mirrors the existing harness; controls cwd, asserts via screen capture):
```bash
#!/usr/bin/env bash
# Drive the first-run experience across folder types against an isolated instance.
# Usage: ty-qa-firstrun.sh
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

ROOT="$TY_QA_ROOT/firstrun"
GITPROJ="$ROOT/acme-rocket"
PLAIN="$ROOT/just-a-folder"
SID="$TY_UI_SESSION"

echo "==> Building"; ( cd "$TY_REPO_ROOT" && go build -o "$TY_BIN" ./cmd/task )

setup_dirs() {
  rm -rf "$ROOT"; mkdir -p "$GITPROJ" "$PLAIN"
  git -C "$GITPROJ" init -q
  git -C "$GITPROJ" config user.email qa@ty.local
  git -C "$GITPROJ" config user.name "ty qa"
  printf '# Acme Rocket\n\nTool for launching rockets.\n' > "$GITPROJ/README.md"
  git -C "$GITPROJ" add -A && git -C "$GITPROJ" commit -qm init
}

launch() { # $1 = cwd
  tmux kill-session -t "$SID" 2>/dev/null || true
  rm -f "$WORKTREE_DB_PATH" "$TY_QA_STATE"
  tmux new-session -d -s "$SID" -x 220 -y 50 -n tui -c "$1" \
    "WORKTREE_DB_PATH='$WORKTREE_DB_PATH' WORKTREE_SESSION_ID='$WORKTREE_SESSION_ID' '$TY_BIN' --debug-state-file '$TY_QA_STATE'"
  sleep 5
}

cap() { tmux capture-pane -t "${SID}:tui" -p | sed 's/[[:space:]]*$//' | grep -v '^[[:space:]]*$'; }

setup_dirs
echo "### Scenario A: git repo → suggestion card"; launch "$GITPROJ"; cap | head -25
echo "### Scenario B: plain folder → welcome fork";  launch "$PLAIN";  cap | head -25
echo "==> tmux attach -t $SID to drive manually; ty-qa-down.sh to tear down"
```

- [ ] **Step 2: Run it; eyeball both scenarios**

Run: `chmod +x scripts/qa/ty-qa-firstrun.sh && scripts/qa/ty-qa-firstrun.sh`
Expected:
- Scenario A shows the upgraded suggestion card with `Name:` (LLM or basename), `Alias:`, `Worktrees: true`.
- Scenario B shows the Welcome fork: "Welcome to TaskYou 👋", `[Set up a project] [Just start a task]`.

- [ ] **Step 3: Drive the fork → picker → create, and verify a non-git project**

Manually (or scripted with `ty-qa-key.sh`): from Scenario B press `enter` on "Set up a project" → fuzzy picker appears; type to filter; `enter` to pick `just-a-folder` → suggestion card → confirm → board shows a project with worktrees off. Assert no git repo was created in `just-a-folder`:
Run: `test ! -d /tmp/ty-qa/firstrun/just-a-folder/.git && echo "OK: non-git project, no git created"`
Expected: `OK: ...`.

- [ ] **Step 4: Nested-repo worktree sanity (the open question from the design)**

Create a parent git repo with a nested git repo inside, register it as a worktree project, create a task, and confirm the worktree is created without error:
```bash
P=/tmp/ty-qa/firstrun/parent
rm -rf "$P"; mkdir -p "$P/sub"
git -C "$P" init -q && git -C "$P" config user.email q@q && git -C "$P" config user.name q
echo x > "$P/a.txt"; git -C "$P" add -A; git -C "$P" commit -qm init
git -C "$P/sub" init -q  # nested repo
WORKTREE_DB_PATH=$WORKTREE_DB_PATH WORKTREE_SESSION_ID=$WORKTREE_SESSION_ID "$TY_BIN" projects create parent --path "$P" >/dev/null
WORKTREE_DB_PATH=$WORKTREE_DB_PATH WORKTREE_SESSION_ID=$WORKTREE_SESSION_ID "$TY_BIN" create "nested test" -p parent
# Then drive the daemon/agent path per scripts/qa/README.md tier 2, or inspect .task-worktrees:
ls "$P/.task-worktrees" 2>/dev/null && echo "OK: worktree created with nested repo present"
```
Expected: a worktree directory is created; no fatal error about the nested repo.

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/ty-qa-firstrun.sh
git commit -m "test(onboarding): scripted first-run QA scenario"
```

---

## Task 9: Full regression + lint

- [ ] **Step 1: Run the full suite**

Run: `go build ./... && go test ./internal/ui/... ./internal/ai/... -v`
Expected: build clean, tests PASS.

- [ ] **Step 2: Lint (per project)**

Run: `gofmt -l internal/ui internal/ai && go vet ./internal/ui/... ./internal/ai/...`
Expected: no files listed by gofmt, no vet errors.

- [ ] **Step 3: Tear down QA instance**

Run: `scripts/qa/ty-qa-down.sh --purge`
Expected: sessions killed, isolated DB removed.

---

## Self-Review notes (filled during writing)

- **Spec coverage:** launch tree (T6) ✓; smart heuristic/deny-list (T1) ✓; LLM inference via `claude -p` + degradation (T2/T3/T6) ✓; folder picker (T4) ✓; welcome fork "every junk-folder launch while only personal exists" (T6 `shouldShowWelcomeFork`) ✓; git optional, no worktree prompt for non-git (T1 `UseWorktrees`, T7 step 2) ✓; never lose input (T7) ✓; sub-git-repos confirmed (T8 step 4) ✓.
- **Type consistency:** `ProjectMetadata{Name,Alias,Description}` used identically in T2/T3/T6; `folderEntry{path,isGit}` + `fuzzyFilterFolders` + `folderPickedMsg` consistent across T4/T6; `welcomeChoice` enum consistent T5/T6.
- **Known verification gaps to resolve during impl (not placeholders — explicit checks):** exact style identifiers in `internal/ui` (grep in T4 step 3), the project-list method name (`ListProjects` vs `GetProjects`, T6 step 4), and import-cycle check for `internal/ai`→`internal/executor` (T2 step 4). Each has a concrete grep/command to settle it.
```
