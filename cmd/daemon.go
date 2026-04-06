package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"time"

	"github.com/nullun/hermes-vault-cli/internal/config"
	"github.com/nullun/hermes-vault-cli/internal/daemon"
	"github.com/nullun/hermes-vault-cli/internal/db"
	"github.com/nullun/hermes-vault-cli/internal/logger"

	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the background sync daemon",
	Long: `Manage the Hermes Vault background sync daemon that continuously
syncs the pool state from the blockchain.

This is an advanced feature for users who want instant command responses.
Most users do not need it — deposit, withdraw, and other commands sync
automatically before they run.

While the daemon is running, other commands detect it via the status file
and skip their own catchup step, so they respond immediately.

If you run a daemon, use your own Algorand node rather than the public
AlgoNode endpoints. Public endpoints are fine for occasional interactive
commands, but continuous polling from a daemon may be rate-limited.`,
}

var daemonStartCmd = &cobra.Command{
	Use:               "start",
	Short:             "Start the daemon in the background",
	PersistentPreRunE: initConfigOnly,
	RunE:              runDaemonStart,
}

var daemonRunCmd = &cobra.Command{
	Use:    "run",
	Short:  "Run the daemon in the foreground (internal use)",
	Hidden: true,
	RunE:   runDaemon,
}

var daemonStopCmd = &cobra.Command{
	Use:               "stop",
	Short:             "Stop the running daemon",
	PersistentPreRunE: initConfigOnly,
	RunE:              runDaemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:               "status",
	Short:             "Show daemon status",
	PersistentPreRunE: initConfigOnly,
	RunE:              runDaemonStatus,
}

func init() {
	rootCmd.AddCommand(daemonCmd)
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonRunCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
}

func initConfigOnly(cmd *cobra.Command, args []string) error {
	return config.Load(configPath)
}

func runDaemonStart(cmd *cobra.Command, args []string) error {
	if daemon.IsHealthy(config.StatusPath, config.DaemonStaleness) {
		s, _ := daemon.Read(config.StatusPath)
		return fmt.Errorf("daemon already running (PID %d) — use 'daemon stop' first", s.PID)
	}

	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}

	startArgs := []string{"daemon", "run", "--config", configPath}
	allowIndexer, err := shouldAllowIndexerForDaemonStart()
	if err != nil {
		return err
	}
	if allowIndexer {
		startArgs = append(startArgs, "--allow-indexer")
	}
	daemonProc := exec.Command(bin, startArgs...)
	daemonProc.Stdin = nil
	daemonProc.SysProcAttr = daemon.DaemonProcAttr()
	logFile, err := os.OpenFile(config.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		defer logFile.Close()
		daemonProc.Stdout = logFile
		daemonProc.Stderr = logFile
	}

	if err := daemonProc.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	pid := daemonProc.Process.Pid
	if _, err := waitForDaemonReady(config.StatusPath, pid, 5*time.Second, 100*time.Millisecond); err != nil {
		return fmt.Errorf("daemon failed to become ready: %w", err)
	}
	if err := daemonProc.Process.Release(); err != nil {
		return fmt.Errorf("failed to detach daemon: %w", err)
	}

	fmt.Printf("Daemon started (PID %d)\n", pid)
	return nil
}

func waitForDaemonReady(statusPath string, pid int, timeout, pollInterval time.Duration) (daemon.Status, error) {
	deadline := time.Now().Add(timeout)

	for {
		status, err := daemon.Read(statusPath)
		if err == nil && status.PID == pid && daemon.IsRunning(pid) {
			return status, nil
		}

		if !daemon.IsRunning(pid) {
			if err == nil && status.PID == pid {
				return daemon.Status{}, fmt.Errorf("process %d exited during startup", pid)
			}
			return daemon.Status{}, fmt.Errorf("process %d exited before writing status", pid)
		}

		if time.Now().After(deadline) {
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return daemon.Status{}, fmt.Errorf("timed out waiting for daemon status: %w", err)
			}
			return daemon.Status{}, fmt.Errorf("timed out waiting for status file at %s", statusPath)
		}

		time.Sleep(pollInterval)
	}
}

func shouldAllowIndexerForDaemonStart() (bool, error) {
	if allowIndexerFallback {
		return true, nil
	}
	if err := db.Open(config.DbPath); err != nil {
		return false, fmt.Errorf("database: %w", err)
	}
	defer db.Close()

	avmClient, s, err := buildSubscriber()
	if err != nil {
		return false, err
	}
	needsFallback, roundsBehind, err := s.NeedsIndexerFallback(context.Background())
	if err != nil {
		return false, err
	}
	if !needsFallback {
		return false, nil
	}

	// Case 1: Indexer is configured. Ask user if we should use it.
	if s.HasIndexer() {
		ok, err := confirmIndexerFallback(roundsBehind)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("indexer fallback declined; daemon start aborted")
		}
		return true, nil
	}

	// Case 2: No indexer. Check if algod actually has the blocks.
	watermark, err := db.GetWatermark()
	if err != nil {
		return false, fmt.Errorf("failed to get watermark: %w", err)
	}
	nextRound := watermark + 1
	if watermark == 0 {
		nextRound = s.CreationBlock()
	}
	if nextRound > 0 {
		_, err := avmClient.Algod.Block(nextRound).Do(context.Background())
		if err != nil {
			return false, fmt.Errorf("algod is %d rounds behind and no indexer is configured. The node is missing block %d and cannot sync this much history", roundsBehind, nextRound)
		}
	}

	// Algod has the blocks, so we don't need indexer fallback.
	return false, nil
}

