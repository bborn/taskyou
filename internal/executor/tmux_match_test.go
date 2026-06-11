package executor

import (
	"testing"
)

func TestFindPanesForWindow(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		windowName string
		wantPIDs   []int
	}{
		{
			name:       "exact match",
			input:      "task-daemon-123:task-5:0 12345",
			windowName: "task-5",
			wantPIDs:   []int{12345},
		},
		{
			name:       "no substring match on longer window name",
			input:      "task-daemon-123:task-55:0 12345",
			windowName: "task-5",
			wantPIDs:   nil,
		},
		{
			name:       "no substring match on prefix range",
			input:      "task-daemon-123:task-50:0 11111\ntask-daemon-123:task-55:0 22222\ntask-daemon-123:task-59:0 33333",
			windowName: "task-5",
			wantPIDs:   nil,
		},
		{
			name:       "match among multiple windows",
			input:      "task-daemon-123:task-5:0 11111\ntask-daemon-123:task-55:0 22222\ntask-daemon-123:task-6:0 33333",
			windowName: "task-5",
			wantPIDs:   []int{11111},
		},
		{
			name:       "multiple panes in same window",
			input:      "task-daemon-123:task-5:0 11111\ntask-daemon-123:task-5:1 22222",
			windowName: "task-5",
			wantPIDs:   []int{11111, 22222},
		},
		{
			name:       "no match",
			input:      "task-daemon-123:task-10:0 12345",
			windowName: "task-5",
			wantPIDs:   nil,
		},
		{
			name:       "empty input",
			input:      "",
			windowName: "task-5",
			wantPIDs:   nil,
		},
		{
			name:       "malformed line no colon",
			input:      "nocolon 12345",
			windowName: "task-5",
			wantPIDs:   nil,
		},
		{
			name:       "malformed line no pid",
			input:      "task-daemon-123:task-5:0",
			windowName: "task-5",
			wantPIDs:   nil,
		},
		{
			name:       "malformed pid non-numeric",
			input:      "task-daemon-123:task-5:0 notapid",
			windowName: "task-5",
			wantPIDs:   nil,
		},
		{
			name:       "whitespace and empty lines",
			input:      "  \n\ntask-daemon-123:task-5:0 12345\n  \n",
			windowName: "task-5",
			wantPIDs:   []int{12345},
		},
		{
			name:       "session name contains window name but window differs",
			input:      "task-daemon-task-5:task-55:0 12345",
			windowName: "task-5",
			wantPIDs:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findPanesForWindow(tt.input, tt.windowName)
			if len(got) == 0 && len(tt.wantPIDs) == 0 {
				return // both empty/nil
			}
			if len(got) != len(tt.wantPIDs) {
				t.Fatalf("findPanesForWindow() returned %d PIDs, want %d\n  got:  %v\n  want: %v", len(got), len(tt.wantPIDs), got, tt.wantPIDs)
			}
			for i, pid := range got {
				if pid != tt.wantPIDs[i] {
					t.Errorf("PID[%d] = %d, want %d", i, pid, tt.wantPIDs[i])
				}
			}
		})
	}
}
