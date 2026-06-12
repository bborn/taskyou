package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleTerminal provides a WebSocket that bridges xterm.js to a tmux pane.
// Output: polls tmux capture-pane with ANSI escapes and sends screen updates.
// Input: forwards keyboard data via tmux send-keys.
// The xterm.js terminal is sized to match the tmux pane (read-only size),
// so the rendering matches what the TUI shows.
//
// By default the executor (Claude) pane is mirrored; ?pane=shell mirrors the
// task's workdir shell pane instead (the GUI's Shell tab).
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid task id", http.StatusBadRequest)
		return
	}

	task, err := s.db.GetTask(id)
	if err != nil || task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	paneID := task.ClaudePaneID
	if r.URL.Query().Get("pane") == "shell" {
		paneID = task.ShellPaneID
		if paneID == "" {
			http.Error(w, "task has no shell pane", http.StatusBadRequest)
			return
		}
	}
	if paneID == "" {
		http.Error(w, "task has no executor pane", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	defer conn.Close()

	// Get the tmux pane's actual dimensions and send to client
	cols, rows := getPaneSize(paneID)
	sizeMsg, _ := json.Marshal(map[string]interface{}{
		"type": "size",
		"cols": cols,
		"rows": rows,
	})
	conn.WriteMessage(websocket.TextMessage, sizeMsg)

	// Send initial full capture
	output, err := capturePane(paneID)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("Error: could not read executor pane\r\n"))
		return
	}
	conn.WriteMessage(websocket.TextMessage, []byte(output))

	done := make(chan struct{})

	// Read input from WebSocket → send to tmux
	go func() {
		defer close(done)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if len(msg) == 0 {
				continue
			}

			// Handle resize — resize the executor tmux pane to match browser terminal
			if msg[0] == '{' {
				var resizeMsg struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(msg, &resizeMsg) == nil && resizeMsg.Type == "resize" {
					if resizeMsg.Cols > 0 && resizeMsg.Rows > 0 {
						exec.Command("tmux", "resize-pane", "-t", paneID,
							"-x", strconv.Itoa(resizeMsg.Cols),
							"-y", strconv.Itoa(resizeMsg.Rows)).Run()
					}
					continue
				}
			}

			// Send raw input bytes to tmux using send-keys with literal flag
			input := string(msg)
			exec.Command("tmux", "send-keys", "-t", paneID, "-l", input).Run()
		}
	}()

	// Poll capture-pane and send updates to WebSocket
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	lastOutput := output
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			current, err := capturePane(paneID)
			if err != nil {
				continue
			}
			if current != lastOutput {
				// Clear screen and rewrite
				conn.WriteMessage(websocket.TextMessage, []byte("\033[2J\033[H"+current))
				lastOutput = current
			}
		}
	}
}

// capturePane runs tmux capture-pane and returns the visible pane content
// with trailing whitespace stripped from each line so it renders correctly
// in a browser terminal that may be a different width than the tmux pane.
func capturePane(paneID string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", paneID, "-p", "-e")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("capture-pane: %w", err)
	}
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	// Trim trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\r\n"), nil
}

// getPaneSize returns the width and height of a tmux pane.
func getPaneSize(paneID string) (int, int) {
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_width} #{pane_height}").Output()
	if err != nil {
		return 120, 40
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 120, 40
	}
	cols, err1 := strconv.Atoi(parts[0])
	rows, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 120, 40
	}
	return cols, rows
}
