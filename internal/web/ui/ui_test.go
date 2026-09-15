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

// The pointer page is frequently the first thing a phone sees when someone
// hits `ty serve` from another device; without a viewport meta the browser
// lays it out at 980px and renders it at about a third size.
func TestHandler_PlaceholderIsMobileReady(t *testing.T) {
	if Available() {
		t.Skip("built with the ui tag; placeholder path not active")
	}
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)

	body := w.Body.String()
	for _, want := range []string{
		`name="viewport"`,
		"width=device-width",
		"initial-scale=1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("placeholder page is missing %q", want)
		}
	}
	if strings.Contains(body, "height:100vh") {
		t.Error("placeholder page uses 100vh; mobile browsers need dvh to avoid overflowing the visual viewport")
	}
}
