// Package logger provides file-based logging for Hermes.
// All sensitive events (note generation, confirmation, errors, panics) are
// written to a log file so that notes can be recovered even if the terminal
// session is lost or the process crashes.
package logger

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"sync"
)

var fileLog *log.Logger
var logFile *os.File
var mu sync.Mutex

// Init opens (or creates) the log file at path and sets up the file logger.
// The file is created with mode 0600 so only the owner can read it.
// Init is idempotent — calling it again replaces the logger.
func Init(path string) error {
	mu.Lock()
	defer mu.Unlock()

	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
		fileLog = nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", path, err)
	}
	logFile = f
	fileLog = log.New(f, "", log.LstdFlags|log.LUTC)
	return nil
}

// Log writes a formatted message to the log file.
// It is a no-op if Init has not been called.
func Log(format string, args ...any) {
	if err := LogDurable(format, args...); err != nil && fileLog != nil {
		fileLog.Printf("LOGGER ERROR: %v", err)
	}
}

// LogDurable writes a formatted line to the log file and fsyncs it.
// This is used for note material that must be durably recorded before proceeding.
func LogDurable(format string, args ...any) error {
	mu.Lock()
	defer mu.Unlock()

	if fileLog == nil || logFile == nil {
		return fmt.Errorf("log file is not initialised")
	}
	if err := fileLog.Output(2, fmt.Sprintf(format, args...)); err != nil {
		return fmt.Errorf("write log entry: %w", err)
	}
	if err := logFile.Sync(); err != nil {
		return fmt.Errorf("sync log file: %w", err)
	}
	return nil
}

// LogPanic writes a panic value and full stack trace to the log file.
// Call this from a deferred recover() handler before re-panicking.
func LogPanic(r any) {
	if fileLog != nil {
		fileLog.Printf("PANIC: %v\n%s", r, debug.Stack())
	}
}
