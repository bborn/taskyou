package ui

import (
	"strings"
	"testing"
	"time"
)

func TestBuildDueInfoSeverity(t *testing.T) {
	now := time.Now()

	overdue := BuildDueInfo(now.Add(-2*time.Hour), now)
	if overdue.Severity != DueSeverityOverdue {
		t.Fatalf("expected overdue severity, got %v", overdue.Severity)
	}
	if !strings.Contains(overdue.Text, "late") {
		t.Fatalf("expected overdue text to mention late, got %q", overdue.Text)
	}

	soon := BuildDueInfo(now.Add(30*time.Minute), now)
	if soon.Severity != DueSeveritySoon {
		t.Fatalf("expected soon severity, got %v", soon.Severity)
	}
	if soon.Icon != "⌛" {
		t.Fatalf("expected hourglass icon, got %q", soon.Icon)
	}

	upcoming := BuildDueInfo(now.Add(72*time.Hour), now)
	if upcoming.Severity != DueSeverityUpcoming {
		t.Fatalf("expected upcoming severity, got %v", upcoming.Severity)
	}
	if !strings.HasPrefix(upcoming.Text, "due ") {
		t.Fatalf("expected upcoming text to start with 'due ', got %q", upcoming.Text)
	}
}
