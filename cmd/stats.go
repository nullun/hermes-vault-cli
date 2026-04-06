package cmd

import (
	"fmt"
	"io"

	"github.com/nullun/hermes-vault-cli/internal/db"
	"github.com/nullun/hermes-vault-cli/internal/models"

	"github.com/spf13/cobra"
)

var getStatsFn = db.GetStats
var getWatermarkFn = db.GetWatermark
var getRootFn = db.GetRoot

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show pool statistics",
	Long:  `Display aggregate statistics for the Hermes Vault shielded pool.`,
	RunE:  runStats,
}

func runStats(cmd *cobra.Command, args []string) error {
	stats, err := getStatsFn()
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	watermark, err := getWatermarkFn()
	if err != nil {
		return fmt.Errorf("failed to get watermark: %w", err)
	}

	_, leafCount, err := getRootFn()
	if err != nil {
		return fmt.Errorf("failed to get root: %w", err)
	}

	tvl := stats.TVL()
	out := cmd.OutOrStdout()

	writeStats(out, stats, leafCount, watermark, tvl)
	return nil
}

func writeStats(out io.Writer, stats *models.StatData, leafCount, watermark uint64, tvl *models.Amount) {
	fmt.Fprintln(out, "═══════════════════════════════════")
	fmt.Fprintln(out, "  Hermes Vault Pool Statistics")
	fmt.Fprintln(out, "═══════════════════════════════════")
	fmt.Fprintf(out, "  TVL (Total Value Locked): %s ALGO\n", tvl.AlgoString)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Total Deposited:    %s ALGO\n", stats.DepositTotal.AlgoString)
	fmt.Fprintf(out, "  Total Withdrawn:    %s ALGO\n", stats.WithdrawalTotal.AlgoString)
	fmt.Fprintf(out, "  Total Fees:         %s ALGO\n", stats.FeeTotal.AlgoString)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Deposit Count:      %d\n", stats.DepositCount)
	fmt.Fprintf(out, "  Note Count:         %d\n", stats.NoteCount)
	fmt.Fprintf(out, "  Leaves in Tree:     %d\n", leafCount)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Last Synced Round:  %d\n", watermark)
	fmt.Fprintln(out, "═══════════════════════════════════")
}
