package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/taskfilter"
)

// Saved views over HTTP. The GUI (and any script) reads the same rows the TUI
// picker and `ty views` manage, and resolves them with the same matcher, so a
// view named "Active" cannot come to mean different things on different
// surfaces. See AGENTS.md: a feature isn't done until its logic lives below
// internal/ui and is reachable through this API.

type saveViewRequest struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

func (s *Server) handleListViews(w http.ResponseWriter, r *http.Request) {
	views, err := s.db.ListSavedViews()
	if err != nil {
		jsonErr(w, "failed to list views", http.StatusInternalServerError)
		return
	}
	if views == nil {
		views = []*db.SavedView{}
	}
	jsonOK(w, views)
}

func (s *Server) handleCreateView(w http.ResponseWriter, r *http.Request) {
	var req saveViewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := db.ValidateViewName(req.Name); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	view, err := s.db.SaveView(req.Name, req.Query)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, view)
}

// handleGetView returns a view and the tasks it currently matches, so a client
// can render the view without reimplementing the query grammar.
func (s *Server) handleGetView(w http.ResponseWriter, r *http.Request) {
	view, ok := s.requireView(w, r)
	if !ok {
		return
	}

	tasks, err := s.db.ListTasks(db.ListTasksOptions{IncludeClosed: true})
	if err != nil {
		jsonErr(w, "failed to list tasks", http.StatusInternalServerError)
		return
	}
	matched := s.parseViewQuery(view.Query).Filter(tasks)

	jsonOK(w, map[string]interface{}{
		"name":       view.Name,
		"query":      view.Query,
		"task_count": len(matched),
		"tasks":      toTaskJSONSlice(matched),
	})
}

func (s *Server) handleUpdateView(w http.ResponseWriter, r *http.Request) {
	view, ok := s.requireView(w, r)
	if !ok {
		return
	}

	var req saveViewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// Renaming is a save under the new name; omitting the name edits in place.
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = view.Name
	}
	if name != view.Name {
		if err := s.db.DeleteSavedView(view.Name); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	updated, err := s.db.SaveView(name, req.Query)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, updated)
}

func (s *Server) handleDeleteView(w http.ResponseWriter, r *http.Request) {
	view, ok := s.requireView(w, r)
	if !ok {
		return
	}
	if err := s.db.DeleteSavedView(view.Name); err != nil {
		jsonErr(w, "failed to delete view", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// requireView resolves the {name} path value, writing a 404 when it is unknown.
func (s *Server) requireView(w http.ResponseWriter, r *http.Request) (*db.SavedView, bool) {
	view, err := s.db.GetSavedView(r.PathValue("name"))
	if err != nil {
		jsonErr(w, "failed to load view", http.StatusInternalServerError)
		return nil, false
	}
	if view == nil {
		jsonErr(w, "view not found", http.StatusNotFound)
		return nil, false
	}
	return view, true
}

// parseViewQuery parses a filter query, resolving [project] tags through the
// projects table so aliases behave the same as they do on the board.
func (s *Server) parseViewQuery(query string) taskfilter.Query {
	return taskfilter.Parse(query).ResolveProjects(func(name string) string {
		if p, err := s.db.GetProjectByName(name); err == nil && p != nil {
			return p.Name
		}
		return ""
	})
}
