// Package resources embeds the smart contract artifacts shipped with Hermes.
package resources

import "embed"

//go:embed mainnet/* testnet/* circuits/*
var Resources embed.FS
