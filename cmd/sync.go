package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var syncForceIndexer bool

var ensureRuntimeFn = ensureRuntime
var subscriberSyncToTipFn = func(ctx context.Context) (uint64, error) {
	return rt.subscriber.SyncToTip(ctx)
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Synchronise the local database with the blockchain",
	Long: `Force a synchronisation from the last processed block to the current chain tip.

Every command syncs automatically before it runs, so most users never need
this. Use it only if you suspect the local state is stale.`,
	RunE: runSync,
}

func init() {
	syncCmd.Flags().BoolVar(&syncForceIndexer, "force-indexer", false,
		"Force sync using the indexer instead of algod, even if algod history is available")
}

func runSync(cmd *cobra.Command, args []string) error {
	if err := ensureRuntimeFn(); err != nil {
		return err
	}

	if syncForceIndexer {
		rt.subscriber.SetForceIndexer(true)
	}

	ctx := context.Background()
	n, err := subscriberSyncToTipFn(ctx)
	if err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}
	if n == 0 {
		fmt.Println("Already up to date.")
	} else {
		fmt.Printf("Sync complete. Processed %d new transaction(s).\n", n)
	}
	return nil
}
