package config

import (
	"github.com/algorand/go-algorand-sdk/v2/transaction"
	"github.com/consensys/gnark-crypto/ecc"
)

const (
	MerkleTreeLevels    = 32
	Curve               = ecc.BN254
	RandomNonceByteSize = 31

	DepositMinimumAmount = 1_000_000 // microalgo, or 1 algo

	DepositMethodName    = "deposit"
	WithdrawalMethodName = "withdraw"
	NoOpMethodName       = "noop"

	UserDepositTxnIndex = 1 // index of the user pay txn in the deposit txn group (0-based)
)

const (
	// Number of top-level transactions needed for logicsig verifier opcode budget
	VerifierTopLevelTxnNeeded = 8

	DepositMinFeeMultiplier = 56

	WithdrawalMinFeeMultiplier = 60
	NullifierMbr               = 15_300 // microalgo
	WithdrawalMinFee           = NullifierMbr + WithdrawalMinFeeMultiplier*transaction.MinTxnFee
)

var Hash = NewMiMC(Curve)
