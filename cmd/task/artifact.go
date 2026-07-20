package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/pipeline"
)

// Workflow phases hand documents to each other through the pipeline artifact
// store. That store was reachable only through the taskyou_* MCP tools, so any
// break in the MCP transport (a stale binary launched without --mcp-config, a
// worktree project-key mismatch) left an executor with no sanctioned way to
// finish its phase — agents resorted to hand-writing pipeline_artifacts rows
// with sqlite3. These commands expose the same store over the CLI, which is
// always on PATH and needs no session wiring, so a phase can always complete.
//
// Semantics deliberately mirror the MCP tools: the branch key is derived from
// the task itself via pipeline.GroupKey (never client-supplied), and a missing
// artifact reads as empty rather than erroring.

// resolveArtifactTask loads the task an artifact command applies to, resolving
// the ID from --task-id or the WORKTREE_TASK_ID env var that ty writes into
// every worktree's .envrc, and returns it with its workflow branch key.
func resolveArtifactTask(cmd *cobra.Command, database *db.DB) (*db.Task, string, error) {
	taskID, _ := cmd.Flags().GetInt64("task-id")
	if taskID == 0 {
		if s := strings.TrimSpace(os.Getenv("WORKTREE_TASK_ID")); s != "" {
			parsed, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, "", fmt.Errorf("invalid WORKTREE_TASK_ID: %s", s)
			}
			taskID = parsed
		}
	}
	if taskID == 0 {
		return nil, "", fmt.Errorf("task-id is required (via --task-id flag or WORKTREE_TASK_ID env)")
	}

	task, err := database.GetTask(taskID)
	if err != nil || task == nil {
		return nil, "", fmt.Errorf("task #%d not found", taskID)
	}

	branch := pipeline.GroupKey(task)
	if branch == "" {
		return nil, "", fmt.Errorf("task #%d is not part of a workflow — artifacts are only available inside a pipeline", taskID)
	}
	return task, branch, nil
}

// readArtifactContent takes the content for `artifact set` from --content, from
// --file, or from stdin. Stdin is the default so an agent can pipe a whole
// document without hitting shell argument limits or quoting problems.
func readArtifactContent(cmd *cobra.Command) (string, error) {
	if content, _ := cmd.Flags().GetString("content"); content != "" {
		return content, nil
	}
	if path, _ := cmd.Flags().GetString("file"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		return string(data), nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(data), nil
}

func newArtifactCmd() *cobra.Command {
	artifactCmd := &cobra.Command{
		Use:   "artifact",
		Short: "Read and write workflow phase documents (pipeline artifacts)",
		Long: "Read and write the documents workflow phases hand to each other.\n\n" +
			"This is the CLI equivalent of the taskyou_get_artifact / taskyou_set_artifact\n" +
			"MCP tools, for use when the MCP server is unavailable. The artifact is scoped\n" +
			"to the workflow branch of the task, resolved from --task-id or WORKTREE_TASK_ID.",
	}

	artifactGetCmd := &cobra.Command{
		Use:   "get [name]",
		Short: "Read a workflow artifact (omit name to list all with contents)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			_, branch, err := resolveArtifactTask(cmd, database)
			if err != nil {
				return err
			}

			if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
				name := strings.TrimSpace(args[0])
				content, err := database.GetPipelineArtifact(branch, name)
				if err != nil {
					return err
				}
				if content == "" {
					return fmt.Errorf("no artifact named %q has been produced by this workflow yet", name)
				}
				fmt.Print(content)
				if !strings.HasSuffix(content, "\n") {
					fmt.Println()
				}
				return nil
			}

			artifacts, err := database.ListPipelineArtifacts(branch)
			if err != nil {
				return err
			}
			if len(artifacts) == 0 {
				return fmt.Errorf("no artifacts have been produced by this workflow yet")
			}
			for i, a := range artifacts {
				if i > 0 {
					fmt.Println()
				}
				fmt.Printf("## Artifact: %s\n\n%s\n", a.Name, a.Content)
			}
			return nil
		},
	}

	artifactSetCmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Save a workflow artifact (content from --content, --file, or stdin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("artifact name is required")
			}

			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			task, branch, err := resolveArtifactTask(cmd, database)
			if err != nil {
				return err
			}

			content, err := readArtifactContent(cmd)
			if err != nil {
				return err
			}
			if strings.TrimSpace(content) == "" {
				return fmt.Errorf("content is required (pass --content, --file, or pipe to stdin)")
			}

			if err := database.SetPipelineArtifact(branch, name, content); err != nil {
				return err
			}
			// Mirror the MCP tool's log line so the activity feed reads the same
			// whichever transport the phase used.
			database.AppendTaskLog(task.ID, "system", fmt.Sprintf("Workflow artifact '%s' saved (%d bytes)", name, len(content)))
			fmt.Printf("Artifact '%s' saved (%d bytes). Later phases can read it with: ty artifact get %s\n", name, len(content), name)
			return nil
		},
	}

	artifactListCmd := &cobra.Command{
		Use:   "list",
		Short: "List workflow artifact names and sizes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openTaskDB(db.DefaultPath())
			if err != nil {
				return err
			}
			defer database.Close()

			_, branch, err := resolveArtifactTask(cmd, database)
			if err != nil {
				return err
			}
			artifacts, err := database.ListPipelineArtifacts(branch)
			if err != nil {
				return err
			}
			if len(artifacts) == 0 {
				fmt.Println("No artifacts have been produced by this workflow yet.")
				return nil
			}
			for _, a := range artifacts {
				fmt.Printf("%-24s %8d bytes  %s\n", a.Name, len(a.Content), a.UpdatedAt.Format("2006-01-02 15:04:05"))
			}
			return nil
		},
	}

	for _, c := range []*cobra.Command{artifactGetCmd, artifactSetCmd, artifactListCmd} {
		c.Flags().Int64("task-id", 0, "Task ID (defaults to WORKTREE_TASK_ID)")
	}
	artifactSetCmd.Flags().String("content", "", "Artifact content (otherwise --file or stdin)")
	artifactSetCmd.Flags().String("file", "", "Read artifact content from this file")

	artifactCmd.AddCommand(artifactGetCmd, artifactSetCmd, artifactListCmd)
	return artifactCmd
}
