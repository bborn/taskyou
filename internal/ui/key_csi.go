package ui

import (
	"fmt"
	"reflect"

	tea "github.com/charmbracelet/bubbletea"
)

// translateModernKey converts CSI-u and xterm modifyOtherKeys sequences that
// bubbletea v1 reports as unknownCSISequenceMsg into standard tea.KeyMsg
// values so the textinput/textarea bindings can act on them. macOS terminals
// configured with the kitty keyboard protocol or modifyOtherKeys mode 2
// (Ghostty, WezTerm, modern iTerm2, Kitty) send these sequences for
// Shift+Enter, Option+Delete, Cmd+Delete, and similar combinations that the
// user expects to work like in any other macOS text field.
//
// If msg is not a recognized CSI sequence, it is returned unchanged.
func translateModernKey(msg tea.Msg) tea.Msg {
	seq, ok := csiSequence(msg)
	if !ok {
		return msg
	}
	if translated, ok := csiKeyMap[seq]; ok {
		return translated
	}
	return msg
}

// csiSequence extracts the parameter+final portion of an unknown CSI sequence
// reported by bubbletea (everything after the leading "\x1b["). The underlying
// type is unexported, so we identify it by its type name and copy the bytes
// out via reflection.
func csiSequence(msg tea.Msg) (string, bool) {
	v := reflect.ValueOf(msg)
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return "", false
	}
	if fmt.Sprintf("%T", msg) != "tea.unknownCSISequenceMsg" {
		return "", false
	}
	if v.Len() < 3 {
		return "", false
	}
	buf := make([]byte, v.Len())
	for i := 0; i < v.Len(); i++ {
		buf[i] = byte(v.Index(i).Uint())
	}
	if buf[0] != 0x1b || buf[1] != '[' {
		return "", false
	}
	return string(buf[2:]), true
}

// csiKeyMap maps the body of CSI sequences (everything after "\x1b[") to a
// canonical tea.KeyMsg. The mapping covers the two protocols common on macOS:
//
//   - Kitty keyboard protocol: "<codepoint>;<modifier>u" where modifier is
//     1 + sum of (shift=1, alt=2, ctrl=4, super=8, hyper=16, meta=32).
//   - xterm modifyOtherKeys mode 2: "27;<modifier>;<codepoint>~" where
//     modifier uses the same convention as xterm CSI ~ sequences (2=shift,
//     3=alt, 5=ctrl, 9=super, ...).
//
// We treat super (Cmd on macOS) and meta the same: line-start/line-end edits
// for navigation, full-line deletion for delete/backspace.
var csiKeyMap = map[string]tea.Msg{
	// Shift+Enter: insert newline. The textarea binds tea.KeyEnter to
	// InsertNewline, so we route both protocols to KeyEnter.
	"13;2u":     tea.KeyMsg{Type: tea.KeyEnter},
	"27;2;13~":  tea.KeyMsg{Type: tea.KeyEnter},
	"13;6u":     tea.KeyMsg{Type: tea.KeyEnter}, // shift+ctrl+enter
	"27;6;13~":  tea.KeyMsg{Type: tea.KeyEnter},
	"13;10u":    tea.KeyMsg{Type: tea.KeyEnter}, // shift+super+enter
	"27;10;13~": tea.KeyMsg{Type: tea.KeyEnter},

	// Option/Alt+Backspace and Option/Alt+Delete: delete word backward.
	// textinput/textarea bind alt+backspace to DeleteWordBackward. Most
	// terminals emit \x1b\x7f for option+delete (already parsed as
	// alt+backspace), but CSI-u terminals send these forms.
	"127;3u":    tea.KeyMsg{Type: tea.KeyBackspace, Alt: true},
	"27;3;127~": tea.KeyMsg{Type: tea.KeyBackspace, Alt: true},
	"8;3u":      tea.KeyMsg{Type: tea.KeyBackspace, Alt: true},
	"27;3;8~":   tea.KeyMsg{Type: tea.KeyBackspace, Alt: true},

	// Cmd/Super+Backspace and Cmd/Super+Delete: delete to start of line.
	// textinput/textarea bind ctrl+u to DeleteBeforeCursor, matching the
	// macOS convention for Cmd+Delete in native text fields.
	"127;9u":    tea.KeyMsg{Type: tea.KeyCtrlU},
	"27;9;127~": tea.KeyMsg{Type: tea.KeyCtrlU},
	"8;9u":      tea.KeyMsg{Type: tea.KeyCtrlU},
	"27;9;8~":   tea.KeyMsg{Type: tea.KeyCtrlU},

	// Cmd/Super+Left and Cmd/Super+Right: jump to line start/end. These
	// aren't in the original report but rounding out the macOS-native set
	// avoids the same frustration as the user reported for Cmd+Delete.
	"1;9D": tea.KeyMsg{Type: tea.KeyCtrlA},
	"1;9C": tea.KeyMsg{Type: tea.KeyCtrlE},
}
