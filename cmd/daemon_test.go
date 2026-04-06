package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nullun/hermes-vault-cli/internal/daemon"
)

func TestWaitForDaemonReadyReturnsStatus(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "hermes.status")
	pid := os.Getpid()

	go func() {
		time.Sleep(50 * time.Millisecond)
		data, err := json.Marshal(daemon.Status{
			PID:      pid,
			Round:    42,
			SyncedAt: time.Now(),
		})
		if err != nil {
			return
		}
		if err := os.WriteFile(statusPath, data, 0644); err != nil {
			t.Errorf("failed to write status file: %v", err)
			return
		}
	}()

	status, err := waitForDaemonReady(statusPath, pid, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForDaemonReady returned error: %v", err)
	}
	if status.PID != pid {
		t.Fatalf("expected pid %d, got %d", pid, status.PID)
	}
	if status.Round != 42 {
		t.Fatalf("expected round 42, got %d", status.Round)
	}
}

func TestWaitForDaemonReadyFailsWhenProcessIsNotRunning(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "hermes.status")

	if _, err := waitForDaemonReady(statusPath, 999999, 100*time.Millisecond, 10*time.Millisecond); err == nil {
		t.Fatalf("expected error when process is not running")
	}
}
