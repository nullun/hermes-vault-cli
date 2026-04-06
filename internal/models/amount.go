package models

import (
	"fmt"
	"strings"

	"github.com/nullun/hermes-vault-cli/internal/config"
)

// Amount represents an Algo token amount
type Amount struct {
	AlgoString string
	MicroAlgos uint64
}

func (a Amount) Fee() Amount {
	fee := CalculateWithdrawalFee(a.MicroAlgos)
	return NewAmount(fee)
}

func CalculateWithdrawalFee(amount uint64) uint64 {
	if config.FrontendWithdrawalFeeDivisor == 0 {
		return config.WithdrawalMinFee
	}
	return max(amount/config.FrontendWithdrawalFeeDivisor, config.WithdrawalMinFee)
}

func MicroAlgosToAlgoString(microAlgos uint64) string {
	wholeAlgos := microAlgos / 1_000_000
	remainingMicroAlgos := microAlgos % 1_000_000

	wholeAlgosStr := addThousandSeparators(wholeAlgos)
	fracStr := fmt.Sprintf("%06d", remainingMicroAlgos)
	fracStr = strings.TrimRight(fracStr, "0")
	if fracStr == "" {
		return wholeAlgosStr
	}
	return fmt.Sprintf("%s.%s", wholeAlgosStr, fracStr)
}

func NewAmount(microAlgos uint64) Amount {
	return Amount{
		AlgoString: MicroAlgosToAlgoString(microAlgos),
		MicroAlgos: microAlgos,
	}
}

func addThousandSeparators(n uint64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	remainder := len(s) % 3
	var result []byte
	if remainder > 0 {
		result = append(result, s[:remainder]...)
		result = append(result, ',')
	}
	for i := remainder; i < len(s); i += 3 {
		result = append(result, s[i:i+3]...)
		if i+3 < len(s) {
			result = append(result, ',')
		}
	}
	return string(result)
}
