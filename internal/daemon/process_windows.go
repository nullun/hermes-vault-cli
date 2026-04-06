//go:build windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

// IsRunning returns true if a process with the given PID is alive.
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == 259 // STILL_ACTIVE
}

// SendStop reads the status file and terminates the daemon process.
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
	if err := proc.Kill(); err != nil {
		return s.PID, fmt.Errorf("failed to terminate process %d: %w", s.PID, err)
	}
	return s.PID, nil
}

// DaemonProcAttr returns platform-specific process attributes for the daemon.
func DaemonProcAttr() *syscall.SysProcAttr {
	const createNewProcessGroup = 0x00000200
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// StopSignals returns the OS signals the daemon should listen for to shut down.
func StopSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
