package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/executor"
)

// `ty hosts sync` is the manual trigger for what every placed spawn already
// does: bring a host's skills and plugins in step with this machine's. It is
// for the moments a spawn is not coming — a host just set up, or a skill just
// edited that a running agent should pick up on its next session.

func newHostsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "Work with the hosts tasks are placed on",
	}
	cmd.AddCommand(newHostsSyncCmd())
	return cmd
}

func newHostsSyncCmd() *cobra.Command {
	var force, verbose bool
	cmd := &cobra.Command{
		Use:   "sync <host>...",
		Short: "Copy this machine's skills and plugins to a host now",
		Long: `Bring a host's Claude skills and plugins in step with this machine's.

Every task placed on a host already does this before it spawns; this runs the
same sync on demand. Skills in ~/.claude/skills are copied (symlinks resolved,
without node_modules, .git or macOS binaries), a changed skill's ./setup is
started on the host, and enabled plugins are installed there from their
marketplaces. The account-synced skills, CLAUDE.md, settings and credentials
are never touched.

When the host already matches, this is one short command. Pass --force to redo
everything regardless of what the host last recorded.

Examples:
  ty hosts sync build-box
  ty hosts sync build-box gpu-box --force`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			failed := 0
			for _, host := range args {
				ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
				res, err := executor.SyncHost(ctx, host, force)
				cancel()
				if err != nil {
					failed++
					fmt.Fprintln(cmd.ErrOrStderr(), errorStyle.Render(fmt.Sprintf("%s: %v", host, err)))
					continue
				}
				fmt.Fprintln(cmd.OutOrStdout(), res.Summary(host))
				if verbose {
					for _, s := range res.Skipped {
						fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("  not synced: "+s))
					}
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d host(s) could not be synced", failed, len(args))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Redo everything, ignoring what the host last recorded")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Also list what cannot be synced, and why")
	return cmd
}
