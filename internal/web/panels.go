package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bborn/workflow/internal/panel"
)

func (s *Server) handlePanelProviders(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireTask(w, r); !ok {
		return
	}
	jsonOK(w, panel.New(s.db).Providers())
}
func (s *Server) handlePanels(w http.ResponseWriter, r *http.Request) {
	t, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	svc := panel.New(s.db)
	switch r.Method {
	case http.MethodGet:
		tabs, err := svc.List(t.ID)
		if err != nil {
			jsonErr(w, err.Error(), 500)
			return
		}
		jsonOK(w, tabs)
	case http.MethodPost:
		var req struct {
			ProviderID string `json:"provider_id"`
			Resource   string `json:"resource"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			jsonErr(w, "invalid panel request", 400)
			return
		}
		if dec.Decode(&struct{}{}) != io.EOF {
			jsonErr(w, "expected one panel request", 400)
			return
		}
		p, err := svc.Open(t.ID, req.ProviderID, req.Resource)
		if err != nil {
			jsonErr(w, err.Error(), 400)
			return
		}
		jsonOK(w, p)
	}
}
func (s *Server) handleClosePanel(w http.ResponseWriter, r *http.Request) {
	t, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	if err := panel.New(s.db).Close(t.ID, r.PathValue("panelID")); err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) handlePanelContent(w http.ResponseWriter, r *http.Request) {
	t, ok := s.requireTask(w, r)
	if !ok {
		return
	}
	c, err := panel.New(s.db).Content(r.Context(), t.ID, r.PathValue("panelID"))
	if err != nil {
		code := http.StatusUnprocessableEntity
		if errors.Is(err, panel.ErrNotFound) {
			code = 404
		}
		jsonErr(w, err.Error(), code)
		return
	}
	jsonOK(w, c)
}
