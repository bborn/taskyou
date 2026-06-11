package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// These tests verify that the form treats modern CSI-u and modifyOtherKeys
// sequences the same way it treats their legacy equivalents. They cover the
// keys reported broken in the bug: Option+Delete, Cmd+Delete, and
// Shift+Return inside the body/details field.

func newFormForKeyTest(t *testing.T, focused FormField, value string) *FormModel {
	t.Helper()
	m := NewFormModel(nil, 100, 50, "", nil)
	m.focused = focused
	switch focused {
	case FieldTitle:
		m.blurAll()
		m.titleInput.Focus()
		m.titleInput.SetValue(value)
		m.titleInput.CursorEnd()
	case FieldBody:
		m.blurAll()
		m.bodyInput.Focus()
		m.bodyInput.SetValue(value)
		m.bodyInput.CursorEnd()
	}
	return m
}

func TestForm_ShiftEnterInsertsNewlineInBody(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"native KeyEnter", tea.KeyMsg{Type: tea.KeyEnter}},
		{"kitty CSI-u shift+enter", captureCSI(t, []byte("\x1b[13;2u"))},
		{"modifyOtherKeys shift+enter", captureCSI(t, []byte("\x1b[27;2;13~"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newFormForKeyTest(t, FieldBody, "hello")
			m.Update(tc.msg)
			if got := m.bodyInput.Value(); got != "hello\n" {
				t.Errorf("body = %q, want %q", got, "hello\n")
			}
		})
	}
}

func TestForm_OptionDeleteDeletesWordInBody(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"native alt+backspace", tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}},
		{"kitty CSI-u option+delete", captureCSI(t, []byte("\x1b[127;3u"))},
		{"modifyOtherKeys option+delete", captureCSI(t, []byte("\x1b[27;3;127~"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newFormForKeyTest(t, FieldBody, "hello world foo")
			m.Update(tc.msg)
			if got := m.bodyInput.Value(); got != "hello world " {
				t.Errorf("body = %q, want %q", got, "hello world ")
			}
		})
	}
}

func TestForm_OptionDeleteDeletesWordInTitle(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"native alt+backspace", tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}},
		{"kitty CSI-u option+delete", captureCSI(t, []byte("\x1b[127;3u"))},
		{"modifyOtherKeys option+delete", captureCSI(t, []byte("\x1b[27;3;127~"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newFormForKeyTest(t, FieldTitle, "hello world foo")
			m.Update(tc.msg)
			if got := m.titleInput.Value(); got != "hello world " {
				t.Errorf("title = %q, want %q", got, "hello world ")
			}
		})
	}
}

func TestForm_CmdDeleteClearsLineInBody(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"native ctrl+u", tea.KeyMsg{Type: tea.KeyCtrlU}},
		{"kitty CSI-u cmd+delete", captureCSI(t, []byte("\x1b[127;9u"))},
		{"modifyOtherKeys cmd+delete", captureCSI(t, []byte("\x1b[27;9;127~"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newFormForKeyTest(t, FieldBody, "hello world foo")
			m.Update(tc.msg)
			if got := m.bodyInput.Value(); got != "" {
				t.Errorf("body = %q, want empty", got)
			}
		})
	}
}

func TestForm_CmdDeleteClearsLineInTitle(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"native ctrl+u", tea.KeyMsg{Type: tea.KeyCtrlU}},
		{"kitty CSI-u cmd+delete", captureCSI(t, []byte("\x1b[127;9u"))},
		{"modifyOtherKeys cmd+delete", captureCSI(t, []byte("\x1b[27;9;127~"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newFormForKeyTest(t, FieldTitle, "hello world foo")
			m.Update(tc.msg)
			if got := m.titleInput.Value(); got != "" {
				t.Errorf("title = %q, want empty", got)
			}
		})
	}
}