// daemonShouldUpgradeInterval checks if the round number is divisible by the
// next interval tier (10x the current), signalling the interval should ratchet up.
// The interval only ever increases: 1 → 10 → 100 → 1000 → 10000 (final).
func daemonShouldUpgradeInterval(round, currentInterval uint64) bool {
	nextInterval := currentInterval * 10
	if nextInterval > 10000 {
		return false
	}
	return round%nextInterval == 0
}

func runDaemon(cmd *cobra.Command, args []string) error {
	if err := ensureRuntime(); err != nil {
		return fmt.Errorf("app init: %w", err)
	}

	pid := os.Getpid()
	round, err := db.GetWatermark()
	if err != nil {
		return fmt.Errorf("failed to get watermark: %w", err)
	}

	st := daemon.Status{
		PID:      pid,
		Round:    round,
		SyncedAt: time.Now(),
	}

	if err := daemon.Write(config.StatusPath, st); err != nil {
		return fmt.Errorf("failed to write status file: %w", err)
	}
	defer os.Remove(config.StatusPath)

	ctx, stop := signal.NotifyContext(context.Background(), daemon.StopSignals()...)
	defer stop()

	fmt.Printf("Daemon started (PID %d) at round %d.\n", pid, round)
	fmt.Println("Press Ctrl-C or run 'hermes daemon stop' to quit.")
	logger.Log("DAEMON started PID=%d round=%d", pid, round)
	logger.Log("DAEMON using adaptive logging: logs all blocks initially, then rounds ending in 0, 00, 000, 0000, etc")

	algod := rt.avm.Algod
	if algod == nil {
		return fmt.Errorf("algod client is nil after ensureRuntime")
	}

	currentInterval := config.SyncInterval
	maxInterval := 5 * time.Minute
	logInterval := uint64(1) // Starts at 1 (log every block), ratchets up

	for {
		if ctx.Err() != nil {
			fmt.Println("\nDaemon stopping...")
			logger.Log("DAEMON stopped PID=%d", pid)
			return nil
		}

		// Update status: currently syncing
		st.IsSyncing = true
		_ = daemon.Write(config.StatusPath, st)

		_, err := rt.subscriber.SyncToTip(ctx)
		st.IsSyncing = false

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Log("DAEMON sync error: %v", err)
			st.LastError = err.Error()
			st.LastErrorAt = time.Now()
			_ = daemon.Write(config.StatusPath, st)

			// Exponential backoff
			fmt.Fprintf(os.Stderr, "Sync error: %v. Retrying in %v...\n", err, currentInterval)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(currentInterval):
				currentInterval *= 2
				if currentInterval > maxInterval {
					currentInterval = maxInterval
				}
			}
			continue
		}

		// Success: reset backoff
		currentInterval = config.SyncInterval
		st.LastError = ""
		st.LastErrorAt = time.Time{}
		if r, err := db.GetWatermark(); err != nil {
			logger.Log("DAEMON failed to read watermark: %v", err)
		} else {
			round = r
		}
		st.Round = round
		st.SyncedAt = time.Now()

		if err := daemon.Write(config.StatusPath, st); err != nil {
			logger.Log("DAEMON failed to update status file: %v", err)
		}

		// Check if interval should ratchet up (1 → 10 → 100 → 1000 → 10000)
		if daemonShouldUpgradeInterval(round, logInterval) {
			logInterval *= 10
			logger.Log("DAEMON logging interval changed: now logging every %d rounds", logInterval)
		}

		// Log if this round is aligned with the current interval
		if round%logInterval == 0 {
			logger.Log("DAEMON synced to round %d", round)
		}

		// Wait for the next block using algod's StatusAfterBlock
		// This is much more efficient than polling at fixed intervals
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		status, err := algod.StatusAfterBlock(round).Do(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Log("DAEMON StatusAfterBlock error: %v", err)
			// Fall back to polling on error
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(config.SyncInterval):
				continue
			}
		}

		// If new block arrived, loop will SyncToTip again
		if status.LastRound <= round {
			// No new block yet, wait a bit and try again
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(config.SyncInterval):
				continue
			}
		}
	}
}

func runDaemonStop(cmd *cobra.Command, args []string) error {
	pid, err := daemon.SendStop(config.StatusPath)
	if err != nil {
		return err
	}
	fmt.Printf("Sent SIGTERM to daemon (PID %d)\n", pid)
	return nil
}

func runDaemonStatus(cmd *cobra.Command, args []string) error {
	s, err := daemon.Read(config.StatusPath)
	if err != nil {
		fmt.Println("Daemon: not running")
		return nil
	}
	if !daemon.IsRunning(s.PID) {
		fmt.Printf("Daemon: stopped (stale status file for PID %d)\n", s.PID)
		return nil
	}
	age := time.Since(s.SyncedAt).Round(time.Second)
	healthy := age <= 3*config.SyncInterval && s.LastError == ""
	healthStr := "healthy"
	if s.IsSyncing {
		healthStr = "syncing"
	} else if s.LastError != "" {
		healthStr = "error"
	} else if !healthy {
		healthStr = "stalled"
	}
	fmt.Printf("Daemon: running (%s)\n", healthStr)
	fmt.Printf("  PID:          %d\n", s.PID)
	fmt.Printf("  Current round: %d\n", s.Round)
	fmt.Printf("  Last synced:  %s ago\n", age)
	if s.LastError != "" {
		fmt.Printf("  Last error:   %s (%s ago)\n", s.LastError, time.Since(s.LastErrorAt).Round(time.Second))
	}
	return nil
}
