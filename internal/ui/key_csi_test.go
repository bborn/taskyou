package ui

import (
	"bytes"
	"io"
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// captureCSI runs bubbletea against raw input bytes so we can grab the
// unknownCSISequenceMsg it produces (the type is unexported and can only be
// obtained from the program's message channel).
func captureCSI(t *testing.T, input []byte) tea.Msg {
	t.Helper()

	type captureModel struct{ captured tea.Msg }
	model := &captureCSIModel{}
	prog := tea.NewProgram(
		model,
		tea.WithInput(io.NopCloser(bytes.NewReader(input))),
		tea.WithOutput(io.Discard),
	)
	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		prog.Quit()
		close(done)
	}()
	if _, err := prog.Run(); err != nil {
		t.Fatalf("program failed: %v", err)
	}
	<-done
	return model.captured
}

type captureCSIModel struct{ captured tea.Msg }

func (m *captureCSIModel) Init() tea.Cmd { return nil }
func (m *captureCSIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.QuitMsg); ok {
		return m, tea.Quit
	}
	// Capture only the first non-internal message.
	if m.captured == nil {
		typ := reflect.TypeOf(msg)
		if typ != nil && typ.String() != "tea.QuitMsg" {
			m.captured = msg
		}
	}
	return m, nil
}
func (m *captureCSIModel) View() string { return "" }

func TestTranslateModernKey_ShiftEnter(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
	}{
		{"kitty CSI-u 13;2u", []byte("\x1b[13;2u")},
		{"modifyOtherKeys 27;2;13~", []byte("\x1b[27;2;13~")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := captureCSI(t, tc.input)
			if msg == nil {
				t.Fatal("no CSI message captured")
			}
			out := translateModernKey(msg)
			km, ok := out.(tea.KeyMsg)
			if !ok {
				t.Fatalf("expected KeyMsg, got %T", out)
			}
			if km.Type != tea.KeyEnter {
				t.Errorf("expected KeyEnter, got %v", km.Type)
			}
		})
	}
}

func TestTranslateModernKey_OptionDelete(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
	}{
		{"kitty CSI-u 127;3u", []byte("\x1b[127;3u")},
		{"modifyOtherKeys 27;3;127~", []byte("\x1b[27;3;127~")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := captureCSI(t, tc.input)
			out := translateModernKey(msg)
			km, ok := out.(tea.KeyMsg)
			if !ok {
				t.Fatalf("expected KeyMsg, got %T", out)
			}
			if km.Type != tea.KeyBackspace || !km.Alt {
				t.Errorf("expected alt+backspace, got type=%v alt=%v", km.Type, km.Alt)
			}
			if km.String() != "alt+backspace" {
				t.Errorf("expected stringification alt+backspace, got %q", km.String())
			}
		})
	}
}

func TestTranslateModernKey_CmdDelete(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
	}{
		{"kitty CSI-u 127;9u", []byte("\x1b[127;9u")},
		{"modifyOtherKeys 27;9;127~", []byte("\x1b[27;9;127~")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := captureCSI(t, tc.input)
			out := translateModernKey(msg)
			km, ok := out.(tea.KeyMsg)
			if !ok {
				t.Fatalf("expected KeyMsg, got %T", out)
			}
			if km.Type != tea.KeyCtrlU {
				t.Errorf("expected ctrl+u, got %v", km.Type)
			}
		})
	}
}

func TestTranslateModernKey_UnknownSequencePassThrough(t *testing.T) {
	msg := captureCSI(t, []byte("\x1b[99;99u"))
	out := translateModernKey(msg)
	// Unknown CSI sequences must come back unchanged so callers can keep
	// processing them (or ignore them) as before.
	if !reflect.DeepEqual(out, msg) {
		t.Errorf("unknown CSI was modified: got %#v want %#v", out, msg)
	}
}

func TestTranslateModernKey_NonCSIPassThrough(t *testing.T) {
	original := tea.KeyMsg{Type: tea.KeyEnter}
	if got := translateModernKey(original); !reflect.DeepEqual(got, original) {
		t.Errorf("regular KeyMsg modified: got %#v want %#v", got, original)
	}

	if got := translateModernKey(nil); got != nil {
		t.Errorf("nil msg modified: got %#v", got)
	}
}
