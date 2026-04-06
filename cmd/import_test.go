package cmd

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nullun/hermes-vault-cli/internal/models"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy() error = %v", err)
	}
	_ = r.Close()
	return buf.String()
}

func TestRunImportReturnsExistingConfirmedNote(t *testing.T) {
	originalEnsure := ensureImportSyncFn
	originalGetConfirmed := getConfirmedNoteByCommitmentFn
	originalGetTxn := getChainTxnByCommitmentFn
	originalDeletePending := deleteUnconfirmedByCommitmentFn
	originalSave := saveConfirmedNoteFn
	defer func() {
		ensureImportSyncFn = originalEnsure
		getConfirmedNoteByCommitmentFn = originalGetConfirmed
		getChainTxnByCommitmentFn = originalGetTxn
		deleteUnconfirmedByCommitmentFn = originalDeletePending
		saveConfirmedNoteFn = originalSave
	}()

	ensureImportSyncFn = func() error { return nil }
	getConfirmedNoteByCommitmentFn = func([]byte) (*models.Note, error) {
		note := models.NewNote(100, [31]byte{}, [31]byte{})
		note.LeafIndex = 9
		note.TxnID = "TXN"
		return note, nil
	}
	getChainTxnByCommitmentFn = func([]byte) (uint64, string, error) {
		t.Fatalf("chain lookup should not run when note already exists")
		return 0, "", nil
	}

	note := models.NewNote(100, [31]byte{}, [31]byte{})
	output := captureStdout(t, func() {
		if err := runImport(nil, []string{note.Text()}); err != nil {
			t.Fatalf("runImport() error = %v", err)
		}
	})

	if !strings.Contains(output, "already exists") {
		t.Fatalf("expected existing-note output, got %q", output)
	}
}

func TestRunImportSavesConfirmedNoteFromChainMatch(t *testing.T) {
	originalEnsure := ensureImportSyncFn
	originalGetConfirmed := getConfirmedNoteByCommitmentFn
	originalGetTxn := getChainTxnByCommitmentFn
	originalDeletePending := deleteUnconfirmedByCommitmentFn
	originalSave := saveConfirmedNoteFn
	defer func() {
		ensureImportSyncFn = originalEnsure
		getConfirmedNoteByCommitmentFn = originalGetConfirmed
		getChainTxnByCommitmentFn = originalGetTxn
		deleteUnconfirmedByCommitmentFn = originalDeletePending
		saveConfirmedNoteFn = originalSave
	}()

	ensureImportSyncFn = func() error { return nil }
	getConfirmedNoteByCommitmentFn = func([]byte) (*models.Note, error) { return nil, nil }
	getChainTxnByCommitmentFn = func([]byte) (uint64, string, error) {
		return 12, "TXN123", nil
	}
	deleted := false
	deleteUnconfirmedByCommitmentFn = func([]byte) error {
		deleted = true
		return nil
	}

	savedLeaf := uint64(0)
	savedTxn := ""
	saveConfirmedNoteFn = func(note *models.Note) error {
		savedLeaf = note.LeafIndex
		savedTxn = note.TxnID
		return nil
	}

	note := models.NewNote(100, [31]byte{}, [31]byte{})
	output := captureStdout(t, func() {
		if err := runImport(nil, []string{note.Text()}); err != nil {
			t.Fatalf("runImport() error = %v", err)
		}
	})

	if savedLeaf != 12 || savedTxn != "TXN123" {
		t.Fatalf("expected imported note metadata to be saved, got leaf=%d txn=%s", savedLeaf, savedTxn)
	}
	if !deleted {
		t.Fatalf("expected pending note cleanup to run")
	}
	if !strings.Contains(output, "Imported note at leaf index 12") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestRunImportFailsWhenNoteNotFoundOnChain(t *testing.T) {
	originalEnsure := ensureImportSyncFn
	originalGetConfirmed := getConfirmedNoteByCommitmentFn
	originalGetTxn := getChainTxnByCommitmentFn
	defer func() {
		ensureImportSyncFn = originalEnsure
		getConfirmedNoteByCommitmentFn = originalGetConfirmed
		getChainTxnByCommitmentFn = originalGetTxn
	}()

	ensureImportSyncFn = func() error { return nil }
	getConfirmedNoteByCommitmentFn = func([]byte) (*models.Note, error) { return nil, nil }
	getChainTxnByCommitmentFn = func([]byte) (uint64, string, error) {
		return 0, "", nil
	}

	note := models.NewNote(100, [31]byte{}, [31]byte{})
	err := runImport(nil, []string{note.Text()})
	if err == nil {
		t.Fatalf("expected runImport to fail")
	}
	if !strings.Contains(err.Error(), "not found in synced chain data") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunImportPropagatesConfirmedSaveFailure(t *testing.T) {
	originalEnsure := ensureImportSyncFn
	originalGetConfirmed := getConfirmedNoteByCommitmentFn
	originalGetTxn := getChainTxnByCommitmentFn
	originalDeletePending := deleteUnconfirmedByCommitmentFn
	originalSave := saveConfirmedNoteFn
	defer func() {
		ensureImportSyncFn = originalEnsure
		getConfirmedNoteByCommitmentFn = originalGetConfirmed
		getChainTxnByCommitmentFn = originalGetTxn
		deleteUnconfirmedByCommitmentFn = originalDeletePending
		saveConfirmedNoteFn = originalSave
	}()

	ensureImportSyncFn = func() error { return nil }
	getConfirmedNoteByCommitmentFn = func([]byte) (*models.Note, error) { return nil, nil }
	getChainTxnByCommitmentFn = func([]byte) (uint64, string, error) {
		return 12, "TXN123", nil
	}
	deleteUnconfirmedByCommitmentFn = func([]byte) error { return nil }
	saveConfirmedNoteFn = func(*models.Note) error {
		return errors.New("db readonly")
	}

	note := models.NewNote(100, [31]byte{}, [31]byte{})
	err := runImport(nil, []string{note.Text()})
	if err == nil {
		t.Fatalf("expected runImport to fail")
	}
	if !strings.Contains(err.Error(), "could not be saved to the local database") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportArgsShowHelpfulUsage(t *testing.T) {
	var output bytes.Buffer
	importCmd.SetOut(&output)
	importCmd.SetErr(&output)
	t.Cleanup(func() {
		importCmd.SetOut(nil)
		importCmd.SetErr(nil)
	})

	err := importCmd.Args(importCmd, nil)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("expected help output, got %q", output.String())
	}
}
