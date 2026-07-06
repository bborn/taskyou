package ui

// Git-aware, read-only file & diff viewer for the task detail view.
//
// This renders entirely inside the existing DetailModel viewport box: a file
// tree (left column) listing the files the task branch changed vs its base, and
// a content pane (the scrollable viewport) showing the unified diff for the
// selected file — with chroma syntax highlighting, and glamour-rendered markdown
// for .md files when toggled to "rendered" mode.
//
// The viewer never writes to the viewport directly: every content update goes
// through DetailModel.setViewportContent(), and all of its display state is
// folded into DetailModel.viewSignature(), so the View() render cache stays
// correct (see detail.go).

import (
	"fmt"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// maxDiffBytes bounds how much text we render for a single file so a huge diff
// or generated file can't lock up the UI thread during highlighting.
const maxDiffBytes = 400 * 1024

// diffFileEntry is one changed file in the task branch.
type diffFileEntry struct {
	path   string // path relative to the worktree root
	status string // git status letter: M, A, D, R, C, or "?" for untracked
}

// diffViewer holds all state for the detail view's file/diff viewer. A nil or
// inactive diffViewer means the detail view renders its normal task content.
type diffViewer struct {
	active bool

	worktree  string
	base      string // resolved base ref (a merge-base sha, or "HEAD" fallback)
	baseLabel string // human label for the base, e.g. "main"

	loading bool   // file list is loading
	loadErr string // file list load error (user-visible)

	files    []diffFileEntry
	selected int

	showRendered bool // false = unified diff, true = rendered file content

	// Content pane state for the currently selected file.
	contentLoading bool
	contentPath    string   // path the rendered content belongs to
	contentMode    bool     // showRendered value the content was rendered for
	rendered       string   // final, ready-to-display content string
	rawLines       []string // raw (unhighlighted) content lines; set in diff mode for the line cursor
	cursor         int      // cursor line index into rawLines (diff mode only)

	// Interactive review: comments the user attaches to the diff, to be sent to
	// the task's live executor (or copied to the clipboard when none is running).
	comments    []reviewComment
	commenting  bool            // true while the comment text input is open
	input       textinput.Model // comment text input
	statusMsg   string          // transient status line (e.g. "Sent 3 comments")
	statusIsErr bool
}

// reviewComment is one piece of review feedback anchored to a file (and, in diff
// mode, a specific line quoted from the diff).
type reviewComment struct {
	file string
	line string // quoted diff line the comment anchors to ("" for file-level)
	body string
}

// --- messages -------------------------------------------------------------

type diffFilesLoadedMsg struct {
	taskID    int64
	base      string
	baseLabel string
	files     []diffFileEntry
	err       error
}

type diffContentLoadedMsg struct {
	taskID       int64
	path         string
	showRendered bool
	// raw text plus a hint about how to render it on the main thread
	text  string
	kind  diffContentKind
	isMD  bool
	err   error
	empty bool // no changes / nothing to show
}

type reviewSentMsg struct {
	taskID       int64
	count        int
	viaClipboard bool
	err          error
}

type diffContentKind int

const (
	diffKindDiff diffContentKind = iota // unified diff text -> chroma "diff"
	diffKindFile                        // raw file content -> chroma by name / glamour
)

// --- public hooks used by DetailModel / app.go ----------------------------

// FileViewerActive reports whether the file/diff viewer is currently open.
func (m *DetailModel) FileViewerActive() bool {
	return m.diff != nil && m.diff.active
}

// OpenFileViewer opens the read-only file/diff viewer for the task's worktree
// and returns the command that asynchronously loads the changed-file list.
func (m *DetailModel) OpenFileViewer() tea.Cmd {
	if m.task == nil {
		return nil
	}
	if m.diff == nil {
		m.diff = &diffViewer{}
	}
	d := m.diff
	d.active = true
	d.worktree = m.task.WorktreePath
	d.loading = true
	d.loadErr = ""
	d.files = nil
	d.selected = 0
	d.showRendered = false
	d.rendered = ""
	d.rawLines = nil
	d.cursor = 0
	d.contentPath = ""
	d.contentLoading = false
	d.commenting = false
	d.statusMsg = ""
	// Note: d.comments is intentionally preserved so reopening the viewer keeps
	// any unsent review feedback.

	// Narrow the viewport to the content column and reset scroll.
	if m.ready {
		m.viewport.Width = m.contentViewportWidth()
		m.viewport.GotoTop()
		m.setViewportContent()
	}

	if d.worktree == "" {
		// Nothing to load; renderDiffContent shows an empty state.
		d.loading = false
		return nil
	}
	taskID := m.task.ID
	worktree := d.worktree
	source := m.task.SourceBranch
	return func() tea.Msg {
		base, label, files, err := loadChangedFiles(worktree, source)
		return diffFilesLoadedMsg{taskID: taskID, base: base, baseLabel: label, files: files, err: err}
	}
}

// CloseFileViewer closes the viewer and restores normal task content.
func (m *DetailModel) CloseFileViewer() {
	if m.diff == nil || !m.diff.active {
		return
	}
	m.diff.active = false
	if m.ready {
		m.viewport.Width = m.contentViewportWidth()
		m.viewport.GotoTop()
		m.setViewportContent()
	}
}

// HandleFileViewerKey handles a key while the viewer is open. It returns whether
// the key was consumed and any command to run. Keys it does not consume (e.g.
// j/k scrolling) fall through to the normal viewport handling in app.go.
func (m *DetailModel) HandleFileViewerKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.diff == nil || !m.diff.active {
		return false, nil
	}
	d := m.diff
	switch msg.String() {
	case "esc", "v", "q":
		m.CloseFileViewer()
		return true, nil
	case "up":
		if len(d.files) > 0 {
			d.selected--
			if d.selected < 0 {
				d.selected = len(d.files) - 1
			}
			return true, m.loadSelectedFileContent()
		}
		return true, nil
	case "down":
		if len(d.files) > 0 {
			d.selected++
			if d.selected >= len(d.files) {
				d.selected = 0
			}
			return true, m.loadSelectedFileContent()
		}
		return true, nil
	case "tab":
		if len(d.files) > 0 {
			d.showRendered = !d.showRendered
			return true, m.loadSelectedFileContent()
		}
		return true, nil
	case "j":
		// Move the line cursor down in diff mode; otherwise fall through to scroll.
		if d.cursorActive() {
			m.moveCursor(1)
			return true, nil
		}
		return false, nil
	case "k":
		if d.cursorActive() {
			m.moveCursor(-1)
			return true, nil
		}
		return false, nil
	case "c":
		// Start a review comment on the current file/line.
		if len(d.files) > 0 {
			return true, m.startComment()
		}
		return true, nil
	case "s":
		// Send the collected review to the executor (or clipboard).
		if len(d.comments) > 0 {
			return true, m.sendReviewCmd()
		}
		d.statusMsg = "No comments yet — press c to add one"
		d.statusIsErr = true
		m.setViewportContent()
		return true, nil
	}
	return false, nil
}

