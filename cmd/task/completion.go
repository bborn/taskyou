package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
)

// newCompletionCmd creates the completion command with subcommands for each shell.
func newCompletionCmd(rootCmd *cobra.Command) *cobra.Command {
	completionCmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for ty.

To load completions:

Bash:
  $ source <(ty completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ ty completion bash > /etc/bash_completion.d/ty
  # macOS:
  $ ty completion bash > $(brew --prefix)/etc/bash_completion.d/ty

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it. You can execute the following once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ ty completion zsh > "${fpath[1]}/_ty"

  # You will need to start a new shell for this setup to take effect.

Fish:
  $ ty completion fish | source

  # To load completions for each session, execute once:
  $ ty completion fish > ~/.config/fish/completions/ty.fish

PowerShell:
  PS> ty completion powershell | Out-String | Invoke-Expression

  # To load completions for every new session, run:
  PS> ty completion powershell > ty.ps1
  # and source this file from your PowerShell profile.
`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		Run: func(cmd *cobra.Command, args []string) {
			switch args[0] {
			case "bash":
				rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				rootCmd.GenFishCompletion(os.Stdout, true)
			case "powershell":
				rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
			}
		},
	}

	return completionCmd
}

// completeTaskIDs returns a completion function that suggests task IDs with their titles.
func completeTaskIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return fetchTaskCompletions(toComplete)
}

// completeTaskIDsThenStatus completes task ID for first arg, status for second.
func completeTaskIDsThenStatus(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return fetchTaskCompletions(toComplete)
	}
	if len(args) == 1 {
		return validStatuses(), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completeTaskIDsThenProject completes task ID for first arg, project name for second.
func completeTaskIDsThenProject(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return fetchTaskCompletions(toComplete)
	}
	if len(args) == 1 {
		return fetchProjectCompletions()
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completeProjectNames returns a completion function for project names.
func completeProjectNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return fetchProjectCompletions()
}

// completeSettingKeys returns a completion function for settings set first arg.
func completeSettingKeys(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return []string{
			"anthropic_api_key\tAPI key for ghost text autocomplete",
			"autocomplete_enabled\tEnable/disable ghost text (true/false)",
			"idle_suspend_timeout\tIdle timeout before suspending (e.g. 6h)",
		}, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completeTypeNames returns a completion function for task type names.
func completeTypeNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return fetchTypeCompletions()
}

// completeFlagProjects provides completions for --project flag values.
func completeFlagProjects(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return fetchProjectCompletions()
}

// completeFlagExecutors provides completions for --executor flag values.
func completeFlagExecutors(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{
		"claude\tAnthropic Claude (default)",
		"codex\tOpenAI Codex",
		"gemini\tGoogle Gemini",
		"pi\tInflection Pi",
		"opencode\tOpenCode",
		"openclaw\tOpenClaw",
	}, cobra.ShellCompDirectiveNoFileComp
}

// completeFlagTypes provides completions for --type flag values.
func completeFlagTypes(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	comps, directive := fetchTypeCompletions()
	if len(comps) > 0 {
		return comps, directive
	}
	// Fallback to built-in types if DB unavailable
	return []string{"code", "writing", "thinking"}, cobra.ShellCompDirectiveNoFileComp
}

// completeFlagStatuses provides completions for --status flag values.
func completeFlagStatuses(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return validStatuses(), cobra.ShellCompDirectiveNoFileComp
}

// fetchTaskCompletions opens the DB and returns task ID completions.
func fetchTaskCompletions(toComplete string) ([]string, cobra.ShellCompDirective) {
	database, err := db.Open(db.DefaultPath())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer database.Close()

	tasks, err := database.ListTasks(db.ListTasksOptions{IncludeClosed: true, Limit: 50})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var completions []string
	for _, t := range tasks {
		desc := t.Title
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
		completions = append(completions, fmt.Sprintf("%d\t[%s] %s", t.ID, t.Status, desc))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// fetchProjectCompletions opens the DB and returns project name completions.
func fetchProjectCompletions() ([]string, cobra.ShellCompDirective) {
	database, err := db.Open(db.DefaultPath())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer database.Close()

	projects, err := database.ListProjects()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var completions []string
	for _, p := range projects {
		desc := p.Name
		if p.Path != "" {
			desc = p.Path
		}
		completions = append(completions, fmt.Sprintf("%s\t%s", p.Name, desc))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// fetchTypeCompletions opens the DB and returns task type completions.
func fetchTypeCompletions() ([]string, cobra.ShellCompDirective) {
	database, err := db.Open(db.DefaultPath())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer database.Close()

	types, err := database.ListTaskTypes()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var completions []string
	for _, t := range types {
		label := t.Label
		if label == "" {
			label = t.Name
		}
		completions = append(completions, fmt.Sprintf("%s\t%s", t.Name, label))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
