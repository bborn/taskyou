package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleMobileServesConsole(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/m", nil)
	rec := httptest.NewRecorder()

	s.handleMobile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"<title>TaskYou", "/api/board", "/api/tasks/", "Needs you"} {
		if !strings.Contains(body, want) {
			t.Errorf("mobile console missing %q", want)
		}
	}
}

func TestMobileRouteRegistered(t *testing.T) {
	s := New(Config{Addr: ":0"})
	srv := httptest.NewServer(s.srv.Handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/m")
	if err != nil {
		t.Fatalf("GET /m: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /m status = %d, want 200", resp.StatusCode)
	}
}
