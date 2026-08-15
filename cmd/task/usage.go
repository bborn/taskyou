package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/claudeusage"
	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/executor"
)

// `ty usage` exposes, per Claude profile, how much of that account's rate
// limits are already spent. It exists so a decision that used to be guesswork —
// "which of my logins should this task run under?" — can be made from real
// numbers, by a person at the terminal or by a plugin's routing script.
//
// The plugin path is why the output is shaped the way it is. A hook script has
// no JSON parser to lean on, so --percent prints one bare number and nothing
// else; comparing profiles is then a numeric sort in shell. --json is for
// anything richer.

// profileResult pairs a probed config dir with its outcome. Errors are carried
// rather than returned so one unreadable profile (an expired login, say) still
// lets the others report.
type profileResult struct {
	ConfigDir string                `json:"config_dir"`
	Snapshot  *claudeusage.Snapshot `json:"snapshot,omitempty"`
	Error     string                `json:"error,omitempty"`
}

func newUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show how much of each Claude profile's rate limits are used",
		Long: `Report Claude subscription usage for one or more profiles.

A profile is a CLAUDE_CONFIG_DIR — one logged-in Claude account. With no
--config-dir flags, every config dir ty knows about is probed: the default
(~/.claude) plus each distinct dir configured on a project.

Usage is read from the same endpoint Claude Code's own /usage command uses,
authenticated with the credentials already stored for that profile. Nothing is
written: no token is refreshed, rewritten, or printed.

Results are cached for a minute, because that endpoint rate-limits and routing
calls it on every task spawn. Use --refresh to force a live read.

Examples:
  ty usage                                   # every profile ty knows about
  ty usage --config-dir ~/.claude-work       # one profile, with account email
  ty usage --config-dir ~/.claude-work --percent   # just "42" — for scripts
  ty usage --json                            # full detail
  ty usage --refresh                         # ignore the cache`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dirs, _ := cmd.Flags().GetStringArray("config-dir")
			asJSON, _ := cmd.Flags().GetBool("json")
			percentOnly, _ := cmd.Flags().GetBool("percent")
			refresh, _ := cmd.Flags().GetBool("refresh")

			if len(dirs) == 0 {
				dirs = knownConfigDirs()
			}
			if len(dirs) == 0 {
				return fmt.Errorf("no Claude config dirs to check")
			}
			if percentOnly && len(dirs) != 1 {
				return fmt.Errorf("--percent needs exactly one --config-dir (got %d)", len(dirs))
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			client := claudeusage.NewClient()
			client.NoCache = refresh
			results := probeProfiles(ctx, client, dirs, !percentOnly)

			switch {
			case percentOnly:
				if results[0].Error != "" {
					return fmt.Errorf("%s", results[0].Error)
				}
				// One bare number, no styling, no trailing prose: routing
				// scripts read this with $(...) and compare it numerically.
				fmt.Printf("%.0f\n", results[0].Snapshot.UsedPercent())
			case asJSON:
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			default:
				printUsageTable(results)
			}
			return nil
		},
	}

	cmd.Flags().StringArray("config-dir", nil, "Claude config dir to check (repeatable; defaults to every dir ty knows about)")
	cmd.Flags().Bool("json", false, "Emit JSON")
	cmd.Flags().Bool("percent", false, "Print only the binding limit's used percent (requires one --config-dir)")
	cmd.Flags().Bool("refresh", false, "Bypass the cache and read live")
	return cmd
}

// probeProfiles fetches usage for each dir. withAccount adds the account email,
// which costs a second request per profile — worth it for a human reading a
// table, wasted for a script that only wants a number.
func probeProfiles(ctx context.Context, client *claudeusage.Client, dirs []string, withAccount bool) []profileResult {
	results := make([]profileResult, 0, len(dirs))
	for _, dir := range dirs {
		res := profileResult{ConfigDir: dir}
		var snap *claudeusage.Snapshot
		var err error
		if withAccount {
			snap, err = client.FetchWithAccount(ctx, dir)
		} else {
			snap, err = client.Fetch(ctx, dir)
		}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Snapshot = snap
			res.ConfigDir = snap.ConfigDir
		}
		results = append(results, res)
	}
	return results
}

// knownConfigDirs collects every Claude config dir ty is already aware of: the
// default, plus whatever projects have been pointed at. Opening the DB is best
// effort — `ty usage` must still work from a machine with no board.
func knownConfigDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		d = executor.ResolveClaudeConfigDir(d)
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}

	add("") // the default dir

	if database, err := openTaskDB(db.DefaultPath()); err == nil {
		defer database.Close() //nolint:errcheck // read-only lookup
		if projects, err := database.ListProjects(); err == nil {
			for _, p := range projects {
				if strings.TrimSpace(p.ClaudeConfigDir) != "" {
					add(p.ClaudeConfigDir)
				}
			}
		}
	}

	sort.Strings(dirs[1:]) // keep the default first, order the rest stably
	return dirs
}

func printUsageTable(results []profileResult) {
	for _, r := range results {
		fmt.Println(boldStyle.Render(r.ConfigDir))
		if r.Error != "" {
			fmt.Println("  " + errorStyle.Render(r.Error))
			fmt.Println()
			continue
		}
		if r.Snapshot.Email != "" {
			fmt.Println("  " + dimStyle.Render(r.Snapshot.Email))
		}
		style := successStyle
		switch used := r.Snapshot.UsedPercent(); {
		case used >= 90:
			style = errorStyle
		case used >= 70:
			style = warnStyle
		}
		line := r.Snapshot.Describe()
		if r.Snapshot.Stale {
			line += fmt.Sprintf("  (cached %s ago; the usage API is unreachable)", r.Snapshot.Age().Round(time.Minute))
		}
		fmt.Println("  " + style.Render(line))
		for _, l := range r.Snapshot.Limits {
			line := fmt.Sprintf("    %-16s %3.0f%%", l.Kind, l.Percent)
			if l.Scope != "" {
				line += "  " + l.Scope
			}
			if l.ResetsAt != nil {
				line += "  resets " + l.ResetsAt.Local().Format("Mon 15:04")
			}
			fmt.Println(dimStyle.Render(line))
		}
		fmt.Println()
	}
}
