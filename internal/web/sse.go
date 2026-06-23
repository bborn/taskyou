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

	// Track the newest activity-timeline event we've already streamed so we only
	// push new lifecycle events. Clients fetch the initial timeline via the REST
	// endpoint, so we start from the current max and stream forward from there.
	var lastTimelineID int64
	if entries, err := s.db.GetTaskTimeline(id, 1); err == nil && len(entries) > 0 {
		lastTimelineID = entries[len(entries)-1].ID
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case <-ticker.C:
			logs, err := s.db.GetTaskLogsSince(id, sinceID)
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
				fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
				sinceID = l.ID
			}
			if len(logs) > 0 {
				flusher.Flush()
			}

			// Stream any new activity-timeline events.
			lastTimelineID = s.streamTimelineSince(w, flusher, id, lastTimelineID)
		}
	}
}

// streamTimelineSince writes any timeline entries newer than lastID as SSE
// "timeline" events and returns the new high-water mark.
func (s *Server) streamTimelineSince(w http.ResponseWriter, flusher http.Flusher, taskID, lastID int64) int64 {
	entries, err := s.db.GetTaskTimeline(taskID, 200)
	if err != nil {
		return lastID
	}
	var wrote bool
	for _, e := range entries {
		if e.ID <= lastID {
			continue
		}
		data, _ := json.Marshal(toTimelineJSON(e))
		fmt.Fprintf(w, "event: timeline\ndata: %s\n\n", data)
		lastID = e.ID
		wrote = true
	}
	if wrote {
		flusher.Flush()
	}
	return lastID
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

	// Send initial board snapshot immediately
	s.sendBoardEvent(w, flusher)

	// Track last seen event ID
	var lastEventID int64
	row := s.db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM event_log")
	row.Scan(&lastEventID)

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
				s.sendBoardEvent(w, flusher)
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
