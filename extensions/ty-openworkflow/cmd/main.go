// ty-openworkflow: A sidecar for spawning workflows on ephemeral compute platforms.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bborn/workflow/extensions/ty-openworkflow/internal/bridge"
	"github.com/bborn/workflow/extensions/ty-openworkflow/internal/compute"
	"github.com/bborn/workflow/extensions/ty-openworkflow/internal/config"
	"github.com/bborn/workflow/extensions/ty-openworkflow/internal/runner"
	"github.com/bborn/workflow/extensions/ty-openworkflow/internal/state"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	cfg     *config.Config
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "ty-openworkflow",
		Short: "Spawn workflows on ephemeral compute platforms",
		Long: `ty-openworkflow is a sidecar for TaskYou that enables spawning
workflows on ephemeral compute platforms like Cloudflare Workers,
Docker containers, or local processes.

It follows the OpenWorkflow architecture pattern for durable,
fault-tolerant workflow execution with deterministic replay.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var err error
			path := cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			cfg, err = config.Load(path)
			return err
		},
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	// Add commands
	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(deployCmd())
	rootCmd.AddCommand(startCmd())
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(listCmd())
	rootCmd.AddCommand(cancelCmd())
	rootCmd.AddCommand(initCmd())
	rootCmd.AddCommand(adaptersCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serveCmd runs the webhook server and background polling.
func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the webhook server and polling",
		Long:  `Starts a webhook server to receive workflow completion callbacks and polls for run status updates.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Handle signals
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Println("\nShutting down...")
				cancel()
			}()

			// Initialize components
			r, err := initRunner()
			if err != nil {
				return err
			}

			// Start polling in background
			go r.StartPolling(ctx)

			// Start webhook server if enabled
			if cfg.Webhook.Enabled {
				mux := http.NewServeMux()
				mux.HandleFunc(cfg.Webhook.Path, func(w http.ResponseWriter, req *http.Request) {
					handleWebhook(r, w, req)
				})

				addr := fmt.Sprintf("%s:%d", cfg.Webhook.Host, cfg.Webhook.Port)
				server := &http.Server{Addr: addr, Handler: mux}

				go func() {
					fmt.Printf("Webhook server listening on %s\n", addr)
					if err := server.ListenAndServe(); err != http.ErrServerClosed {
						fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
					}
				}()

				<-ctx.Done()
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				server.Shutdown(shutdownCtx)
			} else {
				fmt.Println("Polling for workflow status updates...")
				<-ctx.Done()
			}

			return nil
		},
	}
}

func handleWebhook(r *runner.Runner, w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var payload struct {
		RunID  string         `json:"runId"`
		Status string         `json:"status"`
		Output map[string]any `json:"output"`
		Error  string         `json:"error"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := r.HandleWebhook(payload.RunID, payload.Status, payload.Output, payload.Error); err != nil {
		fmt.Fprintf(os.Stderr, "Webhook error: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true}`))
}

// deployCmd deploys a workflow.
func deployCmd() *cobra.Command {
	var (
		name        string
		description string
		version     string
		runtime     string
		adapter     string
		codeFile    string
	)

	cmd := &cobra.Command{
		Use:   "deploy <workflow-id>",
		Short: "Deploy a workflow to a compute platform",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workflowID := args[0]

			// Read workflow code
			var code string
			if codeFile != "" {
				data, err := os.ReadFile(codeFile)
				if err != nil {
					return fmt.Errorf("read code file: %w", err)
				}
				code = string(data)
			} else {
				// Read from stdin
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				code = string(data)
			}

			if code == "" {
				return fmt.Errorf("workflow code is required (use -f or pipe to stdin)")
			}

			if adapter == "" {
				adapter = cfg.DefaultAdapter
			}

			workflow := &compute.WorkflowDefinition{
				ID:          workflowID,
				Name:        name,
				Description: description,
				Version:     version,
				Runtime:     runtime,
				Code:        code,
			}

			if workflow.Name == "" {
				workflow.Name = workflowID
			}
			if workflow.Version == "" {
				workflow.Version = "1.0.0"
			}
			if workflow.Runtime == "" {
				workflow.Runtime = "node"
			}

			r, err := initRunner()
			if err != nil {
				return err
			}

			if err := r.DeployWorkflow(context.Background(), workflow, adapter); err != nil {
				return err
			}

			fmt.Printf("Deployed workflow %s to %s\n", workflowID, adapter)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "workflow name")
	cmd.Flags().StringVar(&description, "description", "", "workflow description")
	cmd.Flags().StringVar(&version, "version", "1.0.0", "workflow version")
	cmd.Flags().StringVar(&runtime, "runtime", "node", "workflow runtime (node, python)")
	cmd.Flags().StringVarP(&adapter, "adapter", "a", "", "compute adapter (exec, docker, cloudflare)")
	cmd.Flags().StringVarP(&codeFile, "file", "f", "", "workflow code file")

	return cmd
}

