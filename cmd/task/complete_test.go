package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// buildTestRoot wires just the two commands whose names/aliases can collide.
// Order matches main(): complete is registered before close.
func buildTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "ty"}
	root.AddCommand(newCompleteCmd())
	root.AddCommand(&cobra.Command{
		Use:     "close <task-id>",
		Aliases: []string{"done"},
		Short:   "Mark a task as done",
		Run:     func(cmd *cobra.Command, args []string) {},
	})
	return root
}

// `ty complete` must resolve to the real completion command, never to `ty close`.
// These are NOT interchangeable: close is a plain status write, while complete
// runs the verify gate, parks human gates, and routes PRs. Cobra returns the
// first name-or-alias match, so if `close` ever re-declares a "complete" alias
// this silently starts depending on registration order — and an agent calling
// `ty complete` would skip every gate.
func TestCompleteResolvesToCompletionCommandNotClose(t *testing.T) {
	root := buildTestRoot()

	cmd, _, err := root.Find([]string{"complete"})
	if err != nil {
		t.Fatalf("find complete: %v", err)
	}
	if cmd.Name() != "complete" {
		t.Fatalf("`ty complete` resolved to %q, want the completion command", cmd.Name())
	}
	if !strings.Contains(cmd.Short, "taskyou_complete") {
		t.Errorf("resolved command is not the completion command: %q", cmd.Short)
	}
}

// `ty close` and its remaining alias must still work — this change must not
// break the human's way of approving a gate.
func TestCloseAndDoneStillResolveToClose(t *testing.T) {
	root := buildTestRoot()
	for _, name := range []string{"close", "done"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		if cmd.Name() != "close" {
			t.Errorf("`ty %s` resolved to %q, want close", name, cmd.Name())
		}
	}
}

// The completion command must refuse to run without a summary — an empty
// summary produces a useless activity log and, for a gate, gives the reviewing
// human nothing to review.
func TestCompleteRequiresSummary(t *testing.T) {
	cmd := newCompleteCmd()
	cmd.SetArgs([]string{"42"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("expected a missing-summary error, got %v", err)
	}
}

// With no argument and no WORKTREE_TASK_ID there is nothing to complete; it must
// say so rather than defaulting to some arbitrary task.
func TestCompleteRequiresTaskID(t *testing.T) {
	t.Setenv("WORKTREE_TASK_ID", "")
	cmd := newCompleteCmd()
	cmd.SetArgs([]string{"--summary", "did the thing"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "task id is required") {
		t.Fatalf("expected a missing-task-id error, got %v", err)
	}
}
