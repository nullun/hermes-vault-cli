package models

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nullun/hermes-vault-cli/internal/config"

	"github.com/algorand/go-algorand-sdk/v2/types"
)

// Input represents a raw string input from the user
type Input string

func (input Input) ToAmount() (Amount, error) {
	intStr, decStr, hasDecimal := strings.Cut(string(input), ".")
	intStr = strings.ReplaceAll(intStr, ",", "")
	integer, err := strconv.ParseUint(intStr, 10, 64)
	if err != nil {
		return Amount{}, fmt.Errorf("invalid integer part: %w", err)
	}
	var decimal uint64
	if hasDecimal {
		if len(decStr) > 6 {
			return Amount{}, fmt.Errorf("too many decimal places")
		}
		if len(decStr) < 6 {
			decStr += strings.Repeat("0", 6-len(decStr))
		}
		decimal, err = strconv.ParseUint(decStr, 10, 64)
		if err != nil {
			return Amount{}, fmt.Errorf("invalid decimal part: %w", err)
		}
	}
	microalgos := integer*1_000_000 + decimal
	return NewAmount(microalgos), nil
}

func (input Input) ToAddress() (Address, error) {
	address, err := types.DecodeAddress(string(input))
	if err != nil {
		return "", fmt.Errorf("error decoding address: %w", err)
	}
	return Address(address.String()), nil
}

// ToNote parses a 140-character hex note string into a Note.
// Format: 16 hex chars (amount) + 62 hex chars (K) + 62 hex chars (R)
func (input Input) ToNote() (*Note, error) {
	amountByteSize := 8
	nonceByteSize := config.RandomNonceByteSize
	amountAndNonceSize := amountByteSize + nonceByteSize

	if len(input) != 140 {
		return nil, errors.New("invalid secret note length (expected 140 hex characters)")
	}
	decoded, err := hex.DecodeString(string(input))
	if err != nil {
		return nil, fmt.Errorf("error decoding hex string: %w", err)
	}
	amount := decoded[:amountByteSize]
	var k, r [config.RandomNonceByteSize]byte
	copy(k[:], decoded[amountByteSize:amountAndNonceSize])
	copy(r[:], decoded[amountAndNonceSize:])
	note := NewNote(binary.BigEndian.Uint64(amount), k, r)
	return note, nil
}
