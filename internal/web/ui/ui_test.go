package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_PlaceholderWithoutEmbeddedUI(t *testing.T) {
	if Available() {
		t.Skip("built with the ui tag; placeholder path not active")
	}
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "doesn't include the web UI") {
		t.Errorf("expected pointer page, got: %.120s", w.Body.String())
	}
}
