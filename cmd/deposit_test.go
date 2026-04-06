package cmd

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/nullun/hermes-vault-cli/internal/models"
)

func TestInitConfigAndDBFailsWhenLoggerInitFails(t *testing.T) {
	originalConfigLoad := configLoadFn
	originalLoggerInit := loggerInitFn
	defer func() {
		configLoadFn = originalConfigLoad
		loggerInitFn = originalLoggerInit
	}()

	configLoadFn = func(string) error {
		return nil
	}
	loggerInitFn = func(path string) error {
		return errors.New("logger unavailable")
	}

	err := initConfigAndDB(rootCmd, nil)
	if err == nil {
		t.Fatalf("expected initConfigAndDB to fail")
	}
	if !strings.Contains(err.Error(), "log file") {
		t.Fatalf("expected log file error, got %v", err)
	}
}

func TestRecordPendingNoteFailsWhenDurableLogFails(t *testing.T) {
	originalLogDurable := logDurableFn
	originalRegister := registerUnconfirmedNoteFn
	defer func() {
		logDurableFn = originalLogDurable
		registerUnconfirmedNoteFn = originalRegister
	}()

	logDurableFn = func(format string, args ...any) error {
		return errors.New("disk full")
	}

	calledRegister := false
	registerUnconfirmedNoteFn = func(*models.Note) (int64, error) {
		calledRegister = true
		return 1, nil
	}

	note := models.NewNote(100, [31]byte{}, [31]byte{})
	note.TxnID = "TXN"

	_, err := recordPendingNote("DEPOSIT NOTE_GENERATED txn=%s amount_ualgos=%d note=%s", note)
	if err == nil {
		t.Fatalf("expected recordPendingNote to fail")
	}
	if calledRegister {
		t.Fatalf("db registration should not run when durable log write fails")
	}
}

func TestRecordPendingNoteFailsWhenDatabaseWriteFails(t *testing.T) {
	originalLogDurable := logDurableFn
	originalRegister := registerUnconfirmedNoteFn
	defer func() {
		logDurableFn = originalLogDurable
		registerUnconfirmedNoteFn = originalRegister
	}()

	logDurableFn = func(format string, args ...any) error {
		return nil
	}
	registerUnconfirmedNoteFn = func(*models.Note) (int64, error) {
		return 0, errors.New("database readonly")
	}

	note := models.NewNote(100, [31]byte{}, [31]byte{})
	note.TxnID = "TXN"

	_, err := recordPendingNote("DEPOSIT NOTE_GENERATED txn=%s amount_ualgos=%d note=%s", note)
	if err == nil {
		t.Fatalf("expected recordPendingNote to fail")
	}
	if !strings.Contains(err.Error(), "could not record your note in the local database") {
		t.Fatalf("expected local database recording failure, got %v", err)
	}
}

func TestPersistConfirmedNoteFailsCatastrophically(t *testing.T) {
	originalSave := saveConfirmedNoteFn
	defer func() {
		saveConfirmedNoteFn = originalSave
	}()

	saveConfirmedNoteFn = func(*models.Note) error {
		return errors.New("database readonly")
	}

	note := models.NewNote(100, [31]byte{}, [31]byte{})
	note.TxnID = "TXN"

	err := persistConfirmedNote(note)
	if err == nil {
		t.Fatalf("expected persistConfirmedNote to fail")
	}
	if !strings.Contains(err.Error(), "could not be saved to the local database") {
		t.Fatalf("expected local database save failure, got %v", err)
	}
}

func TestWithdrawArgsShowHelpfulUsage(t *testing.T) {
	var output bytes.Buffer
	withdrawCmd.SetOut(&output)
	withdrawCmd.SetErr(&output)
	t.Cleanup(func() {
		withdrawCmd.SetOut(nil)
		withdrawCmd.SetErr(nil)
	})

	err := withdrawCmd.Args(withdrawCmd, nil)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("expected help output, got %q", output.String())
	}
}

func TestDepositArgsShowHelpfulUsage(t *testing.T) {
	var output bytes.Buffer
	depositCmd.SetOut(&output)
	depositCmd.SetErr(&output)
	t.Cleanup(func() {
		depositCmd.SetOut(nil)
		depositCmd.SetErr(nil)
	})

	err := depositCmd.Args(depositCmd, nil)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("expected help output, got %q", output.String())
	}
}
