package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	sdk_models "github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
)

// saveStxnFile writes the signed transaction group bytes to a .stxn file in the
// current directory. The format is standard Algorand msgpack and can be inspected
// with tools like `goal clerk inspect` or `algokit`. Returns the absolute path.
func saveStxnFile(signedGroup []byte, txnIDPrefix string) (string, error) {
	if len(txnIDPrefix) > 12 {
		txnIDPrefix = txnIDPrefix[:12]
	}
	filename := fmt.Sprintf("simulate_%s_%d.stxn", txnIDPrefix, time.Now().Unix())
	if err := os.WriteFile(filename, signedGroup, 0600); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filename)
	if err != nil {
		return filename, nil
	}
	return abs, nil
}

// printSimulateResult prints a human-readable summary of an algod simulation response.
func printSimulateResult(resp sdk_models.SimulateResponse) {
	if len(resp.TxnGroups) == 0 {
		fmt.Println("  (no results returned)")
		return
	}
	group := resp.TxnGroups[0]

	if group.FailureMessage != "" {
		failedAt := "unknown txn"
		if len(group.FailedAt) > 0 {
			failedAt = fmt.Sprintf("txn[%d]", group.FailedAt[0])
		}
		fmt.Printf("  ✗ Failed at %s: %s\n", failedAt, group.FailureMessage)
	} else {
		fmt.Printf("  ✓ Passed\n")
	}

	if group.AppBudgetAdded > 0 {
		fmt.Printf("  Opcode budget: %d consumed / %d available\n",
			group.AppBudgetConsumed, group.AppBudgetAdded)
	}

	for i, txnResult := range group.TxnResults {
		budget := txnResult.AppBudgetConsumed
		logs := txnResult.TxnResult.Logs
		lsigBudget := txnResult.LogicSigBudgetConsumed
		if budget > 0 || lsigBudget > 0 || len(logs) > 0 {
			fmt.Printf("  txn[%d]:", i)
			if budget > 0 {
				fmt.Printf(" app_budget=%d", budget)
			}
			if lsigBudget > 0 {
				fmt.Printf(" lsig_budget=%d", lsigBudget)
			}
			if len(logs) > 0 {
				fmt.Printf(" logs=%d", len(logs))
			}
			fmt.Println()
		}
	}
}
