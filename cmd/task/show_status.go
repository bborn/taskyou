package main

import (
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// placementLine renders where a task runs and why, as one line for `ty show`.
//
// It used to print as two lines, "Ran on: local" and "Because: <reason>", and
// on a blocked task the "Because:" read as the reason for the block. A task
// that sat on Claude's trust dialog showed
// "Because: no host in hosts.yaml serves <project>", which is the placement
// resolver correctly keeping it local, and nothing to do with why it stopped.
// So placement is labelled as placement, and the block reason has its own line
// (blockReason).
//
// An empty result means no placement handler has answered for this task, and
// nothing is printed — exactly what a machine without a fleet always showed.
func placementLine(target, reason string) string {
	target = strings.TrimSpace(target)
	reason = strings.TrimSpace(reason)
	if target == "" && reason == "" {
		return ""
	}
	where := target
	if !executor.IsRemotePlacement(target) {
		where = "local"
	}
	if reason == "" {
		return where
	}
	return where + " — " + reason
}

// blockReason returns why a blocked task is blocked: the reason recorded on the
// transition into blocked, and the evidence that transition observed (for a
// task parked on a dialog, which dialog). Events are in append order, as
// GetAppliedStatusEvents returns them. Both are empty when no transition into
// blocked is on record.
func blockReason(events []db.StatusEvent) (reason, detail string) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.To != db.StatusBlocked {
			continue
		}
		reason = oneLine(ev.Reason, 200)
		detail = oneLine(ev.Evidence.Observed, 200)
		if detail == reason {
			detail = ""
		}
		return reason, detail
	}
	return "", ""
}

// oneLine collapses whitespace and caps s at max runes, for a single
// terminal line.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
