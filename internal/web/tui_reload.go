package web

import (
	"github.com/bborn/workflow/internal/tuireload"
	"net/http"
)

func (s *Server) handleTUIReload(w http.ResponseWriter, r *http.Request) {
	if err := tuireload.Request(s.db); err != nil {
		http.Error(w, "could not request TUI reload", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
