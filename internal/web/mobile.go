package web

import (
	_ "embed"
	"net/http"
)

// mobileHTML is the self-contained mobile console. Unlike the React UI (which is
// only embedded behind a build tag + Node build), this page is always available
// from a plain `go build`, so `ty serve` over Tailscale gives you a usable phone
// interface with zero extra steps. It is pure HTML/CSS/vanilla-JS and drives the
// existing JSON API.
//
//go:embed mobile.html
var mobileHTML []byte

// handleMobile serves the mobile console at /m.
func (s *Server) handleMobile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(mobileHTML)
}
