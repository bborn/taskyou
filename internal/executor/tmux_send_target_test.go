package executor

import (
	"testing"
)

// When the detail view joins the agent pane into the UI session, the shell pane
// collapses onto window index 0. Sending to windowTarget+".0" then misdelivers
// input to the shell and the agent starves. agentSendTargetForPane must prefer
// the persisted, stable pane id (the same one the UI uses for capture).
func TestAgentSendTargetForPane(t *testing.T) {
	tests := []struct {
		name         string
		claudePaneID string
		windowTarget string
		want         string
	}{
		{
			name:         "persisted pane id is used, never window .0",
			claudePaneID: "%3412",
			windowTarget: "task-daemon-123:task-5",
			want:         "%3412",
		},
		{
			name:         "no persisted pane id -> fall back to window .0",
			claudePaneID: "",
			windowTarget: "task-daemon-123:task-5",
			want:         "task-daemon-123:task-5.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentSendTargetForPane(tt.claudePaneID, tt.windowTarget)
			if got != tt.want {
				t.Errorf("agentSendTargetForPane(%q, %q) = %q, want %q",
					tt.claudePaneID, tt.windowTarget, got, tt.want)
			}
		})
	}
}