// startCmd starts a workflow run.
func startCmd() *cobra.Command {
	var (
		inputJSON   string
		inputFile   string
		taskTitle   string
		createTask  bool
	)

	cmd := &cobra.Command{
		Use:   "start <workflow-id>",
		Short: "Start a workflow run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workflowID := args[0]

			// Parse input
			var input map[string]any
			if inputFile != "" {
				data, err := os.ReadFile(inputFile)
				if err != nil {
					return fmt.Errorf("read input file: %w", err)
				}
				if err := json.Unmarshal(data, &input); err != nil {
					return fmt.Errorf("parse input: %w", err)
				}
			} else if inputJSON != "" {
				if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
					return fmt.Errorf("parse input: %w", err)
				}
			} else {
				input = make(map[string]any)
			}

			r, err := initRunner()
			if err != nil {
				return err
			}

			ctx := context.Background()

			if createTask || cfg.TaskYou.AutoCreateTasks {
				if taskTitle == "" {
					taskTitle = fmt.Sprintf("Workflow: %s", workflowID)
				}

				run, task, err := r.StartWorkflowWithTask(ctx, workflowID, input, taskTitle)
				if err != nil {
					return err
				}

				fmt.Printf("Started workflow run %s (task #%d)\n", run.ID, task.ID)
				fmt.Printf("Status: %s\n", run.Status)
			} else {
				run, err := r.StartWorkflow(ctx, workflowID, input, 0)
				if err != nil {
					return err
				}

				fmt.Printf("Started workflow run %s\n", run.ID)
				fmt.Printf("Status: %s\n", run.Status)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&inputJSON, "input", "i", "", "input JSON")
	cmd.Flags().StringVar(&inputFile, "input-file", "", "input JSON file")
	cmd.Flags().StringVarP(&taskTitle, "task", "t", "", "task title (creates linked task)")
	cmd.Flags().BoolVar(&createTask, "create-task", false, "create a linked task")

	return cmd
}

// statusCmd shows the status of a workflow run.
func statusCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status <run-id>",
		Short: "Show workflow run status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]

			r, err := initRunner()
			if err != nil {
				return err
			}

			run, err := r.GetRunStatus(context.Background(), runID)
			if err != nil {
				return err
			}

			if jsonOutput {
				data, _ := json.MarshalIndent(run, "", "  ")
				fmt.Println(string(data))
			} else {
				fmt.Printf("Run ID:     %s\n", run.ID)
				fmt.Printf("Workflow:   %s\n", run.WorkflowID)
				fmt.Printf("Status:     %s\n", run.Status)
				fmt.Printf("Started:    %s\n", run.StartedAt.Format(time.RFC3339))
				if run.CompletedAt != nil {
					fmt.Printf("Completed:  %s\n", run.CompletedAt.Format(time.RFC3339))
				}
				if run.Error != "" {
					fmt.Printf("Error:      %s\n", run.Error)
				}
				if run.Output != nil {
					output, _ := json.MarshalIndent(run.Output, "", "  ")
					fmt.Printf("Output:\n%s\n", output)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")

	return cmd
}

// listCmd lists workflows or runs.
func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workflows or runs",
	}

	// List workflows
	workflowsCmd := &cobra.Command{
		Use:   "workflows",
		Short: "List deployed workflows",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := initRunner()
			if err != nil {
				return err
			}

			workflows, err := r.ListWorkflows()
			if err != nil {
				return err
			}

			if len(workflows) == 0 {
				fmt.Println("No workflows deployed")
				return nil
			}

			fmt.Printf("%-20s %-15s %-10s %-10s %s\n", "ID", "NAME", "VERSION", "ADAPTER", "UPDATED")
			for _, w := range workflows {
				fmt.Printf("%-20s %-15s %-10s %-10s %s\n",
					w.ID, w.Name, w.Version, w.Adapter, w.UpdatedAt.Format("2006-01-02 15:04"))
			}

			return nil
		},
	}

	// List runs
	var (
		workflowID string
		runStatus  string
		limit      int
	)

	runsCmd := &cobra.Command{
		Use:   "runs",
		Short: "List workflow runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := initRunner()
			if err != nil {
				return err
			}

			runs, err := r.ListRuns(workflowID, runStatus, limit)
			if err != nil {
				return err
			}

			if len(runs) == 0 {
				fmt.Println("No runs found")
				return nil
			}

			fmt.Printf("%-40s %-20s %-10s %-10s %s\n", "ID", "WORKFLOW", "STATUS", "TASK", "STARTED")
			for _, run := range runs {
				taskStr := "-"
				if run.TaskID > 0 {
					taskStr = fmt.Sprintf("#%d", run.TaskID)
				}
				fmt.Printf("%-40s %-20s %-10s %-10s %s\n",
					run.ID, run.WorkflowID, run.Status, taskStr, run.StartedAt.Format("2006-01-02 15:04"))
			}

			return nil
		},
	}

	runsCmd.Flags().StringVarP(&workflowID, "workflow", "w", "", "filter by workflow ID")
	runsCmd.Flags().StringVarP(&runStatus, "status", "s", "", "filter by status")
	runsCmd.Flags().IntVarP(&limit, "limit", "n", 20, "max results")

	cmd.AddCommand(workflowsCmd, runsCmd)
	return cmd
}

