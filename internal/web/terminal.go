package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/bborn/workflow/internal/executor"
	"github.com/bborn/workflow/internal/tmuxctl"
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

	terminal := paneTerminal{ctx: r.Context(), runner: executor.LocalRunner{}}
	paneID := task.ClaudePaneID
	shell := r.URL.Query().Get("pane") == "shell"
	if shell {
		paneID = task.ShellPaneID
	}
	if task.PlacementTarget != "" && task.PlacementTarget != "local" {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		info := s.remoteTerminalInfo(ctx, task, false)
		cancel()
		if info.Error != "" {
			http.Error(w, info.Error, http.StatusConflict)
			return
		}
		paneID = info.ClaudePaneID
		if shell {
			paneID = info.ShellPaneID
		}
		terminal.runner = executor.RemoteRunner{Host: info.RemoteHost}
	}
	if paneID == "" {
		http.Error(w, "task has no requested terminal pane", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	defer conn.Close()

	// Get the tmux pane's actual dimensions and send to client
	cols, rows := terminal.getPaneSize(paneID)
	sizeMsg, _ := json.Marshal(map[string]interface{}{
		"type": "size",
		"cols": cols,
		"rows": rows,
	})
	conn.WriteMessage(websocket.TextMessage, sizeMsg)

	// Send initial full capture
	output, err := terminal.paneFrame(paneID)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("Error: could not read executor pane\r\n"))
		return
	}
	conn.WriteMessage(websocket.TextMessage, []byte("\033[2J\033[H"+output))

	done := make(chan struct{})
	// Buffered so the reader never blocks; coalesces bursts into one redraw.
	redraw := make(chan struct{}, 1)
	inputActivity := make(chan struct{}, 1)
	triggerRedraw := func() {
		select {
		case redraw <- struct{}{}:
		default:
		}
	}

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
						if err := terminal.run("resize-pane", "-t", paneID,
							"-x", strconv.Itoa(resizeMsg.Cols),
							"-y", strconv.Itoa(resizeMsg.Rows)); err != nil {
							return
						}
						// Push a fresh frame at the new size instead of waiting up
						// to a full tick — otherwise the client renders the pane's
						// old (wider) width and the content wraps/gaps until the
						// next poll catches up.
						triggerRedraw()
					}
					continue
				}
			}

			// Send raw input bytes to tmux using send-keys with literal flag
			input := string(msg)
			if err := terminal.run("send-keys", "-t", paneID, "-l", input); err != nil {
				return
			}
			select {
			case inputActivity <- struct{}{}:
			default:
			}
		}
	}()

	// Poll capture-pane and send updates to WebSocket
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Briefly sample faster after input so shell echo is visible within a frame,
	// including output that arrives after send-keys returns. Idle terminals keep
	// their inexpensive polling interval; bursts share one timer.
	fastTimer := time.NewTimer(time.Hour)
	fastTimer.Stop()
	defer fastTimer.Stop()
	var fastTick <-chan time.Time
	var fastUntil time.Time
	lastOutput := output
	sendFrame := func() bool {
		current, err := terminal.paneFrame(paneID)
		if err != nil {
			return false
		}
		if current != lastOutput {
			// Clear screen and rewrite
			conn.WriteMessage(websocket.TextMessage, []byte("\033[2J\033[H"+current))
			lastOutput = current
		}
		return true
	}
	for {
		select {
		case <-done:
			return
		case <-inputActivity:
			fastUntil = time.Now().Add(time.Second)
			if fastTick == nil {
				fastTimer.Reset(16 * time.Millisecond)
				fastTick = fastTimer.C
			}
		case <-fastTick:
			if !sendFrame() {
				return
			}
			if time.Now().Before(fastUntil) {
				fastTimer.Reset(33 * time.Millisecond)
			} else {
				fastTick = nil
			}
		case <-redraw:
			// Give the pane a beat to reflow after the SIGWINCH before capturing.
			time.Sleep(80 * time.Millisecond)
			if !sendFrame() {
				return
			}
		case <-ticker.C:
			if !sendFrame() {
				return
			}
		}
	}
}

// paneFrame captures the pane content and appends an absolute cursor move to
// tmux's real caret position, so xterm's own (blinking) cursor lands where the
// TUI's caret actually is — e.g. inside Claude Code's "> " input box — instead
// of trailing the last line of captured text (the mode-line). capture-pane
// discards cursor position, so without this every client parks the cursor on
// the wrong row.
func (terminal paneTerminal) paneFrame(paneID string) (string, error) {
	content, err := terminal.capturePane(paneID)
	if err != nil {
		return "", err
	}
	if x, y, ok := terminal.paneCursor(paneID); ok {
		// tmux reports 0-based col/row; CUP (\033[row;colH) is 1-based.
		content += fmt.Sprintf("\033[%d;%dH", y+1, x+1)
	}
	return content, nil
}

// paneCursor returns the tmux pane's current cursor position (0-based col, row).
func (terminal paneTerminal) paneCursor(paneID string) (x, y int, ok bool) {
	out, err := terminal.output("display-message", "-t", paneID, "-p", "#{cursor_x} #{cursor_y}")
	if err != nil {
		return 0, 0, false
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) != 2 {
		return 0, 0, false
	}
	cx, err1 := strconv.Atoi(parts[0])
	cy, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return cx, cy, true
}

// capturePane runs tmux capture-pane and returns the visible pane content
// with trailing whitespace stripped from each line so it renders correctly
// in a browser terminal that may be a different width than the tmux pane.
func (terminal paneTerminal) capturePane(paneID string) (string, error) {
	out, err := terminal.output("capture-pane", "-t", paneID, "-p", "-e")
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
func (terminal paneTerminal) getPaneSize(paneID string) (int, int) {
	out, err := terminal.output("display-message", "-t", paneID, "-p", "#{pane_width} #{pane_height}")
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

// Every terminal operation carries its host, including keystrokes and resize.
// Bound commands separately so a lost SSH connection cannot hang the socket.
type paneTerminal struct {
	ctx    context.Context
	runner executor.Runner
}

func (terminal paneTerminal) output(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(terminal.ctx, 15*time.Second)
	defer cancel()
	return terminal.runner.Command(ctx, "", "tmux", terminal.tmuxArgs(args)...).Output()
}
func (terminal paneTerminal) run(args ...string) error {
	ctx, cancel := context.WithTimeout(terminal.ctx, 15*time.Second)
	defer cancel()
	return terminal.runner.Command(ctx, "", "tmux", terminal.tmuxArgs(args)...).Run()
}

// tmuxArgs addresses the agent server when the terminal is local (see tmuxctl).
func (terminal paneTerminal) tmuxArgs(args []string) []string {
	if terminal.runner.Target() == "" {
		return tmuxctl.AgentArgs(args...)
	}
	return args
}
