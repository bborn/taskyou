package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/panel"
	"github.com/bborn/workflow/internal/ui"
)

func newPanelCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "panel", Short: "Open, inspect, and close task workspace resources"}
	for _, verb := range []string{"list", "providers", "open", "close", "content"} {
		sub := &cobra.Command{Use: verb + " <task-id>", Args: cobra.MinimumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id")
			}
			d, err := db.Open(db.DefaultPath())
			if err != nil {
				return err
			}
			defer d.Close()
			s := panel.New(d)
			var result any
			switch c.Name() {
			case "list":
				result, err = s.List(id)
			case "providers":
				if _, err = s.List(id); err == nil {
					result = s.Providers()
				}
			case "open":
				resource := ""
				if len(args) > 2 {
					resource = args[2]
				}
				result, err = s.Open(id, args[1], resource)
			case "close":
				err = s.Close(id, args[1])
				result = map[string]bool{"closed": err == nil}
			case "content":
				result, err = s.Content(c.Context(), id, args[1])
			}
			if err != nil {
				return err
			}
			enc := json.NewEncoder(c.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}}
		switch verb {
		case "open":
			sub.Use = "open <task-id> <provider> [resource]"
			sub.Args = cobra.RangeArgs(2, 3)
		case "close", "content":
			sub.Use = verb + " <task-id> <panel-id>"
			sub.Args = cobra.ExactArgs(2)
		default:
			sub.Args = cobra.ExactArgs(1)
		}
		cmd.AddCommand(sub)
	}
	cmd.AddCommand(&cobra.Command{Use: "view <task-id>", Short: "Open the terminal workspace viewer", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return err
		}
		d, err := db.Open(db.DefaultPath())
		if err != nil {
			return err
		}
		defer d.Close()
		svc := panel.New(d)
		if _, err = svc.List(id); err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(c.Context())
		defer cancel()
		_, err = tea.NewProgram(ui.NewWorkspaceModel(ctx, svc, id), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
		return err
	}})
	return cmd
}
