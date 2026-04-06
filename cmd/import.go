package cmd

import (
	"fmt"

	"github.com/nullun/hermes-vault-cli/internal/db"
	"github.com/nullun/hermes-vault-cli/internal/models"

	"github.com/spf13/cobra"
)

var (
	ensureImportSyncFn              = ensureRuntime
	getConfirmedNoteByCommitmentFn  = db.GetNoteByCommitment
	getChainTxnByCommitmentFn       = db.GetTxnByCommitment
	deleteUnconfirmedByCommitmentFn = db.DeleteUnconfirmedNoteByCommitment
)

var importCmd = &cobra.Command{
	Use:   "import <secret-note>",
	Short: "Import an existing secret note into the local database",
	Long: `Import a secret note that may have been created on another machine or
before Hermes was installed locally.

Hermes verifies the note against on-chain state using its commitment. If the
commitment is already present on-chain, the note is saved as confirmed. If not,
the command refuses to import it as confirmed.`,
	Args: requireExactArgs(1),
	RunE: runImport,
}

func runImport(cmd *cobra.Command, args []string) error {
	if err := ensureImportSyncFn(); err != nil {
		return err
	}

	note, err := models.Input(args[0]).ToNote()
	if err != nil {
		return fmt.Errorf("invalid secret note: %w", err)
	}
	commitment := note.Commitment()

	existing, err := getConfirmedNoteByCommitmentFn(commitment)
	if err != nil {
		return fmt.Errorf("failed to check existing notes: %w", err)
	}
	if existing != nil {
		fmt.Printf("Note already exists in the local database at leaf index %d.\n", existing.LeafIndex)
		return nil
	}

	leafIndex, txnID, err := getChainTxnByCommitmentFn(commitment)
	if err != nil {
		return fmt.Errorf("failed to reconcile note with chain state: %w", err)
	}
	if txnID == "" {
		return fmt.Errorf("note was not found in synced chain data. Run 'hermes sync' and try again after the deposit confirms")
	}

	note.LeafIndex = leafIndex
	note.TxnID = txnID
	if err := persistConfirmedNote(note); err != nil {
		return err
	}
	if err := deleteUnconfirmedByCommitmentFn(commitment); err != nil {
		return fmt.Errorf("note was imported, but cleanup of pending records failed: %w", err)
	}

	fmt.Printf("Imported note at leaf index %d.\n", leafIndex)
	return nil
}
