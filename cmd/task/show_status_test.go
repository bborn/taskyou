package main

import (
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

func TestPlacementLine(t *testing.T) {
	const noHost = "no host in /home/u/.config/on/hosts.yaml serves myapp (4 hosts in inventory)"
	tests := []struct {
		name, target, reason, want string
	}{
		{"no placement handler answered", "", "", ""},
		{"resolver kept it local", "", noHost, "local — " + noHost},
		{"recorded as local", "local", "chosen by hand when the task was created", "local — chosen by hand when the task was created"},
		{"placed on a host", "build-a", "only host serving taskyou", "build-a — only host serving taskyou"},
		{"host with no reason", "build-a", "", "build-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := placementLine(tt.target, tt.reason); got != tt.want {
				t.Errorf("placementLine(%q, %q) = %q, want %q", tt.target, tt.reason, got, tt.want)
			}
		})
	}
}

// A task blocked on Claude's trust dialog, with a local placement note on
// record. The block reason must come from the blocking transition, never from
// the placement reason.
func TestBlockReasonComesFromTheBlockingTransition(t *testing.T) {
	dialog := "Waiting on Claude Code's workspace trust prompt — choose \"Yes, I trust this folder\" to let the task start"
	events := []db.StatusEvent{
		{From: "", To: db.StatusQueued, Reason: "task created"},
		{From: db.StatusQueued, To: db.StatusProcessing, Reason: "picked up for execution"},
		{From: db.StatusProcessing, To: db.StatusBlocked, Reason: "an older block"},
		{From: db.StatusBlocked, To: db.StatusQueued, Reason: "retried"},
		{From: db.StatusQueued, To: db.StatusProcessing, Reason: "picked up for execution"},
		{From: db.StatusProcessing, To: db.StatusBlocked,
			Reason:   "the executor is waiting on a dialog and cannot make progress",
			Evidence: db.Evidence{Observed: dialog}},
	}
	reason, detail := blockReason(events)
	if reason != "the executor is waiting on a dialog and cannot make progress" {
		t.Errorf("reason = %q", reason)
	}
	if detail != dialog {
		t.Errorf("detail = %q, want %q", detail, dialog)
	}
	if strings.Contains(reason+detail, "serves") {
		t.Errorf("block reason mentions placement: %q / %q", reason, detail)
	}
}

func TestBlockReasonEdgeCases(t *testing.T) {
	if r, d := blockReason(nil); r != "" || d != "" {
		t.Errorf("no events: got %q / %q, want empty", r, d)
	}
	never := []db.StatusEvent{{To: db.StatusQueued, Reason: "task created"}}
	if r, d := blockReason(never); r != "" || d != "" {
		t.Errorf("never blocked: got %q / %q, want empty", r, d)
	}
	same := []db.StatusEvent{{To: db.StatusBlocked, Reason: "stuck", Evidence: db.Evidence{Observed: "stuck"}}}
	if r, d := blockReason(same); r != "stuck" || d != "" {
		t.Errorf("duplicate detail: got %q / %q, want %q / \"\"", r, d, "stuck")
	}
	long := []db.StatusEvent{{To: db.StatusBlocked, Reason: "asked", Evidence: db.Evidence{Observed: "agent question:\n" + strings.Repeat("x", 500)}}}
	_, d := blockReason(long)
	if strings.Contains(d, "\n") || len([]rune(d)) > 200 {
		t.Errorf("detail not collapsed to one capped line: %d runes, newline=%v", len([]rune(d)), strings.Contains(d, "\n"))
	}
}
