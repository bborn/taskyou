package webapi

import (
	"net/http"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// ProjectResponse represents a project in JSON responses.
type ProjectResponse struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Aliases      string    `json:"aliases"`
	Instructions string    `json:"instructions"`
	Color        string    `json:"color"`
	CreatedAt    time.Time `json:"created_at"`
}

func projectToResponse(p *db.Project) *ProjectResponse {
	return &ProjectResponse{
		ID:           p.ID,
		Name:         p.Name,
		Path:         p.Path,
		Aliases:      p.Aliases,
		Instructions: p.Instructions,
		Color:        p.Color,
		CreatedAt:    p.CreatedAt.Time,
	}
}

// handleListProjects handles GET /projects
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.ListProjects()
	if err != nil {
		s.logger.Error("list projects failed", "error", err)
		jsonError(w, "Failed to list projects", http.StatusInternalServerError)
		return
	}

	responses := make([]*ProjectResponse, len(projects))
	for i, p := range projects {
		responses[i] = projectToResponse(p)
	}

	jsonResponse(w, responses, http.StatusOK)
}

// CreateProjectRequest represents a request to create a project.
type CreateProjectRequest struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Aliases      string `json:"aliases,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	Color        string `json:"color,omitempty"`
}

// handleCreateProject handles POST /projects
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req CreateProjectRequest
	if err := parseJSON(r, &req); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		jsonError(w, "Name is required", http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		jsonError(w, "Path is required", http.StatusBadRequest)
		return
	}

	project := &db.Project{
		Name:         req.Name,
		Path:         req.Path,
		Aliases:      req.Aliases,
		Instructions: req.Instructions,
		Color:        req.Color,
	}

	if err := s.db.CreateProject(project); err != nil {
		s.logger.Error("create project failed", "error", err)
		jsonError(w, "Failed to create project", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, projectToResponse(project), http.StatusCreated)
}

// handleGetProject handles GET /projects/{id}
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	project, err := s.getProjectByID(id)
	if err != nil {
		s.logger.Error("get project failed", "error", err)
		jsonError(w, "Failed to get project", http.StatusInternalServerError)
		return
	}

	if project == nil {
		jsonError(w, "Project not found", http.StatusNotFound)
		return
	}

	jsonResponse(w, projectToResponse(project), http.StatusOK)
}

// getProjectByID retrieves a project by ID by scanning all projects.
func (s *Server) getProjectByID(id int64) (*db.Project, error) {
	projects, err := s.db.ListProjects()
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, nil
}

// UpdateProjectRequest represents a request to update a project.
type UpdateProjectRequest struct {
	Name         *string `json:"name,omitempty"`
	Path         *string `json:"path,omitempty"`
	Aliases      *string `json:"aliases,omitempty"`
	Instructions *string `json:"instructions,omitempty"`
	Color        *string `json:"color,omitempty"`
}

// handleUpdateProject handles PUT /projects/{id}
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	var req UpdateProjectRequest
	if err := parseJSON(r, &req); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	project, err := s.getProjectByID(id)
	if err != nil {
		s.logger.Error("get project failed", "error", err)
		jsonError(w, "Failed to get project", http.StatusInternalServerError)
		return
	}
	if project == nil {
		jsonError(w, "Project not found", http.StatusNotFound)
		return
	}

	// Apply updates
	if req.Name != nil {
		project.Name = *req.Name
	}
	if req.Path != nil {
		project.Path = *req.Path
	}
	if req.Aliases != nil {
		project.Aliases = *req.Aliases
	}
	if req.Instructions != nil {
		project.Instructions = *req.Instructions
	}
	if req.Color != nil {
		project.Color = *req.Color
	}

	if err := s.db.UpdateProject(project); err != nil {
		s.logger.Error("update project failed", "error", err)
		jsonError(w, "Failed to update project", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, projectToResponse(project), http.StatusOK)
}

// handleDeleteProject handles DELETE /projects/{id}
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id, err := getIDParam(r)
	if err != nil {
		jsonError(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteProject(id); err != nil {
		s.logger.Error("delete project failed", "error", err)
		jsonError(w, "Failed to delete project", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGetSettings handles GET /settings
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings := make(map[string]string)

	// Get common settings
	keys := []string{"theme", "pane_height", "default_project", "default_type"}
	for _, key := range keys {
		if value, err := s.db.GetSetting(key); err == nil {
			settings[key] = value
		}
	}

	jsonResponse(w, settings, http.StatusOK)
}

// handleUpdateSettings handles PUT /settings
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings map[string]string
	if err := parseJSON(r, &settings); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	for key, value := range settings {
		if err := s.db.SetSetting(key, value); err != nil {
			s.logger.Error("set setting failed", "key", key, "error", err)
		}
	}

	jsonResponse(w, map[string]bool{"success": true}, http.StatusOK)
}
