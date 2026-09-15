package executor

import (
	"reflect"
	"strings"
	"testing"
)

func TestOrphanHiddenShellWindows(t *testing.T) {
	row := func(f ...string) string { return strings.Join(f, "\t") }
	listing := strings.Join([]string{
		row("task-daemon-1", "@1", "_hidden_shell_5436", "zsh"),
		row("task-daemon-1", "@2", "_hidden_shell_5436", "-zsh"),
		// Running a dev server: not idle, keep it.
		row("task-daemon-1", "@3", "_hidden_shell_5436", "ruby"),
		// Another task's shell.
		row("task-daemon-1", "@4", "_hidden_shell_5117", "zsh"),
		// The same window seen through a view session grouped with the daemon's.
		row("ty-view-9-1", "@1", "_hidden_shell_5436", "zsh"),
		row("task-daemon-1", "@5", "task-5436", "zsh"),
	}, "\n")
	got := orphanHiddenShellWindows(listing, HiddenShellWindowName(5436))
	if want := []string{"@1", "@2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orphanHiddenShellWindows = %v, want %v", got, want)
	}
}
