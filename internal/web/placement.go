package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/bborn/workflow/internal/executor"
)

func (s *Server) handleGetPlacement(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	placement, err := s.db.GetTaskPlacementDecision(task.ID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	health, err := s.db.RemoteHostHealth(placement.Target)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	remoteDir, _, err := s.db.GetTaskRemoteWorktree(task.ID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"target": placement.Target, "workdir": placement.WorkDir, "reason": placement.Reason, "decided": placement.Decided, "health": health, "remote_worktree": remoteDir})
}

func (s *Server) handleSetPlacement(w http.ResponseWriter, r *http.Request) {
	task, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	var req struct {
		Target  string `json:"target"`
		WorkDir string `json:"workdir"`
		Force   bool   `json:"force"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		jsonErr(w, "invalid placement request", http.StatusBadRequest)
		return
	}
	// A handoff can take 90 seconds; extend this response only.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(4 * time.Minute))
	result, err := executor.PlaceTask(r.Context(), s.db, task.ID, req.Target, req.WorkDir, req.Force)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusConflict)
		return
	}
	jsonOK(w, result)
}
