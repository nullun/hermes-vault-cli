// Package daemon manages the Hermes background sync process.
// It provides a status file that other commands read to detect whether the
// daemon is running and recently synced, allowing them to skip catchup.
package daemon

import (
	"encoding/json"
	"os"
	"time"
)

// Status is written to the status file by a running daemon.
type Status struct {
	PID         int       `json:"pid"`
	Round       uint64    `json:"round"`
	SyncedAt    time.Time `json:"synced_at"`
	IsSyncing   bool      `json:"is_syncing"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
}

// Write serialises status to path. Existing file is overwritten.
func Write(path string, s Status) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Read parses the status file at path.
func Read(path string) (Status, error) {
	var s Status
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// IsHealthy returns true if the status file exists, the daemon process is
// running, and the last successful sync was within staleness.
func IsHealthy(path string, staleness time.Duration) bool {
	s, err := Read(path)
	if err != nil {
		return false
	}
	return IsRunning(s.PID) && time.Since(s.SyncedAt) <= staleness
}
