package db

import (
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) string {
	t.Helper()

	Close()
	dbPath := filepath.Join(t.TempDir(), "hermes.db")
	if err := Open(dbPath); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(Close)
	return dbPath
}

func TestSaveDepositIsIdempotent(t *testing.T) {
	openTestDB(t)

	commitment := []byte("deposit-commitment-0000000000000001")
	root := []byte("deposit-root-000000000000000000000001")

	if err := SaveDeposit(1, commitment, "TXN1", "ADDR", 100, root, 10); err != nil {
		t.Fatalf("SaveDeposit() first call error = %v", err)
	}
	if err := SaveDeposit(1, commitment, "TXN1", "ADDR", 100, root, 10); err != nil {
		t.Fatalf("SaveDeposit() duplicate call error = %v", err)
	}

	stats, err := GetStats()
	if err != nil {
		t.Fatalf("GetStats() error = %v", err)
	}
	if stats.DepositTotal.MicroAlgos != 100 {
		t.Fatalf("expected deposit total 100, got %d", stats.DepositTotal.MicroAlgos)
	}
	if stats.DepositCount != 1 {
		t.Fatalf("expected deposit count 1, got %d", stats.DepositCount)
	}
}

func TestSaveWithdrawalIsIdempotent(t *testing.T) {
	openTestDB(t)

	commitment := []byte("withdraw-commitment-0000000000000001")
	nullifier := []byte("withdraw-nullifier-0000000000000001")
	root := []byte("withdraw-root-0000000000000000000001")

	if err := SaveWithdrawal(2, commitment, "TXN2", "ADDR", 80, 5, nullifier, root, 11); err != nil {
		t.Fatalf("SaveWithdrawal() first call error = %v", err)
	}
	if err := SaveWithdrawal(2, commitment, "TXN2", "ADDR", 80, 5, nullifier, root, 11); err != nil {
		t.Fatalf("SaveWithdrawal() duplicate call error = %v", err)
	}

	stats, err := GetStats()
	if err != nil {
		t.Fatalf("GetStats() error = %v", err)
	}
	if stats.WithdrawalTotal.MicroAlgos != 80 {
		t.Fatalf("expected withdrawal total 80, got %d", stats.WithdrawalTotal.MicroAlgos)
	}
	if stats.FeeTotal.MicroAlgos != 5 {
		t.Fatalf("expected fee total 5, got %d", stats.FeeTotal.MicroAlgos)
	}
}
