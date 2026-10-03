package clipboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fake struct {
	env     map[string]string
	have    map[string]bool
	fail    map[string]bool
	clients int
	ran     []string
	stdin   []string
	tmux    []string
}

func (f *fake) copier(goos string) *Copier {
	return &Copier{
		GOOS:   goos,
		Getenv: func(k string) string { return f.env[k] },
		LookPath: func(name string) (string, error) {
			if f.have[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, stdin, name string, args ...string) error {
			f.ran = append(f.ran, strings.Join(append([]string{name}, args...), " "))
			f.stdin = append(f.stdin, stdin)
			if f.fail[name] {
				return errors.New("exit status 1")
			}
			return nil
		},
		TmuxClients: func(context.Context) int { return f.clients },
		TmuxLoad: func(_ context.Context, text string) error {
			f.tmux = append(f.tmux, text)
			return nil
		},
	}
}

// The text arrives exactly as given: a copy exists so that the user does not
// get a wrapped, trimmed or re-flowed version of it.
func TestCopyDeliversTheExactTextOnMac(t *testing.T) {
	f := &fake{have: map[string]bool{"pbcopy": true}}
	text := "ssh agents@host 'cat ~/file' | \\\n  pbcopy  \n"
	routes, err := f.copier("darwin").Copy(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.stdin) != 1 || f.stdin[0] != text {
		t.Fatalf("pbcopy got %q, want %q", f.stdin, text)
	}
	if len(routes) != 1 || !strings.Contains(routes[0], "pbcopy") {
		t.Errorf("routes = %v", routes)
	}
}

func TestCopyPicksTheLinuxClipboardForTheSession(t *testing.T) {
	f := &fake{
		env:  map[string]string{"WAYLAND_DISPLAY": "wayland-1", "DISPLAY": ":0"},
		have: map[string]bool{"wl-copy": true, "xclip": true},
	}
	if _, err := f.copier("linux").Copy(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.ran, ",") != "wl-copy" {
		t.Errorf("ran %v, want only wl-copy", f.ran)
	}

	// No Wayland: X11, with xclip reading the CLIPBOARD selection, not PRIMARY.
	f = &fake{env: map[string]string{"DISPLAY": ":0"}, have: map[string]bool{"xclip": true, "xsel": true}}
	if _, err := f.copier("linux").Copy(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.ran, ",") != "xclip -selection clipboard" {
		t.Errorf("ran %v", f.ran)
	}
}

func TestCopyFallsBackWhenAClipboardCommandFails(t *testing.T) {
	f := &fake{
		env:  map[string]string{"DISPLAY": ":0"},
		have: map[string]bool{"xclip": true, "xsel": true},
		fail: map[string]bool{"xclip": true},
	}
	routes, err := f.copier("linux").Copy(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || !strings.Contains(routes[0], "xsel") {
		t.Errorf("routes = %v, want xsel", routes)
	}
}

// A headless machine reached over ssh has no clipboard of its own worth
// writing; the open ty view carries it to the user's terminal instead.
func TestCopyReachesTheTerminalThroughAnOpenView(t *testing.T) {
	f := &fake{clients: 1}
	routes, err := f.copier("linux").Copy(context.Background(), "token-123")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.tmux) != 1 || f.tmux[0] != "token-123" {
		t.Fatalf("tmux got %q", f.tmux)
	}
	if len(routes) != 1 || !strings.Contains(routes[0], "OSC 52") {
		t.Errorf("routes = %v", routes)
	}
}

func TestCopyWithNoRouteSaysSo(t *testing.T) {
	f := &fake{}
	if _, err := f.copier("linux").Copy(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "no clipboard command") {
		t.Fatalf("err = %v", err)
	}
	if len(f.tmux) != 0 {
		t.Error("loaded a tmux buffer with no client attached to relay it")
	}
}

func TestCopyRejectsEmptyAndOversizedText(t *testing.T) {
	f := &fake{have: map[string]bool{"pbcopy": true}}
	if _, err := f.copier("darwin").Copy(context.Background(), ""); err == nil {
		t.Error("empty text was accepted")
	}
	if _, err := f.copier("darwin").Copy(context.Background(), strings.Repeat("a", MaxBytes+1)); err == nil {
		t.Error("oversized text was accepted")
	}
	if len(f.ran) != 0 {
		t.Errorf("ran %v for rejected text", f.ran)
	}
}
