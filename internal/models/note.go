package models

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/nullun/hermes-vault-cli/internal/config"
)

// Note is the secret value that authorizes withdrawals from the pool.
type Note struct {
	Amount    uint64
	K         [config.RandomNonceByteSize]byte
	R         [config.RandomNonceByteSize]byte
	LeafIndex uint64
	TxnID     string
	IsUsed    bool
}

func GenerateNote(amount uint64) (*Note, error) {
	k, errK := generateRandomNonce()
	r, errR := generateRandomNonce()
	if errK != nil || errR != nil {
		return nil, fmt.Errorf("error generating random bytes for k: %w / r: %w", errK, errR)
	}
	return NewNote(amount, k, r), nil
}

func NewNote(amount uint64, k, r [config.RandomNonceByteSize]byte) *Note {
	return &Note{
		Amount:    amount,
		K:         k,
		R:         r,
		LeafIndex: EmptyLeafIndex,
	}
}

func (n *Note) Text() string {
	return fmt.Sprintf("%016x%x%x", n.Amount, n.K, n.R)
}

func (n *Note) Nullifier() []byte {
	k32Byte := append([]byte{0}, n.K[:]...)
	return config.Hash(uint64ToBytes32(n.Amount), k32Byte)
}

func (n *Note) Commitment() []byte {
	return config.Hash(n.LeafValue())
}

func (n *Note) LeafValue() []byte {
	ab := uint64ToBytes32(n.Amount)
	k32Byte := append([]byte{0}, n.K[:]...)
	r32Byte := append([]byte{0}, n.R[:]...)
	return config.Hash(ab, k32Byte, r32Byte)
}

func (n *Note) MaxWithdrawalAmount() Amount {
	fee := CalculateWithdrawalFee(n.Amount)
	if n.Amount <= fee {
		return NewAmount(0)
	}
	return NewAmount(n.Amount - fee)
}

func (n *Note) AmountAlgoString() string {
	return MicroAlgosToAlgoString(n.Amount)
}

func GenerateChangeNote(withdrawalAmount Amount, fromNote *Note) (*Note, error) {
	deduction := withdrawalAmount.MicroAlgos + CalculateWithdrawalFee(withdrawalAmount.MicroAlgos)
	if deduction < withdrawalAmount.MicroAlgos {
		return nil, fmt.Errorf("overflow in deduction")
	}
	if fromNote.Amount < deduction {
		return nil, fmt.Errorf("note amount too small")
	}
	change := fromNote.Amount - deduction
	note, err := GenerateNote(change)
	if err != nil {
		return nil, fmt.Errorf("error generating note: %w", err)
	}
	return note, nil
}

func generateRandomNonce() ([config.RandomNonceByteSize]byte, error) {
	var arr [config.RandomNonceByteSize]byte
	_, err := rand.Read(arr[:])
	if err != nil {
		return [config.RandomNonceByteSize]byte{}, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return arr, nil
}

func uint64ToBytes32(amount uint64) []byte {
	amountBytes := make([]byte, 32)
	binary.BigEndian.PutUint64(amountBytes[24:], amount)
	return amountBytes
}
