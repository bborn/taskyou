package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bborn/workflow/internal/db"
)

// UILogger provides file-based logging for the UI package. The TUI draws on
// stdout, so diagnostics cannot go there; they go to ui.log beside the database.
//
// Three properties this file depends on, each learned the hard way:
//
//   - It sits next to the database, so an isolated instance (a QA harness with
//     its own WORKTREE_DB_PATH) writes to its own log. Sharing one file made a
//     QA run's errors look like they came from the live TUI.
//   - Every line carries the pid. Several ty processes append concurrently —
//     TUIs, the daemon — and without it there is no way to tell who wrote what.
//   - It rotates. Unrotated, this file reached 205MB and eight months of history.
type UILogger struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	pid    int
	writes int
}

// maxLogBytes is the size at which the log is rotated to ui.log.1. One previous
// generation is kept: enough to span a restart, bounded on disk.
const maxLogBytes = 16 << 20

// logSyncEvery flushes to disk every N lines rather than on every one. A crash
// can now lose a few trailing lines; in exchange the UI is not paying an fsync
// per log statement, which at debug volume is thousands per task switch.
const logSyncEvery = 64

var uiLogger *UILogger
var loggerOnce sync.Once

// GetLogger returns the singleton UI logger instance.
// Call CloseLogger() when the application exits.
func GetLogger() *UILogger {
	loggerOnce.Do(func() {
		uiLogger = &UILogger{}
		uiLogger.init()
	})
	return uiLogger
}

func (l *UILogger) init() {
	logPath := LogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	l.file = f
	l.path = logPath
	l.pid = os.Getpid()
}

// LogPath returns the path to the log file: ui.log beside the database, so an
// instance pointed at another database logs beside that one instead of into the
// live instance's file.
func LogPath() string {
	return filepath.Join(filepath.Dir(db.DefaultPath()), "ui.log")
}

// CloseLogger closes the log file.
func CloseLogger() {
	if uiLogger != nil && uiLogger.file != nil {
		uiLogger.file.Close()
	}
}

func (l *UILogger) log(level, format string, args ...interface{}) {
	if l == nil || l.file == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] [%d] %s: %s\n", timestamp, l.pid, level, msg)
	l.file.WriteString(line)

	l.writes++
	if l.writes%logSyncEvery == 0 {
		l.file.Sync()
		l.rotateIfLarge()
	}
}

// rotateIfLarge moves the log aside once it passes maxLogBytes, keeping one
// previous generation. Caller holds the mutex.
func (l *UILogger) rotateIfLarge() {
	if l.path == "" {
		return
	}
	info, err := l.file.Stat()
	if err != nil || info.Size() < maxLogBytes {
		return
	}
	// Other ty processes hold their own descriptor on the same inode. Renaming
	// leaves them writing to the rotated file until they notice; reopening here
	// is what moves this process onto the fresh one. Nothing is lost either way.
	_ = os.Rename(l.path, l.path+".1")
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	_ = l.file.Close()
	l.file = f
}

// Info logs an info message.
func (l *UILogger) Info(format string, args ...interface{}) {
	l.log("INFO", format, args...)
}

// Error logs an error message.
func (l *UILogger) Error(format string, args ...interface{}) {
	l.log("ERROR", format, args...)
}

// Debug logs a debug message.
func (l *UILogger) Debug(format string, args ...interface{}) {
	l.log("DEBUG", format, args...)
}

// Warn logs a warning message.
func (l *UILogger) Warn(format string, args ...interface{}) {
	l.log("WARN", format, args...)
}
