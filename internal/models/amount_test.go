package models

import (
	"testing"

	"github.com/nullun/hermes-vault-cli/internal/config"
)

func TestFeeValueReceiver(t *testing.T) {
	// Fee() should be callable on a non-addressable Amount (e.g. directly
	// from NewAmount), which requires a value receiver.
	fee := NewAmount(1_000_000).Fee()
	if fee.MicroAlgos < config.WithdrawalMinFee {
		t.Fatalf("expected fee >= WithdrawalMinFee (%d), got %d", config.WithdrawalMinFee, fee.MicroAlgos)
	}
}

func TestFeeReturnsMinFee(t *testing.T) {
	amount := NewAmount(100_000)
	fee := amount.Fee()
	if fee.MicroAlgos != config.WithdrawalMinFee {
		t.Fatalf("expected min fee %d, got %d", config.WithdrawalMinFee, fee.MicroAlgos)
	}
}
