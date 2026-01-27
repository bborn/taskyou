package webapi

import (
	"fmt"
	"net/http"
	"os/exec"
	"sync"
)

// terminalManager manages ttyd processes for task terminals.
type terminalManager struct {
	mu       sync.Mutex
	sessions map[int64]*ttydSession
}

type ttydSession struct {
	taskID  int64
	port    int
	process *exec.Cmd
}

var terminals = &terminalManager{
	sessions: make(map[int64]*ttydSession),
}

// BasePort is the starting port for ttyd instances.
const ttydBasePort = 7681

// getTerminalPort returns the port for a task's terminal.
func getTerminalPort(taskID int64) int {
	return ttydBasePort + int(taskID%1000)
}

// handleGetTerminal returns terminal connection info for a task.
func (s *Server) handleGetTerminal(w http.ResponseWriter, r *http.Request) {
	taskID, err := getIDParam(r)
	if err != nil {
		jsonError(w, "invalid task ID", http.StatusBadRequest)
		return
	}

	// Get task to find its daemon session
	task, err := s.db.GetTask(taskID)
	if err != nil {
		jsonError(w, "task not found", http.StatusNotFound)
		return
	}

	// Check if task has a tmux session
	if task.DaemonSession == "" {
		jsonError(w, "task has no active terminal session", http.StatusNotFound)
		return
	}

	windowName := fmt.Sprintf("task-%d", taskID)
	tmuxTarget := fmt.Sprintf("%s:%s", task.DaemonSession, windowName)

	// Check if tmux window exists
	if err := exec.Command("tmux", "has-session", "-t", tmuxTarget).Run(); err != nil {
		jsonError(w, "task terminal session not running", http.StatusNotFound)
		return
	}

	// Start ttyd if not already running
	port, err := terminals.ensureRunning(taskID, tmuxTarget)
	if err != nil {
		s.logger.Error("failed to start terminal", "task", taskID, "error", err)
		jsonError(w, "failed to start terminal: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"task_id":      taskID,
		"port":         port,
		"tmux_target":  tmuxTarget,
		"websocket_url": fmt.Sprintf("ws://localhost:%d/ws", port),
	}, http.StatusOK)
}

// ensureRunning starts ttyd for a task if not already running.
func (tm *terminalManager) ensureRunning(taskID int64, tmuxTarget string) (int, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Check if already running
	if session, ok := tm.sessions[taskID]; ok {
		// Verify process is still alive
		if session.process != nil && session.process.Process != nil {
			if err := session.process.Process.Signal(nil); err == nil {
				return session.port, nil
			}
		}
		// Process died, clean up
		delete(tm.sessions, taskID)
	}

	port := getTerminalPort(taskID)

	// Check if ttyd is available
	if _, err := exec.LookPath("ttyd"); err != nil {
		return 0, fmt.Errorf("ttyd not installed: %w", err)
	}

	// Start ttyd attached to the tmux session
	// -W = writable (interactive)
	// -p = port
	// -t = terminal type options for better compatibility
	cmd := exec.Command("ttyd",
		"-W",
		"-p", fmt.Sprintf("%d", port),
		"-t", "fontSize=14",
		"-t", "fontFamily=monospace",
		"tmux", "attach-session", "-t", tmuxTarget,
	)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to start ttyd: %w", err)
	}

	tm.sessions[taskID] = &ttydSession{
		taskID:  taskID,
		port:    port,
		process: cmd,
	}

	// Clean up process when it exits
	go func() {
		cmd.Wait()
		tm.mu.Lock()
		delete(tm.sessions, taskID)
		tm.mu.Unlock()
	}()

	return port, nil
}

// stopTerminal stops the ttyd process for a task.
func (tm *terminalManager) stopTerminal(taskID int64) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if session, ok := tm.sessions[taskID]; ok {
		if session.process != nil && session.process.Process != nil {
			session.process.Process.Kill()
		}
		delete(tm.sessions, taskID)
	}
}

// handleStopTerminal stops a task's terminal.
func (s *Server) handleStopTerminal(w http.ResponseWriter, r *http.Request) {
	taskID, err := getIDParam(r)
	if err != nil {
		jsonError(w, "invalid task ID", http.StatusBadRequest)
		return
	}

	terminals.stopTerminal(taskID)
	w.WriteHeader(http.StatusNoContent)
}