// moveCursor moves the diff line cursor and keeps it visible in the viewport.
func (m *DetailModel) moveCursor(delta int) {
	d := m.diff
	n := len(d.rawLines)
	if n == 0 {
		return
	}
	d.cursor += delta
	if d.cursor < 0 {
		d.cursor = 0
	}
	if d.cursor >= n {
		d.cursor = n - 1
	}
	// Keep the cursor row (offset by the 2-line content header) on screen.
	row := d.cursor + diffHeaderLines
	if row < m.viewport.YOffset {
		m.viewport.SetYOffset(row)
	} else if row >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.SetYOffset(row - m.viewport.Height + 1)
	}
	m.setViewportContent()
}

// diffHeaderLines is the number of lines renderDiffContent prepends before the
// actual diff/file body (title + blank). Used for cursor scroll math.
const diffHeaderLines = 2

// --- interactive review comments ------------------------------------------

// InCommentInput reports whether the comment text input is currently open.
func (m *DetailModel) InCommentInput() bool {
	return m.diff != nil && m.diff.active && m.diff.commenting
}

// startComment opens the comment text input for the current file/line.
func (m *DetailModel) startComment() tea.Cmd {
	d := m.diff
	ti := textinput.New()
	ti.Placeholder = "comment for the agent…"
	ti.Prompt = "› "
	ti.CharLimit = 500
	ti.Width = m.contentViewportWidth() - 6
	ti.Focus()
	d.input = ti
	d.commenting = true
	d.statusMsg = ""
	d.statusIsErr = false
	m.setViewportContent()
	return textinput.Blink
}

