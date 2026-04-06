package avm

import (
	"fmt"
	"strings"
)

type SendTxnErrorType int

const (
	TxnTimeout SendTxnErrorType = iota
	TxnRejected
	TxnOverSpend
	TxnExpired
	TxnInternal
	TxnMinimumBalanceRequirement
)

func (e SendTxnErrorType) String() string {
	switch e {
	case TxnTimeout:
		return "TxnTimeoutError"
	case TxnRejected:
		return "TxnRejectionError"
	case TxnOverSpend:
		return "TxnOverSpendError"
	case TxnExpired:
		return "TxnExpiredError"
	case TxnInternal:
		return "TxnInternalError"
	case TxnMinimumBalanceRequirement:
		return "TxnMinimumBalanceRequirementError"
	default:
		return "TxnUnknownError"
	}
}

type TxnConfirmationError struct {
	Type    SendTxnErrorType
	Message string
}

func (e *TxnConfirmationError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Type.String(), e.Message)
}

func parseWaitForConfirmationError(err error) *TxnConfirmationError {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "timed out") {
		return &TxnConfirmationError{Type: TxnTimeout, Message: err.Error()}
	}
	if strings.Contains(err.Error(), "Transaction rejected") {
		return &TxnConfirmationError{Type: TxnRejected, Message: err.Error()}
	}
	return &TxnConfirmationError{Type: TxnInternal, Message: err.Error()}
}

func parseSendTransactionError(err error) *TxnConfirmationError {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "logic eval error"):
		return &TxnConfirmationError{Type: TxnRejected, Message: msg}
	case strings.Contains(msg, "overspend"):
		return &TxnConfirmationError{Type: TxnOverSpend, Message: msg}
	case strings.Contains(msg, "txn dead"):
		return &TxnConfirmationError{Type: TxnExpired, Message: msg}
	case strings.Contains(msg, "balance") && strings.Contains(msg, "below min"):
		return &TxnConfirmationError{Type: TxnMinimumBalanceRequirement, Message: msg}
	default:
		return &TxnConfirmationError{Type: TxnRejected, Message: msg}
	}
}

// NewInternalError creates a TxnConfirmationError wrapping an internal error message.
func NewInternalError(s string) *TxnConfirmationError {
	return &TxnConfirmationError{Type: TxnInternal, Message: s}
}
