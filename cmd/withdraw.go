package cmd

import (
	"fmt"
	"os"

	"github.com/nullun/hermes-vault-cli/internal/avm"
	"github.com/nullun/hermes-vault-cli/internal/db"
	"github.com/nullun/hermes-vault-cli/internal/logger"
	"github.com/nullun/hermes-vault-cli/internal/models"

	"github.com/algorand/go-algorand-sdk/v2/crypto"
	"github.com/spf13/cobra"
)

var withdrawSimulate bool

var withdrawCmd = &cobra.Command{
	Use:   "withdraw <secret-note> <amount> <recipient-address>",
	Short: "Withdraw ALGO from the shielded pool",
	Long: `Withdraw ALGO from the Hermes Vault shielded pool using your secret note.

The withdrawal amount must be less than the note amount minus the protocol fee.
Any remaining balance will be returned in a new secret note.

No wallet signature is required — the withdrawal is proved via your secret note.

Example:
  hermes withdraw <140-char-note> 5 RECIPIENT_ADDRESS`,
	Args: requireExactArgs(3),
	RunE: runWithdraw,
}

func init() {
	withdrawCmd.Flags().BoolVar(&withdrawSimulate, "simulate", false,
		"Sign transactions and run against the algod simulation endpoint without submitting; saves a .stxn file for inspection")
}