// UpdateCommentInput feeds a message to the open comment input. Enter saves the
// comment, Esc cancels; everything else edits the text.
func (m *DetailModel) UpdateCommentInput(msg tea.Msg) (*DetailModel, tea.Cmd) {
	d := m.diff
	if d == nil || !d.commenting {
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			d.commenting = false
			d.input.Blur()
			m.setViewportContent()
			return m, nil
		case "enter":
			body := strings.TrimSpace(d.input.Value())
			if body != "" {
				d.comments = append(d.comments, reviewComment{
					file: d.selectedPath(),
					line: d.cursorAnchor(),
					body: body,
				})
				d.statusMsg = fmt.Sprintf("Added comment (%d pending)", len(d.comments))
				d.statusIsErr = false
			}
			d.commenting = false
			d.input.Blur()
			m.setViewportContent()
			return m, nil
		}
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	m.setViewportContent()
	return m, cmd
}

// cursorAnchor returns the trimmed diff line the cursor is on (without the
// leading +/-/space marker), or "" for file-level comments.
func (d *diffViewer) cursorAnchor() string {
	if !d.cursorActive() || d.cursor < 0 || d.cursor >= len(d.rawLines) {
		return ""
	}
	line := d.rawLines[d.cursor]
	if len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' ') {
		line = line[1:]
	}
	return strings.TrimSpace(line)
}

// sendReviewCmd composes the collected comments and delivers them to the task's
// live executor pane, or copies them to the clipboard when no agent is running.
func (m *DetailModel) sendReviewCmd() tea.Cmd {
	if m.task == nil || m.diff == nil || len(m.diff.comments) == 0 {
		return nil
	}
	taskID := m.task.ID
	pane := m.claudePaneID
	count := len(m.diff.comments)
	line := composeReviewLine(m.task.BranchName, m.diff.comments)
	block := composeReviewBlock(m.task.BranchName, m.diff.comments)
	return func() tea.Msg {
		if pane != "" {
			if err := sendLiteralToPane(pane, line); err != nil {
				return reviewSentMsg{taskID: taskID, count: count, err: err}
			}
			return reviewSentMsg{taskID: taskID, count: count}
		}
		// No live executor — fall back to the clipboard so the review isn't lost.
		if err := clipboard.WriteAll(block); err != nil {
			return reviewSentMsg{taskID: taskID, count: count, err: err}
		}
		return reviewSentMsg{taskID: taskID, count: count, viaClipboard: true}
	}
}

// HandleReviewSent applies the result of a send attempt.
func (m *DetailModel) HandleReviewSent(msg reviewSentMsg) {
	if m.diff == nil || m.task == nil || msg.taskID != m.task.ID {
		return
	}
	d := m.diff
	switch {
	case msg.err != nil:
		d.statusMsg = "Send failed: " + msg.err.Error()
		d.statusIsErr = true
	case msg.viaClipboard:
		d.comments = nil
		d.statusMsg = fmt.Sprintf("No live executor — copied %d comments to clipboard", msg.count)
		d.statusIsErr = false
	default:
		d.comments = nil
		d.statusMsg = fmt.Sprintf("Sent %d comments to the agent", msg.count)
		d.statusIsErr = false
	}
	m.setViewportContent()
}

