package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bborn/workflow/internal/db"
	"github.com/bborn/workflow/internal/hooks"
	"github.com/bborn/workflow/internal/registry"
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

// --- Plugin discovery and installation -------------------------------------
//
// The GUI's half of the plugin browser (parity with the TUI's `m` view and the
// `ty plugins` CLI). All three go through internal/hooks and internal/registry,
// so "installed" means the same thing everywhere.

// installedPluginJSON is one plugin on disk.
type installedPluginJSON struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Dir         string   `json:"dir"`
	Hooks       []string `json:"hooks,omitempty"`
	Actions     []string `json:"actions,omitempty"`
	Workflows   []string `json:"workflows,omitempty"`
	Routines    []string `json:"routines,omitempty"`
	Services    []string `json:"services,omitempty"`
	// SourceID is the catalog entry it was installed from, when known.
	SourceID string `json:"source_id,omitempty"`
}

// catalogEntryJSON is one row of the plugin browser: a catalog entry, an
// installed plugin, or both.
type catalogEntryJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Author      string   `json:"author,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Provides    []string `json:"provides,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	Source      string   `json:"source,omitempty"`
	Subdir      string   `json:"subdir,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Installed   bool     `json:"installed"`
	// InCatalog is false for a plugin that is installed but uncatalogued — the
	// row is still shown so the browser can manage it.
	InCatalog bool `json:"in_catalog"`
}

// handleListPlugins lists the installed plugins and what each provides.
func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	dir := hooks.DefaultPluginsDir()
	plugins, _ := hooks.LoadPlugins(dir)
	sources := hooks.LoadSources(dir)

	out := []installedPluginJSON{}
	for _, p := range plugins {
		row := installedPluginJSON{
			Name:        p.Name,
			Version:     p.Version,
			Description: strings.Join(strings.Fields(p.Description), " "),
			Dir:         p.Dir,
			Workflows:   p.Workflows,
			Routines:    p.Routines,
			SourceID:    sources[filepath.Base(p.Dir)].ID,
		}
		for event := range p.Hooks {
			row.Hooks = append(row.Hooks, event)
		}
		sort.Strings(row.Hooks)
		for _, a := range p.Actions {
			row.Actions = append(row.Actions, a.DisplayLabel())
		}
		for _, svc := range p.Services {
			row.Services = append(row.Services, svc.Name)
		}
		out = append(out, row)
	}
	jsonOK(w, out)
}

// handlePluginCatalog returns the searchable catalog, marked up with what is
// already installed. `q` filters, `scope` narrows to installed/available, and
// `refresh=1` re-fetches instead of using the cached copy.
func (s *Server) handlePluginCatalog(w http.ResponseWriter, r *http.Request) {
	loader := registry.Default()
	ctx := r.Context()
	res := loader.Load(ctx)
	if r.URL.Query().Get("refresh") == "1" {
		res = loader.Refresh(ctx)
	}

	dir := hooks.DefaultPluginsDir()
	plugins, _ := hooks.LoadPlugins(dir)

	entries := res.Entries
	if q := r.URL.Query().Get("q"); strings.TrimSpace(q) != "" {
		entries = registry.Search(entries, q)
	}

	claimed := map[string]bool{}
	rows := []catalogEntryJSON{}
	for _, e := range entries {
		row := catalogEntryJSON{
			ID: e.ID, Name: e.DisplayName(), Description: strings.Join(strings.Fields(e.Description), " "),
			Author: e.Author, Category: e.Category, Tags: e.Tags, Provides: e.Provides,
			Requires: e.Requires, Source: e.Source, Subdir: e.Subdir, Homepage: e.Homepage,
			InCatalog: true,
		}
		if installedDir, ok := hooks.IsInstalled(dir, e.ID, plugins); ok {
			row.Installed = true
			claimed[strings.ToLower(installedDir)] = true
		}
		rows = append(rows, row)
	}
	// Installed plugins no catalog entry claims still belong in the list.
	query := r.URL.Query().Get("q")
	for _, p := range plugins {
		if claimed[strings.ToLower(filepath.Base(p.Dir))] {
			continue
		}
		if strings.TrimSpace(query) != "" && !matchesPluginQuery(p, query) {
			continue
		}
		rows = append(rows, catalogEntryJSON{
			ID: p.Name, Name: p.Name, Description: strings.Join(strings.Fields(p.Description), " "),
			Category: "local", Installed: true, InCatalog: false, Source: p.Dir,
		})
	}

	switch r.URL.Query().Get("scope") {
	case "installed":
		rows = filterCatalogRows(rows, func(row catalogEntryJSON) bool { return row.Installed })
	case "available":
		rows = filterCatalogRows(rows, func(row catalogEntryJSON) bool { return !row.Installed })
	}

	jsonOK(w, map[string]any{
		"plugins": rows,
		// stale says the catalog came from the cache or the bundled snapshot, so
		// the GUI can offer a refresh instead of quietly showing old data.
		"stale": res.Stale(),
	})
}

func filterCatalogRows(rows []catalogEntryJSON, keep func(catalogEntryJSON) bool) []catalogEntryJSON {
	out := []catalogEntryJSON{}
	for _, row := range rows {
		if keep(row) {
			out = append(out, row)
		}
	}
	return out
}

// matchesPluginQuery is the substring fallback for an uncatalogued plugin, which
// has no entry for the catalog scorer to rank.
func matchesPluginQuery(p hooks.Plugin, query string) bool {
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(p.Name), q) ||
		strings.Contains(strings.ToLower(p.Description), q)
}

// handleInstallPlugin installs a plugin by catalog ID, git URL, or local path —
// the same resolution the CLI does, so `{"id":"slack"}` works here too.
func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Subdir string `json:"subdir"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	arg := req.ID
	if arg == "" {
		arg = req.Source
	}
	if arg == "" {
		jsonErr(w, "id or source is required", http.StatusBadRequest)
		return
	}

	// Bounded below the server's write timeout, so a slow clone comes back as an
	// error the GUI can show rather than a dropped connection.
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()

	entries := registry.Default().Load(ctx).Entries
	install, err := hooks.Resolve(ctx, arg, entries)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Subdir != "" {
		install.Subdir = req.Subdir
	}
	if req.Name != "" {
		install.Name = req.Name
	}

	res, err := hooks.Install(ctx, hooks.DefaultPluginsDir(), install)
	if err != nil {
		jsonOK(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	jsonOK(w, map[string]any{
		"ok": true, "updated": res.Updated, "dir": res.Dir, "plugins": res.Plugins,
	})
}

// handleRemovePlugin uninstalls a plugin by name.
func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		jsonErr(w, "name is required", http.StatusBadRequest)
		return
	}
	dir, inCheckout, err := hooks.Remove(req.Name, hooks.DefaultPluginsDir())
	if err != nil {
		jsonOK(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	jsonOK(w, map[string]any{"ok": true, "dir": dir, "in_collection_checkout": inCheckout})
}