func runWithdraw(cmd *cobra.Command, args []string) error {
	if err := ensureRuntime(); err != nil {
		return err
	}

	// Parse inputs
	fromNote, err := models.Input(args[0]).ToNote()
	if err != nil {
		return fmt.Errorf("invalid secret note: %w", err)
	}

	amount, err := models.Input(args[1]).ToAmount()
	if err != nil {
		return fmt.Errorf("invalid amount %q: %w", args[1], err)
	}

	recipient, err := models.Input(args[2]).ToAddress()
	if err != nil {
		return fmt.Errorf("invalid recipient address: %w", err)
	}

	// Validate withdrawal amount
	maxAmount := fromNote.MaxWithdrawalAmount()
	if amount.MicroAlgos == 0 {
		return fmt.Errorf("withdrawal amount must be greater than zero")
	}
	if amount.MicroAlgos > maxAmount.MicroAlgos {
		return fmt.Errorf("maximum withdrawable amount is %s ALGO (note amount minus fee)",
			maxAmount.AlgoString)
	}

	// Look up leaf index from the note's commitment
	leafIndex, err := db.GetLeafIndexByCommitment(fromNote.Commitment())
	if err != nil {
		return fmt.Errorf("note not found in the pool — sync may be needed or note is invalid: %w", err)
	}
	fromNote.LeafIndex = leafIndex

	// Generate change note for the remaining balance
	changeNote, err := models.GenerateChangeNote(amount, fromNote)
	if err != nil {
		return fmt.Errorf("failed to generate change note: %w", err)
	}

	fee := amount.Fee()

	fmt.Printf("Withdrawing %s ALGO to %s\n", amount.AlgoString, string(recipient))
	fmt.Printf("Protocol fee: %s ALGO\n", fee.AlgoString)
	if changeNote.Amount > 0 {
		fmt.Printf("Change note will hold: %s ALGO\n", changeNote.AmountAlgoString())
	} else {
		fmt.Println("No change (full withdrawal)")
	}
	fmt.Println()
	fmt.Println("Generating zero-knowledge proof (this may take a moment)...")

	withdrawData := &models.WithdrawalData{
		Amount:     amount,
		Fee:        fee,
		Address:    recipient,
		FromNote:   fromNote,
		ChangeNote: changeNote,
	}

	txns, err := rt.avm.CreateWithdrawalTxns(withdrawData)
	if err != nil {
		return fmt.Errorf("failed to create withdrawal transactions: %w", err)
	}

	// Set TxnID on the change note
	changeNote.TxnID = crypto.GetTxID(txns[0])

	// ── Simulate path ────────────────────────────────────────────────────────
	if withdrawSimulate {
		fmt.Println("Signing transaction group (not submitting)...")
		signedGroup, err := rt.avm.SignWithdrawalGroup(txns)
		if err != nil {
			return fmt.Errorf("signing transactions: %w", err)
		}
		path, err := saveStxnFile(signedGroup, changeNote.TxnID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save .stxn file: %v\n", err)
		} else {
			fmt.Printf("Signed transactions saved to: %s\n", path)
		}
		fmt.Println("Calling simulation endpoint...")
		resp, err := rt.avm.SimulateGroup(signedGroup, len(txns))
		if err != nil {
			return fmt.Errorf("simulation request failed: %w", err)
		}
		printSimulateResult(resp)
		if changeNote.Amount > 0 {
			fmt.Println()
			fmt.Println("══════════════════════════════════════════════════")
			fmt.Println("  CHANGE NOTE (SIMULATED — NOT SUBMITTED, NOT SAVED):")
			fmt.Println()
			fmt.Printf("  %s\n", changeNote.Text())
			fmt.Printf("  Remaining: %s ALGO\n", changeNote.AmountAlgoString())
			fmt.Println("══════════════════════════════════════════════════")
			fmt.Println()
			fmt.Println("This note is not valid — it was generated for simulation only.")
		}
		return nil
	}

	// ── Live path ─────────────────────────────────────────────────────────────
	noteID, err := recordPendingNote("WITHDRAWAL CHANGE_NOTE_GENERATED txn=%s amount_ualgos=%d note=%s", changeNote)
	if err != nil {
		return err
	}

	fmt.Println("Submitting withdrawal to network...")
	leafIdx, txnID, confirmErr := rt.avm.SendWithdrawalToNetwork(txns)
	if confirmErr != nil {
		if confirmErr.Type != avm.TxnTimeout {
			_ = db.DeleteUnconfirmedNote(noteID)
		} else {
			logger.Log("WITHDRAWAL TIMEOUT txn=%s — unconfirmed change note retained in db", changeNote.TxnID)
		}
		return handleWithdrawError(confirmErr)
	}

	logger.Log("WITHDRAWAL CONFIRMED txn=%s leaf_index=%d note=%s", txnID, leafIdx, changeNote.Text())

	changeNote.LeafIndex = leafIdx
	if txnID != changeNote.TxnID {
		fmt.Fprintf(os.Stderr, "Warning: txnID mismatch %s != %s\n", txnID, changeNote.TxnID)
	}

	if err := persistConfirmedNote(changeNote); err != nil {
		logger.Log("WITHDRAWAL ERROR failed to save change note txn=%s: %v — note=%s", txnID, err, changeNote.Text())
		return err
	} else {
		_ = db.DeleteUnconfirmedNote(noteID)
		logger.Log("WITHDRAWAL NOTE_SAVED txn=%s leaf_index=%d", txnID, leafIdx)
	}

	fmt.Printf("\n✓ Withdrawal successful! Leaf index: %d\n\n", leafIdx)

	if changeNote.Amount > 0 {
		fmt.Println("══════════════════════════════════════════════════")
		fmt.Println("  YOUR NEW SECRET NOTE (save this securely):")
		fmt.Println()
		fmt.Printf("  %s\n", changeNote.Text())
		fmt.Println()
		fmt.Printf("  Remaining balance: %s ALGO\n", changeNote.AmountAlgoString())
		fmt.Println("══════════════════════════════════════════════════")
		fmt.Println()
		fmt.Println("⚠️  Store this new note safely — it holds your remaining balance.")
	} else {
		fmt.Println("Full withdrawal complete — no change note.")
	}

	return nil
}

func handleWithdrawError(e *avm.TxnConfirmationError) error {
	switch e.Type {
	case avm.TxnRejected:
		return fmt.Errorf("withdrawal rejected by the network.\n" +
			"Ensure your secret note is valid and has not already been spent.")
	case avm.TxnTimeout:
		return fmt.Errorf("withdrawal submitted but confirmation timed out.\n" +
			"Run 'hermes sync' then 'hermes notes' to check status.")
	default:
		return fmt.Errorf("withdrawal failed: %w", e)
	}
}