// sendLiteralToPane sends literal text + Enter to a specific tmux pane id. We
// target the persisted pane id (not the daemon session) because the detail view
// joins the executor pane into the UI session.
func sendLiteralToPane(paneID, text string) error {
	if err := osExec.Command("tmux", "send-keys", "-t", paneID, "-l", text).Run(); err != nil {
		return err
	}
	return osExec.Command("tmux", "send-keys", "-t", paneID, "Enter").Run()
}

// composeReviewLine builds a single-line review message (safe to send to a TUI
// agent without embedded newlines triggering an early submit).
func composeReviewLine(branch string, comments []reviewComment) string {
	var b strings.Builder
	b.WriteString("Code review")
	if branch != "" {
		b.WriteString(" on " + branch)
	}
	b.WriteString(fmt.Sprintf(" (%d comments): ", len(comments)))
	for i, c := range comments {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(fmt.Sprintf("[%d] %s", i+1, c.file))
		if c.line != "" {
			b.WriteString(fmt.Sprintf(" @ `%s`", c.line))
		}
		b.WriteString(": " + c.body)
		if !strings.HasSuffix(strings.TrimSpace(c.body), ".") {
			b.WriteString(".")
		}
	}
	b.WriteString(" Please address these in the worktree.")
	return b.String()
}

// composeReviewBlock builds a readable multi-line version for the clipboard.
func composeReviewBlock(branch string, comments []reviewComment) string {
	var b strings.Builder
	b.WriteString("Code review")
	if branch != "" {
		b.WriteString(" on " + branch)
	}
	b.WriteString(":\n\n")
	for i, c := range comments {
		b.WriteString(fmt.Sprintf("%d. %s", i+1, c.file))
		if c.line != "" {
			b.WriteString(fmt.Sprintf("\n   > %s", c.line))
		}
		b.WriteString("\n   " + c.body + "\n\n")
	}
	b.WriteString("Please address these in the worktree.")
	return b.String()
}

// HandleDiffFilesLoaded applies an async file-list load result.
func (m *DetailModel) HandleDiffFilesLoaded(msg diffFilesLoadedMsg) tea.Cmd {
	if m.diff == nil || !m.diff.active || m.task == nil || msg.taskID != m.task.ID {
		return nil
	}
	d := m.diff
	d.loading = false
	if msg.err != nil {
		d.loadErr = msg.err.Error()
		m.setViewportContent()
		return nil
	}
	d.base = msg.base
	d.baseLabel = msg.baseLabel
	d.files = msg.files
	d.selected = 0
	m.setViewportContent()
	if len(d.files) > 0 {
		return m.loadSelectedFileContent()
	}
	return nil
}

// HandleDiffContentLoaded applies an async per-file content load result and
// renders it (chroma / glamour) on the main thread.
func (m *DetailModel) HandleDiffContentLoaded(msg diffContentLoadedMsg) {
	if m.diff == nil || !m.diff.active || m.task == nil || msg.taskID != m.task.ID {
		return
	}
	d := m.diff
	// Ignore stale results (user already moved on / toggled mode).
	if msg.path != d.selectedPath() || msg.showRendered != d.showRendered {
		return
	}
	d.contentLoading = false
	d.contentPath = msg.path
	d.contentMode = msg.showRendered
	d.rawLines = nil
	d.cursor = 0
	switch {
	case msg.err != nil:
		d.rendered = lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75")).
			Render("Failed to load: " + msg.err.Error())
	case msg.empty:
		d.rendered = Dim.Render("(no textual changes)")
	case msg.kind == diffKindFile && msg.isMD:
		d.rendered = m.renderViewerMarkdown(msg.text)
	case msg.kind == diffKindFile:
		d.rendered = highlightSource(msg.text, msg.path)
	default: // diff — keep the raw lines so the line cursor can anchor comments
		d.rendered = highlightSource(msg.text, "diff.diff")
		d.rawLines = strings.Split(strings.TrimRight(msg.text, "\n"), "\n")
	}
	m.viewport.GotoTop()
	m.setViewportContent()
}

// cursorActive reports whether the line cursor (and line-anchored comments) apply
// to the current content — i.e. we're viewing a unified diff with raw lines.
func (d *diffViewer) cursorActive() bool {
	return !d.showRendered && len(d.rawLines) > 0
}

