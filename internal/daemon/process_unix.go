//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

// IsRunning returns true if a process with the given PID is alive.
// Uses signal 0, which tests process existence without sending a real signal.
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// SendStop reads the status file and sends SIGTERM to the daemon process.
// Returns the PID that was signalled.
func SendStop(path string) (int, error) {
	s, err := Read(path)
	if err != nil {
		return 0, fmt.Errorf("no daemon status file found — daemon may not be running")
	}
	if !IsRunning(s.PID) {
		os.Remove(path)
		return s.PID, fmt.Errorf("process %d is not running (removed stale status file)", s.PID)
	}
	proc, _ := os.FindProcess(s.PID)
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return s.PID, fmt.Errorf("failed to signal process %d: %w", s.PID, err)
	}
	return s.PID, nil
}

// DaemonProcAttr returns platform-specific process attributes for the daemon.
func DaemonProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// StopSignals returns the OS signals the daemon should listen for to shut down.
func StopSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGINT}
}
