package web

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bborn/workflow/internal/db"
)

type boardStreamRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *boardStreamRecorder) Flush() {
	w.ResponseRecorder.Flush()
	w.cancel()
}

func TestBoardStreamSignalPreservesDefaultSnapshot(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.CreateTask(&db.Task{Title: "Board snapshot task", Status: db.StatusBacklog}); err != nil {
		t.Fatal(err)
	}
	for _, signal := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		url := "/api/board/stream"
		if signal {
			url += "?signal=true"
		}
		w := &boardStreamRecorder{httptest.NewRecorder(), cancel}
		(&Server{db: database}).handleBoardStream(w, httptest.NewRequest("GET", url, nil).WithContext(ctx))
		cancel()
		if signal {
			if w.Body.String() != "event: board\ndata: {}\n\n" {
				t.Fatalf("unexpected signal: %s", w.Body.String())
			}
		} else if !strings.Contains(w.Body.String(), "Board snapshot task") {
			t.Fatal("default stream lost task snapshot")
		}
	}
}
