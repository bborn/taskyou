package ui

import (
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/db"
)

// --- pure helpers ---------------------------------------------------------

func TestIsMarkdown(t *testing.T) {
	cases := map[string]bool{
		"README.md":      true,
		"docs/guide.MD":  true,
		"notes.markdown": true,
		"x.mdown":        true,
		"main.go":        false,
		"Makefile":       false,
		"a.txt":          false,
	}
	for path, want := range cases {
		if got := isMarkdown(path); got != want {
			t.Errorf("isMarkdown(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestStatusGlyph(t *testing.T) {
	cases := map[string]string{"A": "A", "M": "M", "D": "D", "?": "+"}
	for status, want := range cases {
		if got := statusGlyph(status); got != want {
			t.Errorf("statusGlyph(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 20); got != "short" {
		t.Errorf("truncate kept-as-is failed: %q", got)
	}
	got := truncate("internal/ui/diffviewer.go", 10)
	if got == "" || []rune(got)[0] != '…' {
		t.Errorf("truncate should left-trim with ellipsis, got %q", got)
	}
	if w := lipglossWidth(got); w > 10 {
		t.Errorf("truncate width = %d, want <= 10", w)
	}
	if truncate("anything", 0) != "" {
		t.Error("truncate with width 0 should be empty")
	}
}

func TestClampText(t *testing.T) {
	small := "hello"
	if clampText(small) != small {
		t.Error("clampText should not change small text")
	}
	big := strings.Repeat("x", maxDiffBytes+100)
	out := clampText(big)
	if len(out) <= maxDiffBytes || !strings.Contains(out, "truncated") {
		t.Errorf("clampText should truncate large text with a notice (len=%d)", len(out))
	}
}

func TestSortDiffFiles(t *testing.T) {
	files := []diffFileEntry{
		{path: "z.go"}, {path: "a.go"}, {path: "m/b.go"},
	}
	sortDiffFiles(files)
	if files[0].path != "a.go" || files[1].path != "m/b.go" || files[2].path != "z.go" {
		t.Errorf("sortDiffFiles wrong order: %+v", files)
	}
}

// --- git integration ------------------------------------------------------

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := osExec.Command("git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// newTestRepo builds a worktree on a feature branch with a mix of committed,
// uncommitted, and untracked changes vs main, and returns its path.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Title\n\noriginal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")

	git(t, dir, "checkout", "-b", "feature")
	// Committed modification.
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "-am", "change base")
	// Uncommitted modification of a tracked markdown file.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Title\n\nupdated body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Untracked new file.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("brand new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadChangedFiles(t *testing.T) {
	dir := newTestRepo(t)
	base, label, files, err := loadChangedFiles(dir, "")
	if err != nil {
		t.Fatalf("loadChangedFiles: %v", err)
	}
	if label != "main" {
		t.Errorf("base label = %q, want main", label)
	}
	if base == "" || base == "HEAD" {
		t.Errorf("expected a merge-base sha, got %q", base)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.path] = f.status
	}
	if got["base.go"] != "M" {
		t.Errorf("base.go status = %q, want M", got["base.go"])
	}
	if got["README.md"] != "M" {
		t.Errorf("README.md status = %q, want M", got["README.md"])
	}
	if got["new.txt"] != "?" {
		t.Errorf("new.txt status = %q, want ? (untracked)", got["new.txt"])
	}
}

func TestLoadFileContentDiffAndRendered(t *testing.T) {
	dir := newTestRepo(t)
	base, _, _, err := loadChangedFiles(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	// Diff mode for a tracked, modified file.
	diff, kind, empty, err := loadFileContent(dir, base, diffFileEntry{path: "base.go", status: "M"}, false)
	if err != nil || empty || kind != diffKindDiff {
		t.Fatalf("diff load: err=%v empty=%v kind=%v", err, empty, kind)
	}
	if !strings.Contains(diff, "+func main() { println") {
		t.Errorf("diff missing the added line:\n%s", diff)
	}

	// Diff mode for an untracked file (whole file shows as added).
	udiff, _, uempty, err := loadFileContent(dir, base, diffFileEntry{path: "new.txt", status: "?"}, false)
	if err != nil {
		t.Fatalf("untracked diff: %v", err)
	}
	if uempty || !strings.Contains(udiff, "brand new") {
		t.Errorf("untracked diff should include new content:\n%s", udiff)
	}

	// Rendered mode reads the working-tree file.
	rendered, kind, empty, err := loadFileContent(dir, base, diffFileEntry{path: "README.md", status: "M"}, true)
	if err != nil || empty || kind != diffKindFile {
		t.Fatalf("rendered load: err=%v empty=%v kind=%v", err, empty, kind)
	}
	if !strings.Contains(rendered, "updated body") {
		t.Errorf("rendered file should reflect working tree:\n%s", rendered)
	}
}

func TestResolveDiffBaseFallback(t *testing.T) {
	// A non-repo dir should fall back to HEAD/HEAD without panicking.
	base, label := resolveDiffBase(t.TempDir(), "")
	if base != "HEAD" || label != "HEAD" {
		t.Errorf("resolveDiffBase fallback = (%q,%q), want (HEAD,HEAD)", base, label)
	}
}

// --- DetailModel integration ----------------------------------------------

func newViewerModel(t *testing.T, worktree string) *DetailModel {
	t.Helper()
	m := &DetailModel{
		task:    &db.Task{ID: 1, WorktreePath: worktree, Body: "task body"},
		width:   120,
		height:  40,
		focused: true,
	}
	m.initViewport()
	return m
}

func TestContentViewportWidthShrinksWhenViewerActive(t *testing.T) {
	m := newViewerModel(t, "")
	full := m.contentViewportWidth()
	m.diff = &diffViewer{active: true}
	narrowed := m.contentViewportWidth()
	if narrowed >= full {
		t.Errorf("expected content width to shrink when viewer active: full=%d narrowed=%d", full, narrowed)
	}
	if narrowed < 10 {
		t.Errorf("content width should be clamped to >= 10, got %d", narrowed)
	}
}

func TestViewSignatureChangesWithViewerState(t *testing.T) {
	m := newViewerModel(t, "")
	sigBase := m.viewSignature("h", "help")

	m.diff = &diffViewer{active: true, files: []diffFileEntry{{path: "a"}, {path: "b"}}}
	sigActive := m.viewSignature("h", "help")
	if sigActive == sigBase {
		t.Error("viewSignature should change when the viewer becomes active")
	}

	m.diff.selected = 1
	sigSelected := m.viewSignature("h", "help")
	if sigSelected == sigActive {
		t.Error("viewSignature should change when the selected file changes")
	}

	m.diff.showRendered = true
	if m.viewSignature("h", "help") == sigSelected {
		t.Error("viewSignature should change when toggling rendered mode")
	}
}

func TestOpenAndCloseFileViewer(t *testing.T) {
	dir := newTestRepo(t)
	m := newViewerModel(t, dir)

	if m.FileViewerActive() {
		t.Fatal("viewer should start inactive")
	}
	cmd := m.OpenFileViewer()
	if !m.FileViewerActive() {
		t.Fatal("viewer should be active after OpenFileViewer")
	}
	if cmd == nil {
		t.Fatal("OpenFileViewer should return a load command for a worktree task")
	}
	// renderContent should now produce the viewer pane (loading state).
	if !strings.Contains(m.renderContent(), "Changed") && !strings.Contains(m.renderContent(), "Loading") && !strings.Contains(m.renderContent(), "Diff") {
		t.Errorf("renderContent should show viewer content, got:\n%s", m.renderContent())
	}

	// Run the load command and feed the result back.
	msg := cmd()
	loaded, ok := msg.(diffFilesLoadedMsg)
	if !ok {
		t.Fatalf("expected diffFilesLoadedMsg, got %T", msg)
	}
	m.HandleDiffFilesLoaded(loaded)
	if len(m.diff.files) == 0 {
		t.Fatal("expected changed files to be populated")
	}

	// Navigate down through the tree.
	before := m.diff.selected
	handled, _ := m.HandleFileViewerKey(tea.KeyMsg{Type: tea.KeyDown})
	if !handled {
		t.Error("down key should be consumed by the viewer")
	}
	if m.diff.selected == before && len(m.diff.files) > 1 {
		t.Error("down key should advance the selection")
	}

	// esc closes the viewer.
	handled, _ = m.HandleFileViewerKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled || m.FileViewerActive() {
		t.Error("esc should close the viewer")
	}
	// Back to normal task content (the "Description" label is always present
	// for a task with a body, and is not split by glamour styling).
	if !strings.Contains(m.renderContent(), "Description") {
		t.Error("closing the viewer should restore task content")
	}
}

func TestHandleDiffContentLoadedRenders(t *testing.T) {
	dir := newTestRepo(t)
	m := newViewerModel(t, dir)
	m.OpenFileViewer()
	m.diff.files = []diffFileEntry{{path: "README.md", status: "M"}}
	m.diff.selected = 0
	m.diff.showRendered = true

	m.HandleDiffContentLoaded(diffContentLoadedMsg{
		taskID:       1,
		path:         "README.md",
		showRendered: true,
		text:         "# Heading\n\nbody text\n",
		kind:         diffKindFile,
		isMD:         true,
	})
	if m.diff.rendered == "" {
		t.Fatal("expected rendered markdown content")
	}
	// glamour styles each word separately, so check the words individually.
	if !strings.Contains(m.diff.rendered, "body") || !strings.Contains(m.diff.rendered, "text") {
		t.Errorf("rendered markdown should contain the body words:\n%s", m.diff.rendered)
	}
}

func TestFileViewerKeyIgnoredWhenInactive(t *testing.T) {
	m := newViewerModel(t, "")
	if handled, _ := m.HandleFileViewerKey(tea.KeyMsg{Type: tea.KeyDown}); handled {
		t.Error("keys should not be consumed when the viewer is inactive")
	}
}

// lipglossWidth is a tiny wrapper so the helper test does not import lipgloss.
func lipglossWidth(s string) int {
	return len([]rune(s))
}
