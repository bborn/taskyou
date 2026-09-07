package executor

import (
	"reflect"
	"testing"
)

func TestDuplicateTaskWindowsPreservesReplacementForStaleID(t *testing.T) {
	listing := "task-daemon-qa:@2:task-1\ntask-daemon-qa:@3:task-1\ntask-ui-qa:@4:task-1\ntask-daemon-linked:@2:task-1\ntask-daemon-qa:@5:task-2"
	for _, tc := range []struct {
		saved, want string
		duplicates  []string
	}{
		{"@gone", "@2", []string{"@3"}},
		{"@3", "@3", []string{"@2"}},
		{"", "@2", []string{"@3"}},
	} {
		canonical, duplicates := duplicateTaskWindows(listing, "task-1", tc.saved)
		if canonical != tc.want || !reflect.DeepEqual(duplicates, tc.duplicates) {
			t.Fatalf("saved %s: survivor=%s duplicates=%v", tc.saved, canonical, duplicates)
		}
	}
	canonical, duplicates := duplicateTaskWindows("task-daemon-qa:@2:task-1-shell", "task-1", "@gone")
	if canonical != "" || len(duplicates) != 0 {
		t.Fatal("shell-only window marked for deletion")
	}
}
