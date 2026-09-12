package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bborn/workflow/internal/db"
)

func (s *Server) handleTaskStream(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid task id"}`, http.StatusBadRequest)
		return
	}

	sinceID := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			sinceID = n
		}
	}

	// EventSource sends this on reconnect, advancing beyond the initial URL.
	if last, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && last > sinceID {
		sinceID = last
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case <-ticker.C:
			logs, err := s.db.GetTaskLogsSinceLimit(id, sinceID, 500)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":\"db error\"}\n\n")
				flusher.Flush()
				return
			}
			for _, l := range logs {
				entry := logJSON{
					ID:        l.ID,
					LineType:  l.LineType,
					Content:   l.Content,
					CreatedAt: apiTime(l.CreatedAt.Time),
				}
				data, _ := json.Marshal(entry)
				fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", l.ID, data)
				sinceID = l.ID
			}
			if len(logs) > 0 {
				flusher.Flush()
			}
		}
	}
}

// handleBoardStream sends SSE events when the board changes.
// It polls the event_log table for new events and pushes a full board
// snapshot whenever task state changes. This replaces client-side polling.
func (s *Server) handleBoardStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Consumers needing their own richer payload can request a cheap change
	// signal rather than make us build a snapshot they immediately discard.
	signalOnly := r.URL.Query().Get("signal") == "true"
	send := func() {
		if signalOnly {
			fmt.Fprint(w, "event: board\ndata: {}\n\n")
			flusher.Flush()
		} else {
			s.sendBoardEvent(w, flusher)
		}
	}
	// Read the cursor BEFORE the snapshot. A mutation during serialization
	// must still be observed by the next poll.
	var lastEventID int64
	row := s.db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM event_log")
	row.Scan(&lastEventID)
	send()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case <-ticker.C:
			// Check for new events since last seen
			var maxID int64
			row := s.db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM event_log")
			if err := row.Scan(&maxID); err != nil {
				continue
			}
			if maxID > lastEventID {
				lastEventID = maxID
				send()
			}
		}
	}
}

func (s *Server) sendBoardEvent(w http.ResponseWriter, flusher http.Flusher) {
	tasks, err := s.db.ListTasks(db.ListTasksOptions{IncludeClosed: true, Limit: 500})
	if err != nil {
		return
	}
	snapshot := BuildBoardSnapshot(tasks, 50)
	data, _ := json.Marshal(snapshot)
	fmt.Fprintf(w, "event: board\ndata: %s\n\n", data)
	flusher.Flush()
}
