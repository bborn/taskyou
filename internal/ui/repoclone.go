package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bborn/workflow/internal/github"
)

// repoCloneState is where the clone view is in its (short) life.
type repoCloneState int

const (
	repoCloneConfirm repoCloneState = iota // showing the destination, waiting for enter
	repoCloneRunning                       // git clone in flight
	repoCloneFailed                        // git said no; the user can correct and retry
)

// repoCloneDoneMsg reports the outcome of a clone attempt.
type repoCloneDoneMsg struct {
	path string
	err  error
}

// repoClonedMsg says the repo is on disk at path. The app hands it to
// handleFolderPicked, so from here on it's an ordinary local-folder project.
type repoClonedMsg struct{ path string }

// repoCloneTickMsg drives the spinner while the clone runs.
type repoCloneTickMsg struct{}

// repoCloneReuseNotice is what we say when the destination already holds the
// repo: nothing is downloaded, we just adopt it.
const repoCloneReuseNotice = "You already have this repo here — enter uses it as-is, nothing is downloaded."

// RepoCloneModel confirms where a pasted repo URL should land, clones it, and
// reports the local path. It never creates a project itself — that stays with
// the folder-picked path.
type RepoCloneModel struct {
	ref    github.RepoRef
	cloner github.Cloner
	dest   textinput.Model // ~-collapsed destination, editable

	state   repoCloneState
	reuse   bool   // the destination is already a checkout of this repo
	notice  string // how the destination was chosen, when it's worth saying
	errText string // git's own words, or ours

	home string
	// reuseChecked memoizes "is this path already a checkout of ref?" — the
	// answer costs a git subprocess and the destination is edited a keystroke
	// at a time.
	reuseChecked map[string]bool
	frame        int
	// cancel stops an in-flight clone (esc). Nil unless one is running.
	cancel context.CancelFunc

	width  int
	height int
}

// NewRepoCloneModel prepares a clone of ref into the default clone root.
func NewRepoCloneModel(ref github.RepoRef, width, height int) *RepoCloneModel {
	return newRepoCloneModel(ref, github.Cloner{}, width, height)
}

// newRepoCloneModel is the seam the tests use to substitute a fake cloner.
func newRepoCloneModel(ref github.RepoRef, cloner github.Cloner, width, height int) *RepoCloneModel {
	home, _ := os.UserHomeDir()
	ti := textinput.New()
	ti.Prompt = Icon("❯ ", "> ")
	ti.PromptStyle = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	ti.Focus()

	m := &RepoCloneModel{ref: ref, cloner: cloner, dest: ti, home: home, width: width, height: height, reuseChecked: map[string]bool{}}
	m.resolveDestination()
	m.layout()
	return m
}

// resolveDestination picks where the clone goes and explains the choice when
// it wasn't the obvious one.
func (m *RepoCloneModel) resolveDestination() {
	dest, err := m.cloner.Resolve(m.ref)
	if err != nil {
		m.state = repoCloneFailed
		m.errText = err.Error()
		m.dest.SetValue(collapseHomePath(filepath.Join(github.DefaultCloneRoot(), m.ref.Name), m.home))
		return
	}
	m.reuse = dest.Reuse
	m.dest.SetValue(collapseHomePath(dest.Path, m.home))
	m.dest.CursorEnd()
	switch {
	case dest.Reuse:
		m.notice = repoCloneReuseNotice
	case dest.Renamed:
		taken := collapseHomePath(filepath.Join(filepath.Dir(dest.Path), m.ref.Name), m.home)
		m.notice = taken + " is something else, so this goes to " + collapseHomePath(dest.Path, m.home) + ". Edit the path to change it."
	}
}

func (m *RepoCloneModel) Init() tea.Cmd { return textinput.Blink }

func (m *RepoCloneModel) Update(msg tea.Msg) (*RepoCloneModel, tea.Cmd) {
	switch msg := msg.(type) {
	case repoCloneTickMsg:
		if m.state != repoCloneRunning {
			return m, nil
		}
		m.frame++
		return m, m.tick()

	case repoCloneDoneMsg:
		if m.cancel != nil {
			m.cancel() // release the context now that the clone is over
			m.cancel = nil
		}
		if msg.err != nil {
			m.state = repoCloneFailed
			m.errText = cloneFailureText(msg.err)
			return m, nil
		}
		path := msg.path
		m.state = repoCloneConfirm
		return m, func() tea.Msg { return repoClonedMsg{path: path} }

	case tea.KeyMsg:
		if m.state == repoCloneRunning {
			return m, nil // the spinner owns the screen; esc is handled by the app
		}
		if msg.String() == "enter" {
			return m, m.start()
		}
	}

	if m.state == repoCloneRunning {
		return m, nil
	}
	var cmd tea.Cmd
	before := m.dest.Value()
	m.dest, cmd = m.dest.Update(msg)
	if m.dest.Value() != before {
		// The destination moved, so anything we said about the old one —
		// "you already have this" or a stale git error — no longer holds.
		m.reuse = m.isCheckoutOf(expandPath(m.dest.Value()))
		m.notice = ""
		if m.reuse {
			m.notice = repoCloneReuseNotice
		}
		if m.state == repoCloneFailed {
			m.state = repoCloneConfirm
			m.errText = ""
		}
	}
	return m, cmd
}

