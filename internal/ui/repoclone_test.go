package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bborn/workflow/internal/github"
)

// fakeCloner builds a Cloner rooted in a temp dir whose clone step is a
// function under the test's control — nothing touches the network.
func fakeCloner(t *testing.T, run func(ctx context.Context, url, dest string) error) (github.Cloner, string) {
	t.Helper()
	root := t.TempDir()
	return github.Cloner{
		Root: root,
		RemoteURL: func(dir string) (string, error) {
			data, err := os.ReadFile(filepath.Join(dir, ".git", "origin"))
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(data)), nil
		},
		Run: run,
	}, root
}

// writeCheckout makes dir look like a checkout of remote to fakeCloner.
func writeCheckout(t *testing.T, dir, remote string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "origin"), []byte(remote), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRef(t *testing.T, s string) github.RepoRef {
	t.Helper()
	ref, err := github.ParseRepoRef(s)
	if err != nil {
		t.Fatalf("ParseRepoRef(%q): %v", s, err)
	}
	return ref
}

// drain executes the command chain a model returns, feeding every resulting
// message back into the model until a repoClonedMsg falls out (or the chain
// runs dry). Spinner ticks are noise here.
func drain(m *RepoCloneModel, cmd tea.Cmd) tea.Msg {
	pending := []tea.Cmd{cmd}
	for i := 0; len(pending) > 0 && i < 50; i++ {
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		msg := next()
		switch typed := msg.(type) {
		case tea.BatchMsg:
			pending = append(pending, typed...)
			continue
		case repoClonedMsg:
			return typed
		case repoCloneTickMsg, nil:
			continue
		}
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		pending = append(pending, cmd)
	}
	return nil
}

func TestRepoClone_ShowsDestinationBeforeCloning(t *testing.T) {
	cloner, root := fakeCloner(t, func(context.Context, string, string) error {
		t.Fatal("clone should not run before the user confirms")
		return nil
	})
	m := newRepoCloneModel(mustRef(t, "https://github.com/bborn/taskyou"), cloner, 100, 40)

	if want := filepath.Join(root, "taskyou"); expandPath(m.dest.Value()) != want {
		t.Errorf("destination = %q, want %q", m.dest.Value(), want)
	}
	view := m.View()
	if !strings.Contains(view, "bborn/taskyou") || !strings.Contains(view, "taskyou") {
		t.Errorf("view should name the repo and the destination, got:\n%s", view)
	}
}

func TestRepoClone_EnterClonesAndHandsBackThePath(t *testing.T) {
	var cloned string
	cloner, root := fakeCloner(t, func(_ context.Context, url, dest string) error {
		if url != "https://github.com/bborn/taskyou.git" {
			t.Errorf("clone url = %q", url)
		}
		cloned = dest
		writeCheckout(t, dest, url)
		return nil
	})
	m := newRepoCloneModel(mustRef(t, "bborn/taskyou"), cloner, 100, 40)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := drain(m, cmd)

	done, ok := msg.(repoClonedMsg)
	if !ok {
		t.Fatalf("want repoClonedMsg, got %T", msg)
	}
	want := filepath.Join(root, "taskyou")
	if done.path != want || cloned != want {
		t.Errorf("cloned to %q / reported %q, want %q", cloned, done.path, want)
	}
}

func TestRepoClone_ExistingCloneIsAdoptedWithoutCloning(t *testing.T) {
	cloner, root := fakeCloner(t, func(context.Context, string, string) error {
		t.Fatal("an existing clone of the same repo must not be re-cloned")
		return nil
	})
	existing := filepath.Join(root, "taskyou")
	writeCheckout(t, existing, "git@github.com:bborn/taskyou.git")

	m := newRepoCloneModel(mustRef(t, "https://github.com/bborn/taskyou"), cloner, 100, 40)
	if !m.reuse {
		t.Fatal("an existing checkout of the same repo should be offered for reuse")
	}
	if !strings.Contains(m.View(), "already have this repo") {
		t.Errorf("view should say the repo is already there, got:\n%s", m.View())
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	done, ok := drain(m, cmd).(repoClonedMsg)
	if !ok {
		t.Fatal("enter should hand back the existing path immediately")
	}
	if done.path != existing {
		t.Errorf("path = %q, want %q", done.path, existing)
	}
}

func TestRepoClone_UnrelatedDirectoryGetsANonCollidingDestination(t *testing.T) {
	cloner, root := fakeCloner(t, func(context.Context, string, string) error { return nil })
	if err := os.MkdirAll(filepath.Join(root, "taskyou", "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := newRepoCloneModel(mustRef(t, "bborn/taskyou"), cloner, 100, 40)

	if want := filepath.Join(root, "taskyou-2"); expandPath(m.dest.Value()) != want {
		t.Errorf("destination = %q, want %q", m.dest.Value(), want)
	}
	if !strings.Contains(m.View(), "taskyou-2") {
		t.Errorf("view should show where the clone is actually going, got:\n%s", m.View())
	}
}

func TestRepoClone_FailureSurfacesGitStderrAndStaysPut(t *testing.T) {
	cloner, _ := fakeCloner(t, func(context.Context, string, string) error {
		return &github.CloneError{
			Stderr: "Cloning into '/tmp/x'...\nremote: Repository not found.\nfatal: repository not found",
			Err:    errors.New("exit status 128"),
		}
	})
	m := newRepoCloneModel(mustRef(t, "bborn/nope"), cloner, 100, 40)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if msg := drain(m, cmd); msg != nil {
		t.Fatalf("a failed clone should not report a path, got %T", msg)
	}
	if m.state != repoCloneFailed {
		t.Fatalf("state = %v, want failed", m.state)
	}
	view := m.View()
	if !strings.Contains(view, "Repository not found") {
		t.Errorf("view should show git's own words, got:\n%s", view)
	}
	if !strings.Contains(view, "try again") {
		t.Errorf("view should say what to do next, got:\n%s", view)
	}
}

func TestRepoClone_EditingTheDestinationClearsAStaleError(t *testing.T) {
	cloner, _ := fakeCloner(t, func(context.Context, string, string) error {
		return errors.New("boom")
	})
	m := newRepoCloneModel(mustRef(t, "bborn/taskyou"), cloner, 100, 40)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = drain(m, cmd)
	if m.state != repoCloneFailed {
		t.Fatalf("state = %v, want failed", m.state)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if m.state != repoCloneConfirm || m.errText != "" {
		t.Errorf("editing the destination should clear the error, got state %v err %q", m.state, m.errText)
	}
}

func TestRepoClone_SpinnerRunsWhileCloning(t *testing.T) {
	cloner, _ := fakeCloner(t, func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	m := newRepoCloneModel(mustRef(t, "bborn/taskyou"), cloner, 100, 40)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.state != repoCloneRunning {
		t.Fatalf("state = %v, want running", m.state)
	}
	first := m.View()
	if !strings.Contains(first, "Cloning bborn/taskyou") {
		t.Errorf("running view should name the repo, got:\n%s", first)
	}
	m, _ = m.Update(repoCloneTickMsg{})
	if m.View() == first {
		t.Error("the spinner should advance on a tick — a still frame reads as a hung TUI")
	}

	// esc cancels the in-flight clone rather than leaving it running.
	if !m.Cancel() {
		t.Fatal("Cancel should report that a clone was in flight")
	}
	if m.state != repoCloneFailed || !strings.Contains(m.errText, "canceled") {
		t.Errorf("after cancel: state %v err %q", m.state, m.errText)
	}
}
