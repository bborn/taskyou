package executor

import (
	"testing"
)

func TestDetectRateLimit(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		executorName string
		wantDetected bool
		wantContains string
	}{
		// Claude rate limit patterns
		{
			name:         "claude rate limit",
			output:       "Error: rate limit exceeded, please try again",
			executorName: "claude",
			wantDetected: true,
			wantContains: "rate limit",
		},
		{
			name:         "claude 429 error",
			output:       "HTTP 429 Too Many Requests",
			executorName: "claude",
			wantDetected: true,
			wantContains: "429",
		},
		{
			name:         "claude resource exhausted",
			output:       "ResourceExhausted: API quota exceeded",
			executorName: "claude",
			wantDetected: true,
			wantContains: "exhausted",
		},
		{
			name:         "claude overloaded",
			output:       "The API is currently overloaded",
			executorName: "claude",
			wantDetected: true,
			wantContains: "overloaded",
		},
		{
			name:         "claude quota exceeded",
			output:       "Error: quota_exceeded for your organization",
			executorName: "claude",
			wantDetected: true,
			wantContains: "Quota exceeded",
		},
		{
			name:         "claude usage limit",
			output:       "You have hit your usage limit for this month",
			executorName: "claude",
			wantDetected: true,
			wantContains: "Usage limit",
		},

		// Codex rate limit patterns
		{
			name:         "codex rate limit",
			output:       "Error: Rate limit reached for requests",
			executorName: "codex",
			wantDetected: true,
			wantContains: "rate limit",
		},
		{
			name:         "codex TPM limit",
			output:       "Rate limit: tokens per min (TPM) exceeded",
			executorName: "codex",
			wantDetected: true,
			wantContains: "Token/request",
		},
		{
			name:         "codex insufficient quota",
			output:       "Error: insufficient_quota - you have exceeded your billing limit",
			executorName: "codex",
			wantDetected: true,
			wantContains: "Insufficient quota",
		},

		// Gemini rate limit patterns
		{
			name:         "gemini rate limit",
			output:       "Error: RESOURCE_EXHAUSTED: Quota exceeded",
			executorName: "gemini",
			wantDetected: true,
			wantContains: "429",
		},
		{
			name:         "gemini quota exceeded",
			output:       "quotaExceeded: Project has exceeded its quota",
			executorName: "gemini",
			wantDetected: true,
			wantContains: "Quota exceeded",
		},

		// Generic patterns (any executor)
		{
			name:         "generic try again later",
			output:       "Service unavailable, please try again later",
			executorName: "claude",
			wantDetected: true,
			wantContains: "retry later",
		},
		{
			name:         "generic temporarily unavailable",
			output:       "API is temporarily unavailable",
			executorName: "codex",
			wantDetected: true,
			wantContains: "temporarily unavailable",
		},

		// Negative cases
		{
			name:         "normal output",
			output:       "Task completed successfully",
			executorName: "claude",
			wantDetected: false,
		},
		{
			name:         "error but not rate limit",
			output:       "Error: File not found",
			executorName: "claude",
			wantDetected: false,
		},
		{
			name:         "empty output",
			output:       "",
			executorName: "claude",
			wantDetected: false,
		},
		{
			name:         "pattern for different executor",
			output:       "codex: insufficient_quota",
			executorName: "gemini",
			wantDetected: false, // This pattern is for codex, not gemini
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, message := DetectRateLimit(tt.output, tt.executorName)
			if detected != tt.wantDetected {
				t.Errorf("DetectRateLimit() detected = %v, want %v", detected, tt.wantDetected)
			}
			if tt.wantDetected && tt.wantContains != "" {
				if message == "" {
					t.Errorf("DetectRateLimit() message is empty, want to contain %q", tt.wantContains)
				}
			}
		})
	}
}

func TestDetectRateLimitInLines(t *testing.T) {
	lines := []string{
		"Executing task...",
		"Sending request to API",
		"Error: rate limit exceeded",
		"Task failed",
	}

	detected, message := DetectRateLimitInLines(lines, "claude")
	if !detected {
		t.Error("DetectRateLimitInLines() should detect rate limit in lines")
	}
	if message == "" {
		t.Error("DetectRateLimitInLines() message should not be empty")
	}

	// Test with no rate limit
	normalLines := []string{
		"Executing task...",
		"Task completed successfully",
	}

	detected, _ = DetectRateLimitInLines(normalLines, "claude")
	if detected {
		t.Error("DetectRateLimitInLines() should not detect rate limit in normal output")
	}
}

func TestDetectContextLimit(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		executorName string
		wantDetected bool
	}{
		{
			name:         "claude context too long",
			output:       "Error: context window too long",
			executorName: "claude",
			wantDetected: true,
		},
		{
			name:         "claude max tokens",
			output:       "maximum tokens exceeded: 128000",
			executorName: "claude",
			wantDetected: true,
		},
		{
			name:         "codex context length exceeded",
			output:       "context_length_exceeded: Input too long",
			executorName: "codex",
			wantDetected: true,
		},
		{
			name:         "gemini context limit",
			output:       "CONTEXT_LENGTH_EXCEEDED",
			executorName: "gemini",
			wantDetected: true,
		},
		{
			name:         "normal output",
			output:       "Processing request...",
			executorName: "claude",
			wantDetected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, _ := DetectContextLimit(tt.output, tt.executorName)
			if detected != tt.wantDetected {
				t.Errorf("DetectContextLimit() detected = %v, want %v", detected, tt.wantDetected)
			}
		})
	}
}
