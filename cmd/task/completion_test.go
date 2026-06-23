package main

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
)

func TestNewCompletionCmd(t *testing.T) {
	rootCmd := &cobra.Command{Use: "ty"}
	completionCmd := newCompletionCmd(rootCmd)

	if completionCmd.Use != "completion [bash|zsh|fish|powershell]" {
		t.Errorf("unexpected Use: %s", completionCmd.Use)
	}

	if len(completionCmd.ValidArgs) != 4 {
		t.Errorf("expected 4 valid args, got %d", len(completionCmd.ValidArgs))
	}

	expected := map[string]bool{"bash": true, "zsh": true, "fish": true, "powershell": true}
	for _, arg := range completionCmd.ValidArgs {
		if !expected[arg] {
			t.Errorf("unexpected valid arg: %s", arg)
		}
	}
}

func TestCompletionCmdOutput(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			rootCmd := &cobra.Command{Use: "ty"}
			completionCmd := newCompletionCmd(rootCmd)
			rootCmd.AddCommand(completionCmd)

			// Capture output
			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			rootCmd.SetArgs([]string{"completion", shell})
			err := rootCmd.Execute()

			w.Close()
			os.Stdout = old

			if err != nil {
				t.Fatalf("completion %s failed: %v", shell, err)
			}

			buf := make([]byte, 1024)
			n, _ := r.Read(buf)
			if n == 0 {
				t.Errorf("completion %s produced no output", shell)
			}
		})
	}
}

func TestCompleteTaskIDsThenStatus(t *testing.T) {
	// When 1 arg already provided (task ID), should return statuses
	completions, directive := completeTaskIDsThenStatus(nil, []string{"42"}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected NoFileComp directive")
	}

	statuses := validStatuses()
	if len(completions) != len(statuses) {
		t.Errorf("expected %d statuses, got %d", len(statuses), len(completions))
	}

	// When 2 args already provided, no more completions
	completions, _ = completeTaskIDsThenStatus(nil, []string{"42", "done"}, "")
	if len(completions) != 0 {
		t.Errorf("expected no completions for 2+ args, got %d", len(completions))
	}
}

func TestCompleteSettingKeys(t *testing.T) {
	completions, directive := completeSettingKeys(nil, []string{}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected NoFileComp directive")
	}
	if len(completions) != 8 {
		t.Errorf("expected 8 setting keys, got %d", len(completions))
	}

	// After first arg, no more completions
	completions, _ = completeSettingKeys(nil, []string{"anthropic_api_key"}, "")
	if len(completions) != 0 {
		t.Errorf("expected no completions after key, got %d", len(completions))
	}
}

func TestCompleteFlagExecutors(t *testing.T) {
	completions, directive := completeFlagExecutors(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected NoFileComp directive")
	}
	if len(completions) != 6 {
		t.Errorf("expected 6 executors, got %d", len(completions))
	}
}

func TestCompleteFlagStatuses(t *testing.T) {
	completions, directive := completeFlagStatuses(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("expected NoFileComp directive")
	}
	if len(completions) == 0 {
		t.Error("expected some status completions")
	}
}
