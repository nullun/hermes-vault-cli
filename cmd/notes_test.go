package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nullun/hermes-vault-cli/internal/models"
)

func TestWriteNoteBlockFormatsReadableOutput(t *testing.T) {
	note := models.Note{
		Amount:    10_000_000,
		LeafIndex: 42,
		TxnID:     "ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890",
	}

	var out bytes.Buffer
	writeNoteBlock(&out, note)
	rendered := out.String()

	for _, want := range []string{
		"Leaf index:  42",
		"Amount:      10 ALGO",
		"Txn ID:      ABCDEFGH...34567890",
		"Note:        0000000000989680",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in output, got %q", want, rendered)
		}
	}
}

func TestShortenTxnID(t *testing.T) {
	if got := shortenTxnID("1234567890abcdef"); got != "1234567890abcdef" {
		t.Fatalf("expected unmodified short txn id, got %q", got)
	}

	if got := shortenTxnID("ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"); got != "ABCDEFGH...34567890" {
		t.Fatalf("unexpected shortened txn id: %q", got)
	}
}

func TestWriteNoteBlockShowsSpentStatusForZeroAmount(t *testing.T) {
	note := models.Note{
		Amount:    0,
		LeafIndex: 42,
		TxnID:     "TXNID123",
		IsUsed:    false,
	}

	var out bytes.Buffer
	writeNoteBlock(&out, note)
	rendered := out.String()

	if !strings.Contains(rendered, "Status:      Spent") {
		t.Fatalf("expected 'Status:      Spent' in output, got: %s", rendered)
	}
}

func TestWriteNoteBlockShowsSpentStatusForUsedNote(t *testing.T) {
	note := models.Note{
		Amount:    10_000_000,
		LeafIndex: 42,
		TxnID:     "TXNID123",
		IsUsed:    true,
	}

	var out bytes.Buffer
	writeNoteBlock(&out, note)
	rendered := out.String()

	if !strings.Contains(rendered, "Status:      Spent") {
		t.Fatalf("expected 'Status:      Spent' in output, got: %s", rendered)
	}
}

func TestWriteNoteBlockShowsAvailableStatusForAvailableNote(t *testing.T) {
	note := models.Note{
		Amount:    10_000_000,
		LeafIndex: 42,
		TxnID:     "TXNID123",
		IsUsed:    false,
	}

	var out bytes.Buffer
	writeNoteBlock(&out, note)
	rendered := out.String()

	if !strings.Contains(rendered, "Status:      Available") {
		t.Fatalf("expected 'Status:      Available' in output, got: %s", rendered)
	}
}
