package executor

import "testing"

func TestDetectAuthPrompt(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "empty content",
			content: "",
			want:    false,
		},
		{
			name:    "normal working output",
			content: "Editing main.go\nRunning tests...\nAll tests passed.",
			want:    false,
		},
		{
			name:    "please run /login",
			content: "Your session has ended.\nPlease run /login to continue.",
			want:    true,
		},
		{
			name:    "oauth token expired",
			content: "Error: OAuth token has expired. Please re-authenticate.",
			want:    true,
		},
		{
			name:    "invalid api key",
			content: "API Error: Invalid API key · Please run /login",
			want:    true,
		},
		{
			name:    "login screen",
			content: "Select login method:\n1. Claude account with subscription\n2. Anthropic Console account",
			want:    true,
		},
		{
			name:    "authentication error",
			content: `{"type":"error","error":{"type":"authentication_error"}}`,
			want:    true,
		},
		{
			name:    "case insensitive",
			content: "PLEASE RUN /LOGIN",
			want:    true,
		},
		{
			// A diff that merely mentions login should not trip detection.
			name:    "false positive guard - code mentioning login",
			content: "func handleLogin() {\n  // redirect to /login on failure\n}",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, got := DetectAuthPrompt(tt.content)
			if got != tt.want {
				t.Errorf("DetectAuthPrompt() = %v, want %v (reason=%q)", got, tt.want, reason)
			}
			if got && reason == "" {
				t.Errorf("DetectAuthPrompt() returned match with empty reason")
			}
		})
	}
}