// loadSelectedFileContent kicks off async loading of the selected file's content
// (diff or rendered file) and shows a loading placeholder immediately.
func (m *DetailModel) loadSelectedFileContent() tea.Cmd {
	d := m.diff
	if d == nil || len(d.files) == 0 {
		return nil
	}
	if d.selected < 0 || d.selected >= len(d.files) {
		d.selected = 0
	}
	entry := d.files[d.selected]
	d.contentLoading = true
	d.rendered = Dim.Render("Loading " + entry.path + "…")
	m.viewport.GotoTop()
	m.setViewportContent()

	taskID := m.task.ID
	worktree := d.worktree
	base := d.base
	showRendered := d.showRendered
	return func() tea.Msg {
		text, kind, empty, err := loadFileContent(worktree, base, entry, showRendered)
		return diffContentLoadedMsg{
			taskID:       taskID,
			path:         entry.path,
			showRendered: showRendered,
			text:         text,
			kind:         kind,
			isMD:         isMarkdown(entry.path),
			empty:        empty,
			err:          err,
		}
	}
}

func (d *diffViewer) selectedPath() string {
	if d == nil || d.selected < 0 || d.selected >= len(d.files) {
		return ""
	}
	return d.files[d.selected].path
}

// --- layout helpers -------------------------------------------------------

// diffTreeWidth returns the width of the file-tree column.
func (m *DetailModel) diffTreeWidth() int {
	inner := m.width - 4
	tw := 30
	if max := inner / 3; tw > max {
		tw = max
	}
	if tw < 16 {
		tw = 16
	}
	if tw > inner-10 {
		tw = inner - 10
	}
	if tw < 0 {
		tw = 0
	}
	return tw
}

// contentViewportWidth returns the viewport width for the content pane, which
// shrinks to leave room for the file tree while the viewer is open.
func (m *DetailModel) contentViewportWidth() int {
	base := m.width - 4
	if base < 1 {
		base = 1
	}
	if m.diff != nil && m.diff.active {
		w := base - m.diffTreeWidth() - 1 // 1 col gutter between tree and content
		if w < 10 {
			w = 10
		}
		return w
	}
	return base
}

// renderDiffContent renders the content-pane text (right column) that goes into
// the viewport while the viewer is open.
func (m *DetailModel) renderDiffContent() string {
	d := m.diff
	var b strings.Builder

	mode := "diff"
	if d.showRendered {
		mode = "file"
	}
	title := "Changes"
	if d.baseLabel != "" {
		title = "Diff vs " + d.baseLabel
	}
	// Keep the header exactly diffHeaderLines tall so the line-cursor scroll math
	// stays correct: a single title line + one blank line.
	b.WriteString(Bold.Render(title))
	if len(d.files) > 0 && d.selected < len(d.files) {
		b.WriteString(Dim.Render(fmt.Sprintf("  —  %s  [%s]", d.files[d.selected].path, mode)))
	}
	if n := len(d.comments); n > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Render(fmt.Sprintf("  · %d comment%s", n, plural(n))))
	}
	b.WriteString("\n\n")

	switch {
	case d.worktree == "":
		b.WriteString(Dim.Render("This task has no worktree yet — nothing to diff."))
	case d.loading:
		b.WriteString(Dim.Render("Loading changed files…"))
	case d.loadErr != "":
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75")).Render(d.loadErr))
	case len(d.files) == 0:
		label := d.baseLabel
		if label == "" {
			label = "base"
		}
		b.WriteString(Dim.Render("No changes vs " + label + "."))
	case d.cursorActive():
		b.WriteString(markCursorLine(d.rendered, d.cursor))
	default:
		b.WriteString(d.rendered)
	}
	return b.String()
}

