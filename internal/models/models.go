// Package models defines Hermes domain types shared across commands and services.
package models

import "math"

const (
	EmptyTxnID     = ""
	EmptyLeafIndex = math.MaxUint64
)

type WithdrawalData struct {
	Amount     Amount
	Fee        Amount
	Address    Address
	FromNote   *Note
	ChangeNote *Note
}

type DepositData struct {
	Amount  Amount
	Address Address
	Note    *Note
}

type StatData struct {
	DepositTotal    Amount
	WithdrawalTotal Amount
	FeeTotal        Amount
	DepositCount    uint64
	NoteCount       uint64
}

func (s *StatData) TVL() *Amount {
	tvl := s.DepositTotal.MicroAlgos - s.WithdrawalTotal.MicroAlgos - s.FeeTotal.MicroAlgos
	tvlAmount := NewAmount(tvl)
	return &tvlAmount
}
