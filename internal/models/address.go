package models

import "github.com/nullun/hermes-vault-cli/internal/config"

// Address represents a valid Algorand address
type Address string

func (a Address) Start() string {
	return splitEnds(string(a), config.NumCharsToHighlight, partStart)
}
func (a Address) End() string {
	return splitEnds(string(a), config.NumCharsToHighlight, partEnd)
}

type addressPart int

const (
	partStart addressPart = iota
	partEnd
)

func splitEnds(s string, n int, part addressPart) string {
	if len(s) <= 2*n {
		if part == partStart {
			return s
		}
		return ""
	}
	if part == partStart {
		return s[:n]
	}
	return s[len(s)-n:]
}