// start kicks off the clone (or adopts an existing checkout unchanged).
func (m *RepoCloneModel) start() tea.Cmd {
	path := strings.TrimSpace(expandPath(m.dest.Value()))
	if path == "" {
		m.state = repoCloneFailed
		m.errText = "Enter a folder to clone into, e.g. " + collapseHomePath(filepath.Join(github.DefaultCloneRoot(), m.ref.Name), m.home)
		return nil
	}
	if m.isCheckoutOf(path) {
		return func() tea.Msg { return repoClonedMsg{path: path} }
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.state = repoCloneRunning
	m.errText = ""
	m.frame = 0

	ref, cloner := m.ref, m.cloner
	clone := func() tea.Msg {
		err := cloner.Clone(ctx, ref, path)
		return repoCloneDoneMsg{path: path, err: err}
	}
	return tea.Batch(clone, m.tick())
}

// isCheckoutOf is cloner.IsCheckoutOf, memoized per path.
func (m *RepoCloneModel) isCheckoutOf(path string) bool {
	if got, ok := m.reuseChecked[path]; ok {
		return got
	}
	got := m.cloner.IsCheckoutOf(path, m.ref)
	m.reuseChecked[path] = got
	return got
}

// Cancel stops an in-flight clone. It reports whether there was one — the app
// uses that to decide whether esc means "stop cloning" or "go back".
func (m *RepoCloneModel) Cancel() bool {
	if m.state != repoCloneRunning || m.cancel == nil {
		return false
	}
	m.cancel()
	m.cancel = nil
	m.state = repoCloneFailed
	m.errText = "Clone canceled — nothing was left on disk. Press enter to try again."
	return true
}

func (m *RepoCloneModel) tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return repoCloneTickMsg{} })
}

// cloneFailureText renders a clone error the way the Welcome view talks: what
// happened, in git's own words, plus the next step.
func cloneFailureText(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "Clone canceled — nothing was left on disk. Press enter to try again."
	}
	return err.Error()
}

func (m *RepoCloneModel) View() string {
	w := m.contentWidth()

	parts := []string{
		Title.Render("Clone from GitHub"),
		Dim.Render(truncateRunes(m.ref.String(), w)),
		"",
	}

	switch m.state {
	case repoCloneRunning:
		frame := spinnerFrames[m.frame%len(spinnerFrames)]
		parts = append(parts,
			lipgloss.NewStyle().Foreground(ColorPrimary).Render(frame)+" "+
				truncateRunes("Cloning "+m.ref.Slug()+" into "+m.dest.Value(), w-2),
			"",
			Dim.Render("A big repo can take a minute."),
			"",
			HelpBar.Render(HelpKey.Render("esc")+" "+HelpDesc.Render("cancel")),
		)
	default:
		parts = append(parts,
			Bold.Render("Clone into"),
			m.dest.View(),
			"",
		)
		if m.errText != "" {
			// Wrapped, not truncated: git's reason and the fix that follows it
			// are both long, and the tail is the part that tells you what to do.
			for i, line := range strings.Split(m.errText, "\n") {
				// Only the first line is flagged; the rest wrap flush, since an
				// indent under the icon falls apart the moment a line wraps.
				if i == 0 {
					line = Icon(IconWarningUnicode, IconWarningASCII) + " " + line
				}
				parts = append(parts, Error.Width(w).Render(line))
			}
			parts = append(parts, "", Dim.Width(w).Render("Fix the path above and press enter to try again."))
		} else if m.notice != "" {
			style := Dim
			if m.reuse {
				style = Success
			}
			parts = append(parts, wrapNotice(style, m.notice, w))
		}
		enterDesc := "clone"
		if m.reuse {
			enterDesc = "use it"
		}
		parts = append(parts, "",
			HelpBar.Render(
				HelpKey.Render("enter")+" "+HelpDesc.Render(enterDesc)+"  "+
					HelpKey.Render("esc")+" "+HelpDesc.Render("back")))
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(w + 4).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// wrapNotice hard-wraps a sentence to the panel width so long destination
// paths don't blow the box open.
func wrapNotice(style lipgloss.Style, text string, width int) string {
	return style.Width(width).Render(text)
}

func (m *RepoCloneModel) contentWidth() int {
	w := m.width - 10
	if w > 72 {
		w = 72
	}
	if w < 24 {
		w = 24
	}
	return w
}

func (m *RepoCloneModel) layout() {
	m.dest.Width = m.contentWidth() - lipgloss.Width(m.dest.Prompt) - 1
}

func (m *RepoCloneModel) SetSize(w, h int) {
	m.width, m.height = w, h
	m.layout()
}
