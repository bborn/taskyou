package web

import (
	"encoding/json"
	"net/http"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
)

// pluginActionJSON is one user-invocable plugin action.
type pluginActionJSON struct {
	Plugin  string `json:"plugin"`
	ID      string `json:"id"`
	Label   string `json:"label"`
	Command string `json:"command"`
}

// handleListPluginActions lists every installed plugin action. This is the GUI
// analog of the TUI action picker and `ty plugins list`.
func (s *Server) handleListPluginActions(w http.ResponseWriter, r *http.Request) {
	plugins, _ := hooks.LoadPlugins(hooks.DefaultPluginsDir())
	out := []pluginActionJSON{}
	for _, p := range plugins {
		for _, a := range p.Actions {
			out = append(out, pluginActionJSON{
				Plugin:  p.Name,
				ID:      a.ID,
				Label:   a.DisplayLabel(),
				Command: a.Command,
			})
		}
	}
	jsonOK(w, out)
}

// handleRunPluginAction runs a plugin action, optionally against a task. Same
// runner as the CLI (`ty plugins run`) and the TUI picker.
func (s *Server) handleRunPluginAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Plugin string `json:"plugin"`
		Action string `json:"action"`
		TaskID int64  `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Plugin == "" || req.Action == "" {
		jsonErr(w, "plugin and action are required", http.StatusBadRequest)
		return
	}

	plugins, _ := hooks.LoadPlugins(hooks.DefaultPluginsDir())
	plugin, action, err := hooks.FindAction(plugins, req.Plugin, req.Action)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusNotFound)
		return
	}

	var task *db.Task
	if req.TaskID != 0 {
		task, err = s.db.GetTask(req.TaskID)
		if err != nil {
			jsonErr(w, "database error", http.StatusInternalServerError)
			return
		}
		if task == nil {
			jsonErr(w, "task not found", http.StatusNotFound)
			return
		}
	}

	out, runErr := hooks.RunAction(r.Context(), plugin, action, task)
	resp := map[string]any{"output": string(out), "ok": runErr == nil}
	if runErr != nil {
		resp["error"] = runErr.Error()
	}
	jsonOK(w, resp)
}
