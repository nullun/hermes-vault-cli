// Package cmd implements the Hermes CLI commands.
package cmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nullun/hermes-vault-cli/internal/avm"
	"github.com/nullun/hermes-vault-cli/internal/config"
	"github.com/nullun/hermes-vault-cli/internal/daemon"
	"github.com/nullun/hermes-vault-cli/internal/db"
	"github.com/nullun/hermes-vault-cli/internal/logger"
	"github.com/nullun/hermes-vault-cli/internal/sync"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/indexer"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	configPath           string
	allowIndexerFallback bool
	Version              string
)

// runtime holds the lazily-initialised state shared across commands that need
// blockchain access: the avm client, the sync subscriber, and a cancel func
// for background cleanup.
type runtime struct {
	avm        *avm.Client
	subscriber *sync.Subscriber
	cancelSync context.CancelFunc
}

var rt *runtime

var configLoadFn = config.Load
var loggerInitFn = logger.Init

var rootCmd = &cobra.Command{
	Use:   "hermes",
	Short: "hermes – local-first CLI for the Hermes Vault shielded pool",
	Long: `hermes is a CLI tool for depositing and withdrawing ALGO through
the Hermes Vault shielded pool (hermesvault.org) using zero-knowledge proofs.

Your secret notes are the keys to your funds. Save them safely.`,
	Version:           "dev",
	PersistentPreRunE: initConfigAndDB,
	SilenceErrors:     true,
	SilenceUsage:      true,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of hermes",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("hermes %s\n", Version)
	},
}

func Execute(v string) {
	Version = v
	rootCmd.Version = v
	if err := rootCmd.Execute(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.SetVersionTemplate("hermes {{.Version}}\n")

	rootCmd.PersistentFlags().StringVar(&configPath, "config", "",
		"config file (default: ~/.config/hermes/config or HERMES_CONFIG env var)")
	rootCmd.PersistentFlags().BoolVar(&allowIndexerFallback, "allow-indexer", false,
		"allow falling back to the configured indexer when algod history is unavailable")

	rootCmd.AddCommand(depositCmd)
	rootCmd.AddCommand(withdrawCmd)
	rootCmd.AddCommand(notesCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(statsCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(versionCmd)
}

// initConfigAndDB is the PersistentPreRunE that initialises config and db.
func initConfigAndDB(cmd *cobra.Command, args []string) error {
	// Load config
	if err := configLoadFn(configPath); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// Init file logger before any note-producing command can proceed.
	if err := loggerInitFn(config.LogPath); err != nil {
		return fmt.Errorf("log file: %w", err)
	}
	logger.Log("START command=%s", cmd.Name())

	// Open database
	if err := db.Open(config.DbPath); err != nil {
		return fmt.Errorf("database: %w", err)
	}

	return nil
}

// initRuntime initialises algod, app artifacts, subscriber, and runs sync.
// This is called lazily only by commands that need it.
func initRuntime() error {
	avmClient, subscriber, err := buildSubscriber()
	if err != nil {
		return err
	}

	rt = &runtime{
		avm:        avmClient,
		subscriber: subscriber,
	}

	ctx := context.Background()

	// Skip catchup if the daemon is running and recently synced.
	daemonHealthy := daemon.IsHealthy(config.StatusPath, config.DaemonStaleness)

	if daemonHealthy {
		fmt.Fprintln(os.Stderr, "Daemon active — skipping catchup")
	} else {
		if _, err := rt.subscriber.SyncToTip(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: blockchain sync failed: %v\n", err)
		}
	}

	// Only start the background cleanup routine when the daemon is not running.
	if !daemonHealthy {
		db.CleanupUnconfirmedNotes()
		cleanupCtx, cancel := context.WithCancel(ctx)
		rt.cancelSync = cancel
		db.StartCleanupRoutine(cleanupCtx, config.CleanupInterval)
	}

	return nil
}

func buildSubscriber() (*avm.Client, *sync.Subscriber, error) {
	// Create avm client (algod + embedded resources)
	avmClient, err := avm.New(config.AlgodURL, config.AlgodToken, config.Network)
	if err != nil {
		return nil, nil, err
	}

	// Build subscriber
	depositSel, withdrawSel, err := getMethodSelectors(avmClient)
	if err != nil {
		return nil, nil, fmt.Errorf("method selectors: %w", err)
	}

	idxClient := buildIndexerClient()

	s := sync.New(
		avmClient.Algod, idxClient,
		avmClient.App.ID, avmClient.AppCreationBlock,
		depositSel, withdrawSel,
		config.AlgodURL,
	)
	s.SetAllowIndexerFallback(allowIndexerFallback)
	s.SetConfirmIndexerFallbackFn(confirmIndexerFallback)
	return avmClient, s, nil
}

// ensureRuntime lazily initialises the runtime if not already done.
func ensureRuntime() error {
	if rt != nil {
		return nil
	}
	return initRuntime()
}

func getMethodSelectors(c *avm.Client) (deposit, withdraw []byte, err error) {
	depMethod, err := c.App.Schema.Contract.GetMethodByName(config.DepositMethodName)
	if err != nil {
		return nil, nil, fmt.Errorf("finding deposit method: %w", err)
	}
	wdMethod, err := c.App.Schema.Contract.GetMethodByName(config.WithdrawalMethodName)
	if err != nil {
		return nil, nil, fmt.Errorf("finding withdrawal method: %w", err)
	}
	return depMethod.GetSelector(), wdMethod.GetSelector(), nil
}

func buildIndexerClient() *indexer.Client {
	if config.IndexerURL == "" {
		return nil
	}
	url := config.IndexerURL
	if !strings.HasPrefix(url, "http") {
		url = "https://" + url
	}
	c, err := indexer.MakeClient(url, config.IndexerToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create indexer client: %v\n", err)
		return nil
	}
	return c
}

func confirmIndexerFallback(rounds uint64) (bool, error) {
	if allowIndexerFallback {
		return true, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, fmt.Errorf("algod history gap is %d rounds; rerun with --allow-indexer to use the configured indexer", rounds)
	}

	fmt.Fprintf(os.Stderr, "Algod is %d rounds behind the requested history. Fall back to the configured indexer? [y/N]: ", rounds)
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("read indexer fallback confirmation: %w", err)
	}

	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}
