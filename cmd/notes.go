package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/nullun/hermes-vault-cli/internal/db"
	"github.com/nullun/hermes-vault-cli/internal/models"

	"github.com/spf13/cobra"
)

var notesShowAll bool
var listNotesFn = db.ListNotes

var notesCmd = &cobra.Command{
	Use:   "notes",
	Short: "List your saved secret notes",
	Long: `List all secret notes stored in your local database.

Notes are saved automatically after each successful deposit or withdrawal.
The note text is what you need to make a withdrawal.`,
	RunE: runNotes,
}

func init() {
	notesCmd.Flags().BoolVar(&notesShowAll, "all", false, "Show all notes, including those already used")
}

func runNotes(cmd *cobra.Command, args []string) error {
	notes, err := listNotesFn(notesShowAll)
	if err != nil {
		return fmt.Errorf("failed to list notes: %w", err)
	}

	out := cmd.OutOrStdout()

	if len(notes) == 0 {
		if notesShowAll {
			fmt.Fprintln(out, "No notes found in local database.")
		} else {
			fmt.Fprintln(out, "No active notes found in local database. Use --all to see used or zero-amount notes.")
		}
		fmt.Fprintln(out, "Notes are saved here automatically after each deposit or withdrawal.")
		return nil
	}

	if notesShowAll {
		fmt.Fprintf(out, "Found %d total note(s) in local database.\n\n", len(notes))
	} else {
		fmt.Fprintf(out, "Found %d active note(s) in local database.\n\n", len(notes))
	}

	for i, note := range notes {
		writeNoteBlock(out, note)
		if i < len(notes)-1 {
			fmt.Fprintln(out)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Keep these notes safe. Anyone with a note can withdraw the funds it represents.")
	return nil
}

func writeNoteBlock(w io.Writer, note models.Note) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Leaf index:\t%d\n", note.LeafIndex)
	fmt.Fprintf(tw, "Amount:\t%s ALGO\n", models.MicroAlgosToAlgoString(note.Amount))
	fmt.Fprintf(tw, "Txn ID:\t%s\n", shortenTxnID(note.TxnID))
	fmt.Fprintf(tw, "Note:\t%s\n", note.Text())
	if note.Amount == 0 || note.IsUsed {
		fmt.Fprintf(tw, "Status:\tSpent\n")
	} else {
		fmt.Fprintf(tw, "Status:\tAvailable\n")
	}
	_ = tw.Flush()
}

func shortenTxnID(txnID string) string {
	if len(txnID) <= 16 {
		return txnID
	}
	return txnID[:8] + "..." + txnID[len(txnID)-8:]
}