// markCursorLine prefixes each line of rendered content with a gutter, drawing a
// bar on the cursor line so the user can see which line a comment will anchor to.
func markCursorLine(rendered string, cursor int) string {
	lines := strings.Split(rendered, "\n")
	bar := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("▌")
	for i := range lines {
		if i == cursor {
			lines[i] = bar + " " + lines[i]
		} else {
			lines[i] = "  " + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// renderDiffTree renders the file-tree column (left). It is rendered directly by
// View(); its state is folded into viewSignature so the cache stays correct.
func (m *DetailModel) renderDiffTree(height int) string {
	d := m.diff
	tw := m.diffTreeWidth()
	if tw <= 0 || height <= 0 {
		return ""
	}

	var lines []string
	header := fmt.Sprintf("Changed files (%d)", len(d.files))
	lines = append(lines, Bold.Render(truncate(header, tw)))
	lines = append(lines, "")

	if d.loading {
		lines = append(lines, Dim.Render("Loading…"))
	} else if len(d.files) == 0 {
		lines = append(lines, Dim.Render("— none —"))
	} else {
		// Window the list so the selected entry stays visible.
		visible := height - 2
		if visible < 1 {
			visible = 1
		}
		start := 0
		if d.selected >= visible {
			start = d.selected - visible + 1
		}
		end := start + visible
		if end > len(d.files) {
			end = len(d.files)
		}
		for i := start; i < end; i++ {
			lines = append(lines, m.renderTreeRow(d.files[i], i == d.selected, tw))
		}
	}

	col := lipgloss.NewStyle().Width(tw).Height(height)
	body := strings.Join(lines, "\n")
	return col.Render(body)
}

func (m *DetailModel) renderTreeRow(entry diffFileEntry, selected bool, width int) string {
	badge := statusStyle(entry.status).Render(statusGlyph(entry.status))
	name := entry.path
	// Show a compact path: keep the trailing segments that fit.
	avail := width - 2 // badge + space
	if avail < 4 {
		avail = 4
	}
	name = truncate(name, avail)
	row := badge + " " + name
	if selected {
		sel := lipgloss.NewStyle().
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#1A1B26")).
			Bold(true).
			Width(width)
		return sel.Render(truncate(statusGlyph(entry.status)+" "+entry.path, width))
	}
	return row
}

// --- git helpers (run on goroutines) --------------------------------------

// loadChangedFiles resolves the base ref and lists the files changed on the task
// branch vs that base (committed, staged, unstaged, and untracked).
func loadChangedFiles(worktree, sourceBranch string) (base, label string, files []diffFileEntry, err error) {
	base, label = resolveDiffBase(worktree, sourceBranch)

	seen := map[string]bool{}
	// Tracked changes vs the base.
	out, derr := gitOutput(worktree, "diff", "--name-status", "--no-renames", base)
	if derr != nil {
		// Base may be unusable; fall back to working-tree-vs-HEAD.
		base, label = "HEAD", "HEAD"
		out, derr = gitOutput(worktree, "diff", "--name-status", "--no-renames", base)
		if derr != nil {
			return base, label, nil, derr
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		status := string(fields[0][0])
		path := fields[len(fields)-1]
		if seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, diffFileEntry{path: path, status: status})
	}

	// Untracked files (newly created, not yet added).
	if uout, uerr := gitOutput(worktree, "ls-files", "--others", "--exclude-standard"); uerr == nil {
		for _, line := range strings.Split(uout, "\n") {
			path := strings.TrimSpace(line)
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, diffFileEntry{path: path, status: "?"})
		}
	}

	sortDiffFiles(files)
	return base, label, files, nil
}

// loadFileContent loads either the unified diff or the rendered file content for
// a single changed file.
func loadFileContent(worktree, base string, entry diffFileEntry, showRendered bool) (text string, kind diffContentKind, empty bool, err error) {
	if showRendered {
		// Rendered file: read the current working-tree file.
		if entry.status == "D" {
			return "", diffKindFile, true, nil
		}
		data, rerr := os.ReadFile(filepath.Join(worktree, entry.path))
		if rerr != nil {
			return "", diffKindFile, false, rerr
		}
		return clampText(string(data)), diffKindFile, len(data) == 0, nil
	}

	// Unified diff.
	var out string
	if entry.status == "?" {
		// Untracked: diff against /dev/null so the whole file shows as added.
		// git exits 1 when there's a diff, which is expected here.
		out, _ = gitOutput(worktree, "diff", "--no-index", "--", os.DevNull, filepath.Join(worktree, entry.path))
	} else {
		out, err = gitOutput(worktree, "diff", base, "--", entry.path)
		if err != nil {
			return "", diffKindDiff, false, err
		}
	}
	out = clampText(out)
	return out, diffKindDiff, strings.TrimSpace(out) == "", nil
}

// resolveDiffBase finds a good base ref to diff the task branch against and
// returns its merge-base sha plus a human label.
func resolveDiffBase(worktree, sourceBranch string) (base, label string) {
	candidates := []string{}
	if sourceBranch != "" {
		candidates = append(candidates, sourceBranch)
	}
	if def := defaultBranchName(worktree); def != "" {
		candidates = append(candidates, def)
	}
	candidates = append(candidates, "main", "master")

	tried := map[string]bool{}
	for _, c := range candidates {
		if c == "" || tried[c] {
			continue
		}
		tried[c] = true
		if mb, err := gitOutput(worktree, "merge-base", c, "HEAD"); err == nil {
			mb = strings.TrimSpace(mb)
			if mb != "" {
				return mb, c
			}
		}
	}
	// Fallback: compare against HEAD (uncommitted changes only).
	return "HEAD", "HEAD"
}

// defaultBranchName mirrors executor.getDefaultBranch but scoped to the worktree.
func defaultBranchName(worktree string) string {
	if out, err := gitOutput(worktree, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(out)
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	for _, b := range []string{"main", "master"} {
		if err := osExec.Command("git", "-C", worktree, "rev-parse", "--verify", b).Run(); err == nil {
			return b
		}
	}
	return ""
}

func gitOutput(worktree string, args ...string) (string, error) {
	full := append([]string{"-C", worktree}, args...)
	out, err := osExec.Command("git", full...).Output()
	return string(out), err
}

// --- rendering helpers ----------------------------------------------------

// renderViewerMarkdown renders markdown with glamour at the content-pane width.
func (m *DetailModel) renderViewerMarkdown(src string) string {
	width := m.contentViewportWidth()
	if width < 10 {
		width = 10
	}
	style := "dark"
	if !m.focused {
		style = "notty"
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return src
	}
	out, err := renderer.Render(src)
	if err != nil {
		return src
	}
	return strings.TrimSpace(out)
}

// highlightSource highlights source/diff text with chroma for terminal output.
func highlightSource(source, filename string) string {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	it, err := lexer.Tokenise(nil, source)
	if err != nil {
		return source
	}
	var sb strings.Builder
	if err := formatter.Format(&sb, style, it); err != nil {
		return source
	}
	return sb.String()
}

func isMarkdown(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown" || ext == ".mdown"
}

func clampText(s string) string {
	if len(s) <= maxDiffBytes {
		return s
	}
	return s[:maxDiffBytes] + "\n\n… (truncated — file too large to display in full)"
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	// Trim from the left so the most specific path segment stays visible.
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width("…"+string(runes)) > width {
		runes = runes[1:]
	}
	return "…" + string(runes)
}

func statusGlyph(status string) string {
	switch status {
	case "A":
		return "A"
	case "M":
		return "M"
	case "D":
		return "D"
	case "R":
		return "R"
	case "C":
		return "C"
	case "?":
		return "+"
	default:
		return status
	}
}

func statusStyle(status string) lipgloss.Style {
	switch status {
	case "A", "?":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379")) // green
	case "M":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#E5C07B")) // yellow
	case "D":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75")) // red
	default:
		return lipgloss.NewStyle().Foreground(ColorMuted)
	}
}

// sortDiffFiles sorts changed files by path for stable display.
func sortDiffFiles(files []diffFileEntry) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j-1].path > files[j].path; j-- {
			files[j-1], files[j] = files[j], files[j-1]
		}
	}
}
