package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UILogger provides file-based logging for the UI package.
// Logs are written to ~/.local/share/task/ui.log
type UILogger struct {
	mu   sync.Mutex
	file *os.File
}

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
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	logDir := filepath.Join(home, ".local", "share", "task")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}

	logPath := filepath.Join(logDir, "ui.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	l.file = f
}

// LogPath returns the path to the log file.
func LogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "task", "ui.log")
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
	line := fmt.Sprintf("[%s] %s: %s\n", timestamp, level, msg)
	l.file.WriteString(line)
	l.file.Sync()
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