// cancelCmd cancels a workflow run.
func cancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Cancel a workflow run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID := args[0]

			r, err := initRunner()
			if err != nil {
				return err
			}

			if err := r.CancelRun(context.Background(), runID); err != nil {
				return err
			}

			fmt.Printf("Canceled run %s\n", runID)
			return nil
		},
	}
}

// initCmd initializes configuration.
func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := cfgFile
			if path == "" {
				path = config.ConfigPath()
			}

			// Check if config exists
			if _, err := os.Stat(path); err == nil {
				fmt.Printf("Config already exists at %s\n", path)
				return nil
			}

			// Create default config
			cfg := config.DefaultConfig()

			if err := config.Save(cfg, path); err != nil {
				return err
			}

			fmt.Printf("Created config at %s\n", path)
			fmt.Println("\nEdit this file to configure compute adapters.")
			return nil
		},
	}
}

// adaptersCmd shows available adapters.
func adaptersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "adapters",
		Short: "List available compute adapters",
		RunE: func(cmd *cobra.Command, args []string) error {
			factory := initAdapters()

			fmt.Println("Available adapters:")
			for _, adapter := range factory.All() {
				status := "not available"
				if adapter.IsAvailable() {
					status = "available"
				}
				fmt.Printf("  %-15s %s\n", adapter.Name(), status)
			}

			return nil
		},
	}
}

// initRunner creates and initializes all components.
func initRunner() (*runner.Runner, error) {
	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// Open state database
	stateDB, err := state.Open(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}

	// Initialize adapters
	factory := initAdapters()

	// Initialize bridge
	br := bridge.NewBridge(cfg.TaskYou.CLI, cfg.TaskYou.Project)

	// Create runner
	r := runner.NewRunner(factory, stateDB, br, runner.Config{
		WebhookURL:   cfg.WebhookURL(),
		PollInterval: cfg.PollInterval,
	})

	return r, nil
}

// initAdapters creates and registers compute adapters.
func initAdapters() *compute.Factory {
	factory := compute.NewFactory()

	// Register exec adapter
	if cfg.Adapters.Exec.Enabled {
		execAdapter := compute.NewExecAdapter(compute.ExecConfig{
			WorkDir: cfg.Adapters.Exec.WorkDir,
		})
		factory.Register(execAdapter)
	}

	// Register docker adapter
	if cfg.Adapters.Docker.Enabled {
		dockerAdapter := compute.NewDockerAdapter(compute.DockerConfig{
			WorkDir: cfg.Adapters.Docker.WorkDir,
			Network: cfg.Adapters.Docker.Network,
		})
		factory.Register(dockerAdapter)
	}

	// Register cloudflare adapter
	if cfg.Adapters.Cloudflare.Enabled {
		cfAdapter := compute.NewCloudflareAdapter(compute.CloudflareConfig{
			AccountID: cfg.Adapters.Cloudflare.AccountID,
			APIToken:  cfg.Adapters.Cloudflare.APIToken,
			Namespace: cfg.Adapters.Cloudflare.Namespace,
		})
		factory.Register(cfAdapter)
	}

	return factory
}
